package bl4ck3gg.adbox.agent;

import android.app.UiAutomation;
import android.os.SystemClock;
import android.view.accessibility.AccessibilityNodeInfo;
import org.json.JSONArray;
import org.json.JSONObject;

/**
 * McpCore is the protocol, independent of how the bytes arrive.
 *
 * The same class serves a listening HTTP socket and a reverse connection that dialled
 * out to its peer, because a JSON-RPC frame does not know or care which direction the
 * TCP handshake went. Keeping the transport out of the tool logic is what makes the
 * reverse mode a transport change rather than a rewrite.
 */
public class McpCore {

    private static final long CLICK_SETTLE_MS = 3000;

    private final UiActions actions;
    private final UiAutomation automation;

    public McpCore(UiActions actions, UiAutomation automation) {
        this.actions = actions;
        this.automation = automation;
    }

    /**
     * Handles one JSON-RPC frame.
     *
     * Returns the response text, or null when the frame was a notification and carries
     * no reply. Callers on a stream must not write anything for null.
     */
    public String handle(String requestJson) throws Exception {
        JSONObject req = new JSONObject(requestJson);
        Object id = req.opt("id");
        String method = req.optString("method", "");
        JSONObject params = req.optJSONObject("params");
        if (params == null) params = new JSONObject();

        if (id == null || "notifications/initialized".equals(method)) {
            return null;
        }

        JSONObject resp = new JSONObject();
        resp.put("jsonrpc", "2.0");
        resp.put("id", id);

        switch (method) {
            case "initialize":
                resp.put("result", new JSONObject()
                        .put("protocolVersion", "2024-11-05")
                        .put("capabilities", new JSONObject().put("tools", new JSONObject().put("listChanged", false)))
                        .put("serverInfo", new JSONObject().put("name", "uia2-mcp").put("version", "0.1.0")));
                break;
            case "tools/list":
                resp.put("result", new JSONObject().put("tools", new JSONArray(TOOLS_JSON)));
                break;
            case "tools/call":
                resp.put("result", executeTool(params.optString("name"), optObject(params, "arguments")));
                break;
            default:
                resp.put("error", new JSONObject().put("code", -32601).put("message", "Method not found: " + method));
        }
        return resp.toString();
    }

    private JSONObject optObject(JSONObject o, String key) {
        JSONObject v = o.optJSONObject(key);
        return v == null ? new JSONObject() : v;
    }

    /**
     * The tool catalogue, written as the JSON it is, because building this with
     * put(...) chains hides the shape: four tools nested five levels deep read as
     * thirty lines of punctuation and no structure.
     */
    private static final String TOOLS_JSON = """
            [
              {
                "name": "ui_dump",
                "description": "Capture the screen hierarchy as compact actionable elements (~90% smaller than raw XML): text, id, desc, class, center point, and whether each is clickable.",
                "inputSchema": {"type": "object", "additionalProperties": false}
              },
              {
                "name": "ui_click",
                "description": "Click an element by text, id, or coordinates, then report whether anything observable changed (verified, changes, settle_ms). Prefer text or id over raw coordinates.",
                "inputSchema": {
                  "type": "object",
                  "properties": {
                    "text": {"type": "string", "description": "Substring of the element text or content description"},
                    "id":   {"type": "string", "description": "Resource id, short form (e.g. login_button)"},
                    "x":    {"type": "integer", "description": "Raw x coordinate"},
                    "y":    {"type": "integer", "description": "Raw y coordinate"}
                  },
                  "additionalProperties": false
                }
              },
              {
                "name": "ui_input",
                "description": "Inject input natively through the accessibility service, without spawning adb shell.",
                "inputSchema": {
                  "type": "object",
                  "properties": {
                    "action":    {"type": "string", "enum": ["text", "key", "swipe"]},
                    "text":      {"type": "string", "description": "ASCII text to type, for action=text"},
                    "key":       {"type": "string", "description": "home, back, enter, recent, or a numeric keycode"},
                    "direction": {"type": "string", "enum": ["up", "down", "left", "right"], "description": "For action=swipe"}
                  },
                  "required": ["action"],
                  "additionalProperties": false
                }
              },
              {
                "name": "ui_wait",
                "description": "Poll in-process until text appears or vanishes, answering once instead of asking again next turn.",
                "inputSchema": {
                  "type": "object",
                  "properties": {
                    "text":            {"type": "string", "description": "Text to watch for"},
                    "until":           {"type": "string", "enum": ["appear", "vanish"], "description": "Default appear"},
                    "timeout_seconds": {"type": "number", "description": "Default 5.0"}
                  },
                  "required": ["text"],
                  "additionalProperties": false
                }
              }
            ]
            """;

