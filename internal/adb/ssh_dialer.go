package adb

import (
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"adbox-mcp/internal/adb/internal/errors"
	"adbox-mcp/internal/adb/wire"
)

// SSHDialer reaches an adb server through an SSH connection, so a device that
// can dial out can be controlled without a public address, a port forward, or a
// VPN.
//
// This is what makes an Android phone with sshd installed reachable at all: the
// phone connects outward to a host we already have, and every later connection
// is multiplexed over that one transport. Nothing needs to be listening on the
// phone's network.
//
// It implements both Dialer and RawDialer, so it substitutes for the TCP and
// SOCKS5 dialers everywhere they are accepted, including the socket paths used
// by screen streaming.
type SSHDialer struct {
	addr   string
	config *ssh.ClientConfig

	mu     sync.Mutex
	client *ssh.Client
}

// SSHOptions are the credentials and transport settings for an SSH endpoint.
type SSHOptions struct {
	// Address is host:port of the SSH server.
	Address string
	// User is the login name.
	User string
	// Password authenticates with a password. Mutually optional with KeyPath.
	Password string
	// KeyPath is a private key file on this machine.
	KeyPath string
	// KeyPassphrase decrypts an encrypted private key.
	KeyPassphrase string
	// Timeout bounds both the TCP connect and the handshake. Defaults to 20s.
	Timeout time.Duration
	// HostKeyCallback decides whether to trust the server key. When nil the
	// connection is REFUSED rather than silently trusting anything, because an
	// unverified key is an interception, not a convenience.
	HostKeyCallback ssh.HostKeyCallback
	// InsecureIgnoreHostKey trusts any server key. It exists for the phone case
	// where the host key changes on every reflash and there is no known_hosts
	// to pin against; callers must opt in explicitly.
	InsecureIgnoreHostKey bool
}

// NewSSHDialer builds a dialer from options. It does not connect yet: the first
// Dial establishes the transport, so constructing one is free.
func NewSSHDialer(opts SSHOptions) (*SSHDialer, error) {
	if opts.Address == "" {
		return nil, errors.WrapErrorf(nil, errors.ServerNotAvailable, "ssh: address is required")
	}
	if opts.User == "" {
		return nil, errors.WrapErrorf(nil, errors.ServerNotAvailable, "ssh: user is required")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultDialTimeout
	}

	var auths []ssh.AuthMethod
	if opts.KeyPath != "" {
		key, err := os.ReadFile(opts.KeyPath)
		if err != nil {
			return nil, errors.WrapErrorf(err, errors.ServerNotAvailable, "ssh: read key %s", opts.KeyPath)
		}
		var signer ssh.Signer
		if opts.KeyPassphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(opts.KeyPassphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(key)
		}
		if err != nil {
			return nil, errors.WrapErrorf(err, errors.ServerNotAvailable, "ssh: parse key %s", opts.KeyPath)
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}
	if opts.Password != "" {
		auths = append(auths, ssh.Password(opts.Password))
	}
	if len(auths) == 0 {
		return nil, errors.WrapErrorf(nil, errors.ServerNotAvailable, "ssh: no credentials (need password or key)")
	}

	hostKey := opts.HostKeyCallback
	if hostKey == nil {
		if opts.InsecureIgnoreHostKey {
			hostKey = ssh.InsecureIgnoreHostKey()
		} else {
			return nil, errors.WrapErrorf(nil, errors.ServerNotAvailable,
				"ssh: refusing to connect without a host key policy; set InsecureIgnoreHostKey or supply HostKeyCallback")
		}
	}

	return &SSHDialer{
		addr: opts.Address,
		config: &ssh.ClientConfig{
			User:            opts.User,
			Auth:            auths,
			HostKeyCallback: hostKey,
			Timeout:         opts.Timeout,
		},
	}, nil
}

// ensure returns a live client, reconnecting when the previous transport died.
// A phone drops its link constantly (doze, network change, sshd restart), so a
// cached client that is not checked would fail every later call.
func (d *SSHDialer) ensure() (*ssh.Client, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.client != nil {
		// A zero-length channel open is the cheapest liveness probe: it fails
		// immediately when the transport is gone and costs nothing when it is not.
		if _, _, err := d.client.OpenChannel("session", nil); err == nil {
			return d.client, nil
		}
		_ = d.client.Close()
		d.client = nil
	}

	client, err := ssh.Dial("tcp", d.addr, d.config)
	if err != nil {
		return nil, errors.WrapErrorf(err, errors.ServerNotAvailable, "ssh: dial %s", d.addr)
	}
	d.client = client
	return client, nil
}

// dialThrough opens a direct-tcpip channel to address, which is what `ssh -L`
// does under the hood: the SSH server makes the TCP connection on its side.
func (d *SSHDialer) dialThrough(address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.WrapErrorf(err, errors.ServerNotAvailable, "ssh: bad address %q", address)
	}

	client, err := d.ensure()
	if err != nil {
		return nil, err
	}

	conn, err := client.Dial("tcp", net.JoinHostPort(host, port))
	if err != nil {
		// The cached transport may have died between the probe and the dial.
		// Drop it so the next call reconnects instead of failing forever.
		d.mu.Lock()
		if d.client == client {
			_ = d.client.Close()
			d.client = nil
		}
		d.mu.Unlock()
		return nil, errors.WrapErrorf(err, errors.ServerNotAvailable, "ssh: open channel to %s", address)
	}
	return conn, nil
}

