// Package mcpserver exposes the ADB endpoint manager over the Model Context Protocol.
//
// THE POINT OF THIS SERVER is the endpoint layer, not the tools. Every other Android
// MCP shells out to a local `adb` binary, which pins it to devices plugged into the
// same machine. Here an endpoint is a host:port (optionally behind a SOCKS5 proxy), so
// a device in a rack, behind NAT, or reached through an SSH tunnel is addressable with
// the same tools as one on the desk.
//
// Tools are grouped the way the protocol is, so a caller can predict where a capability
// lives:
//
//	endpoints_*   the connections to adb daemons
//	devices_*     what is attached to them
//	device_*      one device: shell, files, forward, props
//
// Every call takes an optional endpoint= to disambiguate, because two racks can hold
// devices with the same serial.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"adbox-mcp/internal/endpoint"
	"adbox-mcp/internal/tasks"
	"adbox-mcp/internal/screen"
	"adbox-mcp/internal/tether"
)

type Server struct {
	mgr    *endpoint.Manager
	tasks  *tasks.Registry
	tether *tether.Manager
	screen *screen.Manager
}

func New(mgr *endpoint.Manager) *Server {
	return &Server{
		mgr:    mgr,
		tasks:  tasks.NewRegistry(),
		tether: tether.NewManager(),
		screen: screen.NewManager(),
	}
}

// Run serves MCP over stdio until the context is cancelled.
func (s *Server) Run(ctx context.Context) error {
	srv := mcp.NewServer(&mcp.Implementation{Name: "adbox", Version: "0.1.0"}, nil)
	s.registerTools(srv)
	return srv.Run(ctx, &mcp.StdioTransport{})
}

func textResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
	}, nil
}

func errResult(format string, args ...any) (*mcp.CallToolResult, error) {
	msg := fmt.Sprintf(format, args...)
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}, nil
}

// splitSelector separates "serial@endpoint" into its two parts. An endpoint id is
// either the six-hex id or the human name, and both are accepted everywhere a device
// is addressed, because a caller reading a listing should be able to paste back
// whichever of the two it saw.
func splitSelector(sel string) (device, endpointName string) {
	if at := strings.LastIndex(sel, "@"); at >= 0 {
		return sel[:at], sel[at+1:]
	}
	return sel, ""
}
