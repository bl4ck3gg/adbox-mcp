package mr.robot.agent.scrcpy.video;

import mr.robot.agent.scrcpy.AsyncProcessor;
import mr.robot.agent.scrcpy.Options;
import mr.robot.agent.scrcpy.device.ConfigurationException;
import mr.robot.agent.scrcpy.device.Size;
import mr.robot.agent.scrcpy.device.Streamer;
import mr.robot.agent.scrcpy.util.Codec;
import mr.robot.agent.scrcpy.util.CodecOption;
import mr.robot.agent.scrcpy.util.CodecUtils;
import mr.robot.agent.scrcpy.util.IO;
import mr.robot.agent.scrcpy.util.VideoEncoderCapabilities;
import mr.robot.agent.scrcpy.util.Ln;
import mr.robot.agent.scrcpy.util.LogUtils;

import android.media.MediaCodec;
import android.media.MediaCodecInfo;
import android.media.MediaCodecList;
import android.media.MediaFormat;
import android.os.Looper;
import android.os.SystemClock;
import android.view.Surface;

import java.io.IOException;
import java.nio.ByteBuffer;
import java.util.List;
import java.util.Locale;
import java.util.concurrent.atomic.AtomicBoolean;

public class SurfaceEncoder implements AsyncProcessor, Runnable {

    /** 动态启停：为 false 时暂停采集与编码，不释放资源；由控制命令 SET_VIDEO_ENABLED 切换 */
    private final AtomicBoolean videoEnabled = new AtomicBoolean(false);
    private final Object enableLock = new Object();

    private static final int DEFAULT_I_FRAME_INTERVAL = 10; // seconds
    private static final String KEY_MAX_FPS_TO_ENCODER = "max-fps-to-encoder";

    // Keep the values in descending order
    private static final int[] MAX_SIZE_FALLBACK = {2560, 1920, 1600, 1280, 1024, 800, 640, 512, 480, 360};
    private static final int MAX_CONSECUTIVE_ERRORS = 3;

    private final SurfaceCapture capture;
    private final Streamer streamer;
    private final String encoderName;
    private final List<CodecOption> codecOptions;
    private final int videoBitRate;
    private final float maxFps;
    private final boolean downsizeOnError;

    private boolean firstFrameSent;
    private int consecutiveErrors;
    private boolean videoHeaderSent;

    private Thread thread;
    private TerminationListener terminationListener;
    private final AtomicBoolean stopped = new AtomicBoolean();

    private final CaptureReset reset = new CaptureReset();

    public SurfaceEncoder(SurfaceCapture capture, Streamer streamer, Options options) {
        this.capture = capture;
        this.streamer = streamer;
        this.videoBitRate = options.getVideoBitRate();
        this.maxFps = options.getMaxFps();
        this.codecOptions = options.getVideoCodecOptions();
        this.encoderName = options.getVideoEncoder();
        this.downsizeOnError = options.getDownsizeOnError();
    }

    /** 由控制命令 SET_VIDEO_ENABLED 调用，动态启停视频流（不重启进程） */
    public void setVideoEnabled(boolean enabled) {
        videoEnabled.set(enabled);
        synchronized (enableLock) {
            enableLock.notifyAll();
        }
    }

