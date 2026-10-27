package bl4ck3gg.adbox.agent;

/**
 * A minimal JSON writer, because the only JSON this server produces is its own and
 * pulling Jackson in for that would cost more than the whole protocol layer.
 */
public final class Json {
    private Json() {}

    public static String esc(String s) {
        if (s == null) return "null";
        StringBuilder b = new StringBuilder(s.length() + 8);
        b.append('"');
        for (int i = 0; i < s.length(); i++) {
            char c = s.charAt(i);
            switch (c) {
                case '"': b.append("\\\""); break;
                case '\\': b.append("\\\\"); break;
                case '\n': b.append("\\n"); break;
                case '\r': b.append("\\r"); break;
                case '\t': b.append("\\t"); break;
                default:
                    if (c < 0x20) {
                        b.append(String.format("\\u%04x", (int) c));
                    } else {
                        b.append(c);
                    }
            }
        }
        b.append('"');
        return b.toString();
    }

    public static String str(String s) {
        return s == null ? "null" : esc(s);
    }
}