// Dial connects to an adb server through the SSH transport.
func (d *SSHDialer) Dial(address string) (*wire.Conn, error) {
	conn, err := d.dialThrough(address)
	if err != nil {
		return nil, err
	}
	safeConn := wire.MultiCloseable(conn)
	return &wire.Conn{
		Scanner: wire.NewScanner(safeConn),
		Sender:  wire.NewSender(safeConn),
	}, nil
}

// DialRaw returns a plain connection across the same transport, which is what
// the screen session and forward helpers use.
func (d *SSHDialer) DialRaw(network, address string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("ssh dialer only supports tcp, got %q", network)
	}
	return d.dialThrough(address)
}

// Close tears the transport down.
func (d *SSHDialer) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.client == nil {
		return nil
	}
	err := d.client.Close()
	d.client = nil
	return err
}

// Run executes a command on the SSH host and returns its combined output. It is
// how adbox installs and starts sshd on a device it can already reach over adb,
// which is the bootstrap step: adb to plant the key, SSH to keep working after
// the cable is gone.
func (d *SSHDialer) Run(command string) (string, error) {
	client, err := d.ensure()
	if err != nil {
		return "", err
	}
	session, err := client.NewSession()
	if err != nil {
		return "", errors.WrapErrorf(err, errors.ServerNotAvailable, "ssh: new session")
	}
	defer session.Close()

	out, err := session.CombinedOutput(command)
	if err != nil {
		return string(out), errors.WrapErrorf(err, errors.ServerNotAvailable, "ssh: run %q", command)
	}
	return string(out), nil
}

// ParseSSHSpec turns a compact endpoint spec into SSHOptions.
//
//	ssh://user@host:22            password prompt is impossible here, so a key
//	ssh://user@host:22?key=~/id  a key file, tilde expanded
//	ssh://user@host:22?insecure=1&password=...   phone case, changing host key
func ParseSSHSpec(spec string) (SSHOptions, error) {
	var opts SSHOptions
	rest := strings.TrimPrefix(spec, "ssh://")
	if rest == spec {
		return opts, fmt.Errorf("ssh spec must start with ssh://, got %q", spec)
	}

	// Split query first so a password containing '?' is still handled predictably.
	query := ""
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		query, rest = rest[i+1:], rest[:i]
	}

	at := strings.LastIndexByte(rest, '@')
	if at < 0 {
		return opts, fmt.Errorf("ssh spec needs user@host: %q", spec)
	}
	opts.User = rest[:at]
	host := rest[at+1:]
	if !strings.Contains(host, ":") {
		host += ":22"
	}
	opts.Address = host

	for _, pair := range strings.Split(query, "&") {
		if pair == "" {
			continue
		}
		k, v, _ := strings.Cut(pair, "=")
		switch k {
		case "key":
			if strings.HasPrefix(v, "~/") {
				home, _ := os.UserHomeDir()
				v = home + v[1:]
			}
			opts.KeyPath = v
		case "password":
			opts.Password = v
		case "passphrase":
			opts.KeyPassphrase = v
		case "insecure":
			opts.InsecureIgnoreHostKey = v == "1" || v == "true"
		default:
			return opts, fmt.Errorf("ssh spec: unknown option %q", k)
		}
	}
	return opts, nil
}