    private void streamCapture() throws IOException, ConfigurationException {
        Codec codec = streamer.getCodec();
        MediaCodec mediaCodec = createMediaCodec(codec, encoderName);
        MediaFormat format = createFormat(
                codec.getMimeType(), videoBitRate, maxFps, codecOptions, mediaCodec);

        capture.init(reset);

        try {
            while (!stopped.get()) {
                synchronized (enableLock) {
                    while (!videoEnabled.get() && !stopped.get()) {
                        try {
                            enableLock.wait();
                        } catch (InterruptedException e) {
                            Thread.currentThread().interrupt();
                            return;
                        }
                    }
                }
                if (stopped.get()) break;

                boolean alive;
                do {
                    if (!videoEnabled.get()) break;
                    reset.consumeReset();
                    capture.prepare();
                    Size size = applyEncoderCapabilityClamp(mediaCodec, codec, capture.getSize());
                    if (!videoHeaderSent) {
                        streamer.writeVideoHeader(size);
                        videoHeaderSent = true;
                    }
                    format.setInteger(MediaFormat.KEY_WIDTH, size.getWidth());
                    format.setInteger(MediaFormat.KEY_HEIGHT, size.getHeight());

                    Surface surface = null;
                    boolean mediaCodecStarted = false;
                    boolean captureStarted = false;
                    try {
                        mediaCodec.configure(format, null, null, MediaCodec.CONFIGURE_FLAG_ENCODE);
                        surface = mediaCodec.createInputSurface();

                        capture.start(surface);
                        captureStarted = true;

                        mediaCodec.start();
                        mediaCodecStarted = true;

                        reset.setRunningMediaCodec(mediaCodec);

                        if (stopped.get()) {
                            alive = false;
                        } else {
                            boolean resetRequested = reset.consumeReset();
                            if (!resetRequested) {
                                encode(mediaCodec, streamer, true);
                            }
                            alive = videoEnabled.get() && !stopped.get() && !capture.isClosed();
                        }
                    } catch (IllegalStateException | IllegalArgumentException | IOException e) {
                        if (IO.isBrokenPipe(e)) {
                            throw e;
                        }
                        Ln.e("Capture/encoding error: " + e.getClass().getName() + ": " + e.getMessage());
                        if (!prepareRetry(size)) {
                            throw e;
                        }
                        alive = true;
                    } finally {
                        reset.setRunningMediaCodec(null);
                        if (captureStarted) {
                            capture.stop();
                        }
                        if (mediaCodecStarted) {
                            try {
                                mediaCodec.stop();
                            } catch (IllegalStateException e) {
                                // ignore
                            }
                        }
                        mediaCodec.reset();
                        if (surface != null) {
                            surface.release();
                        }
                    }
                } while (alive && videoEnabled.get() && !stopped.get() && !capture.isClosed());
            }
        } finally {
            mediaCodec.release();
            capture.release();
        }
    }

    private boolean prepareRetry(Size currentSize) {
        if (firstFrameSent) {
            ++consecutiveErrors;
            if (consecutiveErrors >= MAX_CONSECUTIVE_ERRORS) {
                // Definitively fail
                return false;
            }

            // Wait a bit to increase the probability that retrying will fix the problem
            SystemClock.sleep(50);
            return true;
        }

        if (!downsizeOnError) {
            // Must fail immediately
            return false;
        }

        // Downsizing on error is only enabled if an encoding failure occurs before the first frame (downsizing later could be surprising)

        int newMaxSize = chooseMaxSizeFallback(currentSize);
        if (newMaxSize == 0) {
            // Must definitively fail
            return false;
        }

        boolean accepted = capture.setMaxSize(newMaxSize);
        if (!accepted) {
            return false;
        }

        // Retry with a smaller size
        Ln.i("Retrying with -m" + newMaxSize + "...");
        return true;
    }

    private static int chooseMaxSizeFallback(Size failedSize) {
        int currentMaxSize = Math.max(failedSize.getWidth(), failedSize.getHeight());
        for (int value : MAX_SIZE_FALLBACK) {
            if (value < currentMaxSize) {
                // We found a smaller value to reduce the video size
                return value;
            }
        }
        // No fallback, fail definitively
        return 0;
    }

    /**
     * 根据当前选用编码器的 {@link android.media.MediaCodecInfo.VideoCapabilities} 收紧采集长边，
     * 使 VirtualDisplay / OpenGL 输出与 MediaFormat 宽高一致。
     */
    private Size applyEncoderCapabilityClamp(MediaCodec mediaCodec, Codec codec, Size size)
            throws ConfigurationException, IOException {
        Size rounded = size.round8();
        int capMax = VideoEncoderCapabilities.computeMaxSupportedLongSide(
                mediaCodec, codec.getMimeType(), rounded);
        if (capMax > 0 && capMax < rounded.getMax()) {
            if (capture.setMaxSize(capMax)) {
                capture.prepare();
                Size next = capture.getSize().round8();
                Ln.i("Video encoder capability pre-clamp: -m" + capMax + " -> " + next);
                return next;
            }
        }
        return rounded;
    }

