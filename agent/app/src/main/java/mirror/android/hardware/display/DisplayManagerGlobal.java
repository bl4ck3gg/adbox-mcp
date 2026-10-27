package mirror.android.hardware.display;

import android.os.IInterface;
import android.view.Display;
import mirror.MethodParams;
import mirror.RefClass;
import mirror.RefMethod;
import mirror.RefObject;
import mirror.RefStaticMethod;

/* loaded from: classes2.dex */
public class DisplayManagerGlobal {
    public static Class<?> TYPE = RefClass.load((Class<?>) DisplayManagerGlobal.class, "android.hardware.display.DisplayManagerGlobal");
    public static RefMethod<int[]> getDisplayIds;
    public static RefStaticMethod<Object> getInstance;

    @MethodParams({int.class})
    public static RefMethod<Display> getRealDisplay;
    public static RefObject<IInterface> mDm;
}
