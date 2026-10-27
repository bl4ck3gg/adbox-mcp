// adbox-mcp exposes Android devices over MCP by speaking the ADB wire protocol
// directly, with no dependency on a local adb binary or Android SDK.
//
// The reason it exists: every other Android MCP shells out to `adb`, which can only
// reach devices attached to the machine it runs on. An endpoint here is a host:port,
// optionally behind SOCKS5, so a device in a rack, behind NAT, or across an SSH
// tunnel is addressable with the same tools as one on the desk.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"adbox-mcp/internal/endpoint"
	"adbox-mcp/internal/mcpserver"
)

type endpointFlags []string

func (e *endpointFlags) String() string     { return strings.Join(*e, "; ") }
func (e *endpointFlags) Set(v string) error { *e = append(*e, v); return nil }

func main() {
	var eps endpointFlags
	flag.Var(&eps, "endpoint", "ADB endpoint: adb=host:port[,name=alias][,proxy=socks5://host:port] (repeatable)")
	debug := flag.Bool("debug", false, "Enable debug logging")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `adbox-mcp — MCP server for Android devices over raw ADB endpoints

Usage: adbox-mcp [-endpoint SPEC]... [-debug]

An endpoint is where an adb daemon listens. It may be local, remote, or behind a
SOCKS5 proxy, which is what lets this reach devices no local adb could see:

  adbox-mcp -endpoint "adb=127.0.0.1:5037,name=desk"
  adbox-mcp -endpoint "adb=10.0.0.9:5037,name=rack,proxy=socks5://127.0.0.1:11080"

Flags:
`)
		flag.PrintDefaults()
	}
	flag.Parse()

	if *debug {
		os.Setenv("ADBOX_DEBUG", "1")
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: map[bool]slog.Level{true: slog.LevelDebug, false: slog.LevelInfo}[*debug],
	})))

	mgr := endpoint.NewManager()

	specs := []string(eps)
	if len(specs) == 0 {
		// Default to the conventional local daemon so a plain `adbox-mcp` works
		// on a developer machine without arguments.
		specs = []string{"adb=127.0.0.1:5037,name=local"}
	}

	for _, spec := range specs {
		id, err := mgr.AddEndpoint(spec)
		if err != nil {
			slog.Error("failed to add endpoint", "spec", spec, "err", err)
			os.Exit(1)
		}
		slog.Info("endpoint registered", "spec", spec, "id", id)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	srv := mcpserver.New(mgr)
	if err := srv.Run(ctx); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}
