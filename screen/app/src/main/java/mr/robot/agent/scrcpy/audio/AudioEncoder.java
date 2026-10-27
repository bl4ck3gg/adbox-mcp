package mr.robot.agent.scrcpy.audio;

import mr.robot.agent.scrcpy.AndroidVersions;
import mr.robot.agent.scrcpy.AsyncProcessor;
import mr.robot.agent.scrcpy.Options;
import mr.robot.agent.scrcpy.control.AudioEnableController;
import mr.robot.agent.scrcpy.device.ConfigurationException;
import mr.robot.agent.scrcpy.device.Streamer;
import mr.robot.agent.scrcpy.util.Codec;
import mr.robot.agent.scrcpy.util.CodecOption;
import mr.robot.agent.scrcpy.util.CodecUtils;
import mr.robot.agent.scrcpy.util.IO;
import mr.robot.agent.scrcpy.util.Ln;
import mr.robot.agent.scrcpy.util.LogUtils;

import android.annotation.TargetApi;
import android.media.MediaCodec;
import android.media.MediaFormat;
import android.os.Build;
import android.os.Handler;
import android.os.HandlerThread;
import android.os.Looper;

import java.io.IOException;
import java.nio.ByteBuffer;
import java.util.List;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.ThreadFactory;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;

public final class AudioEncoder implements AsyncProcessor, AudioEnableController {

    private static class InputTask {
        private final int index;

        InputTask(int index) {
            this.index = index;
        }
    }

    private static class OutputTask {
        private final int index;
        private final MediaCodec.BufferInfo bufferInfo;

        OutputTask(int index, MediaCodec.BufferInfo bufferInfo) {
            this.index = index;
            this.bufferInfo = bufferInfo;
        }
    }

    private static final int SAMPLE_RATE = AudioConfig.SAMPLE_RATE;
    private static final int CHANNELS = AudioConfig.CHANNELS;

    private final AudioCapture capture;
    private final Streamer streamer;
    private final int bitRate;
    private final List<CodecOption> codecOptions;
    private final String encoderName;

    private boolean recreatePts;
    private long previousPts;

    private final BlockingQueue<InputTask> inputTasks = new ArrayBlockingQueue<>(64);
    private final BlockingQueue<OutputTask> outputTasks = new ArrayBlockingQueue<>(64);

    private Thread thread;
    private HandlerThread mediaCodecThread;

    /** 单线程池串行执行 encode，启停无竞态；encode 内 input/output 用 ioExecutor 的 2 线程 */
    private ExecutorService encodeExecutor;
    private ExecutorService ioExecutor;
    /** 当前 encode 任务，stop() 时 cancel 以便尽快结束 */
    private volatile Future<?> currentEncodeFuture;

    /** 仅 stop() -> end() 置 true；manager 在循环中无锁读取，必须 volatile 保证可见性 */
    private volatile boolean ended;
    private volatile boolean cycleEnded;
    private volatile boolean audioEnabled = false;
    private final Object enableLock = new Object();
    private volatile boolean audioHeaderSent;

    public AudioEncoder(AudioCapture capture, Streamer streamer, Options options) {
        this.capture = capture;
        this.streamer = streamer;
        this.bitRate = options.getAudioBitRate();
        this.codecOptions = options.getAudioCodecOptions();
        this.encoderName = options.getAudioEncoder();
    }

    @Override
    public void setAudioEnabled(boolean enabled) {
        audioEnabled = enabled;
        if (!enabled) {
            synchronized (this) {
                cycleEnded = true;
                notifyAll();
            }
        }
        synchronized (enableLock) {
            enableLock.notifyAll();
        }
    }

