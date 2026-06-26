package tui

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newRecord(level slog.Level, msg string, attrs ...slog.Attr) slog.Record {
	r := slog.NewRecord(time.Now(), level, msg, 0)
	r.AddAttrs(attrs...)
	return r
}

// TestHeadlessStderr: NewHandler(nil)-equivalent writes a plain structured line to
// the configured writer and does not panic with no program / no bridge.
func TestHeadlessStderr(t *testing.T) {
	var buf bytes.Buffer
	h := newHandlerWith(&buf, 0, nil) // no sink → headless (stderr-only) path

	if h.ch != nil {
		t.Fatal("headless handler must not allocate a bridge channel")
	}

	err := h.Handle(context.Background(), newRecord(slog.LevelInfo, "player joined",
		slog.String("name", "Alice"), slog.String("addr", "1.2.3.4")))
	if err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"player joined", "name=Alice", "addr=1.2.3.4"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stderr line missing %q: %q", want, out)
		}
	}
}

// TestBridgeNonBlocking (race gate): a tiny bounded bridge fed 10_000 concurrent
// Handle calls must never block, must drop on full (drops > 0), and the always-on
// stderr path must see all 10_000 records (never drops).
func TestBridgeNonBlocking(t *testing.T) {
	const total = 10_000
	const bridge = 4 // deliberately tiny so the bridge overflows and drops

	var buf threadSafeBuffer
	var delivered atomic.Uint64

	// A slow sink: sleeps so the bridge channel saturates and Handle must drop.
	sink := func(logLineMsg) {
		delivered.Add(1)
		time.Sleep(time.Millisecond)
	}
	h := newHandlerWith(&buf, bridge, sink)

	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		const workers = 20
		per := total / workers
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < per; i++ {
					_ = h.Handle(context.Background(),
						newRecord(slog.LevelInfo, "flood", slog.Int("i", i)))
				}
			}()
		}
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Handle calls blocked: 10k concurrent emits did not complete (bridge must be non-blocking)")
	}

	// The always-on stderr path must have recorded every single line.
	if got := strings.Count(buf.String(), "flood"); got != total {
		t.Fatalf("stderr path dropped lines: saw %d 'flood' records, want %d", got, total)
	}

	// The bounded bridge with a slow sink must have dropped at least once.
	if h.drops.Load() == 0 {
		t.Fatal("expected drop-on-full to fire (drops==0); bridge did not exercise the drop path")
	}
}

// TestFormatLineSanitizes: a record carrying an attr value with an embedded ANSI
// escape must emit a line with no raw ESC byte (terminal-injection mitigation).
func TestFormatLineSanitizes(t *testing.T) {
	r := newRecord(slog.LevelWarn, "kick",
		slog.String("name", "evil\x1b[2Jname"),
		slog.String("note", "tab\tand\x00null"))

	line := formatLine(r)

	if strings.ContainsRune(line, '\x1b') {
		t.Fatalf("formatLine left a raw ESC byte in the output: %q", line)
	}
	for _, bad := range []rune{'\x00', '\t', '\x7f'} {
		if strings.ContainsRune(line, bad) {
			t.Fatalf("formatLine left control byte %#x in the output: %q", bad, line)
		}
	}
	// The printable remainder survives — only the control bytes are removed. The ESC
	// (\x1b) is gone, so the surrounding "[2J" is now inert literal text, not a
	// terminal command; that is the mitigation working as intended.
	if !strings.Contains(line, "evil") || !strings.Contains(line, "name=") {
		t.Fatalf("formatLine stripped printable content: %q", line)
	}
	if !strings.Contains(line, "tabandnull") {
		t.Fatalf("formatLine stripped printable content: %q", line)
	}
}

// threadSafeBuffer serializes concurrent Writes (slog.TextHandler holds its own
// mutex, but the test reads buf after the goroutines join — guard Write anyway).
type threadSafeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *threadSafeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *threadSafeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
