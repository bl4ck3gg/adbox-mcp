package bl4ck3gg.adbox.agent;

import android.graphics.Rect;
import android.view.accessibility.AccessibilityNodeInfo;
import java.util.ArrayList;
import java.util.List;

/**
 * CompactProjection turns the live accessibility tree into a tiny JSON-ready list.
 *
 * It skips layout containers that have no text and no click handler, which removes
 * ~80% of nodes before any string is ever allocated. No XML is ever generated.
 */
public class CompactProjection {

    public static class Element {
        public String text;
        public String id;
        public String desc;
        public String cls;
        public int cx, cy;
        public int w, h;
        public boolean clickable;
        public boolean scrollable;
        public boolean focused;
        public boolean checked;
    }

    public static class Snapshot {
        public String packageName;
        public int totalNodes;
        public int keptNodes;
        public List<Element> elements = new ArrayList<>();
        public String digest;
    }

    public static Snapshot capture(AccessibilityNodeInfo root) {
        Snapshot snap = new Snapshot();
        if (root == null) {
            snap.packageName = "";
            snap.digest = "empty";
            return snap;
        }

        CharSequence pkg = root.getPackageName();
        snap.packageName = pkg != null ? pkg.toString() : "";

        int[] counter = new int[1];
        walk(root, snap.elements, counter, 0, 40);
        snap.totalNodes = counter[0];
        snap.keptNodes = snap.elements.size();

        // Stable coarse digest: package + count + first few labels
        StringBuilder sb = new StringBuilder();
        sb.append(snap.packageName).append("|").append(snap.keptNodes);
        for (int i = 0; i < Math.min(5, snap.elements.size()); i++) {
            Element e = snap.elements.get(i);
            String lbl = e.text != null ? e.text : (e.desc != null ? e.desc : e.id);
            if (lbl != null) sb.append(";").append(lbl);
        }
        snap.digest = sb.toString();
        return snap;
    }

    private static void walk(AccessibilityNodeInfo node, List<Element> out, int[] counter, int depth, int maxDepth) {
        if (node == null || depth > maxDepth) return;
        counter[0]++;

        Rect bounds = new Rect();
        node.getBoundsInScreen(bounds);
        boolean visible = bounds.width() > 0 && bounds.height() > 0;

        CharSequence text = node.getText();
        CharSequence desc = node.getContentDescription();
        String resId = node.getViewIdResourceName();
        boolean clickable = node.isClickable();
        boolean scrollable = node.isScrollable();

        boolean hasLabel = (text != null && text.length() > 0)
                || (desc != null && desc.length() > 0)
                || (resId != null && resId.length() > 0);

        if (visible && (clickable || scrollable || hasLabel)) {
            Element e = new Element();
            if (text != null && text.length() > 0) e.text = text.toString();
            if (desc != null && desc.length() > 0) e.desc = desc.toString();
            if (resId != null) {
                int colon = resId.indexOf(":id/");
                e.id = colon >= 0 ? resId.substring(colon + 4) : resId;
            }
            CharSequence cls = node.getClassName();
            if (cls != null) {
                String s = cls.toString();
                int dot = s.lastIndexOf('.');
                e.cls = dot >= 0 ? s.substring(dot + 1) : s;
            }
            e.cx = bounds.centerX();
            e.cy = bounds.centerY();
            e.w = bounds.width();
            e.h = bounds.height();
            e.clickable = clickable;
            e.scrollable = scrollable;
            e.focused = node.isFocused();
            e.checked = node.isChecked();
            out.add(e);
        }

        int count = node.getChildCount();
        for (int i = 0; i < count; i++) {
            AccessibilityNodeInfo child = node.getChild(i);
            if (child != null) {
                walk(child, out, counter, depth + 1, maxDepth);
                child.recycle();
            }
        }
    }
}