    private static MediaFormat createFormat(String mimeType, int bitRate, List<CodecOption> codecOptions) {
        MediaFormat format = new MediaFormat();
        format.setString(MediaFormat.KEY_MIME, mimeType);
        format.setInteger(MediaFormat.KEY_BIT_RATE, bitRate);
        format.setInteger(MediaFormat.KEY_CHANNEL_COUNT, CHANNELS);
        format.setInteger(MediaFormat.KEY_SAMPLE_RATE, SAMPLE_RATE);

        if (codecOptions != null) {
            for (CodecOption option : codecOptions) {
                String key = option.getKey();
                Object value = option.getValue();
                CodecUtils.setCodecOption(format, key, value);
                Ln.d("Audio codec option set: " + key + " (" + value.getClass().getSimpleName() + ") = " + value);
            }
        }

        return format;
    }

    @TargetApi(AndroidVersions.API_24_ANDROID_7_0)
    private void inputThread(MediaCodec mediaCodec, AudioCapture capture) throws IOException, InterruptedException {
        final MediaCodec.BufferInfo bufferInfo = new MediaCodec.BufferInfo();

        while (!Thread.currentThread().isInterrupted()) {
            InputTask task = inputTasks.take();
            ByteBuffer buffer = mediaCodec.getInputBuffer(task.index);
            int r = capture.read(buffer, bufferInfo);
            if (r <= 0) {
                throw new IOException("Could not read audio: " + r);
            }

            mediaCodec.queueInputBuffer(task.index, bufferInfo.offset, bufferInfo.size, bufferInfo.presentationTimeUs, bufferInfo.flags);
        }
    }

    private void outputThread(MediaCodec mediaCodec) throws IOException, InterruptedException {
        if (!audioHeaderSent) {
            streamer.writeAudioHeader();
            audioHeaderSent = true;
        }
        while (!Thread.currentThread().isInterrupted()) {
            OutputTask task = outputTasks.take();
            ByteBuffer buffer = mediaCodec.getOutputBuffer(task.index);
            try {
                if (recreatePts) {
                    fixTimestamp(task.bufferInfo);
                }
                streamer.writePacket(buffer, task.bufferInfo);
            } finally {
                mediaCodec.releaseOutputBuffer(task.index, false);
            }
        }
    }

    private void fixTimestamp(MediaCodec.BufferInfo bufferInfo) {
        assert recreatePts;

        if ((bufferInfo.flags & MediaCodec.BUFFER_FLAG_CODEC_CONFIG) != 0) {
            // Config packet, nothing to fix
            return;
        }

        long pts = bufferInfo.presentationTimeUs;
        if (previousPts != 0) {
            long now = System.nanoTime() / 1000;
            // This specific encoder produces PTS matching the exact number of samples
            long duration = pts - previousPts;
            bufferInfo.presentationTimeUs = now - duration;
        }

        previousPts = pts;
    }

    @Override
    public void start(TerminationListener listener) {
        ThreadFactory encodeFactory = r -> new Thread(r, "audio-encode-worker");
        AtomicInteger ioThreadNum = new AtomicInteger(0);
        ThreadFactory ioFactory = r -> new Thread(r, ioThreadNum.getAndIncrement() == 0 ? "audio-in" : "audio-out");
        encodeExecutor = Executors.newSingleThreadExecutor(encodeFactory);
        ioExecutor = Executors.newFixedThreadPool(2, ioFactory);

        Runnable encodeTask = () -> {
            try {
                encode();
            } catch (ConfigurationException e) {
                Ln.e("Audio codec configuration error (will retry when re-enabled)", e);
            } catch (AudioCaptureException e) {
                // user-friendly message already logged
            } catch (Throwable t) {
                Ln.e("Audio encoding error (will retry when re-enabled)", t);
            }
        };

        thread = new Thread(() -> {
            try {
                while (!ended) {
                    synchronized (enableLock) {
                        while (!audioEnabled && !ended) {
                            try {
                                enableLock.wait();
                            } catch (InterruptedException e) {
                                Thread.currentThread().interrupt();
                                return;
                            }
                        }
                    }
                    if (ended) break;
                    cycleEnded = false;
                    currentEncodeFuture = encodeExecutor.submit(encodeTask);
                    try {
                        currentEncodeFuture.get();
                    } catch (InterruptedException e) {
                        Thread.currentThread().interrupt();
                        return;
                    } catch (ExecutionException e) {
                        // encode 内已打日志，此处仅忽略
                    } finally {
                        currentEncodeFuture = null;
                    }
                }
            } finally {
                Ln.d("Audio encoder stopped");
                if (encodeExecutor != null) encodeExecutor.shutdownNow();
                if (ioExecutor != null) ioExecutor.shutdownNow();
                listener.onTerminated(false);
            }
        }, "audio-encoder");
        thread.start();
    }

