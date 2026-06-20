package log

import (
	"io"
	"os"
	"path/filepath"

	"log/slog"
)

var logFile *os.File

// Options controls how Init configures the default logger.
type Options struct {
	// DisableStderr stops log output from being written to os.Stderr.
	// This is useful for commands that produce human-readable terminal output
	// and still want logs persisted to LogPath.
	DisableStderr bool
}

// Init initializes slog to write JSON logs to stderr and to logPath.
// The log file is created if it does not already exist.
func Init(logPath string) error {
	return InitWithOptions(logPath, Options{})
}

// InitWithOptions initializes slog with configurable output sinks.
func InitWithOptions(logPath string, opts Options) error {
	if logPath == "" {
		w := io.Writer(os.Stderr)
		if opts.DisableStderr {
			w = io.Discard
		}
		slog.SetDefault(slog.New(slog.NewTextHandler(w, nil)))
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

	var writers []io.Writer
	writers = append(writers, f)
	if !opts.DisableStderr {
		writers = append(writers, os.Stderr)
	}

	handler := slog.NewJSONHandler(io.MultiWriter(writers...), &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(handler))
	return nil
}

// SetStderrEnabled toggles whether the default logger writes to os.Stderr.
// Logging to the configured log file is unaffected.
func SetStderrEnabled(enabled bool) {
	if logFile == nil {
		return
	}
	var writers []io.Writer
	writers = append(writers, logFile)
	if enabled {
		writers = append(writers, os.Stderr)
	}
	handler := slog.NewJSONHandler(io.MultiWriter(writers...), &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(handler))
}
