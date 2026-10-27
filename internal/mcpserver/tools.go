package mcpserver

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"adbox-mcp/internal/adb"
	"adbox-mcp/internal/embedded"
	"adbox-mcp/internal/tasks"
)

type emptyArgs struct{}

func handle[In any](srv *mcp.Server, tool *mcp.Tool, fn func(ctx context.Context, in In) (*mcp.CallToolResult, error)) {
	mcp.AddTool(srv, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		res, err := fn(ctx, in)
		return res, nil, err
	})
}
func (s *Server) registerTools(srv *mcp.Server) {
	// ── endpoints ────────────────────────────────────────────────────────
	handle(srv, &mcp.Tool{
		Name:        "endpoints_list",
		Description: "List all registered ADB daemon endpoints, with reachability status and the proxy each uses.",
	}, func(ctx context.Context, _ emptyArgs) (*mcp.CallToolResult, error) {
		return textResult(s.mgr.ListEndpoints())
	})

	type addEndpointArgs struct {
		Spec string `json:"spec" jsonschema:"Endpoint specification: adb=host:port[,name=alias][,proxy=socks5://host:port]"`
	}
	handle(srv, &mcp.Tool{
		Name:        "endpoints_add",
		Description: "Register an ADB daemon endpoint. The spec accepts adb=host:port, name=alias, and proxy=socks5://host:port to tunnel through an SSH dynamic forward.",
	}, func(ctx context.Context, args addEndpointArgs) (*mcp.CallToolResult, error) {
		id, err := s.mgr.AddEndpoint(args.Spec)
		if err != nil {
			return errResult("add endpoint: %v", err)
		}
		return textResult(map[string]any{"ok": true, "endpoint_id": id})
	})

	type removeEndpointArgs struct {
		Target string `json:"target" jsonschema:"Endpoint ID, alias, or host:port to remove"`
	}
	handle(srv, &mcp.Tool{
		Name:        "endpoints_remove",
		Description: "Unregister an ADB daemon endpoint by its ID, alias, or host:port.",
	}, func(ctx context.Context, args removeEndpointArgs) (*mcp.CallToolResult, error) {
		ok := s.mgr.RemoveEndpoint(args.Target)
		return textResult(map[string]any{"ok": ok})
	})

	// ── devices ──────────────────────────────────────────────────────────
	handle(srv, &mcp.Tool{
		Name:        "devices_list",
		Description: "List every Android device visible on any active endpoint, with serial, model, state, and the endpoint it belongs to.",
	}, func(ctx context.Context, _ emptyArgs) (*mcp.CallToolResult, error) {
		records, err := s.mgr.ListDevices()
		if err != nil {
			return errResult("list devices: %v", err)
		}
		return textResult(map[string]any{"count": len(records), "devices": records})
	})

	// ── device_reboot ────────────────────────────────────────────────────
	type rebootArgs struct {
		Device string `json:"device" jsonschema:"Device serial"`
		Mode   string `json:"mode,omitempty" jsonschema:"Reboot mode: system (default), recovery, or bootloader"`
	}
	handle(srv, &mcp.Tool{
		Name:        "device_reboot",
		Description: "Reboot the device. Uses 'svc power reboot' by default, which works under uid 2000 without root.",
	}, func(ctx context.Context, args rebootArgs) (*mcp.CallToolResult, error) {
		dev, rec, err := s.mgr.ResolveDevice(args.Device)
		if err != nil {
			return errResult("resolve device: %v", err)
		}
		cmd := "svc power reboot"
		if args.Mode == "recovery" || args.Mode == "bootloader" {
			cmd = "reboot " + args.Mode
		}
		// The connection drops immediately when the kernel starts tearing down,
		// so an error or EOF is the normal success path.
		_, _ = dev.RunCommand(cmd)
		return textResult(map[string]any{
			"device":   rec.Serial,
			"endpoint": rec.Endpoint,
			"mode":     args.Mode,
			"rebooting": true,
		})
	})

	// ── device_shell ─────────────────────────────────────────────────────
	type shellArgs struct {
		Device  string `json:"device" jsonschema:"Device serial (optional: serial@endpoint to disambiguate)"`
		Command string `json:"command" jsonschema:"Shell command line to execute on the device"`
		Timeout int    `json:"timeout_seconds,omitempty" jsonschema:"Execution timeout in seconds (default 30)"`
	}
	handle(srv, &mcp.Tool{
		Name:        "device_shell",
		Description: "Run a shell command on an Android device and return its output. This speaks the native adb shell protocol, not an agent.",
	}, func(ctx context.Context, args shellArgs) (*mcp.CallToolResult, error) {
		dev, rec, err := s.mgr.ResolveDevice(args.Device)
		if err != nil {
			return errResult("resolve device: %v", err)
		}
		to := 30 * time.Second
		if args.Timeout > 0 {
			to = time.Duration(args.Timeout) * time.Second
		}
		type res struct {
			out string
			err error
		}
		done := make(chan res, 1)
		go func() {
			out, err := dev.RunCommand(args.Command)
			done <- res{out: out, err: err}
		}()
		select {
		case r := <-done:
			if r.err != nil {
				return errResult("shell: %v (output so far: %q)", r.err, r.out)
			}
			return textResult(map[string]any{
				"device":   rec.Serial,
				"endpoint": rec.Endpoint,
				"output":   r.out,
			})
		case <-time.After(to):
			return errResult("command timed out after %v", to)
		case <-ctx.Done():
			return errResult("context cancelled")
		}
	})

	// ── device_spawn ─────────────────────────────────────────────────────
	//
	// A detached process is the one thing a shell cannot do for you over adb.
	// `cmd &` returns, but the child dies with the session: adbd tears down the
	// whole process group when the shell's stdin closes or the connection drops
	// by timeout, and no amount of nohup or setsid survives that -- the signal
	// goes to the group, not the process.
	//
	// The way out is to never let the session be torn down. We open an
	// interactive shell, write the command, and half-close our write side. The
	// device shell reads EOF on stdin, exits normally, and the child -- nohup'd,
	// so no SIGHUP -- is reparented to init instead of being killed with its
	// parent's group.
	//
	// Measured on a Note 9: the same command that never survived `device_shell`
	// is still listening on its port seconds later.
	type spawnArgs struct {
		Device  string `json:"device" jsonschema:"Device serial (optional: serial@endpoint to disambiguate)"`
		Command string `json:"command" jsonschema:"Shell command line to launch in the background (quote and background it yourself, or pass the bare command)"`
		Settle  int    `json:"settle_ms,omitempty" jsonschema:"Milliseconds to wait before checking, default 1200"`
		Verify  string `json:"verify_command,omitempty" jsonschema:"Optional command run afterwards to prove the process is alive (e.g. pgrep -f mydaemon)"`
	}
	handle(srv, &mcp.Tool{
		Name: "device_spawn",
		Description: "Start a process on the device that outlives the shell session, then report whether it survived. " +
			"Use this instead of device_shell for daemons, servers, and anything that must keep running. " +
			"It half-closes the shell's stdin so the device sees a normal EOF rather than a torn-down session.",
	}, func(ctx context.Context, args spawnArgs) (*mcp.CallToolResult, error) {
		dev, rec, err := s.mgr.ResolveDevice(args.Device)
		if err != nil {
			return errResult("resolve device: %v", err)
		}
		// nohup must be the direct parent of the command, not of an intermediate
		// sh -c: a wrapping shell exits immediately, leaving the real process in
		// the session's process group where adbd can still reach it.
		line := args.Command
		if !strings.Contains(line, " &") {
			line = "/system/bin/nohup " + line + " >/dev/null 2>&1 </dev/null &"
		}
		// The trailing sleep keeps the device shell alive long enough for the
		// child to be reparented. Without it the shell reads EOF and exits in the
		// same breath as the launch, and the teardown races the detach.
		if err := dev.RunInteractiveShellLinesFireAndClose([]string{line, "sleep 1"}); err != nil {
			return errResult("spawn: %v", err)
		}

		settle := 1200
		if args.Settle > 0 {
			settle = args.Settle
		}
		time.Sleep(time.Duration(settle) * time.Millisecond)

		out := map[string]any{
			"device":   rec.Serial,
			"endpoint": rec.Endpoint,
			"command":  line,
			"spawned":  true,
		}
		if args.Verify != "" {
			verifyOut, verr := dev.RunCommand(args.Verify)
			out["verify_command"] = args.Verify
			out["verify_output"] = strings.TrimSpace(verifyOut)
			out["alive"] = verr == nil && strings.TrimSpace(verifyOut) != ""
			if verr != nil {
				out["verify_error"] = verr.Error()
			}
		}
		return textResult(out)
	})

	// ── device_ui_start / device_ui_stop ─────────────────────────────────
	type uiStartArgs struct {
		Device  string `json:"device" jsonschema:"Device serial (optional: serial@endpoint to disambiguate)"`
		Port    int    `json:"port,omitempty" jsonschema:"Local port to expose the UI server on (default 18080)"`
		Restart bool   `json:"restart,omitempty" jsonschema:"Kill any running instance first"`
		Force   bool   `json:"force_push,omitempty" jsonschema:"Push the embedded APK even if the device copy is the right size"`
	}
	handle(srv, &mcp.Tool{
		Name: "device_ui_start",
		Description: "Install and launch the embedded adbox-agent server on the device, then expose it on a local port. " +
			"One call replaces push + spawn + forward. Returns the URL to open as an MCP endpoint.",
	}, func(ctx context.Context, args uiStartArgs) (*mcp.CallToolResult, error) {
		dev, rec, err := s.mgr.ResolveDevice(args.Device)
		if err != nil {
			return errResult("resolve device: %v", err)
		}
		requestedPort := args.Port

		// Always clear stale agents, not only on restart.
		//
		// UiAutomation is a single global registration: if any earlier instance
		// still holds it, the new process dies with "UiAutomationService already
		// registered!" and never reaches its ready beacon. That is not a rare
		// edge case -- it is what happens every time a spawn is cut short, the
		// device reboots oddly, or an agent was left behind by a previous name.
		//
		// Both class names are matched because the agent was renamed from
		// bl4ck3gg.uia2mcp.Main to bl4ck3gg.adbox.agent.Main, and a device
		// carrying the old build is exactly the device most likely to be holding
		// the registration.
		killCmd := "pkill -f " + embedded.AgentMainClass +
			"; pkill -f bl4ck3gg.uia2mcp.Main" +
			"; sleep 1"
		if _, err := dev.RunCommand(killCmd); err != nil {
			// pkill exits non-zero when nothing matched; that is the good path.
			_ = err
		}

		// Push only when the on-device copy differs in size. A full read would cost
		// a transfer just to compare bytes, and version drift is what actually bites.
		needPush := args.Force
		if !needPush {
			out, _ := dev.RunCommand("stat -c %s " + embedded.AgentRemotePath + " 2>/dev/null")
			needPush = strings.TrimSpace(out) != strconv.Itoa(len(embedded.AgentApk))
		}
		if needPush {
			w, werr := dev.OpenWrite(embedded.AgentRemotePath, 0644, time.Now())
			if werr != nil {
				return errResult("open device writer: %v", werr)
			}
			if _, werr = w.Write(embedded.AgentApk); werr != nil {
				w.Close()
				return errResult("push embedded apk: %v", werr)
			}
			if werr = w.Close(); werr != nil {
				return errResult("close device writer: %v", werr)
			}
		}

		// Same launch shape as device_spawn: nohup must parent the process directly,
		// and the trailing sleep keeps the device shell alive while it detaches.
		launch := "/system/bin/nohup env CLASSPATH=" + embedded.AgentRemotePath +
			" app_process / " + embedded.AgentMainClass + " --port " + strconv.Itoa(requestedPort) +
			" >/dev/null 2>&1 </dev/null &"
		if err := dev.RunInteractiveShellLinesFireAndClose([]string{launch, "sleep 1"}); err != nil {
			return errResult("spawn adbox-agent: %v", err)
		}

		// Poll the ready beacon instead of sleeping a fixed duration.
		var actualRemotePort int
		var agentPid string
		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) {
			out, _ := dev.RunCommand("cat /data/local/tmp/adbox-agent.ready 2>/dev/null")
			out = strings.TrimSpace(out)
			if out != "" {
				for _, line := range strings.Split(out, "\n") {
					parts := strings.SplitN(strings.TrimSpace(line), "=", 2)
					if len(parts) == 2 {
						switch parts[0] {
						case "port":
							actualRemotePort, _ = strconv.Atoi(parts[1])
						case "pid":
							agentPid = parts[1]
						}
					}
				}
				if actualRemotePort > 0 {
					break
				}
			}
			time.Sleep(150 * time.Millisecond)
		}

		if actualRemotePort <= 0 {
			log, _ := dev.RunCommand("tail -n 6 /data/local/tmp/adbox-agent.log 2>/dev/null")
			return errResult("agent failed to report ready within 6s; log: %s", strings.TrimSpace(log))
		}

		// Dynamic forward: tcp:0 on host lets the adb server choose an ephemeral port.
		remSpec := adb.ForwardSpec{Protocol: adb.FProtocolTcp, PortOrName: strconv.Itoa(actualRemotePort)}
		localPort, ferr := dev.ForwardToFreePort(remSpec)
		if ferr != nil {
			return errResult("forward to remote port %d: %v", actualRemotePort, ferr)
		}

		log, _ := dev.RunCommand("tail -n 3 /data/local/tmp/adbox-agent.log 2>/dev/null")

		return textResult(map[string]any{
			"device":       rec.Serial,
			"endpoint":     rec.Endpoint,
			"pushed":       needPush,
			"version":      embedded.AgentVersion,
			"alive":        true,
			"pid":          agentPid,
			"remote_port":  actualRemotePort,
			"local_port":   localPort,
			"mcp_url":      fmt.Sprintf("http://127.0.0.1:%d/mcp", localPort),
			"log_tail":     strings.TrimSpace(log),
		})
	})

	handle(srv, &mcp.Tool{
		Name:        "device_ui_stop",
		Description: "Stop the embedded adbox-agent server and remove its port forward.",
	}, func(ctx context.Context, args struct {
		Device string `json:"device" jsonschema:"Device serial"`
	}) (*mcp.CallToolResult, error) {
		dev, rec, err := s.mgr.ResolveDevice(args.Device)
		if err != nil {
			return errResult("resolve device: %v", err)
		}
		out, _ := dev.RunCommand("pkill -f " + embedded.AgentMainClass + "; echo stopped")
		return textResult(map[string]any{
			"device": rec.Serial,
			"output": strings.TrimSpace(out),
		})
	})

	// ── device_info ────────────────────────────────────────────────��─────
	type devArgs struct {
		Device string `json:"device" jsonschema:"Device serial (optional: serial@endpoint to disambiguate)"`
	}
	handle(srv, &mcp.Tool{
		Name:        "device_info",
		Description: "Read device properties: model, brand, Android version, SDK level, screen size, and battery.",
	}, func(ctx context.Context, args struct {
		Device string `json:"device" jsonschema:"Device serial"`
	}) (*mcp.CallToolResult, error) {
		dev, rec, err := s.mgr.ResolveDevice(args.Device)
		if err != nil {
			return errResult("resolve device: %v", err)
		}
		// Probe essentials with one shell batch
		script := `getprop ro.product.brand; getprop ro.product.model; getprop ro.build.version.release; getprop ro.build.version.sdk; dumpsys battery | grep level | tr -dc '0-9'; echo; wm size 2>/dev/null | grep Physical`
		out, err := dev.RunCommand(script)
		if err != nil {
			return errResult("device info probe: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		info := map[string]any{
			"serial":      rec.Serial,
			"endpoint":    rec.Endpoint,
			"endpoint_id": rec.EndpointID,
			"state":       rec.State,
		}
		if len(lines) > 0 {
			info["brand"] = strings.TrimSpace(lines[0])
		}
		if len(lines) > 1 {
			info["model"] = strings.TrimSpace(lines[1])
		}
		if len(lines) > 2 {
			info["os_version"] = strings.TrimSpace(lines[2])
		}
		if len(lines) > 3 {
			if sdk, e := strconv.Atoi(strings.TrimSpace(lines[3])); e == nil {
				info["sdk_int"] = sdk
			}
		}
		if len(lines) > 4 {
			if b, e := strconv.Atoi(strings.TrimSpace(lines[4])); e == nil {
				info["battery_percent"] = b
			}
		}
		if len(lines) > 5 {
			info["resolution"] = strings.TrimPrefix(strings.TrimSpace(lines[5]), "Physical size: ")
		}
		return textResult(info)
	})

	// ── device_input ─────────────────────────────────────────────────────
	type inputArgs struct {
		Device string `json:"device" jsonschema:"Device serial"`
		Action string `json:"action" jsonschema:"tap, swipe, text, or key"`
		X      int    `json:"x,omitempty" jsonschema:"X coordinate for tap"`
		Y      int    `json:"y,omitempty" jsonschema:"Y coordinate for tap"`
		X2     int    `json:"x2,omitempty" jsonschema:"End X for swipe"`
		Y2     int    `json:"y2,omitempty" jsonschema:"End Y for swipe"`
		Text   string `json:"text,omitempty" jsonschema:"Literal text to type"`
		Key    string `json:"key,omitempty" jsonschema:"Android keycode name or number (e.g. KEYCODE_HOME, 3, BACK, 4)"`
	}
	handle(srv, &mcp.Tool{
		Name:        "device_input",
		Description: "Inject a touch, swipe, text string, or key event into the device through adb input.",
	}, func(ctx context.Context, args inputArgs) (*mcp.CallToolResult, error) {
		dev, _, err := s.mgr.ResolveDevice(args.Device)
		if err != nil {
			return errResult("resolve device: %v", err)
		}
		var cmd string
		switch args.Action {
		case "tap":
			cmd = fmt.Sprintf("input tap %d %d", args.X, args.Y)
		case "swipe":
			cmd = fmt.Sprintf("input swipe %d %d %d %d 200", args.X, args.Y, args.X2, args.Y2)
		case "text":
			// Escape shell metachars
			escaped := strings.ReplaceAll(args.Text, " ", "%s")
			cmd = fmt.Sprintf("input text %s", escaped)
		case "key":
			cmd = fmt.Sprintf("input keyevent %s", args.Key)
		default:
			return errResult("action must be tap, swipe, text, or key (got %q)", args.Action)
		}
		out, err := dev.RunCommand(cmd)
		if err != nil {
			return errResult("input failed: %v", err)
		}
		return textResult(map[string]any{"ok": true, "output": out})
	})

	// ── device_screenshot ────────────────────────────────────────────────
	type shotArgs struct {
		Device string `json:"device" jsonschema:"Device serial"`
	}
	handle(srv, &mcp.Tool{
		Name:        "device_screenshot",
		Description: "Capture the current screen as a PNG and return it as base64-encoded image data.",
	}, func(ctx context.Context, args shotArgs) (*mcp.CallToolResult, error) {
		dev, _, err := s.mgr.ResolveDevice(args.Device)
		if err != nil {
			return errResult("resolve device: %v", err)
		}
		conn, err := dev.OpenCommand("screencap", "-p")
		if err != nil {
			return errResult("screencap: %v", err)
		}
		defer conn.Close()
		pngBytes, err := io.ReadAll(conn)
		if err != nil {
			return errResult("read screenshot: %v", err)
		}
		if len(pngBytes) < 8 || string(pngBytes[:8]) != "\x89PNG\r\n\x1a\n" {
			return errResult("output was not a valid PNG (got %d bytes)", len(pngBytes))
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.ImageContent{
					Data:     pngBytes,
					MIMEType: "image/png",
				},
				&mcp.TextContent{Text: fmt.Sprintf("captured %d bytes PNG", len(pngBytes))},
			},
		}, nil
	})

	// ── device_forwards ──────────────────────────────────────────────────
	type fwdListArgs struct {
		Device string `json:"device" jsonschema:"Device serial"`
	}
	handle(srv, &mcp.Tool{
		Name:        "device_forward_list",
		Description: "List active port forwards on the device (adb forward --list).",
	}, func(ctx context.Context, args fwdListArgs) (*mcp.CallToolResult, error) {
		dev, _, err := s.mgr.ResolveDevice(args.Device)
		if err != nil {
			return errResult("resolve device: %v", err)
		}
		pairs, err := dev.ForwardList()
		if err != nil {
			return errResult("forward list: %v", err)
		}
		return textResult(pairs)
	})

	type fwdAddArgs struct {
		Device string `json:"device" jsonschema:"Device serial"`
		Local  string `json:"local" jsonschema:"Local spec, e.g. tcp:8080"`
		Remote string `json:"remote" jsonschema:"Remote spec, e.g. tcp:8080 or localabstract:sockname"`
	}
	handle(srv, &mcp.Tool{
		Name:        "device_forward",
		Description: "Create an ADB forward rule (adb forward <local> <remote>).",
	}, func(ctx context.Context, args fwdAddArgs) (*mcp.CallToolResult, error) {
		dev, _, err := s.mgr.ResolveDevice(args.Device)
		if err != nil {
			return errResult("resolve device: %v", err)
		}
		localSpec, err := parseForwardSpec(args.Local)
		if err != nil {
			return errResult("local spec: %v", err)
		}
		remoteSpec, err := parseForwardSpec(args.Remote)
		if err != nil {
			return errResult("remote spec: %v", err)
		}
		if err := dev.Forward(localSpec, remoteSpec); err != nil {
			return errResult("create forward: %v", err)
		}
		return textResult(map[string]any{"ok": true})
	})

	// ── device_push / device_pull ────────────────────────────────────────
	type pushArgs struct {
		Device     string   `json:"device" jsonschema:"Device serial"`
		LocalPath  string   `json:"local_path" jsonschema:"Absolute path of the local file"`
		RemotePath string   `json:"remote_path" jsonschema:"Absolute path on the device"`
		WaitSec    *float64 `json:"wait_seconds,omitempty" jsonschema:"How long to wait for completion before returning a task id (default 10, 0 = return immediately)"`
	}
	handle(srv, &mcp.Tool{
		Name: "device_push",
		Description: "Push a local file to the device over the native adb sync protocol. " +
			"Returns the finished result when the transfer completes within wait_seconds, " +
			"otherwise returns a task id to poll with task_status. Large files are expected: " +
			"the transfer keeps running whether or not the caller waits.",
	}, func(ctx context.Context, args pushArgs) (*mcp.CallToolResult, error) {
		dev, _, err := s.mgr.ResolveDevice(args.Device)
		if err != nil {
			return errResult("resolve device: %v", err)
		}
		f, err := os.Open(args.LocalPath)
		if err != nil {
			return errResult("open local file: %v", err)
		}
		stat, err := f.Stat()
		if err != nil {
			f.Close()
			return errResult("stat local file: %v", err)
		}
		size := stat.Size()
		modTime := stat.ModTime()

		task := s.tasks.Start("push", args.Device, func(h *tasks.Handle) error {
			defer f.Close()
			h.SetTotal(size)
			w, err := dev.OpenWrite(args.RemotePath, 0644, modTime)
			if err != nil {
				return fmt.Errorf("open device writer: %w", err)
			}
			defer w.Close()
			n, err := io.Copy(w, io.TeeReader(f, tasks.ProgressWriter{H: h}))
			if err != nil {
				return fmt.Errorf("transfer: %w", err)
			}
			if n != size {
				return fmt.Errorf("short write: sent %d of %d bytes", n, size)
			}
			return nil
		})

		// wait_seconds is a float so that "0" can mean "do not wait at all", while an
		// absent field takes the default. A plain int would make the zero value
		// ambiguous, and defaulting an explicit 0 to 10s would hide the caller's
		// intent to fire and forget.
		wait := 10 * time.Second
		if args.WaitSec != nil {
			wait = time.Duration(*args.WaitSec * float64(time.Second))
		}
		if wait > 0 {
			if final, done := s.tasks.Wait(task.ID, wait); done {
				if final.State == tasks.Failed {
					return errResult("push failed: %s", final.Error)
				}
				return textResult(map[string]any{
					"ok":            true,
					"bytes_written": final.Moved,
					"remote_path":   args.RemotePath,
					"state":         final.State,
				})
			}
		}
		return textResult(map[string]any{
			"ok":          true,
			"state":       task.State,
			"task_id":     task.ID,
			"total_bytes": size,
			"moved_bytes": task.Moved,
			"hint":        "still running; poll task_status with this id",
		})
	})

	// ── task_status / task_list / task_cancel ────────────────────────────
	type taskStatusArgs struct {
		TaskID string `json:"task_id" jsonschema:"Task id returned by an async tool"`
	}
	handle(srv, &mcp.Tool{
		Name:        "task_status",
		Description: "Read a background task: state (running/done/failed/cancelled), moved vs total bytes, and any error.",
	}, func(ctx context.Context, args taskStatusArgs) (*mcp.CallToolResult, error) {
		t, ok := s.tasks.Get(args.TaskID)
		if !ok {
			return errResult("unknown task %q", args.TaskID)
		}
		return textResult(&t)
	})

	handle(srv, &mcp.Tool{
		Name:        "task_list",
		Description: "List every background task this process has run, oldest first.",
	}, func(ctx context.Context, _ emptyArgs) (*mcp.CallToolResult, error) {
		return textResult(s.tasks.List())
	})

	type taskCancelArgs struct {
		TaskID string `json:"task_id" jsonschema:"Task id to stop"`
	}
	handle(srv, &mcp.Tool{
		Name:        "task_cancel",
		Description: "Stop a running background task at its next message boundary.",
	}, func(ctx context.Context, args taskCancelArgs) (*mcp.CallToolResult, error) {
		return textResult(map[string]any{"ok": s.tasks.Cancel(args.TaskID)})
	})
	// ── device_tether_start / stop / list ────────────────────────────────
	type tetherArgs struct {
		Device   string `json:"device" jsonschema:"Device serial"`
		Port     int    `json:"port,omitempty" jsonschema:"Host port for the relay (default 31416)"`
		Remote   string `json:"remote,omitempty" jsonschema:"Device-side socket name (default localabstract:gnirehtet)"`
		Activity string `json:"activity,omitempty" jsonschema:"Activity to start the VPN (default com.genymobile.gnirehtet/.GnirehtetActivity)"`
	}
	handle(srv, &mcp.Tool{
		Name: "device_tether_start",
		Description: "Tether the device over adb: runs a user-space TCP/IP relay in this process, " +
			"reverses localabstract:gnirehtet to it, and launches GnirehtetActivity to request the VPN. " +
			"A human must tap OK on the connection request dialog.",
	}, func(ctx context.Context, args tetherArgs) (*mcp.CallToolResult, error) {
		dev, rec, err := s.mgr.ResolveDevice(args.Device)
		if err != nil {
			return errResult("resolve device: %v", err)
		}
		port := args.Port
		if port <= 0 {
			port = 31416
		}
		remote := args.Remote
		if remote == "" {
			remote = "localabstract:gnirehtet"
		}
		activity := args.Activity
		if activity == "" {
			activity = "com.genymobile.gnirehtet/.GnirehtetActivity"
		}

		session, err := s.tether.Start(rec.Serial, port)
		if err != nil {
			return errResult("start relay: %v", err)
		}

		remSpec, perr := parseForwardSpec(remote)
		if perr != nil {
			return errResult("parse remote: %v", perr)
		}
		locSpec, perr := parseForwardSpec("tcp:" + strconv.Itoa(port))
		if perr != nil {
			return errResult("parse local: %v", perr)
		}
		if rerr := dev.Reverse(remSpec, locSpec); rerr != nil {
			return errResult("adb reverse %s -> tcp:%d: %v", remote, port, rerr)
		}

		// Exclude the shell's own uid from the tunnel, or tethering eats its own
		// control channel.
		//
		// adbd runs as uid 2000, which the platform knows as com.android.shell. If
		// the VPN captures that uid's traffic, the adb connection itself starts
		// flowing through the tunnel it is carrying -- and the moment the tunnel
		// needs that connection to stay up, the two depend on each other. Over USB
		// it survives by accident; over Wi-Fi, where adb IS the network, it is a
		// self-inflicted disconnect.
		//
		// The same package also covers the app_process children this tool starts
		// (they inherit uid 2000), so a spawned bridge never tunnels its own
		// control socket either.
		disallowed := []string{"com.android.shell"}

		// Build the intent with an explicit string-array extra. am's --esa takes a
		// comma-separated list and refuses empty entries, so a single package is
		// passed as one value.
		startCmd := "am start -a com.genymobile.gnirehtet.START -n " + activity +
			" --esa disallowedApplications " + strings.Join(disallowed, ",")
		// -W is deliberately omitted: the system then shows a consent dialog, and
		// waiting for a human inside a tool call is how a call becomes a timeout.
		out, aerr := dev.RunCommand(startCmd)

		return textResult(map[string]any{
			"device":      rec.Serial,
			"endpoint":    rec.Endpoint,
			"relay_port":  session.Port,
			"reverse":     remote + " -> tcp:" + strconv.Itoa(port),
			"activity_ok": aerr == nil,
			"output":      strings.TrimSpace(out),
			"prompt":      "TAP OK ON THE PHONE SCREEN NOW: VpnService.prepare() needs human consent",
		})
	})

	handle(srv, &mcp.Tool{
		Name:        "device_tether_stop",
		Description: "Stop tethering: kill VPN, remove reverse forward, stop relay.",
	}, func(ctx context.Context, args struct {
		Device string `json:"device" jsonschema:"Device serial"`
	}) (*mcp.CallToolResult, error) {
		dev, rec, err := s.mgr.ResolveDevice(args.Device)
		if err != nil {
			return errResult("resolve device: %v", err)
		}
		remSpec, _ := parseForwardSpec("localabstract:gnirehtet")
		_ = dev.ReverseRemove(remSpec)
		s.tether.Stop(rec.Serial)
		out, _ := dev.RunCommand("am force-stop com.genymobile.gnirehtet")
		return textResult(map[string]any{
			"device":  rec.Serial,
			"stopped": true,
			"output":  strings.TrimSpace(out),
		})
	})

}

