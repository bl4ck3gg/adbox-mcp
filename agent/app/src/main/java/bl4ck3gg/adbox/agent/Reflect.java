package bl4ck3gg.adbox.agent;

import android.content.Context;
import android.os.IBinder;
import android.os.IInterface;

import org.json.JSONArray;
import org.json.JSONObject;

import java.lang.reflect.Constructor;
import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.lang.reflect.Modifier;

/**
 * Reflect is the escape hatch: one tool that reaches every hidden API, every
 * system service, and every private method on the device, without shipping a
 * wrapper class for each.
 *
 * WHY THIS EXISTS. The alternative is one hand-written wrapper per capability --
 * one for display power, one for the clipboard, one for the status bar, one for
 * rotation -- each needing a Java change, an APK rebuild, a push, and a
 * reconnect before a caller can use it. That is a fixed cost per unforeseen
 * need, paid forever, and the set of needs is not knowable in advance.
 *
 * The same reasoning termcp applies to a shell applies here: a narrow, well-typed
 * interface for what is common, plus one general evaluator for everything else.
 * ui_dump/ui_click stay typed because they are the hot path; the long tail comes
 * through here.
 *
 * WHAT IT CAN REACH. This runs under app_process as uid 2000, the same identity
 * as `adb shell`, which is why the platform's hidden APIs are reachable at all.
 * Callers get three doors:
 *
 *   static  -- Class.forName plus a static method, e.g. SurfaceControl
 *   service -- ServiceManager.getService(name) then <iface>$Stub.asInterface,
 *              the shape every system-service client uses internally
 *   new     -- construct an instance (needed for Context-bound managers)
 */
public final class Reflect {

    private static Method getServiceMethod;

    private Reflect() {
    }

    /**
     * Execute one request. The shape is deliberately flat rather than a nested
     * expression tree: a tree needs an evaluator with scoping rules, and every
     * real call so far has been one hop deep. When a caller needs a chain, they
     * make two calls and pass the intermediate value by hand.
     *
     *   {"kind":"static","class":"...","method":"...","args":[...]}
     *   {"kind":"service","service":"...","iface":"...","method":"...","args":[...]}
     *   {"kind":"new","class":"...","args":[...]}
     *   {"kind":"field","class":"...","field":"..."}
     */
    public static JSONObject eval(Context context, JSONObject req) throws Exception {
        String kind = req.optString("kind", "static");
        JSONArray args = req.optJSONArray("args");
        String methodName = req.optString("method", "");

        Object target = null;
        Class<?> cls;

        switch (kind) {
            case "service": {
                String service = req.getString("service");
                String iface = req.getString("iface");
                target = getService(service, iface);
                if (target == null) {
                    throw new IllegalStateException("service unavailable: " + service);
                }
                // Look methods up on the interface, not on the proxy: the proxy
                // class name is meaningless and its declared set varies.
                cls = Class.forName(iface);
                break;
            }
            case "new": {
                cls = Class.forName(req.getString("class"));
                Object[] resolved = resolveArgs(args);
                Constructor<?> ctor = findConstructor(cls, resolved);
                if (ctor == null) {
                    throw new NoSuchMethodException(
                            "no constructor on " + cls.getName() + " accepting " + describe(resolved));
                }
                ctor.setAccessible(true);
                return wrap(ctor.newInstance(resolved));
            }
            case "field": {
                cls = Class.forName(req.getString("class"));
                Field f = cls.getDeclaredField(req.getString("field"));
                f.setAccessible(true);
                if (!Modifier.isStatic(f.getModifiers())) {
                    throw new IllegalArgumentException("field " + f.getName() + " is not static");
                }
                return wrap(f.get(null));
            }
            case "static":
            default: {
                cls = Class.forName(req.getString("class"));
                break;
            }
        }

        if (methodName.isEmpty()) {
            return wrap(target);
        }

        Object[] resolved = resolveArgs(args);
        Method method = findMethod(cls, methodName, resolved);
        if (method == null) {
            throw new NoSuchMethodException("no method " + methodName + " on " + cls.getName()
                    + " accepting " + describe(resolved));
        }
        method.setAccessible(true);
        return wrap(method.invoke(target, resolved));
    }

    // ---- service plumbing -------------------------------------------------

    private static Object getService(String name, String iface) throws Exception {
        if (getServiceMethod == null) {
            Class<?> sm = Class.forName("android.os.ServiceManager");
            getServiceMethod = sm.getDeclaredMethod("getService", String.class);
            getServiceMethod.setAccessible(true);
        }
        IBinder binder = (IBinder) getServiceMethod.invoke(null, name);
        if (binder == null) {
            return null;
        }
        // Every AIDL interface exposes asInterface(IBinder) on its Stub. Going
        // through it rather than returning the raw binder is what makes the
        // result callable by name.
        Class<?> stub = Class.forName(iface + "$Stub");
        Method asInterface = stub.getDeclaredMethod("asInterface", IBinder.class);
        asInterface.setAccessible(true);
        return asInterface.invoke(null, binder);
    }

    // ---- argument coercion ------------------------------------------------
    //
    // JSON carries no types, so every argument arrives as a string, a number, a
    // boolean, or a marker for a value that cannot be expressed that way. This
    // turns the caller's intent back into the Java types a signature expects.

    private static Object[] resolveArgs(JSONArray args) throws Exception {
        if (args == null) {
            return new Object[0];
        }
        Object[] out = new Object[args.length()];
        for (int i = 0; i < args.length(); i++) {
            out[i] = resolveValue(args.get(i));
        }
        return out;
    }

