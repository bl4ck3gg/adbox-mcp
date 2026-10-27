package bl4ck3gg.adbox.agent;

import android.app.UiAutomation;
import uiautomator.InstrumentShellWrapper;

import java.io.File;
import java.io.FileOutputStream;
import java.io.PrintStream;

/**
 * Entry point for uia2-mcp when executed through app_process.
 *
 *   CLASSPATH=/data/local/tmp/adbox-agent.apk app_process / bl4ck3gg.adbox.agent.Main [options]
 *
 * Options:
 *   --connect tcp:HOST:PORT        dial out and serve MCP on that stream (reverse)
 *   --connect localabstract:NAME   same, over an Android abstract socket
 *   --port N                       listen on N instead of 18080 (forward mode)
 *   (a bare number is accepted as --port, for the original argv shape)
 *
 * THE ORDER OF THE FIRST TWO CALLS IS LOAD-BEARING. `getContext()` is what bootstraps
 * ActivityThread and the main Looper under app_process; asking for UiAutomation before
 * that hangs forever, because the accessibility connection has nothing to deliver its
 * callbacks to. The upstream server does it in this order and the reason is not
 * documented anywhere -- it was found the hard way.
 *
 * STDOUT IS NOT RELIABLE HERE. Under a pipe or a redirect, Java's System.out is fully
 * buffered, so a process that dies mid-startup leaves an empty log and looks like it
 * never ran. Logging goes to a file through an auto-flushing stream so the last line
 * written is always the last thing that happened.
 */
public class Main {

    private static PrintStream log;

    public static void main(String... args) {
        log = openLog();

        String connect = null;
        String bridge = null;
        String abstractName = "gnirehtet";
        int port = 18080;

        for (int i = 0; i < args.length; i++) {
            String a = args[i];
            if ("bridge".equals(a)) {
                bridge = "yes";
            } else if (a.startsWith("--abstract=")) {
                abstractName = a.substring("--abstract=".length());
            } else if (a.startsWith("--connect=")) {
                connect = a.substring("--connect=".length());
            } else if ("--connect".equals(a)) {
                if (i + 1 < args.length) connect = args[++i];
            } else if (a.startsWith("--port=")) {
                port = parseInt(a.substring("--port=".length()), port);
            } else if ("--port".equals(a)) {
                if (i + 1 < args.length) port = parseInt(args[++i], port);
            } else if (a.matches("\\d+")) {
                port = parseInt(a, port);
            }
        }

        // The bridge entry point needs no Android services at all: it never touches
        // UiAutomation, so it can start before (and independently of) the accessibility
        // connection. That matters because it must work on a device where nothing has
        // granted accessibility access.
        if (bridge != null) {
            step("uia2-mcp bridge starting (tcp " + port + " <-> localabstract:" + abstractName + ")");
            try {
                new Bridge(port, 31417).run();
            } catch (Throwable t) {
                step("FATAL bridge " + t);
                t.printStackTrace(log);
                log.flush();
                System.exit(1);
            }
            return;
        }

        boolean reverse = connect != null && connect.length() > 0;
        step("uia2-mcp starting (" + (reverse ? "reverse -> " + connect : "listen on port " + port) + ")");

        try {
            InstrumentShellWrapper wrapper = InstrumentShellWrapper.getInstance();
            step("wrapper created");

            // Must precede anything that touches UiAutomation. See the class comment.
            wrapper.getContext();
            step("context ready");

            // UiAutomation is obtained and connected once, by us. UiDevice is
            // deliberately not used: it re-derives its own automation inside its
            // constructor and gives the caller no way to supply a connected one.
            UiAutomation automation = wrapper.getUiAutomation();
            if (automation == null) {
                step("FATAL UiAutomation could not be connected");
                System.exit(1);
            }
            step("UiAutomation connected");

            UiActions actions = new UiActions(automation);
            try {
                actions.wake();
                step("wake requested");
            } catch (Throwable t) {
                step("wake failed (continuing): " + t);
            }

            McpCore core = new McpCore(actions, automation);

            if (reverse) {
                new ReverseClient(core, connect).run();
                return;
            }

            McpHttpServer server = new McpHttpServer(port, core);
            server.start();
            int actual = server.getPort();
            step("listening on 127.0.0.1:" + actual + "  (POST /mcp)");

            // Write the ready beacon. The host does NOT read stdout (which is
            // prone to logging noise and buffering issues) -- it reads this file
            // once, gets the actual port, and deletes it.
            try {
                java.io.File readyFile = new java.io.File("/data/local/tmp/adbox-agent.ready");
                java.io.FileWriter fw = new java.io.FileWriter(readyFile);
                fw.write("port=" + actual + "\npid=" + android.os.Process.myPid() + "\n");
                fw.close();
            } catch (Throwable t) {
                step("warning: could not write ready file: " + t);
            }
            while (server.isAlive()) {
                Thread.sleep(1000);
            }
            step("server stopped");
        } catch (Throwable t) {
            step("FATAL " + t);
            t.printStackTrace(log);
            log.flush();
            System.exit(1);
        }
    }

    private static int parseInt(String s, int fallback) {
        try {
            return Integer.parseInt(s.trim());
        } catch (Exception e) {
            return fallback;
        }
    }

    /**
     * Opens the log beside the APK, falling back to stderr when that path is not
     * writable. Auto-flush is the point: a buffered log hides exactly the crash it
     * exists to record.
     */
    private static PrintStream openLog() {
        try {
            File f = new File("/data/local/tmp/uia2-mcp.log");
            return new PrintStream(new FileOutputStream(f, true), true, "UTF-8");
        } catch (Throwable t) {
            return System.err;
        }
    }

    /** Writes one timestamped line to the log, flushed immediately. */
    static void step(String msg) {
        if (log != null) {
            log.println("[adbox-agent] " + msg);
            log.flush();
        }
    }
}
