package mr.robot.agent.shell;

import android.os.Build;

/**
 * Android API level constants (from scrcpy, trimmed for shell utilities).
 */
public final class AndroidVersions {

    private AndroidVersions() {
    }

    public static final int API_29_ANDROID_10 = Build.VERSION_CODES.Q;
    public static final int API_34_ANDROID_14 = Build.VERSION_CODES.UPSIDE_DOWN_CAKE;
    public static final int API_35_ANDROID_15 = Build.VERSION_CODES.VANILLA_ICE_CREAM;
}