    private void encode(MediaCodec codec, Streamer streamer) throws IOException {
        encode(codec, streamer, false);
    }

    private void encode(MediaCodec codec, Streamer streamer, boolean obeyVideoEnabled) throws IOException {
        MediaCodec.BufferInfo bufferInfo = new MediaCodec.BufferInfo();

        boolean eos;
        do {
            if (obeyVideoEnabled && (!videoEnabled.get() || stopped.get())) {
                break;
            }
            int outputBufferId = codec.dequeueOutputBuffer(bufferInfo, -1);
            try {
                eos = (bufferInfo.flags & MediaCodec.BUFFER_FLAG_END_OF_STREAM) != 0;
                if (outputBufferId >= 0 && bufferInfo.size > 0) {
                    ByteBuffer codecBuffer = codec.getOutputBuffer(outputBufferId);

                    boolean isConfig = (bufferInfo.flags & MediaCodec.BUFFER_FLAG_CODEC_CONFIG) != 0;
                    if (!isConfig) {
                        firstFrameSent = true;
                        consecutiveErrors = 0;
                    }

                    streamer.writePacket(codecBuffer, bufferInfo);
                }
            } finally {
                if (outputBufferId >= 0) {
                    codec.releaseOutputBuffer(outputBufferId, false);
                }
            }
        } while (!eos && (!obeyVideoEnabled || (videoEnabled.get() && !stopped.get())));
    }

    private static MediaCodec createMediaCodec(Codec codec, String encoderName) throws IOException, ConfigurationException {
        if (encoderName != null) {
            Ln.d("Creating encoder by name: '" + encoderName + "'");
            try {
                MediaCodec mediaCodec = MediaCodec.createByCodecName(encoderName);
                String mimeType = Codec.getMimeType(mediaCodec);
                if (!codec.getMimeType().equals(mimeType)) {
                    Ln.e("Video encoder type for \"" + encoderName + "\" (" + mimeType + ") does not match codec type (" + codec.getMimeType() + ")");
                    throw new ConfigurationException("Incorrect encoder type: " + encoderName);
                }
                return mediaCodec;
            } catch (IllegalArgumentException e) {
                Ln.e("Video encoder '" + encoderName + "' for " + codec.getName() + " not found\n" + LogUtils.buildVideoEncoderListMessage());
                throw new ConfigurationException("Unknown encoder: " + encoderName);
            } catch (IOException e) {
                Ln.e("Could not create video encoder '" + encoderName + "' for " + codec.getName() + "\n" + LogUtils.buildVideoEncoderListMessage());
                throw e;
            }
        }

        MediaCodec preferred = createPreferredHardwareEncoder(codec.getMimeType());
        if (preferred != null) {
            Ln.i("Using hardware-preferred video encoder: '" + preferred.getName() + "'");
            return preferred;
        }

        try {
            MediaCodec mediaCodec = MediaCodec.createEncoderByType(codec.getMimeType());
            Ln.w("Falling back to default encoder by type: '" + mediaCodec.getName() + "'");
            return mediaCodec;
        } catch (IOException | IllegalArgumentException e) {
            Ln.e("Could not create default video encoder for " + codec.getName() + "\n" + LogUtils.buildVideoEncoderListMessage());
            throw e;
        }
    }

    private static boolean isLikelySoftwareEncoder(String codecName) {
        if (codecName == null) {
            return false;
        }
        String n = codecName.toLowerCase(Locale.US);
        return n.startsWith("omx.google.") || n.startsWith("c2.android.");
    }

    /**
     * {@link MediaCodec#createEncoderByType} 在部分机型上会先落到 OMX.google 软编；Surface 软 AVC 很多旧机扛不住。
     */
    private static MediaCodec createPreferredHardwareEncoder(String mimeType) {
        try {
            MediaCodecList list = new MediaCodecList(MediaCodecList.ALL_CODECS);
            for (MediaCodecInfo info : list.getCodecInfos()) {
                if (!info.isEncoder()) {
                    continue;
                }
                for (String type : info.getSupportedTypes()) {
                    if (!mimeType.equals(type)) {
                        continue;
                    }
                    String name = info.getName();
                    if (isLikelySoftwareEncoder(name)) {
                        continue;
                    }
                    try {
                        return MediaCodec.createByCodecName(name);
                    } catch (IllegalArgumentException | IOException e) {
                        Ln.d("Skip encoder " + name + ": " + e.getMessage());
                    }
                }
            }
        } catch (Throwable t) {
            Ln.w("Hardware encoder enumeration failed: " + t.getMessage());
        }
        return null;
    }

