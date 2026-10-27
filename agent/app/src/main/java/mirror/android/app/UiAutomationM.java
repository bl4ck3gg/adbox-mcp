package mirror.android.app;

import mirror.MethodReflectParams;
import mirror.RefClass;
import mirror.RefConstructor;
import mirror.RefMethod;

/* loaded from: classes2.dex */
public class UiAutomationM {
    public static Class<?> TYPE = RefClass.load((Class<?>) UiAutomationM.class, "android.app.UiAutomation");
    public static RefMethod<Object> connect;

    @MethodReflectParams({"android.os.Looper", "android.app.IUiAutomationConnection"})
    public static RefConstructor<android.app.UiAutomation> ctor;
    public static RefMethod<Object> disconnect;
}
