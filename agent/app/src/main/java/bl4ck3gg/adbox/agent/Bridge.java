package bl4ck3gg.adbox.agent;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.net.ServerSocket;
import java.net.Socket;

/**
 * Bridge joins two TCP sockets on loopback: the internal one the app dials,
 * and the external one the host reaches through adb forward.
 *
 * App (VpnService) -> 127.0.0.1:31417 (internal)
 *                      | Bridge pumps bytes
 * Host (relay)    -> 127.0.0.1:31416 (external, forwarded)
 */
public class Bridge {

    private final int hostPort;
    private final int appPort;

    public Bridge(int hostPort, int appPort) {
        this.hostPort = hostPort;
        this.appPort = appPort;
    }

    public void run() throws IOException {
        ServerSocket appListener = new ServerSocket();
        appListener.setReuseAddress(true);
        appListener.bind(new InetSocketAddress("127.0.0.1", appPort));
        Main.step("bridge listening for app on 127.0.0.1:" + appPort);

        ServerSocket hostListener = new ServerSocket();
        hostListener.setReuseAddress(true);
        hostListener.bind(new InetSocketAddress("127.0.0.1", hostPort));
        Main.step("bridge listening for host on 127.0.0.1:" + hostPort);

        while (true) {
            Socket app = null;
            Socket host = null;
            try {
                // Wait for both sides to arrive, in parallel threads so neither blocks
                Main.step("bridge: waiting for app and host connections...");
                final Socket[] pair = new Socket[2];
                Thread tApp = new Thread(() -> {
                    try { pair[0] = appListener.accept(); } catch (Exception ignored) {}
                });
                Thread tHost = new Thread(() -> {
                    try { pair[1] = hostListener.accept(); } catch (Exception ignored) {}
                });
                tApp.start(); tHost.start();
                try { tApp.join(); tHost.join(); } catch (InterruptedException e) { break; }

                app = pair[0];
                host = pair[1];
                if (app == null || host == null) {
                    Main.step("bridge: one side failed to accept, retrying");
                    continue;
                }
                Main.step("bridge: both connected! pumping packets...");

                pump(app, host);
                Main.step("bridge: session finished");
            } catch (Throwable t) {
                Main.step("bridge error: " + t);
            } finally {
                closeQuietly(app);
                closeQuietly(host);
            }
        }
    }

    private static void pump(Socket a, Socket b) {
        Thread up = new Thread(() -> copy(a, b), "bridge-up");
        Thread down = new Thread(() -> copy(b, a), "bridge-down");
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
