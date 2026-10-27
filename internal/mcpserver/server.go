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
	"os"
	"strconv"
	"log/slog"
)

type Server struct {
	mgr    *endpoint.Manager
	tasks  *tasks.Registry
	tether *tether.Manager
	screen *screen.Manager
	portal *screen.Portal
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
	s.startPortal()

	srv := mcp.NewServer(&mcp.Implementation{Name: "adbox", Version: "0.1.0"}, nil)
	s.registerTools(srv)
	err := srv.Run(ctx, &mcp.StdioTransport{})
	if s.portal != nil {
		s.portal.Close()
	}
	return err
}

// startPortal brings up the device list. It is best effort: a portal that cannot
// bind must not stop the MCP server, because the tools work without one and a
// caller who only ever uses screen_start's own URL never needs it.
func (s *Server) startPortal() {
	host := os.Getenv("ADBOX_SCREEN_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := 0
	if raw := strings.TrimSpace(os.Getenv("ADBOX_PORTAL_PORT")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			port = n
		}
	}
	token := strings.TrimSpace(os.Getenv("ADBOX_TOKEN"))
	if token == "" && os.Getenv("ADBOX_NO_AUTH") != "1" {
		token = screen.NewToken()
	}
	p := screen.NewPortal(s.screen, host, port, token)
	url, err := p.Start()
	if err != nil {
		slog.Warn("portal did not start", "err", err)
		return
	}
	s.portal = p
	slog.Info("portal ready", "url", url)
}

// PortalURL returns the base URL with auth token query appended.
func (s *Server) PortalURL() string {
	if s.portal == nil {
		return ""
	}
	return s.portal.URL()
}

// DeviceURL returns http://host:port/device/<serial>/?token=...
func (s *Server) DeviceURL(serial string) string {
	base := s.PortalURL()
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/device/" + serial + "/"
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
