package mr.robot.agent.shell.util;

import android.util.Log;

import java.io.FileDescriptor;
import java.io.FileOutputStream;
import java.io.OutputStream;
import java.io.PrintStream;

/**
 * Log to logcat and stdout/stderr (for app_process).
 */
public final class Ln {

    private static final String TAG = "RobotAgent";
    private static final String PREFIX = "[shell] ";

    private static final PrintStream CONSOLE_OUT = new PrintStream(new FileOutputStream(FileDescriptor.out));
    private static final PrintStream CONSOLE_ERR = new PrintStream(new FileOutputStream(FileDescriptor.err));

    public enum Level {
        VERBOSE, DEBUG, INFO, WARN, ERROR
    }

    private static Level threshold = Level.INFO;

    private Ln() {
    }

    public static void initLogLevel(Level level) {
        threshold = level;
    }

    public static boolean isEnabled(Level level) {
        return level.ordinal() >= threshold.ordinal();
    }

    public static void e(String message, Throwable throwable) {
        if (isEnabled(Level.ERROR)) {
            Log.e(TAG, message, throwable);
            synchronized (CONSOLE_ERR) {
                CONSOLE_ERR.print(PREFIX + "ERROR: " + message + '\n');
                if (throwable != null) {
                    throwable.printStackTrace(CONSOLE_ERR);
                }
            }
        }
    }

    public static void e(String message) {
        e(message, null);
    }

    static class NullOutputStream extends OutputStream {
        @Override
        public void write(int b) {
        }
    }
}
