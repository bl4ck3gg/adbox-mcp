/*
 * Copyright (C) 2017 Genymobile
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package relay

import (
	"fmt"
	"os"
	"time"
)

// LogLevel represents the logging level
type LogLevel int

const (
	LevelVerbose LogLevel = iota
	LevelDebug
	LevelInfo
	LevelWarning
	LevelError
)

var levelNames = map[LogLevel]string{
	LevelVerbose: "V",
	LevelDebug:   "D",
	LevelInfo:    "I",
	LevelWarning: "W",
	LevelError:   "E",
}

var logThreshold = LevelInfo

// SetLogThreshold sets the log threshold level
func SetLogThreshold(level LogLevel) {
	logThreshold = level
}

// GetLogThreshold returns the current log threshold
func GetLogThreshold() LogLevel {
	return logThreshold
}

// IsEnabled checks if a log level is enabled
func IsEnabled(level LogLevel) bool {
	return level >= logThreshold
}

// IsVerboseEnabled checks if verbose logging is enabled
func IsVerboseEnabled() bool {
	return IsEnabled(LevelVerbose)
}

// IsDebugEnabled checks if debug logging is enabled
func IsDebugEnabled() bool {
	return IsEnabled(LevelDebug)
}

// IsInfoEnabled checks if info logging is enabled
func IsInfoEnabled() bool {
	return IsEnabled(LevelInfo)
}

// IsWarningEnabled checks if warning logging is enabled
func IsWarningEnabled() bool {
	return IsEnabled(LevelWarning)
}

// IsErrorEnabled checks if error logging is enabled
func IsErrorEnabled() bool {
	return IsEnabled(LevelError)
}

func getDate() string {
	return time.Now().Format("2006-01-02 15:04:05.000")
}

func formatLog(level LogLevel, tag, message string) string {
	return fmt.Sprintf("%s %s %s: %s", getDate(), levelNames[level], tag, message)
}

func log(level LogLevel, stream *os.File, tag, message string, err error) {
	if IsEnabled(level) {
		fmt.Fprintln(stream, formatLog(level, tag, message))
		if err != nil {
			fmt.Fprintln(stream, err)
		}
	}
}

// Logv logs a verbose message
func Logv(tag, message string, err error) {
	log(LevelVerbose, os.Stderr, tag, message, err)
}

// LogvSimple logs a simple verbose message
func LogvSimple(tag, message string) {
	Logv(tag, message, nil)
}

// Logd logs a debug message
func Logd(tag, message string, err error) {
	log(LevelDebug, os.Stderr, tag, message, err)
}

// LogdSimple logs a simple debug message
func LogdSimple(tag, message string) {
	Logd(tag, message, nil)
}

// Logi logs an info message
func Logi(tag, message string, err error) {
	log(LevelInfo, os.Stderr, tag, message, err)
}

// LogiSimple logs a simple info message
func LogiSimple(tag, message string) {
	Logi(tag, message, nil)
}

// Logw logs a warning message
func Logw(tag, message string, err error) {
	log(LevelWarning, os.Stderr, tag, message, err)
}

// LogwSimple logs a simple warning message
func LogwSimple(tag, message string) {
	Logw(tag, message, nil)
}

// Loge logs an error message
func Loge(tag, message string, err error) {
	log(LevelError, os.Stderr, tag, message, err)
}

// LogeSimple logs a simple error message
func LogeSimple(tag, message string) {
	Loge(tag, message, nil)
}
