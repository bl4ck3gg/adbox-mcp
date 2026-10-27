package bl4ck3gg.adbox.agent;

import android.app.UiAutomation;
import android.os.SystemClock;
import android.view.InputDevice;
import android.view.KeyEvent;
import android.view.MotionEvent;

/**
 * UiActions drives the device through UiAutomation directly.
 *
 * WHY NOT UiDevice. UiDevice is a convenience wrapper that re-derives its own
 * UiAutomation on construction -- it calls instrumentation.getUiAutomation(),
 * probes it with getServiceInfo(), and throws if anything about that dance is off.
 * Under app_process there is no InstrumentationRegistry, and the retry/reconnect
 * logic it layers on top is not something a caller can control. Owning the
 * UiAutomation ourselves removes that entire class of failure, and the primitives
 * underneath are both fewer and more precise: performAction(ACTION_CLICK) targets a
 * node, where a coordinate tap only targets a pixel.
 */
public class UiActions {

    private final UiAutomation automation;

    public UiActions(UiAutomation automation) {
        this.automation = automation;
    }

    /** Wakes the screen. Returns false when the device already refuses the key. */
    public boolean wake() {
        try {
            long now = SystemClock.uptimeMillis();
            automation.injectInputEvent(keyEvent(now, now, KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_WAKEUP), true);
            automation.injectInputEvent(keyEvent(now, now, KeyEvent.ACTION_UP, KeyEvent.KEYCODE_WAKEUP), true);
            return true;
        } catch (Throwable t) {
            return false;
        }
    }

    /**
     * Taps a point.
     *
     * injectInputEvent is asynchronous on the framework side, so a caller that reads
     * the screen immediately afterwards can race the tap. The short settle here is
     * the cheapest way to keep that race out of callers' hands.
     */
    public boolean tap(int x, int y) {
        long down = SystemClock.uptimeMillis();
        try {
            automation.injectInputEvent(motion(down, down, MotionEvent.ACTION_DOWN, x, y), true);
            automation.injectInputEvent(motion(down, down + 60, MotionEvent.ACTION_UP, x, y), true);
            return true;
        } catch (Throwable t) {
            return false;
        }
    }

/**
     * Long-presses a point.
     *
     * There is no framework primitive for a long press, only the timing of the
     * release: the gesture recogniser decides from how long the DOWN lived. So the
     * duration is real wall-clock time spent between the two events rather than a
     * parameter handed to the system.
     */
    public boolean longClick(int x, int y, long durationMs) {
        if (durationMs <= 0) {
            durationMs = 800; // the platform's own default threshold is 500ms
        }
        long down = SystemClock.uptimeMillis();
        try {
            automation.injectInputEvent(motion(down, down, MotionEvent.ACTION_DOWN, x, y), true);
            SystemClock.sleep(durationMs);
            automation.injectInputEvent(motion(down, down + durationMs, MotionEvent.ACTION_UP, x, y), true);
            return true;
        } catch (Throwable t) {
            return false;
        }
    }

    /** Swipes between two points over the given duration. */
    public boolean swipe(int x1, int y1, int x2, int y2, long durationMs) {
        long down = SystemClock.uptimeMillis();
        try {
            automation.injectInputEvent(motion(down, down, MotionEvent.ACTION_DOWN, x1, y1), true);
            int steps = 12;
            for (int i = 1; i <= steps; i++) {
                float t = i / (float) steps;
                int x = Math.round(x1 + (x2 - x1) * t);
                int y = Math.round(y1 + (y2 - y1) * t);
                automation.injectInputEvent(
                        motion(down, down + (long) (durationMs * t), MotionEvent.ACTION_MOVE, x, y), true);
            }
            automation.injectInputEvent(motion(down, down + durationMs, MotionEvent.ACTION_UP, x2, y2), true);
            return true;
        } catch (Throwable t) {
            return false;
        }
    }

    /** Sends a global action such as back, home, or recents. */
    public boolean globalAction(int action) {
        try {
            return automation.performGlobalAction(action);
        } catch (Throwable t) {
            return false;
        }
    }

    /** Sends a single key down/up pair. */
    public boolean key(int keyCode) {
        try {
            long now = SystemClock.uptimeMillis();
            automation.injectInputEvent(keyEvent(now, now, KeyEvent.ACTION_DOWN, keyCode), true);
            automation.injectInputEvent(keyEvent(now, now, KeyEvent.ACTION_UP, keyCode), true);
            return true;
        } catch (Throwable t) {
            return false;
        }
    }

    /** Builds one key event. The two-argument obtain() defaults to a reasonable source. */
    private static KeyEvent keyEvent(long downTime, long eventTime, int action, int keyCode) {
        return new KeyEvent(downTime, eventTime, action, keyCode, 0);
    }

    /** Builds one touch event, tagged as coming from the touchscreen. */
    private static MotionEvent motion(long downTime, long eventTime, int action, int x, int y) {
        MotionEvent e = MotionEvent.obtain(downTime, eventTime, action, x, y, 0);
        e.setSource(InputDevice.SOURCE_TOUCHSCREEN);
        return e;
    }
}
