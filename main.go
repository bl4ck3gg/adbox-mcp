// Package main is the entry point for the adbox-mcp server.
//
// WHY ZERO STARTUP FLAGS. An MCP server's configuration belongs in its protocol,
// not in the argv string of the process manager that launched it. Hardcoding
// endpoints into the launch command means restarting the server whenever a
// device rack changes IP, and it prevents an assistant from managing its own
// network topology.
//
// adbox-mcp starts with an empty endpoint registry and exposes endpoints_add,
// endpoints_remove, and endpoints_list so that callers configure where to look
// dynamically over stdio.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"adbox-mcp/internal/endpoint"
	"adbox-mcp/internal/mcpserver"
)

func main() {
	debug := flag.Bool("debug", false, "Enable debug logging to stderr")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `adbox-mcp — MCP server for Android devices over raw ADB endpoints

Usage: adbox-mcp [-debug]

Starts the server on stdio. Endpoints are configured dynamically at runtime
through the endpoints_add / endpoints_remove tools, so no flags are needed.
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

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	srv := mcpserver.New(mgr)
	if err := srv.Run(ctx); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}
