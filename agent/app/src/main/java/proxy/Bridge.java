package proxy;

import android.content.Context;
import android.os.Looper;
import mirror.android.app.ActivityThread;

public class Bridge {
    private static Bridge sInstance;
    Object activityThread;
    Context context;

    private Bridge() {
    }

    public static Bridge getInstance() {
        if (sInstance == null) {
            sInstance = new Bridge();
        }
        return sInstance;
    }

    public Context getContext() {
        if (this.activityThread == null) {
            Looper.prepareMainLooper();
            this.activityThread = ActivityThread.systemMain.call(new Object[0]);
            this.context = ActivityThread.getSystemContext.call(this.activityThread, new Object[0]);
        }
        return this.context;
    }
}
