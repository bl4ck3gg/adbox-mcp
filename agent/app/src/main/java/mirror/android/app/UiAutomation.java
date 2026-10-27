package mirror.android.app;

import mirror.MethodParams;
import mirror.MethodReflectParams;
import mirror.RefClass;
import mirror.RefConstructor;
import mirror.RefMethod;

/* loaded from: classes2.dex */
public class UiAutomation {
    public static Class<?> TYPE = RefClass.load((Class<?>) UiAutomation.class, "android.app.UiAutomation");

    @MethodParams({int.class})
    public static RefMethod<Object> connect;

    @MethodParams({int.class, long.class})
    public static RefMethod<Void> connectWithTimeout;

    @MethodReflectParams({"android.os.Looper", "android.app.IUiAutomationConnection"})
    public static RefConstructor<android.app.UiAutomation> ctor;
    public static RefMethod<Void> destroy;
    public static RefMethod<Object> disconnect;
    public static RefMethod<Integer> getFlags;
    public static RefMethod<Boolean> isDestroyed;
}
