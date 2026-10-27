package adb

import (
	"io"
	"net"
	"runtime"

	"adbox-mcp/internal/adb/internal/errors"
	"adbox-mcp/internal/adb/wire"
)

// Dialer knows how to create connections to an adb server.
type Dialer interface {
	Dial(address string) (*wire.Conn, error)
}

type tcpDialer struct{}

// Dial connects to the adb server on the host and port set on the netDialer.
// The zero-value will connect to the default, localhost:5037.
func (tcpDialer) Dial(address string) (*wire.Conn, error) {
	netConn, err := net.Dial("tcp", address)
	if err != nil {
		return nil, errors.WrapErrorf(err, errors.ServerNotAvailable, "error dialing %s", address)
	}
	if netConn == nil {
		return nil, errors.WrapErrorf(nil, errors.ServerNotAvailable, "dial returned nil connection")
	}

	// net.Conn can't be closed more than once, but wire.Conn will try to close both sender and scanner
	// so we need to wrap it to make it safe.
	safeConn := wire.MultiCloseable(netConn)

	var tcpRaw *net.TCPConn
	if tc, ok := netConn.(*net.TCPConn); ok {
		tcpRaw = tc
	}

	// Prevent leaking the network connection, not sure if TCPConn does this itself.
	// Note that the network connection may still be in use after the conn isn't (scanners/senders
	// can give their underlying connections to other scanner/sender types), so we can't
	// set the finalizer on conn.
	runtime.SetFinalizer(safeConn, func(conn io.ReadWriteCloser) {
		conn.Close()
	})

	return &wire.Conn{
		Scanner:    wire.NewScanner(safeConn),
		Sender:     wire.NewSender(safeConn),
		RawTCPConn: tcpRaw,
	}, nil
}

// RawDialer can produce a plain net.Conn across the same transport.
type RawDialer interface {
	DialRaw(network, address string) (net.Conn, error)
}
