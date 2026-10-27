package adb

import (
	"fmt"
	"log/slog"
	"os"
)

var debugLogging = os.Getenv("ADBOX_DEBUG") == "1"

func adbDebugEnabled() bool {
	return debugLogging
}

func adbDebugf(format string, args ...any) {
	if debugLogging {
		slog.Debug(fmt.Sprintf(format, args...))
	}
}

func adbWarnf(format string, args ...any) {
	slog.Warn(fmt.Sprintf(format, args...))
}
