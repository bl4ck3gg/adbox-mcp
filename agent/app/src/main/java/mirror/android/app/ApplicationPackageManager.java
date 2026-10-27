package mirror.android.app;

import mirror.MethodReflectParams;
import mirror.RefClass;
import mirror.RefConstructor;

/* loaded from: classes2.dex */
public class ApplicationPackageManager {
    public static Class<?> TYPE = RefClass.load((Class<?>) ApplicationPackageManager.class, "android.app.ApplicationPackageManager");

    @MethodReflectParams({"android.app.ContextImpl", "android.content.pm.IPackageManager"})
    public static RefConstructor<?> ctor;
}