    @Override
    public void stop() {
        if (thread != null) {
            end();
            Future<?> f = currentEncodeFuture;
            if (f != null) f.cancel(true);
            thread.interrupt();
        }
    }

    @Override
    public void join() throws InterruptedException {
        if (thread != null) {
            thread.join();
        }
    }

    private void end() {
        synchronized (this) {
            cycleEnded = true;
            ended = true;
            notifyAll();
        }
        synchronized (enableLock) {
            enableLock.notifyAll();
        }
    }

    /** 等待当前采集周期结束（setAudioEnabled(false) 或 stop() 触发） */
    private synchronized void waitCycleEnd() {
        try {
            while (!cycleEnded && !ended) {
                wait();
            }
        } catch (InterruptedException e) {
            // ignore
        }
    }

    @TargetApi(AndroidVersions.API_23_ANDROID_6_0)
    private void encode() throws IOException, ConfigurationException, AudioCaptureException {
        if (Build.VERSION.SDK_INT < AndroidVersions.API_30_ANDROID_11) {
            Ln.w("Audio disabled: it is not supported before Android 11");
            streamer.writeDisableStream(false);
            return;
        }

        MediaCodec mediaCodec = null;
        Future<?> inFuture = null;
        Future<?> outFuture = null;

        boolean mediaCodecStarted = false;
        previousPts = 0; // 每轮 encode 独立，避免跨轮复用导致 PTS 跳变
        try {
            capture.checkCompatibility(); // throws an AudioCaptureException on error

            Codec codec = streamer.getCodec();
            mediaCodec = createMediaCodec(codec, encoderName);

            // The default OPUS and FLAC encoders overwrite the input PTS with a value that matches the number of samples. This is not the behavior
            // we want: it ignores any audio clock drift and hard silences (packets not produced on silence). To work around this behavior,
            // regenerate PTS based on the current time and the packet duration.
            String codecName = mediaCodec.getCanonicalName();
            recreatePts = "c2.android.opus.encoder".equals(codecName) || "c2.android.flac.encoder".equals(codecName);

            mediaCodecThread = new HandlerThread("media-codec");
            mediaCodecThread.start();

            MediaFormat format = createFormat(codec.getMimeType(), bitRate, codecOptions);
            mediaCodec.setCallback(new EncoderCallback(), new Handler(mediaCodecThread.getLooper()));
            mediaCodec.configure(format, null, null, MediaCodec.CONFIGURE_FLAG_ENCODE);

            capture.start();

            // 避免上一轮遗留的 Task 被本轮 input/output 使用（index/BufferInfo 可能不匹配新 codec）
            inputTasks.clear();
            outputTasks.clear();

            final MediaCodec mediaCodecRef = mediaCodec;
            inFuture = ioExecutor.submit(() -> {
                try {
                    inputThread(mediaCodecRef, capture);
                } catch (IOException | InterruptedException e) {
                    Ln.e("Audio capture error", e);
                }
            });
            outFuture = ioExecutor.submit(() -> {
                try {
                    outputThread(mediaCodecRef);
                } catch (InterruptedException e) {
                    // expected when cycle ends
                } catch (IOException e) {
                    if (!IO.isBrokenPipe(e)) {
                        Ln.e("Audio encoding error", e);
                    }
                }
            });

            mediaCodec.start();
            mediaCodecStarted = true;

            waitCycleEnd();
        } catch (ConfigurationException e) {
            streamer.writeDisableStream(true);
            throw e;
        } catch (Throwable e) {
            streamer.writeDisableStream(false);
            throw e;
        } finally {
            if (mediaCodecThread != null) {
                try {
                    Looper looper = mediaCodecThread.getLooper();
                    if (looper != null) looper.quitSafely();
                } catch (Throwable t) {
                    Ln.w("AudioEncoder.cleanup: Looper.quitSafely failed: " + t.getMessage());
                }
            }
            if (inFuture != null) inFuture.cancel(true);
            if (outFuture != null) outFuture.cancel(true);
            try {
                if (inFuture != null) inFuture.get(5, TimeUnit.SECONDS);
            } catch (Throwable ignored) {
            }
            try {
                if (outFuture != null) outFuture.get(5, TimeUnit.SECONDS);
            } catch (Throwable ignored) {
            }
            if (mediaCodecThread != null) {
                try {
                    mediaCodecThread.join(2000);
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                }
            }
            try {
                Thread.sleep(50);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            }
            if (mediaCodec != null) {
                if (mediaCodecStarted) {
                    try {
                        mediaCodec.stop();
                    } catch (Throwable t) {
                        Ln.w("AudioEncoder.cleanup: MediaCodec.stop failed: " + t.getMessage());
                    }
                }
                try {
                    mediaCodec.release();
                } catch (Throwable t) {
                    Ln.w("AudioEncoder.cleanup: MediaCodec.release failed: " + t.getMessage());
                }
            }
            if (capture != null) {
                try {
                    capture.stop();
                } catch (Throwable t) {
                    Ln.w("AudioEncoder.cleanup: capture.stop failed: " + t.getMessage());
                }
            }
            mediaCodecThread = null;
        }
    }

