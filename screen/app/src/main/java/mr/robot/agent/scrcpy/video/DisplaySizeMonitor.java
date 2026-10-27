package mr.robot.agent.scrcpy.video;

import mr.robot.agent.scrcpy.AndroidVersions;
import mr.robot.agent.scrcpy.device.Device;
import mr.robot.agent.scrcpy.device.DisplayInfo;
import mr.robot.agent.scrcpy.device.Size;
import mr.robot.agent.scrcpy.util.Ln;
import mr.robot.agent.scrcpy.wrappers.DisplayManager;
import mr.robot.agent.scrcpy.wrappers.DisplayWindowListener;
import mr.robot.agent.scrcpy.wrappers.ServiceManager;

import android.content.res.Configuration;
import android.os.Build;
import android.os.Handler;
import android.os.HandlerThread;
import android.view.IDisplayWindowListener;

public class DisplaySizeMonitor {

    public interface Listener {
        void onDisplaySizeChanged();
    }

    // On Android 14+, 优先用 DisplayWindowListener；若设备上反射失败（ROM/签名差异），回退到 DisplayListener
    private static final boolean PREFER_DISPLAY_WINDOW_LISTENER = Build.VERSION.SDK_INT >= AndroidVersions.API_34_ANDROID_14;

    private DisplayManager.DisplayListenerHandle displayListenerHandle;
    private HandlerThread handlerThread;

    private IDisplayWindowListener displayWindowListener;
    /** 实际是否用了 DisplayWindowListener（若注册失败会回退到 DisplayListener） */
    private boolean usingDisplayWindowListener;

    private int displayId = Device.DISPLAY_ID_NONE;

    private Size sessionDisplaySize;

    private Listener listener;

    public void start(int displayId, Listener listener) {
        // Once started, the listener and the displayId must never change
        assert listener != null;
        this.listener = listener;

        assert this.displayId == Device.DISPLAY_ID_NONE;
        this.displayId = displayId;

        if (!PREFER_DISPLAY_WINDOW_LISTENER) {
            startWithDisplayListener(displayId);
            return;
        }
        displayWindowListener = new DisplayWindowListener() {
            @Override
            public void onDisplayConfigurationChanged(int eventDisplayId, Configuration newConfig) {
                if (Ln.isEnabled(Ln.Level.VERBOSE)) {
                    Ln.v("DisplaySizeMonitor: onDisplayConfigurationChanged(" + eventDisplayId + ")");
                }
                if (eventDisplayId == displayId) {
                    checkDisplaySizeChanged();
                }
            }
        };
        int[] registered = ServiceManager.getWindowManager().registerDisplayWindowListener(displayWindowListener);
        if (registered != null) {
            usingDisplayWindowListener = true;
            return;
        }
        // 设备上 registerDisplayWindowListener 反射失败（NoSuchMethodException 等），回退到 DisplayListener
        displayWindowListener = null;
        startWithDisplayListener(displayId);
    }

    private void startWithDisplayListener(int displayId) {
        handlerThread = new HandlerThread("DisplayListener");
        handlerThread.start();
        Handler handler = new Handler(handlerThread.getLooper());
        displayListenerHandle = ServiceManager.getDisplayManager().registerDisplayListener(eventDisplayId -> {
            if (Ln.isEnabled(Ln.Level.VERBOSE)) {
                Ln.v("DisplaySizeMonitor: onDisplayChanged(" + eventDisplayId + ")");
            }
            if (eventDisplayId == displayId) {
                checkDisplaySizeChanged();
            }
        }, handler);
    }

    /**
     * Stop and release the monitor.
     * <p/>
     * It must not be used anymore.
     * It is ok to call this method even if {@link #start(int, Listener)} was not called.
     */
    public void stopAndRelease() {
        if (usingDisplayWindowListener && displayWindowListener != null) {
            ServiceManager.getWindowManager().unregisterDisplayWindowListener(displayWindowListener);
            displayWindowListener = null;
            usingDisplayWindowListener = false;
        }
        if (displayListenerHandle != null) {
            ServiceManager.getDisplayManager().unregisterDisplayListener(displayListenerHandle);
            displayListenerHandle = null;
        }
        if (handlerThread != null) {
            handlerThread.quitSafely();
            handlerThread = null;
        }
    }

    private synchronized Size getSessionDisplaySize() {
        return sessionDisplaySize;
    }

    public synchronized void setSessionDisplaySize(Size sessionDisplaySize) {
        this.sessionDisplaySize = sessionDisplaySize;
    }

    private void checkDisplaySizeChanged() {
        DisplayInfo di = ServiceManager.getDisplayManager().getDisplayInfo(displayId);
        if (di == null) {
            Ln.w("DisplayInfo for " + displayId + " cannot be retrieved");
            // We can't compare with the current size, so reset unconditionally
            if (Ln.isEnabled(Ln.Level.VERBOSE)) {
                Ln.v("DisplaySizeMonitor: requestReset(): " + getSessionDisplaySize() + " -> (unknown)");
            }
            setSessionDisplaySize(null);
            listener.onDisplaySizeChanged();
        } else {
            Size size = di.getSize();

            // The field is hidden on purpose, to read it with synchronization
            @SuppressWarnings("checkstyle:HiddenField")
            Size sessionDisplaySize = getSessionDisplaySize(); // synchronized

            // .equals() also works if sessionDisplaySize == null
            if (!size.equals(sessionDisplaySize)) {
                // Reset only if the size is different
                if (Ln.isEnabled(Ln.Level.VERBOSE)) {
                    Ln.v("DisplaySizeMonitor: requestReset(): " + sessionDisplaySize + " -> " + size);
                }
                // Set the new size immediately, so that a future onDisplayChanged() event called before the asynchronous prepare()
                // considers that the current size is the requested size (to avoid a duplicate requestReset())
                setSessionDisplaySize(size);
                listener.onDisplaySizeChanged();
            } else if (Ln.isEnabled(Ln.Level.VERBOSE)) {
                Ln.v("DisplaySizeMonitor: Size not changed (" + size + "): do not requestReset()");
            }
        }
    }
}
