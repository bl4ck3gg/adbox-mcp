package mr.robot.agent.scrcpy.util;

import android.media.MediaCodec;
import android.media.MediaCodecInfo;
import android.media.MediaCodecList;
import android.os.Build;

import mr.robot.agent.scrcpy.AndroidVersions;
import mr.robot.agent.scrcpy.device.Size;

import org.json.JSONArray;
import org.json.JSONObject;

/**
 * 在 {@link android.media.MediaCodec#configure} 之前，用 {@link android.media.MediaCodecInfo.VideoCapabilities}
 * 查询当前选用编码器对宽高的支持情况，供采集端与 {@link android.media.MediaFormat} 使用一致尺寸。
 */
public final class VideoEncoderCapabilities {

    private VideoEncoderCapabilities() {
    }

    private static MediaCodecInfo findEncoderInfo(String codecName) {
        if (codecName == null) {
            return null;
        }
        MediaCodecList list = new MediaCodecList(MediaCodecList.ALL_CODECS);
        for (MediaCodecInfo info : list.getCodecInfos()) {
            if (codecName.equals(info.getName())) {
                return info;
            }
        }
        return null;
    }

    /**
     * 在保持与 {@code videoSize} 相同纵横比的前提下（通过 {@link Size#limit}），求最大的长边长度（8 对齐），
     * 使得 {@link android.media.MediaCodecInfo.VideoCapabilities#isSizeSupported(int, int)} 为 true。
     *
     * @return 建议传给 {@link mr.robot.agent.scrcpy.video.SurfaceCapture#setMaxSize(int)} 的值；无法探测或与当前一致时返回 {@code videoSize.getMax()}；
     *         若探测到当前尺寸已超出能力且无法找到任何可行尺寸则返回 {@code 0}（调用方勿改 maxSize）。
     */
    public static int computeMaxSupportedLongSide(MediaCodec codec, String mimeType, Size videoSize) {
        if (codec == null || mimeType == null) {
            return videoSize.getMax();
        }
        Size rounded = videoSize.round8();
        try {
            MediaCodecInfo info = findEncoderInfo(codec.getName());
            if (info == null || !info.isEncoder()) {
                return rounded.getMax();
            }
            MediaCodecInfo.CodecCapabilities caps = info.getCapabilitiesForType(mimeType);
            if (caps == null) {
                return rounded.getMax();
            }
            MediaCodecInfo.VideoCapabilities vc = caps.getVideoCapabilities();
            if (vc == null) {
                return rounded.getMax();
            }
            if (vc.isSizeSupported(rounded.getWidth(), rounded.getHeight())) {
                return rounded.getMax();
            }
            int hi = rounded.getMax();
            int lo = 128;
            int best = 0;
            while (lo <= hi) {
                int mid = ((lo + hi) / 2) & ~7;
                if (mid < 128) {
                    mid = 128;
                }
                Size cand = rounded.limit(mid).round8();
                if (vc.isSizeSupported(cand.getWidth(), cand.getHeight())) {
                    best = cand.getMax();
                    lo = cand.getMax() + 8;
                } else {
                    hi = mid - 8;
                }
            }
            return best;
        } catch (Throwable t) {
            Ln.w("VideoEncoderCapabilities: probe failed: " + t.getMessage());
            return rounded.getMax();
        }
    }

    /**
     * 从编码器 {@link MediaCodecInfo.CodecCapabilities#profileLevels} 中选取
     * {@link MediaCodecInfo.CodecProfileLevel#AVCProfileBaseline} 且 level≤{@code maxLevelInclusive} 的最大 level。
     * 无声明或无 Baseline 时回退：API≤23 → 3.1，否则 4.1（与历史行为一致）。
     */
    public static int pickHighestAvcBaselineLevelCapped(
            MediaCodec codec, String mimeType, int maxLevelInclusive) {
        int fallback = Build.VERSION.SDK_INT <= AndroidVersions.API_23_ANDROID_6_0
                ? MediaCodecInfo.CodecProfileLevel.AVCLevel31
                : MediaCodecInfo.CodecProfileLevel.AVCLevel41;
        if (codec == null || mimeType == null) {
            return fallback;
        }
        try {
            MediaCodecInfo info = findEncoderInfo(codec.getName());
            if (info == null || !info.isEncoder()) {
                return fallback;
            }
            MediaCodecInfo.CodecCapabilities caps = info.getCapabilitiesForType(mimeType);
            if (caps == null) {
                return fallback;
            }
            MediaCodecInfo.CodecProfileLevel[] pls = caps.profileLevels;
            if (pls == null || pls.length == 0) {
                return fallback;
            }
            int best = -1;
            for (MediaCodecInfo.CodecProfileLevel pl : pls) {
                if (pl.profile != MediaCodecInfo.CodecProfileLevel.AVCProfileBaseline) {
                    continue;
                }
                if (pl.level > maxLevelInclusive) {
                    continue;
                }
                if (pl.level > best) {
                    best = pl.level;
                }
            }
            if (best > 0) {
                return best;
            }
        } catch (Throwable t) {
            Ln.w("pickHighestAvcBaselineLevelCapped: " + t.getMessage());
        }
        return fallback;
    }

    /**
     * 编码器对 AVC 声明的 profile/level 列表（原始整型，便于主机或调试）；可能为空数组。
     */
    public static JSONArray buildAvcProfileLevelsJsonArray(MediaCodec codec, String mimeType) {
        JSONArray arr = new JSONArray();
        if (codec == null || mimeType == null) {
            return arr;
        }
        try {
            MediaCodecInfo info = findEncoderInfo(codec.getName());
            if (info == null) {
                return arr;
            }
            MediaCodecInfo.CodecCapabilities caps = info.getCapabilitiesForType(mimeType);
            if (caps == null || caps.profileLevels == null) {
                return arr;
            }
            for (MediaCodecInfo.CodecProfileLevel pl : caps.profileLevels) {
                JSONObject o = new JSONObject();
                o.put("profile", pl.profile);
                o.put("level", pl.level);
                arr.put(o);
            }
        } catch (Throwable t) {
            Ln.w("buildAvcProfileLevelsJsonArray: " + t.getMessage());
        }
        return arr;
    }
}