    private JSONObject executeTool(String name, JSONObject args) throws Exception {
        JSONObject content = new JSONObject();
        content.put("type", "text");

        switch (name == null ? "" : name) {
            case "ui_dump": {
                content.put("text", snapshotToJson(takeSnapshot()).toString());
                break;
            }
            case "ui_click": {
                content.put("text", doClick(args).toString());
                break;
            }
            case "ui_input": {
                content.put("text", new JSONObject().put("ok", doInput(args)).toString());
                break;
            }
            case "device_eval": {
                return Reflect.eval(null, args);
            }
            case "ui_wait": {
                content.put("text", doWait(args).toString());
                break;
            }
            default:
                return error("unknown tool: " + name);
        }
        return new JSONObject().put("content", new JSONArray().put(content));
    }

    private JSONObject doClick(JSONObject args) throws Exception {
        CompactProjection.Snapshot before = takeSnapshot();
        int x = args.optInt("x", -1);
        int y = args.optInt("y", -1);

        if (x < 0 || y < 0) {
            CompactProjection.Element target = findElement(before, args.optString("text", null), args.optString("id", null));
            if (target == null) {
                return new JSONObject().put("isError", true)
                        .put("content", new JSONArray().put(new JSONObject().put("type", "text")
                                .put("text", "element not found: text=" + args.optString("text", null)
                                        + ", id=" + args.optString("id", null))));
            }
            x = target.cx;
            y = target.cy;
        }

        boolean clicked = actions.tap(x, y);

        // A fixed sleep has to guess: 300 ms is too short for a browser cold start
        // (measured at ~2 s on a Note 9) and too long for a checkbox. Polling for the
        // first observable difference answers in milliseconds when the UI reacts
        // instantly, and still reports the truth when the target was inert.
        long start = SystemClock.uptimeMillis();
        long deadline = start + CLICK_SETTLE_MS;
        CompactProjection.Snapshot after = before;
        int polls = 0;
        while (SystemClock.uptimeMillis() < deadline) {
            polls++;
            after = takeSnapshot();
            if (!before.digest.equals(after.digest) || !before.packageName.equals(after.packageName)) {
                break;
            }
            SystemClock.sleep(polls < 4 ? 25 : (polls < 12 ? 75 : 150));
        }

        JSONArray changes = new JSONArray();
        if (!before.packageName.equals(after.packageName)) {
            changes.put("package: " + before.packageName + " -> " + after.packageName);
        }
        if (before.keptNodes != after.keptNodes) {
            changes.put("actionable elements: " + before.keptNodes + " -> " + after.keptNodes);
        }

        return new JSONObject()
                .put("clicked", clicked)
                .put("point", new JSONArray().put(x).put(y))
                .put("verified", !before.digest.equals(after.digest) || !before.packageName.equals(after.packageName))
                .put("settle_ms", SystemClock.uptimeMillis() - start)
                .put("polls", polls)
                .put("changes", changes);
    }

    private boolean doInput(JSONObject args) {
        String action = args.optString("action", "");
        switch (action) {
            case "text": {
                boolean any = false;
                for (char c : args.optString("text", "").toCharArray()) {
                    int code = charToKeyCode(c);
                    if (code != 0) any |= actions.key(code);
                }
                return any;
            }
            case "key": {
                switch (args.optString("key", "").toLowerCase()) {
                    case "home": return actions.globalAction(android.accessibilityservice.AccessibilityService.GLOBAL_ACTION_HOME);
                    case "back": return actions.globalAction(android.accessibilityservice.AccessibilityService.GLOBAL_ACTION_BACK);
                    case "recent": return actions.globalAction(android.accessibilityservice.AccessibilityService.GLOBAL_ACTION_RECENTS);
                    case "enter": return actions.key(android.view.KeyEvent.KEYCODE_ENTER);
                    default:
                        try {
                            return actions.key(Integer.parseInt(args.optString("key", "0")));
                        } catch (NumberFormatException e) {
                            return false;
                        }
                }
            }
            case "swipe": {
                // The screen size comes from the accessibility root's bounds rather
                // than a display API, so it works on any device without extra calls.
                int[] size = screenSize();
                int cx = size[0] / 2;
                int cy = size[1] / 2;
                int span = size[1] / 3;
                switch (args.optString("direction", "up")) {
                    case "up": return actions.swipe(cx, cy + span, cx, cy - span, 200);
                    case "down": return actions.swipe(cx, cy - span, cx, cy + span, 200);
                    case "left": return actions.swipe(cx + span, cy, cx - span, cy, 200);
                    case "right": return actions.swipe(cx - span, cy, cx + span, cy, 200);
                }
                return false;
            }
            default:
                return false;
        }
    }

