package mr.robot.agent.scrcpy.util;

import android.media.MediaCodec;
import android.media.MediaCodecInfo;
import android.media.MediaFormat;
import android.os.Build;

import mr.robot.agent.scrcpy.device.DisplayInfo;
import mr.robot.agent.scrcpy.device.Size;
import mr.robot.agent.scrcpy.wrappers.ServiceManager;

import org.json.JSONObject;

/**
 * 供 Socks5 内建 {@code r:svc:scrcpy.caps} 输出一行 JSON：主机经 smux 读取后收紧 {@code max_size}，无需 {@code shell.exec}。
 */
public final class ScrcpyCapsReporter {

    private ScrcpyCapsReporter() {
    }

    /**
     * 单行 JSON，末尾无换行（由调用方追加 {@code \n}）。
     */
    public static String buildJsonLine() {
        JSONObject root = new JSONObject();
        try {
            root.put("sdk", Build.VERSION.SDK_INT);
            Size probeSize = probeBaseSize();
            root.put("display_probe_w", probeSize.getWidth());
            root.put("display_probe_h", probeSize.getHeight());

            JSONObject avc = new JSONObject();
            MediaCodec codec = null;
            try {
                codec = MediaCodec.createEncoderByType(MediaFormat.MIMETYPE_VIDEO_AVC);
                avc.put("encoder", codec.getName());
                avc.put(
                        "profile_levels",
                        VideoEncoderCapabilities.buildAvcProfileLevelsJsonArray(
                                codec, MediaFormat.MIMETYPE_VIDEO_AVC));
                avc.put(
                        "recommended_baseline_level",
                        VideoEncoderCapabilities.pickHighestAvcBaselineLevelCapped(
                                codec,
                                MediaFormat.MIMETYPE_VIDEO_AVC,
                                MediaCodecInfo.CodecProfileLevel.AVCLevel41));
                int maxLong = VideoEncoderCapabilities.computeMaxSupportedLongSide(
                        codec, MediaFormat.MIMETYPE_VIDEO_AVC, probeSize);
                avc.put("max_long_side", maxLong);
            } catch (Throwable t) {
                avc.put("error", t.getClass().getSimpleName() + ": " + t.getMessage());
            } finally {
                if (codec != null) {
                    try {
                        codec.release();
                    } catch (Throwable ignored) {
                        // ignore
                    }
                }
            }
            root.put("video_avc", avc);
        } catch (Throwable t) {
            try {
                root.put("error", t.getClass().getSimpleName() + ": " + t.getMessage());
            } catch (Throwable ignored) {
                return "{\"error\":\"json_failed\"}";
            }
        }
        return root.toString();
    }

    private static Size probeBaseSize() {
        try {
            DisplayInfo di = ServiceManager.getDisplayManager().getDisplayInfo(0);
            if (di != null && di.getSize() != null) {
                return di.getSize().round8();
            }
        } catch (Throwable ignored) {
            // ignore
        }
        return new Size(1920, 1080).round8();
    }
}