    private static MediaFormat createFormat(
            String videoMimeType,
            int bitRate,
            float maxFps,
            List<CodecOption> codecOptions,
            MediaCodec mediaCodecForAvcLevel) {
        MediaFormat format = new MediaFormat();
        format.setString(MediaFormat.KEY_MIME, videoMimeType);
        format.setInteger(MediaFormat.KEY_BIT_RATE, bitRate);
        // must be present to configure the encoder, but does not impact the actual frame rate, which is variable
        format.setInteger(MediaFormat.KEY_FRAME_RATE, 60);
        format.setInteger(MediaFormat.KEY_COLOR_FORMAT, MediaCodecInfo.CodecCapabilities.COLOR_FormatSurface);
        // 不设置 KEY_COLOR_RANGE，避免浏览器解码器在首帧后按 LIMITED 切换导致「先正常、几秒后变怪」
        format.setInteger(MediaFormat.KEY_I_FRAME_INTERVAL, DEFAULT_I_FRAME_INTERVAL);
        // 不设 KEY_REPEAT_PREVIOUS_FRAME_AFTER：避免无新帧时重复上一帧（偏「省码」），游戏/多端硬解更省解码侧怪异帧
        if (maxFps > 0) {
            // The key existed privately before Android 10:
            // <https://android.googlesource.com/platform/frameworks/base/+/625f0aad9f7a259b6881006ad8710adce57d1384%5E%21/>
            // <https://github.com/Genymobile/scrcpy/issues/488#issuecomment-567321437>
            format.setFloat(KEY_MAX_FPS_TO_ENCODER, maxFps);
        }

        if (codecOptions != null) {
            for (CodecOption option : codecOptions) {
                String key = option.getKey();
                Object value = option.getValue();
                CodecUtils.setCodecOption(format, key, value);
                Ln.d("Video codec option set: " + key + " (" + value.getClass().getSimpleName() + ") = " + value);
            }
        }

        // Baseline；Level 取自当前编码器 profileLevels（不超过 4.1），与 r:svc:scrcpy.caps 中 recommended_baseline_level 一致思路。
        if (MediaFormat.MIMETYPE_VIDEO_AVC.equals(videoMimeType)) {
            format.setInteger(MediaFormat.KEY_PROFILE, MediaCodecInfo.CodecProfileLevel.AVCProfileBaseline);
            int level = VideoEncoderCapabilities.pickHighestAvcBaselineLevelCapped(
                    mediaCodecForAvcLevel,
                    videoMimeType,
                    MediaCodecInfo.CodecProfileLevel.AVCLevel41);
            format.setInteger(MediaFormat.KEY_LEVEL, level);
        }

        return format;
    }

    @Override
    public void start(TerminationListener listener) {
        this.terminationListener = listener;
        thread = new Thread(this, "video");
        thread.start();
    }

    @Override
    public void run() {
        // Some devices (Meizu) deadlock if the video encoding thread has no Looper
        // <https://github.com/Genymobile/scrcpy/issues/4143>
        Looper.prepare();

        try {
            streamCapture();
        } catch (ConfigurationException e) {
            // Do not print stack trace, a user-friendly error-message has already been logged
        } catch (IOException e) {
            // Broken pipe is expected on close, because the socket is closed by the client
            if (!IO.isBrokenPipe(e)) {
                Ln.e("Video encoding error", e);
            }
        } finally {
            Ln.d("Screen streaming stopped");
            if (terminationListener != null) {
                terminationListener.onTerminated(true);
            }
        }
    }

    @Override
    public void stop() {
        if (thread != null) {
            stopped.set(true);
            reset.reset();
        }
    }

    @Override
    public void join() throws InterruptedException {
        if (thread != null) {
            thread.join();
        }
    }
}
