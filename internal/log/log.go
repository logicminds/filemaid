package log

import (
	"io"
	"os"
	"path/filepath"

	"log/slog"
)

var logFile *os.File

// Init initializes slog to write JSON logs to stderr and to logPath.
// The log file is created if it does not already exist.
func Init(logPath string) error {
	if logPath == "" {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
		return nil
	}

	dir := filepath.Dir(logPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}
	logFile = f

	handler := slog.NewJSONHandler(io.MultiWriter(os.Stderr, f), &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(handler))
	return nil
}
