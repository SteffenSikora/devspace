package log

import (
	"github.com/Sirupsen/logrus"
)

var stdoutLog = &stdoutLogger{
	level: logrus.DebugLevel,
}

// StartWait prints a wait message until StopWait is called
func StartWait(message string) {
	stdoutLog.StartWait(message)
}

// StopWait stops printing the wait message
func StopWait() {
	stdoutLog.StopWait()
}

// Debug prints debug information
func Debug(args ...interface{}) {
	stdoutLog.Debug(args...)
}

// Debugf prints formatted debug information
func Debugf(format string, args ...interface{}) {
	stdoutLog.Debugf(format, args...)
}

// Info prints info information
func Info(args ...interface{}) {
	stdoutLog.Info(args...)
}

// Infof prints formatted information
func Infof(format string, args ...interface{}) {
	stdoutLog.Infof(format, args...)
}

// Warn prints warning information
func Warn(args ...interface{}) {
	stdoutLog.Warn(args...)
}

// Warnf prints formatted warning information
func Warnf(format string, args ...interface{}) {
	stdoutLog.Warnf(format, args...)
}

// Error prints error information
func Error(args ...interface{}) {
	stdoutLog.Error(args...)
}

// Errorf prints formatted error information
func Errorf(format string, args ...interface{}) {
	stdoutLog.Errorf(format, args...)
}

// Fatal prints fatal error information
func Fatal(args ...interface{}) {
	stdoutLog.Fatal(args...)
}

// Fatalf prints formatted fatal error information
func Fatalf(format string, args ...interface{}) {
	stdoutLog.Fatalf(format, args...)
}

// Panic prints panic information
func Panic(args ...interface{}) {
	stdoutLog.Panic(args...)
}

// Panicf prints formatted panic information
func Panicf(format string, args ...interface{}) {
	stdoutLog.Panicf(format, args...)
}

// Done prints done information
func Done(args ...interface{}) {
	stdoutLog.Done(args...)
}

// Donef prints formatted info information
func Donef(format string, args ...interface{}) {
	stdoutLog.Donef(format, args...)
}

// Fail prints error information
func Fail(args ...interface{}) {
	stdoutLog.Fail(args...)
}

// Failf prints formatted error information
func Failf(format string, args ...interface{}) {
	stdoutLog.Failf(format, args...)
}

// Print prints information
func Print(level logrus.Level, args ...interface{}) {
	stdoutLog.Print(level, args...)
}

// Printf prints formatted information
func Printf(level logrus.Level, format string, args ...interface{}) {
	stdoutLog.Printf(level, format, args...)
}

// With adds context information to the entry
func With(obj interface{}) *LoggerEntry {
	return stdoutLog.With(obj)
}

// SetLevel changes the log level of the global logger
func SetLevel(level logrus.Level) {
	stdoutLog.SetLevel(level)
}

// StartFileLogging logs the output of the global logger to the file default.log
func StartFileLogging() {
	stdoutLog.fileLogger = GetFileLogger("default")

	OverrideRuntimeErrorHandler()
}

// GetInstance returns the Logger instance
func GetInstance() Logger {
	return stdoutLog
}
