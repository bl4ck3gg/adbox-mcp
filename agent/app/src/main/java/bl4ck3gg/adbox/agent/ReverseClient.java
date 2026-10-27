package bl4ck3gg.adbox.agent;

import android.net.LocalSocket;
import android.net.LocalSocketAddress;

import java.io.BufferedReader;
import java.io.Closeable;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.net.Socket;
import java.nio.charset.StandardCharsets;

/**
 * ReverseClient dials out to its peer instead of listening, then serves MCP over the
 * connection it opened.
 *
 * WHY THIS EXISTS. A listening socket on the device is three problems wearing a trench
 * coat:
 *
 *   1. PORT COLLISION. Whoever owns the port wins, and the loser gets a bind error at
 *      startup with no way to negotiate.
 *   2. EXPOSURE. A listener bound to the loopback inside an Android device is reachable
 *      by every other app on that device, and this server has no authentication to
 *      offer in its defence.
 *   3. LIFECYCLE. A listener waits for someone to arrive, so an idle device holds a
 *      socket open for hours and a stale one survives every restart of its peer.
 *
 * Dialling out inverts all three. The device picks a free local nothing; the peer owns
 * the listening socket and its policies; and when the peer disappears the device knows
 * immediately, because its own connection died.
 *
 * The transport is newline-delimited JSON-RPC on one bidirectional stream. That is the
 * shape a pipe already has, so a peer can hand this connection straight to a tool that
 * speaks MCP over stdio.
 *
 * Adb makes `tcp:` and `localabstract:` interchangeable at either end, so a caller can
 * pair `--connect localabstract:uia2-mcp` with `adb reverse localabstract:uia2-mcp
 * tcp:PORT` and no TCP port exists on the device at all.
 */
public class ReverseClient {

    private static final long RETRY_MIN_MS = 1000;
    private static final long RETRY_MAX_MS = 30000;
    private static final long DIAL_TIMEOUT_MS = 10000;

    private final McpCore core;
    private final String spec;

    public ReverseClient(McpCore core, String spec) {
        this.core = core;
        this.spec = spec;
    }

    /** Runs the dial-serve-reconnect loop forever. */
    public void run() {
        long backoff = RETRY_MIN_MS;
        while (true) {
            Link link = null;
            try {
                link = dial();
                Main.step("reverse connected to " + spec);
                backoff = RETRY_MIN_MS; // a successful dial resets the penalty
                serve(link);
                Main.step("reverse peer closed the stream");
            } catch (Throwable t) {
                Main.step("reverse error: " + t);
            } finally {
                if (link != null) link.close();
            }
            try {
                Main.step("reverse retrying in " + backoff + "ms");
                Thread.sleep(backoff);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                return;
            }
            backoff = Math.min(backoff * 2, RETRY_MAX_MS);
        }
    }

    /**
     * Reads one JSON-RPC frame per line and answers on the same stream.
     *
     * A line with no reply (a notification, or anything McpCore declines) writes nothing:
     * on a shared stream an empty frame would desynchronise the peer's reader.
     */
    private void serve(Link link) throws Exception {
        BufferedReader in = new BufferedReader(new InputStreamReader(link.in, StandardCharsets.UTF_8));
        OutputStream out = link.out;
        String line;
        while ((line = in.readLine()) != null) {
            line = line.trim();
            if (line.isEmpty()) continue;
            String response = core.handle(line);
            if (response == null) continue;
            out.write(response.getBytes(StandardCharsets.UTF_8));
            out.write('\n');
            out.flush();
        }
    }

    /** Opens the link named by the spec: `tcp:host:port` or `localabstract:name`. */
    private Link dial() throws Exception {
        if (spec.startsWith("localabstract:")) {
            String name = spec.substring("localabstract:".length());
            LocalSocket s = new LocalSocket();
            s.connect(new LocalSocketAddress(name, LocalSocketAddress.Namespace.ABSTRACT));
            return new Link(s.getInputStream(), s.getOutputStream(), s);
        }
        String rest = spec.startsWith("tcp:") ? spec.substring(4) : spec;
        int idx = rest.lastIndexOf(':');
        if (idx <= 0) {
            throw new IllegalArgumentException("reverse spec must be tcp:host:port or localabstract:name, got " + spec);
        }
        String host = rest.substring(0, idx);
        int port = Integer.parseInt(rest.substring(idx + 1));
        Socket s = new Socket();
        s.connect(new InetSocketAddress(host, port), (int) DIAL_TIMEOUT_MS);
        return new Link(s.getInputStream(), s.getOutputStream(), s);
    }

    /** A dialled connection, whichever of the two socket families produced it. */
    private static final class Link {
        final InputStream in;
        final OutputStream out;
        private final Closeable closeable;

        Link(InputStream in, OutputStream out, Closeable closeable) {
            this.in = in;
            this.out = out;
            this.closeable = closeable;
        }

        void close() {
            try {
                closeable.close();
            } catch (Exception ignored) {
            }
        }
    }
}
