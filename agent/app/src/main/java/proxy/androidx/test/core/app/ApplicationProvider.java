package proxy.androidx.test.core.app;

import android.content.Context;
import proxy.Bridge;

/* loaded from: classes2.dex */
public final class ApplicationProvider {
    private ApplicationProvider() {
    }

    public static Context getApplicationContext() {
        return Bridge.getInstance().getContext();
    }
}
