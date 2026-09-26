package main

import (
	"io"
	"log/slog"
	"strings"

	slogjournal "github.com/systemd/slog-journal"
)

// Match Logger4Life and FAM's case-insensitive settings and fallback defaults.
func newLogHandler(format, level string, output io.Writer) (slog.Handler, error) {
	var minimum slog.Level
	switch strings.ToLower(level) {
	case "debug":
		minimum = slog.LevelDebug
	case "warn":
		minimum = slog.LevelWarn
	case "error":
		minimum = slog.LevelError
	default:
		minimum = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: minimum}
	switch strings.ToLower(format) {
	case "text":
		return slog.NewTextHandler(output, opts), nil
	case "journal":
		return slogjournal.NewHandler(&slogjournal.Options{
			Level:        minimum,
			ReplaceAttr:  journalReplaceAttr,
			ReplaceGroup: normalizeJournalKey,
		})
	default:
		return slog.NewJSONHandler(output, opts), nil
	}
}

// Journal field names must be ASCII uppercase letters, digits or underscores,
// and application fields must not start with an underscore or digit.
func normalizeJournalKey(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteByte(byte(r - 'a' + 'A'))
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteByte(byte(r))
		default:
			b.WriteByte('_')
		}
	}
	result := strings.TrimLeft(b.String(), "_0123456789")
	if result == "" {
		return "UNKNOWN"
	}
	return result
}

func journalReplaceAttr(_ []string, a slog.Attr) slog.Attr {
	if !a.Equal(slog.Attr{}) {
		a.Key = normalizeJournalKey(a.Key)
	}
	return a
}
