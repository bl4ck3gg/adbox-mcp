package mr.robot.agent.shell;

import mr.robot.agent.shell.util.Ln;
import mr.robot.agent.shell.wrappers.DisplayControl;
import mr.robot.agent.shell.wrappers.SurfaceControl;

import android.os.Build;
import android.os.IBinder;

/**
 * 亮屏/息屏（SurfaceFlinger power mode），供 {@code mr.robot.agent.Main screen-on|screen-off} 使用。
 * 逻辑来自原 scrcpy {@code Device.setDisplayPower}；投屏由宿主嵌入的官方 scrcpy-server.jar 负责，不再内置 Java Server。
 * <p>
 * 不包含 Android 15 上 {@code DisplayManager.requestDisplayPower} 分支：上游 scrcpy 以 {@code USE_ANDROID_15_DISPLAY_POWER=false} 长期关闭，此处保持一致，不引入 ServiceManager/DisplayManager 包装。
 */
public final class DisplayPower {

    public static final int DISPLAY_ID_NONE = -1;

    private DisplayPower() {
    }

    public static boolean setDisplayPower(int displayId, boolean on) {
        if (displayId == DISPLAY_ID_NONE) {
            throw new IllegalArgumentException("displayId");
        }

        boolean applyToMultiPhysicalDisplays = Build.VERSION.SDK_INT >= AndroidVersions.API_29_ANDROID_10;

        if (applyToMultiPhysicalDisplays
                && Build.VERSION.SDK_INT >= AndroidVersions.API_34_ANDROID_14
                && Build.BRAND.equalsIgnoreCase("honor")
                && SurfaceControl.hasGetBuildInDisplayMethod()) {
            applyToMultiPhysicalDisplays = false;
        }

        int mode = on ? SurfaceControl.POWER_MODE_NORMAL : SurfaceControl.POWER_MODE_OFF;
        if (applyToMultiPhysicalDisplays) {
            boolean useDisplayControl =
                    Build.VERSION.SDK_INT >= AndroidVersions.API_34_ANDROID_14 && !SurfaceControl.hasGetPhysicalDisplayIdsMethod();

            long[] physicalDisplayIds = useDisplayControl ? DisplayControl.getPhysicalDisplayIds() : SurfaceControl.getPhysicalDisplayIds();
            if (physicalDisplayIds == null) {
                Ln.e("Could not get physical display ids");
                return false;
            }

            boolean allOk = true;
            for (long physicalDisplayId : physicalDisplayIds) {
                IBinder binder = useDisplayControl ? DisplayControl.getPhysicalDisplayToken(
                        physicalDisplayId) : SurfaceControl.getPhysicalDisplayToken(physicalDisplayId);
                allOk &= SurfaceControl.setDisplayPowerMode(binder, mode);
            }
            return allOk;
        }

        IBinder d = SurfaceControl.getBuiltInDisplay();
        if (d == null) {
            Ln.e("Could not get built-in display");
            return false;
        }
        return SurfaceControl.setDisplayPowerMode(d, mode);
    }
}
