package uiautomator;

import android.accessibilityservice.AccessibilityServiceInfo;
import android.app.Instrumentation;
import android.app.UiAutomation;
import android.content.Context;
import android.os.Build;
import android.os.HandlerThread;
import mirror.android.app.UiAutomationConnection;
import mirror.android.app.UiAutomationM;
import proxy.Bridge;

/* loaded from: classes2.dex */
public class InstrumentShellWrapper extends Instrumentation {
    private static final long CONNECT_TIMEOUT_MILLIS = 5000;
    private static final String HANDLER_THREAD_NAME = "UiAutomatorHandlerThread";
    private static InstrumentShellWrapper sInstance;
    private final HandlerThread mHandlerThread;
    private UiAutomation mUiAutomation;
    private Object mUiAutomationConnection;

    private InstrumentShellWrapper() {
        HandlerThread handlerThread = new HandlerThread(HANDLER_THREAD_NAME);
        this.mHandlerThread = handlerThread;
        this.mUiAutomationConnection = UiAutomationConnection.ctor.newInstance();
        handlerThread.start();
    }

    public static InstrumentShellWrapper getInstance() {
        if (sInstance == null) {
            sInstance = new InstrumentShellWrapper();
        }
        return sInstance;
    }

    @Override // android.app.Instrumentation
    public Context getContext() {
        return Bridge.getInstance().getContext();
    }

    @Override // android.app.Instrumentation
    public Context getTargetContext() {
        return Bridge.getInstance().getContext();
    }

    public void destroy() {
        if (!this.mHandlerThread.isAlive()) {
            throw new IllegalStateException("Already disconnected!");
        }
        this.mHandlerThread.quit();
    }

    public void setCompressedLayoutHierarchy(boolean compressed) {
        AccessibilityServiceInfo info = this.mUiAutomation.getServiceInfo();
        if (compressed) {
            info.flags &= -3;
        } else {
            info.flags |= 2;
        }
        this.mUiAutomation.setServiceInfo(info);
    }

    /** How long to wait for the accessibility service to actually bind after connect(). */
    private static final long CONNECT_WAIT_MILLIS = 3000;

    @Override // android.app.Instrumentation
    public synchronized UiAutomation getUiAutomation(int flags) {
        diag("getUiAutomation(flags=" + flags + ") cached=" + (this.mUiAutomation != null));
        if (this.mUiAutomation != null) {
            try {
                if (!mirror.android.app.UiAutomation.isDestroyed.call(this.mUiAutomation, new Object[0]).booleanValue()) {
                    if (isConnected(this.mUiAutomation)) {
                        diag("  cached automation is connected, reusing");
                        return this.mUiAutomation;
                    }
                    diag("  cached automation NOT connected, reconnecting");
                    // The object exists but the service never bound on the first
                    // attempt. Connect it again rather than building a second one:
                    // this connection is already registered with the accessibility
                    // manager, and registering it twice is an error the server
                    // reports as "UiAutomationService ... already registered!".
                    if (reconnect(this.mUiAutomation, flags)) {
                        return this.mUiAutomation;
                    }
                }
            } catch (Throwable ignored) {
                // fall through and build a fresh one
            }
            destroyQuietly(this.mUiAutomation);
            this.mUiAutomation = null;
        }

        if (this.mUiAutomationConnection == null) {
            diag("  connection is null, returning null");
            return null;
        }

        this.mUiAutomation = mirror.android.app.UiAutomation.ctor.newInstance(
                this.mHandlerThread.getLooper(), this.mUiAutomationConnection);
        diag("  fresh automation created, connecting...");
        if (!reconnect(this.mUiAutomation, flags)) {
            diag("  connect FAILED, returning null");
            destroyQuietly(this.mUiAutomation);
            this.mUiAutomation = null;
            return null;
        }
        diag("  connect OK");
        return this.mUiAutomation;
    }

    /**
     * Connects the automation and waits for the service to bind.
     *
     * UiAutomation.connect() returns as soon as the binder call is made, but
     * mService is filled in by a callback afterwards. Callers that touch the
     * automation immediately -- UiDevice's constructor asks for getServiceInfo()
     * on the very next line -- therefore see "UiAutomation not connected!".
     * Waiting here is what makes the connect synchronous from a caller's view.
     */
    private boolean reconnect(UiAutomation automation, int flags) {
        try {
            if (Build.VERSION.SDK_INT > 30) {
                mirror.android.app.UiAutomation.connectWithTimeout.call(automation,
                        Integer.valueOf(flags), Long.valueOf(CONNECT_TIMEOUT_MILLIS));
            } else {
                mirror.android.app.UiAutomation.connect.call(automation, Integer.valueOf(flags));
            }
        } catch (Throwable t) {
            return false;
        }

        diag("  connect() returned, waiting for bind...");
        long deadline = System.currentTimeMillis() + CONNECT_WAIT_MILLIS;
        while (System.currentTimeMillis() < deadline) {
            if (isConnected(automation)) {
                diag("  probe: connected after " + (System.currentTimeMillis() - (deadline - CONNECT_WAIT_MILLIS)) + "ms");
                return true;
            }
            try {
                Thread.sleep(50);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                return false;
            }
        }
        return false;
    }

    /** Probes connectivity by calling a method that throws when the service is absent. */
    private static boolean isConnected(UiAutomation automation) {
        try {
            automation.getServiceInfo();
            return true;
        } catch (Throwable t) {
            return false;
        }
    }

    private static void diag(String msg) {
        try {
            java.io.FileOutputStream f = new java.io.FileOutputStream("/data/local/tmp/uia2-mcp.log", true);
            try {
                f.write(("[uia2-mcp]   [wrapper] " + msg + "\n").getBytes("UTF-8"));
            } finally {
                f.close();
            }
        } catch (Throwable ignored) {
        }
    }

    private static void destroyQuietly(UiAutomation automation) {
        try {
            mirror.android.app.UiAutomation.destroy.call(automation, new Object[0]);
        } catch (Throwable ignored) {
        }
    }

    @Override // android.app.Instrumentation
    public UiAutomation getUiAutomation() {
        return getUiAutomation(0);
    }
}