    private static MediaCodec createMediaCodec(Codec codec, String encoderName) throws IOException, ConfigurationException {
        if (encoderName != null) {
            Ln.d("Creating audio encoder by name: '" + encoderName + "'");
            try {
                MediaCodec mediaCodec = MediaCodec.createByCodecName(encoderName);
                String mimeType = Codec.getMimeType(mediaCodec);
                if (!codec.getMimeType().equals(mimeType)) {
                    Ln.e("Audio encoder type for \"" + encoderName + "\" (" + mimeType + ") does not match codec type (" + codec.getMimeType() + ")");
                    throw new ConfigurationException("Incorrect encoder type: " + encoderName);
                }
                return mediaCodec;
            } catch (IllegalArgumentException e) {
                Ln.e("Audio encoder '" + encoderName + "' for " + codec.getName() + " not found\n" + LogUtils.buildAudioEncoderListMessage());
                throw new ConfigurationException("Unknown encoder: " + encoderName);
            } catch (IOException e) {
                Ln.e("Could not create audio encoder '" + encoderName + "' for " + codec.getName() + "\n" + LogUtils.buildAudioEncoderListMessage());
                throw e;
            }
        }

        try {
            MediaCodec mediaCodec = MediaCodec.createEncoderByType(codec.getMimeType());
            Ln.d("Using audio encoder: '" + mediaCodec.getName() + "'");
            return mediaCodec;
        } catch (IOException | IllegalArgumentException e) {
            Ln.e("Could not create default audio encoder for " + codec.getName() + "\n" + LogUtils.buildAudioEncoderListMessage());
            throw e;
        }
    }

    private final class EncoderCallback extends MediaCodec.Callback {
        @TargetApi(AndroidVersions.API_24_ANDROID_7_0)
        @Override
        public void onInputBufferAvailable(MediaCodec codec, int index) {
            try {
                inputTasks.put(new InputTask(index));
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            }
        }

        @Override
        public void onOutputBufferAvailable(MediaCodec codec, int index, MediaCodec.BufferInfo bufferInfo) {
            try {
                outputTasks.put(new OutputTask(index, bufferInfo));
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            }
        }

        @Override
        public void onError(MediaCodec codec, MediaCodec.CodecException e) {
            Ln.e("MediaCodec error", e);
        }

        @Override
        public void onOutputFormatChanged(MediaCodec codec, MediaFormat format) {
            // ignore
        }
    }
}
