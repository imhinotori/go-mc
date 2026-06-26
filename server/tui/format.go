package tui

import (
	"log/slog"
	"strings"
)

// timeFormat is the wall-clock layout for a console log line (no date — the
// console is a live stream, not an archive).
const timeFormat = "15:04:05"

// sanitize strips ASCII control bytes (\x00-\x1f and \x7f) from s, keeping
// printable characters and spaces. Untrusted fields (player names, remote
// addresses) flow into log records and then into the TUI viewport; a hostile
// value carrying ANSI escapes (e.g. "\x1b[2J") could clear or corrupt the
// terminal. Stripping the control bytes neutralizes terminal/ANSI injection
// (threat T-19-01). This runs on every attr value and the message before a line
// reaches either stderr-formatting or the viewport.
func sanitize(s string) string {
	// Fast path: nothing to strip.
	clean := true
	for i := 0; i < len(s); i++ {
		if b := s[i]; b < 0x20 || b == 0x7f {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7f {
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// formatLine renders one slog.Record as a single console line:
//
//	LEVEL 15:04:05 message key=val key2=val2
//
// Every attr value and the message are passed through sanitize so attacker-
// controlled content cannot inject terminal control sequences into the viewport.
func formatLine(r slog.Record) string {
	var b strings.Builder
	b.WriteString(r.Level.String())
	b.WriteByte(' ')
	b.WriteString(r.Time.Format(timeFormat))
	b.WriteByte(' ')
	b.WriteString(sanitize(r.Message))

	r.Attrs(func(a slog.Attr) bool {
		b.WriteByte(' ')
		b.WriteString(sanitize(a.Key))
		b.WriteByte('=')
		b.WriteString(sanitize(a.Value.String()))
		return true
	})
	return b.String()
}
