package mirror.android.os;

import android.os.IBinder;
import android.os.IInterface;
import mirror.MethodParams;
import mirror.RefClass;
import mirror.RefStaticMethod;

/* loaded from: classes2.dex */
public class IBatteryPropertiesRegistrar {
    public static Class<?> TYPE = RefClass.load((Class<?>) IBatteryPropertiesRegistrar.class, "android.os.IBatteryPropertiesRegistrar");
    public static RefStaticMethod<Integer> getProperty;

    public static class Stub {
        public static Class<?> TYPE = RefClass.load((Class<?>) Stub.class, "android.os.IBatteryPropertiesRegistrar$Stub");

        @MethodParams({IBinder.class})
        public static RefStaticMethod<IInterface> asInterface;
    }
}