    private JSONObject doWait(JSONObject args) throws Exception {
        String text = args.optString("text", "");
        String until = args.optString("until", "appear");
        long timeout = (long) (args.optDouble("timeout_seconds", 5.0) * 1000);
        long start = SystemClock.uptimeMillis();
        int polls = 0;
        boolean satisfied = false;

        while (SystemClock.uptimeMillis() - start < timeout) {
            polls++;
            boolean present = findElement(takeSnapshot(), text, null) != null;
            if ("appear".equals(until) && present) { satisfied = true; break; }
            if ("vanish".equals(until) && !present) { satisfied = true; break; }
            long elapsed = SystemClock.uptimeMillis() - start;
            SystemClock.sleep(elapsed < 300 ? 50 : (elapsed < 1500 ? 150 : 300));
        }

        return new JSONObject()
                .put("satisfied", satisfied)
                .put("until", until)
                .put("waited_ms", SystemClock.uptimeMillis() - start)
                .put("polls", polls);
    }

    /** Returns {width, height} of the active window, falling back to a common phone size. */
    private int[] screenSize() {
        android.view.accessibility.AccessibilityNodeInfo root = automation.getRootInActiveWindow();
        if (root != null) {
            try {
                android.graphics.Rect r = new android.graphics.Rect();
                root.getBoundsInScreen(r);
                if (r.width() > 0 && r.height() > 0) {
                    return new int[]{r.width(), r.height()};
                }
            } finally {
                root.recycle();
            }
        }
        return new int[]{1080, 1920};
    }

    private CompactProjection.Snapshot takeSnapshot() {
        AccessibilityNodeInfo root = automation.getRootInActiveWindow();
        try {
            return CompactProjection.capture(root);
        } finally {
            if (root != null) root.recycle();
        }
    }

    /**
     * Resolves a target element, preferring what a human would have aimed at.
     *
     * Plain substring matching over the tree in order picks the wrong node: asking for
     * a button label on an Android USB-debugging dialog matched the *title* ("Allow USB
     * debugging?") before the button, so the click landed on dead text and nothing
     * happened. Actionable nodes therefore win, and exact matches beat partial ones.
     */
    private CompactProjection.Element findElement(CompactProjection.Snapshot snap, String text, String id) {
        if (id != null && id.length() > 0) {
            for (CompactProjection.Element e : snap.elements) {
                if (id.equals(e.id)) return e;
            }
        }
        if (text == null || text.length() == 0) {
            return null;
        }

        CompactProjection.Element fallback = null;
        for (CompactProjection.Element e : snap.elements) {
            boolean hit = (e.text != null && e.text.contains(text))
                    || (e.desc != null && e.desc.contains(text));
            if (!hit) continue;

            boolean exact = text.equals(e.text) || text.equals(e.desc);
            if (e.clickable && exact) return e;
            if (fallback == null || (e.clickable && !fallback.clickable)) {
                fallback = e;
            }
        }
        return fallback;
    }

    private JSONObject snapshotToJson(CompactProjection.Snapshot snap) throws Exception {
        JSONObject obj = new JSONObject();
        obj.put("package", snap.packageName);
        obj.put("total_nodes", snap.totalNodes);
        obj.put("kept_nodes", snap.keptNodes);
        obj.put("digest", snap.digest);

        JSONArray arr = new JSONArray();
        for (CompactProjection.Element e : snap.elements) {
            JSONObject eo = new JSONObject();
            if (e.text != null) eo.put("text", e.text);
            if (e.id != null) eo.put("id", e.id);
            if (e.desc != null) eo.put("desc", e.desc);
            if (e.cls != null) eo.put("cls", e.cls);
            eo.put("cx", e.cx).put("cy", e.cy).put("w", e.w).put("h", e.h);
            if (e.clickable) eo.put("clickable", true);
            if (e.scrollable) eo.put("scrollable", true);
            if (e.checked) eo.put("checked", true);
            arr.put(eo);
        }
        obj.put("elements", arr);
        return obj;
    }

    private JSONObject error(String msg) throws Exception {
        return new JSONObject().put("isError", true)
                .put("content", new JSONArray().put(new JSONObject().put("type", "text").put("text", msg)));
    }

    private int charToKeyCode(char c) {
        if (c >= 'a' && c <= 'z') return 29 + (c - 'a');
        if (c >= 'A' && c <= 'Z') return 29 + (c - 'A');
        if (c >= '0' && c <= '9') return 7 + (c - '0');
        if (c == ' ') return 62;
        if (c == '\n') return 66;
        return 0;
    }
}
