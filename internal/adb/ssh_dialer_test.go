package adb

import (
	"strings"
	"testing"
)

// ParseSSHSpec turns an endpoint spec into credentials. It is the only part of
// the SSH transport that can be tested without a server, and it is also where a
// silently wrong parse turns into a connection refused at 3am.

func TestParseSSHSpecBasic(t *testing.T) {
	opts, err := ParseSSHSpec("ssh://root@10.0.0.5?password=secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.User != "root" {
		t.Errorf("user = %q", opts.User)
	}
	// A missing port must default to 22 rather than being left off, because
	// ssh.Dial rejects an address without a port.
	if opts.Address != "10.0.0.5:22" {
		t.Errorf("address = %q, want 10.0.0.5:22", opts.Address)
	}
	if opts.Password != "secret" {
		t.Errorf("password = %q", opts.Password)
	}
}

func TestParseSSHSpecExplicitPort(t *testing.T) {
	opts, err := ParseSSHSpec("ssh://pi@192.168.1.9:2222?key=/tmp/id_ed25519")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Address != "192.168.1.9:2222" {
		t.Errorf("address = %q", opts.Address)
	}
	if opts.KeyPath != "/tmp/id_ed25519" {
		t.Errorf("keyPath = %q", opts.KeyPath)
	}
}

func TestParseSSHSpecInsecure(t *testing.T) {
	// The phone case: a reflashed device presents a new host key every time, so
	// there is nothing to pin against. This must be an explicit opt-in.
	for _, v := range []string{"1", "true"} {
		opts, err := ParseSSHSpec("ssh://u@h?insecure=" + v)
		if err != nil {
			t.Fatalf("insecure=%s: %v", v, err)
		}
		if !opts.InsecureIgnoreHostKey {
			t.Errorf("insecure=%s did not set the flag", v)
		}
	}
	// Anything else leaves it off. "0" must not be read as truthy.
	opts, err := ParseSSHSpec("ssh://u@h?insecure=0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.InsecureIgnoreHostKey {
		t.Error("insecure=0 enabled verification bypass")
	}
}

func TestParseSSHSpecRejectsBadInput(t *testing.T) {
	cases := []string{
		"10.0.0.5:22",             // no ssh:// prefix
		"ssh://10.0.0.5",         // no user
		"ssh://u@h?bogus=1",      // unknown option
	}
	for _, spec := range cases {
		if _, err := ParseSSHSpec(spec); err == nil {
			t.Errorf("expected an error for %q", spec)
		}
	}
}

func TestNewSSHDialerRequiresHostKeyPolicy(t *testing.T) {
	// Trusting any host key silently is an interception, not a convenience.
	_, err := NewSSHDialer(SSHOptions{
		Address:  "10.0.0.5:22",
		User:     "root",
		Password: "x",
	})
	if err == nil {
		t.Fatal("expected refusal without a host key policy")
	}
	if !strings.Contains(err.Error(), "host key") {
		t.Errorf("error should name the missing policy, got: %v", err)
	}
}

func TestNewSSHDialerRequiresCredentials(t *testing.T) {
	_, err := NewSSHDialer(SSHOptions{
		Address:               "10.0.0.5:22",
		User:                  "root",
		InsecureIgnoreHostKey: true,
	})
	if err == nil {
		t.Fatal("expected refusal without credentials")
	}
}

func TestNewSSHDialerRequiredFields(t *testing.T) {
	if _, err := NewSSHDialer(SSHOptions{User: "root", Password: "x", InsecureIgnoreHostKey: true}); err == nil {
		t.Error("expected an error for a missing address")
	}
	if _, err := NewSSHDialer(SSHOptions{Address: "h:22", Password: "x", InsecureIgnoreHostKey: true}); err == nil {
		t.Error("expected an error for a missing user")
	}
}

func TestSSHDialerDoesNotConnectOnConstruction(t *testing.T) {
	// Building a dialer must be free: endpoints get parsed at startup and most
	// of them are never used. Connecting eagerly would turn a typo in an unused
	// endpoint into a startup failure.
	d, err := NewSSHDialer(SSHOptions{
		Address:               "203.0.113.1:22", // TEST-NET-3, guaranteed unroutable
		User:                  "root",
		Password:              "x",
		InsecureIgnoreHostKey: true,
	})
	if err != nil {
		t.Fatalf("construction should not connect: %v", err)
	}
	if d == nil {
		t.Fatal("nil dialer")
	}
}
