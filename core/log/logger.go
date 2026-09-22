package log

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
)

// Global slog logger instance
var logger *slog.Logger
var currentWriter io.Writer = os.Stdout
var currentLevel slog.Level = slog.Level(1000)

// git's own error output echoes the token-authenticated remote URL.
var accessTokenRe = regexp.MustCompile(`x-access-token:[^@\s]+@`)

func sanitize(msg string) string {
	return accessTokenRe.ReplaceAllString(msg, "x-access-token:REDACTED@")
}

func init() {
	// Initialize with high level to disable logging by default
	logger = slog.New(slog.NewTextHandler(currentWriter, &slog.HandlerOptions{
		Level: currentLevel,
	}))
}

func Info(format string, args ...any) {
	if len(args) > 0 {
		logger.Info(sanitize(fmt.Sprintf(format, args...)))
	} else {
		logger.Info(sanitize(format))
	}
}

func Debug(format string, args ...any) {
	if len(args) > 0 {
		logger.Debug(sanitize(fmt.Sprintf(format, args...)))
	} else {
		logger.Debug(sanitize(format))
	}
}

func Warn(format string, args ...any) {
	if len(args) > 0 {
		logger.Warn(sanitize(fmt.Sprintf(format, args...)))
	} else {
		logger.Warn(sanitize(format))
	}
}

func Error(format string, args ...any) {
	if len(args) > 0 {
		logger.Error(sanitize(fmt.Sprintf(format, args...)))
	} else {
		logger.Error(sanitize(format))
	}
}

func SetLevel(level slog.Level) {
	currentLevel = level
	logger = slog.New(slog.NewTextHandler(currentWriter, &slog.HandlerOptions{
		Level: currentLevel,
	}))
}

func SetWriter(writer io.Writer) {
	currentWriter = writer
	logger = slog.New(slog.NewTextHandler(currentWriter, &slog.HandlerOptions{
		Level: currentLevel,
	}))
}

func SetWriterWithLevel(writer io.Writer, level slog.Level) {
	currentWriter = writer
	currentLevel = level
	logger = slog.New(slog.NewTextHandler(currentWriter, &slog.HandlerOptions{
		Level: currentLevel,
	}))
}
