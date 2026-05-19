// Package logging sets up file-based slog for the hanko CLI.
package logging

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
)

// Init sets up file-based logging to $XDG_CACHE_HOME/hanko/logs/hanko.log.
// Returns a cleanup function to close the log file.
func Init() (func(), error) {
	dir := filepath.Join(xdg.CacheHome, "hanko", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return func() {}, err
	}

	f, err := os.OpenFile(filepath.Join(dir, "hanko.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return func() {}, err
	}

	handler := slog.NewJSONHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug})
	slog.SetDefault(slog.New(handler))

	return func() { _ = f.Close() }, nil
}
