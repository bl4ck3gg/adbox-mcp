package bl4ck3gg.adbox.agent;

import org.json.JSONObject;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.net.ServerSocket;
import java.net.Socket;

/**
 * TetherController runs the bridge inside the agent process and commands the
 * VPN app through intents.
 *
 * WHY IT BELONGS IN THE AGENT. When the host coordinates this over ADB, it has
 * to choose ports, run multiple spawn commands, guess if the child stayed up,
 * and reconcile state over a high-latency link. Inside the agent, the bridge
 * runs on an ephemeral port assigned by the OS, the intent is issued locally,
 * and the external port is handed back to the caller in one MCP response.
 */
public final class TetherController {

    private static volatile TetherSession activeSession;

    public static synchronized JSONObject start(JSONObject args) throws Exception {
        if (activeSession != null && activeSession.isRunning()) {
            JSONObject res = new JSONObject();
            res.put("status", "already_running");
            res.put("bridge_port", activeSession.hostPort);
            return res;
        }

        // 1. Let the OS assign an ephemeral port for the internal app side,
        // and one for the external host side (or use the caller-provided hint).
        int hostPortHint = args.optInt("bridge_port", 0);
        ServerSocket appListener = bindEphemeral(0);
        ServerSocket hostListener = bindEphemeral(hostPortHint);

        int appPort = appListener.getLocalPort();
        int hostPort = hostListener.getLocalPort();

        TetherSession session = new TetherSession(appListener, hostListener, appPort, hostPort);
        session.start();
        activeSession = session;

        // 2. Launch the VPN Activity, telling it which port to dial.
        // Also pass the shell blacklist so adbd is never routed into the tunnel.
        String cmd = "am start -a com.genymobile.gnirehtet.START "
                + "-n bl4ck3gg.adbox.agent/com.genymobile.gnirehtet.GnirehtetActivity "
                + "--ei port " + appPort + " "
                + "--esa disallowedApplications com.android.shell";

        try {
            Runtime.getRuntime().exec(cmd);
        } catch (Throwable t) {
            session.stopSession();
            activeSession = null;
            throw new IOException("failed to launch VPN activity: " + t.getMessage(), t);
        }

        JSONObject res = new JSONObject();
        res.put("status", "started");
        res.put("bridge_port", hostPort);
        res.put("internal_port", appPort);
        return res;
    }

    public static synchronized JSONObject stop() throws Exception {
        JSONObject res = new JSONObject();
        if (activeSession != null) {
            activeSession.stopSession();
            activeSession = null;
            res.put("stopped", true);
        } else {
            res.put("stopped", false);
        }
        try {
            Runtime.getRuntime().exec("am start -a com.genymobile.gnirehtet.STOP "
                    + "-n bl4ck3gg.adbox.agent/com.genymobile.gnirehtet.GnirehtetActivity");
        } catch (Throwable ignored) {}
        return res;
    }

    private static ServerSocket bindEphemeral(int hint) throws IOException {
        ServerSocket ss = new ServerSocket();
        ss.setReuseAddress(true);
        if (hint <= 0) {
            ss.bind(new InetSocketAddress("127.0.0.1", 0));
            return ss;
        }
        try {
            ss.bind(new InetSocketAddress("127.0.0.1", hint));
            return ss;
        } catch (IOException e) {
            // Hint taken; fall back to OS assignment
            ss.close();
            ss = new ServerSocket();
            ss.setReuseAddress(true);
            ss.bind(new InetSocketAddress("127.0.0.1", 0));
            return ss;
        }
    }

    private static class TetherSession extends Thread {
        private final ServerSocket appListener;
        private final ServerSocket hostListener;
        final int appPort;
        final int hostPort;
        private volatile boolean running = true;

        TetherSession(ServerSocket appListener, ServerSocket hostListener, int appPort, int hostPort) {
            super("tether-bridge-coordinator");
            this.appListener = appListener;
            this.hostListener = hostListener;
            this.appPort = appPort;
            this.hostPort = hostPort;
            setDaemon(true);
        }

        boolean isRunning() {
            return running && !appListener.isClosed() && !hostListener.isClosed();
        }

        void stopSession() {
            running = false;
            try { appListener.close(); } catch (Throwable ignored) {}
            try { hostListener.close(); } catch (Throwable ignored) {}
        }

        @Override
        public void run() {
            while (running) {
                Socket app = null;
                Socket host = null;
                try {
                    final Socket[] pair = new Socket[2];
                    Thread tApp = new Thread(() -> {
                        try { pair[0] = appListener.accept(); } catch (Exception ignored) {}
                    });
                    Thread tHost = new Thread(() -> {
                        try { pair[1] = hostListener.accept(); } catch (Exception ignored) {}
                    });
                    tApp.start(); tHost.start();
                    tApp.join(); tHost.join();

                    app = pair[0];
                    host = pair[1];
                    if (app == null || host == null) continue;

                    pump(app, host);
                } catch (Throwable ignored) {
                } finally {
                    closeQuietly(app);
                    closeQuietly(host);
                }
            }
        }

        private void pump(Socket a, Socket b) {
            Thread up = new Thread(() -> copy(a, b), "tether-up");
            Thread down = new Thread(() -> copy(b, a), "tether-down");
            up.start(); down.start();
            try { up.join(); down.join(); } catch (InterruptedException ignored) {}
        }

        private static void copy(Socket from, Socket to) {
            try {
                InputStream in = from.getInputStream();
                OutputStream out = to.getOutputStream();
                byte[] buf = new byte[16384];
                int n;
                while ((n = in.read(buf)) >= 0) {
                    out.write(buf, 0, n);
                    out.flush();
                }
            } catch (IOException ignored) {}
        }

        private static void closeQuietly(Socket s) {
            if (s != null) {
                try { s.close(); } catch (IOException ignored) {}
            }
        }
    }
}
