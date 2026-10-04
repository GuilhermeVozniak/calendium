package config

import (
	"io"
	"log/slog"
)

// NewLogger builds the process logger from LOG_FORMAT/LOG_LEVEL: JSON for
// production log shippers, text for a terminal.
func NewLogger(w io.Writer, l Log) *slog.Logger {
	opts := &slog.HandlerOptions{Level: l.Level}
	if l.Format == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}
