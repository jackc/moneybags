package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	slogjournal "github.com/systemd/slog-journal"
)

func TestConsoleLogging(t *testing.T) {
	for _, format := range []string{"", "json", "JSON", "text", "TeXt", "unknown"} {
		for _, level := range []struct {
			name    string
			minimum slog.Level
		}{
			{"", slog.LevelInfo}, {"info", slog.LevelInfo},
			{"DEBUG", slog.LevelDebug}, {"warn", slog.LevelWarn},
			{"error", slog.LevelError}, {"unknown", slog.LevelInfo},
		} {
			t.Run(format+"/"+level.name, func(t *testing.T) {
				var output bytes.Buffer
				handler, err := newLogHandler(format, level.name, &output)
				if err != nil {
					t.Fatal(err)
				}
				logger := slog.New(handler)
				var expected []slog.Level
				for _, severity := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
					logger.Log(context.Background(), severity, "logging test", "request_id", "abc123")
					if severity >= level.minimum {
						expected = append(expected, severity)
					}
				}
				lines := strings.Split(strings.TrimSpace(output.String()), "\n")
				if len(lines) != len(expected) {
					t.Fatalf("got %d records, want %d: %s", len(lines), len(expected), output.String())
				}
				for i, line := range lines {
					if strings.EqualFold(format, "text") {
						for _, field := range []string{"level=" + expected[i].String(), `msg="logging test"`, "request_id=abc123"} {
							if !strings.Contains(line, field) {
								t.Errorf("missing %s in %s", field, line)
							}
						}
					} else {
						var record map[string]any
						if err := json.Unmarshal([]byte(line), &record); err != nil {
							t.Fatal(err)
						}
						if record["level"] != expected[i].String() || record["msg"] != "logging test" || record["request_id"] != "abc123" {
							t.Errorf("unexpected log record: %v", record)
						}
					}
				}
			})
		}
	}
}

func TestJournalHandler(t *testing.T) {
	var output bytes.Buffer
	handler, err := newLogHandler("JoUrNaL", "warn", &output)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := handler.(*slogjournal.Handler); !ok {
		t.Fatalf("expected native journal handler, got %T", handler)
	}
	ctx := context.Background()
	if handler.Enabled(ctx, slog.LevelInfo) || !handler.Enabled(ctx, slog.LevelWarn) || !handler.Enabled(ctx, slog.LevelError) {
		t.Fatal("journal handler does not respect LOG_LEVEL")
	}
}

func TestJournalFieldNames(t *testing.T) {
	for input, want := range map[string]string{
		"http.request.method": "HTTP_REQUEST_METHOD",
		"@timestamp":          "TIMESTAMP", "user_agent.original": "USER_AGENT_ORIGINAL",
		"log.level": "LOG_LEVEL", "ALREADY_UPPER": "ALREADY_UPPER",
		"123leading_digits": "LEADING_DIGITS", "_leading_underscore": "LEADING_UNDERSCORE",
		"": "UNKNOWN", "123_!": "UNKNOWN", "日本語": "UNKNOWN", "café": "CAF_",
	} {
		t.Run(input, func(t *testing.T) {
			if got := normalizeJournalKey(input); got != want {
				t.Errorf("group key = %q, want %q", got, want)
			}
			attr := journalReplaceAttr(nil, slog.String(input, "value"))
			if attr.Key != want || attr.Value.String() != "value" {
				t.Errorf("unexpected attribute: %v", attr)
			}
		})
	}
	if got := journalReplaceAttr(nil, slog.Attr{}); !got.Equal(slog.Attr{}) {
		t.Errorf("empty attribute should remain omitted: %v", got)
	}
}