    private static Object resolveValue(Object raw) throws Exception {
        if (raw == null || raw == JSONObject.NULL) {
            return null;
        }
        if (raw instanceof JSONObject) {
            JSONObject o = (JSONObject) raw;
            // A nested request: evaluate it and use its value as this argument.
            // This is how a caller passes a result from one call into the next.
            if (o.has("class") || o.has("service")) {
                return eval(null, o).opt("value");
            }
            String type = o.optString("type", "");
            switch (type) {
                case "null":
                    return null;
                case "int":
                    return o.getInt("value");
                case "long":
                    return o.getLong("value");
                case "float":
                    return (float) o.getDouble("value");
                case "boolean":
                    return o.getBoolean("value");
                case "string":
                    return o.getString("value");
                case "displayToken":
                    // The most common non-primitive argument in practice. Given
                    // by display id so callers never have to hold an IBinder.
                    return displayToken(o.optInt("display", 0));
                default:
                    return o.opt("value");
            }
        }
        return raw;
    }

    private static Object displayToken(int displayId) throws Exception {
        Class<?> sc = Class.forName("android.view.SurfaceControl");
        try {
            long[] ids = (long[]) sc.getMethod("getPhysicalDisplayIds").invoke(null);
            Method m = sc.getMethod("getPhysicalDisplayToken", long.class);
            if (ids != null && displayId >= 0 && displayId < ids.length) {
                return m.invoke(null, ids[displayId]);
            }
        } catch (NoSuchMethodException ignored) {
            // Below Android 10 there is a single built-in display.
        }
        return sc.getMethod("getInternalDisplayToken").invoke(null);
    }

    // ---- signature matching ----------------------------------------------
    //
    // Reflection needs exact parameter types and JSON cannot supply them, so
    // match by shape: the overload whose arity fits and whose parameters accept
    // the values at hand. This is what makes a universal evaluator usable
    // rather than a puzzle.

    private static Method findMethod(Class<?> cls, String name, Object[] args) {
        for (Method m : cls.getMethods()) {
            if (m.getName().equals(name) && m.getParameterCount() == args.length
                    && compatible(m.getParameterTypes(), args)) {
                return m;
            }
        }
        for (Method m : cls.getDeclaredMethods()) {
            if (m.getName().equals(name) && m.getParameterCount() == args.length
                    && compatible(m.getParameterTypes(), args)) {
                return m;
            }
        }
        return null;
    }

    private static Constructor<?> findConstructor(Class<?> cls, Object[] args) {
        for (Constructor<?> c : cls.getDeclaredConstructors()) {
            if (c.getParameterCount() == args.length && compatible(c.getParameterTypes(), args)) {
                return c;
            }
        }
        return null;
    }

    private static boolean compatible(Class<?>[] params, Object[] args) {
        for (int i = 0; i < params.length; i++) {
            Class<?> p = params[i];
            Object a = args[i];
            if (a == null) {
                if (p.isPrimitive()) {
                    return false;
                }
                continue;
            }
            Class<?> boxed = box(p);
            if (!boxed.isInstance(a)) {
                // Numbers widen; everything else must match exactly.
                if (a instanceof Number && Number.class.isAssignableFrom(boxed)) {
                    continue;
                }
                return false;
            }
        }
        return true;
    }

    private static Class<?> box(Class<?> c) {
        if (!c.isPrimitive()) return c;
        if (c == int.class) return Integer.class;
        if (c == long.class) return Long.class;
        if (c == float.class) return Float.class;
        if (c == double.class) return Double.class;
        if (c == boolean.class) return Boolean.class;
        if (c == byte.class) return Byte.class;
        if (c == short.class) return Short.class;
        if (c == char.class) return Character.class;
        return c;
    }

    private static String describe(Object[] args) {
        StringBuilder sb = new StringBuilder("(");
        for (int i = 0; i < args.length; i++) {
            if (i > 0) sb.append(", ");
            sb.append(args[i] == null ? "null" : args[i].getClass().getSimpleName());
        }
        return sb.append(")").toString();
    }

    // ---- result conversion -----------------------------------------------

    /**
     * Convert a Java value into something the JSON layer can carry.
     *
     * Objects that cannot be expressed as JSON keep their class name alongside
     * a string form, because the caller's next call has to know what type it is
     * holding -- an IInterface's toString alone does not say.
     */
    public static JSONObject wrap(Object value) throws Exception {
        JSONObject out = new JSONObject();
        if (value == null) {
            out.put("value", JSONObject.NULL);
            return out;
        }
        out.put("type", value.getClass().getName());

        if (value instanceof JSONArray || value instanceof JSONObject) {
            out.put("value", value);
        } else if (value instanceof int[]) {
            out.put("value", toArray((int[]) value));
        } else if (value instanceof long[]) {
            out.put("value", toArray((long[]) value));
        } else if (value instanceof Object[]) {
            JSONArray a = new JSONArray();
            for (Object o : (Object[]) value) {
                a.put(String.valueOf(o));
            }
            out.put("value", a);
        } else if (value instanceof Number || value instanceof Boolean || value instanceof String) {
            out.put("value", value);
        } else if (value instanceof CharSequence) {
            out.put("value", value.toString());
        } else if (value instanceof IBinder) {
            out.put("value", "binder:" + value);
        } else if (value instanceof IInterface) {
            out.put("value", value.toString());
        } else {
            out.put("value", String.valueOf(value));
        }
        return out;
    }

    private static JSONArray toArray(long[] values) {
        JSONArray a = new JSONArray();
        for (long v : values) a.put(v);
        return a;
    }

    private static JSONArray toArray(int[] values) {
        JSONArray a = new JSONArray();
        for (int v : values) a.put(v);
        return a;
    }
}