func parseForwardSpec(spec string) (adb.ForwardSpec, error) {
	parts := strings.SplitN(spec, ":", 2)
	if len(parts) != 2 {
		return adb.ForwardSpec{}, fmt.Errorf("spec must be proto:val, got %q", spec)
	}
	switch strings.ToLower(parts[0]) {
	case "tcp":
		return adb.ForwardSpec{Protocol: adb.FProtocolTcp, PortOrName: parts[1]}, nil
	case "localabstract":
		return adb.ForwardSpec{Protocol: adb.FProtocolAbstract, PortOrName: parts[1]}, nil
	case "localreserved":
		return adb.ForwardSpec{Protocol: adb.FProtocolReserved, PortOrName: parts[1]}, nil
	case "localfilesystem":
		return adb.ForwardSpec{Protocol: adb.FProtocolFilesystem, PortOrName: parts[1]}, nil
	default:
		return adb.ForwardSpec{}, fmt.Errorf("unsupported protocol %q", parts[0])
	}
}

// shellQuote wraps a command in single quotes for safe embedding in a shell line.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// vpnNextStep reports whether the VPN is actually running or still waiting for a
// human, because those two states look identical from the outside otherwise.
//
// Installing a VpnService always requires a tap on a system dialog. No permission
// grant, no shell command, and no amount of adb can substitute for it -- the
// consent exists precisely so that a machine cannot do it silently.
func vpnNextStep(dev *adb.Device) string {
	iface, _ := dev.RunCommand("ip addr show tun0 2>/dev/null | head -1")
	if strings.Contains(iface, "tun0") {
		return "tun0 is up: the device is tethered, traffic now crosses the adb tunnel"
	}
	return "waiting for human consent: tap OK on the device's VPN connection dialog " +
		"(VpnService.prepare() requires it and adb cannot grant it)"
}
