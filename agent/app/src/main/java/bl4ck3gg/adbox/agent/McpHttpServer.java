package bl4ck3gg.adbox.agent;

import java.io.BufferedInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.nio.charset.StandardCharsets;

/**
 * McpHttpServer exposes {@link McpCore} over a minimal HTTP/1.1 endpoint.
 *
 * WHY NOT A FRAMEWORK. NanoHTTPD is a competent general-purpose embedded server:
 * it speaks every method, handles chunked bodies, serves files, and parses MIME
 * types. This server answers exactly one request shape -- POST /mcp with a JSON
 * body and a Content-Length -- from exactly one kind of client, over loopback or
 * an adb tunnel. Pulling in the rest cost more than the code that replaces it.
 *
 * What is deliberately not supported: chunked transfer encoding, keep-alive,
 * multipart, and TLS. A client that needs any of those is not this endpoint's
 * client, and failing that plainly beats half-supporting it.
 */
public class McpHttpServer {

    private static final int MAX_BODY = 1 << 20;
    private static final int MAX_HEADER = 1 << 16;

    private final McpCore core;
    private final int port;
    private ServerSocket server;

    public McpHttpServer(int port, McpCore core) {
        this.port = port;
        this.core = core;
    }

    public void start() throws IOException {
        server = new ServerSocket();
        // Bind to loopback: reachable through adb forward and nothing else. There is
        // no authentication here, so the bind address is the access control.
        server.setReuseAddress(true);
        server.bind(new InetSocketAddress("127.0.0.1", port));
        Thread acceptor = new Thread(this::acceptLoop, "uia2-mcp-http");
        acceptor.setDaemon(true);
        acceptor.start();
    }

    public boolean isAlive() {
        return server != null && !server.isClosed();
    }

    private void acceptLoop() {
        while (isAlive()) {
            try {
                Socket socket = server.accept();
                Thread t = new Thread(() -> handle(socket), "uia2-mcp-conn");
                t.setDaemon(true);
                t.start();
            } catch (IOException e) {
                if (!isAlive()) return;
                Main.step("accept failed: " + e);
            }
        }
    }

    /** Serves exactly one request per connection, then closes. */
    private void handle(Socket socket) {
        try (Socket s = socket) {
            s.setSoTimeout(30000);
            InputStream raw = new BufferedInputStream(s.getInputStream());
            OutputStream out = s.getOutputStream();

            String requestLine = readLine(raw, MAX_HEADER);
            if (requestLine == null || requestLine.isEmpty()) return;

            String[] parts = requestLine.split(" ");
            if (parts.length < 2) {
                respond(out, 400, "Bad Request", "");
                return;
            }
            String method = parts[0];
            String path = parts[1];

            int contentLength = 0;
            String header;
            while ((header = readLine(raw, MAX_HEADER)) != null && !header.isEmpty()) {
                int colon = header.indexOf(':');
                if (colon <= 0) continue;
                String name = header.substring(0, colon).trim();
                if (name.equalsIgnoreCase("content-length")) {
                    try {
                        contentLength = Integer.parseInt(header.substring(colon + 1).trim());
                    } catch (NumberFormatException ignored) {
                    }
                }
            }

            if (!path.equals("/mcp") && !path.equals("/")) {
                respond(out, 404, "Not Found", "");
                return;
            }

            if (!method.equals("POST")) {
                respond(out, 405, "Method Not Allowed", "");
                return;
            }
            if (contentLength <= 0) {
                respond(out, 411, "Length Required", "");
                return;
            }
            if (contentLength > MAX_BODY) {
                respond(out, 413, "Payload Too Large", "");
                return;
            }

            byte[] body = readExactly(raw, contentLength);
            String response = core.handle(new String(body, StandardCharsets.UTF_8));

            // A notification has no reply. Answering 202 keeps the HTTP contract
            // honest without inventing a body for something that has none.
            if (response == null) {
                respond(out, 202, "Accepted", "");
            } else {
                respond(out, 200, "OK", response);
            }
        } catch (IOException e) {
            Main.step("http connection failed: " + e);
        } catch (Throwable t) {
            Main.step("http handler error: " + t);
        }
    }

    private static void respond(OutputStream out, int code, String reason, String body) throws IOException {
        byte[] bytes = body.getBytes(StandardCharsets.UTF_8);
        String head = "HTTP/1.1 " + code + " " + reason + "\r\n"
                + "Content-Type: application/json; charset=utf-8\r\n"
                + "Content-Length: " + bytes.length + "\r\n"
                + "Connection: close\r\n"
                + "\r\n";
        out.write(head.getBytes(StandardCharsets.UTF_8));
        out.write(bytes);
        out.flush();
    }

    /** Reads one CRLF-terminated line, returning null at EOF. */
    private static String readLine(InputStream in, int max) throws IOException {
        StringBuilder sb = new StringBuilder(64);
        int b;
        while ((b = in.read()) != -1) {
            if (b == '\n') {
                int len = sb.length();
                if (len > 0 && sb.charAt(len - 1) == '\r') sb.setLength(len - 1);
                return sb.toString();
            }
            sb.append((char) b);
            if (sb.length() > max) throw new IOException("header line too long");
        }
        return sb.length() == 0 ? null : sb.toString();
    }

    private static byte[] readExactly(InputStream in, int length) throws IOException {
        byte[] buf = new byte[length];
        int read = 0;
        while (read < length) {
            int n = in.read(buf, read, length - read);
            if (n < 0) throw new IOException("unexpected EOF after " + read + " of " + length + " bytes");
            read += n;
        }
        return buf;
    }
}
