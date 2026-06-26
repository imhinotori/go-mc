package tui

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
)

// bridgeCap is the depth of the log→TUI hand-off channel. It mirrors the project's
// inboundCap convention. The channel is bounded and the send is non-blocking
// (drop-on-full), so an emitting tick/network goroutine NEVER blocks on a slow or
// stalled render (threat T-19-02). The always-on stderr path keeps the full record
// even when a TUI line is dropped (threat T-19-03).
const bridgeCap = 1024

// fanoutHandler is a slog.Handler that fans each record out to two sinks:
//
//  1. the embedded slog.Handler (a text handler → stderr) — ALWAYS, so headless /
//     Docker logs are preserved exactly as today (the non-TTY default path).
//  2. when a TUI is active, a formatted line pushed NON-BLOCKING into the program
//     via a single forwarder goroutine (one-writer discipline on prog.Send).
//
// Model state is never touched from here; the only cross-goroutine hand-off is the
// bounded channel drained by forwarder, which calls prog.Send (goroutine-safe).
type fanoutHandler struct {
	slog.Handler // embedded text handler → stderr (always on)

	prog *tea.Program    // nil in non-TTY mode
	ch   chan logLineMsg // bounded; nil when no TUI is active
	sink func(logLineMsg) // test seam: forwarder calls this instead of prog.Send when set
	drops atomic.Uint64
}

// NewHandler builds the operator-console log handler. When prog != nil (TTY mode)
// records also flow into the bubbletea program; when prog == nil (headless) only
// the stderr path is active. The stderr path is always on.
func NewHandler(prog *tea.Program) slog.Handler {
	text := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	h := &fanoutHandler{Handler: text, prog: prog}
	if prog != nil {
		h.ch = make(chan logLineMsg, bridgeCap)
		go h.forwarder()
	}
	return h
}

// newHandlerWith is a test-only constructor: it directs the always-on path at w
// and routes the bridge to sink (with an explicit channel capacity) WITHOUT a real
// tea.Program, so the headless-stderr and non-blocking-drop behaviors can be tested
// in isolation.
func newHandlerWith(w io.Writer, cap int, sink func(logLineMsg)) *fanoutHandler {
	text := slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})
	h := &fanoutHandler{Handler: text, sink: sink}
	if sink != nil {
		h.ch = make(chan logLineMsg, cap)
		go h.forwarder()
	}
	return h
}

// Handle writes the record to the always-on stderr path, then — if a TUI bridge is
// active — pushes a formatted line non-blocking, dropping (and counting) when the
// channel is full so the emitting goroutine never blocks.
func (h *fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	// 1) Always keep the structured/stderr path (never let the TUI starve logs).
	_ = h.Handler.Handle(ctx, r)

	// 2) If a TUI bridge is up, push a formatted line NON-BLOCKING.
	if h.ch != nil {
		line := formatLine(r)
		select {
		case h.ch <- logLineMsg(line):
		default:
			h.drops.Add(1) // viewport behind → drop, never block the emitter
		}
	}
	return nil
}

// forwarder is the ONLY goroutine that delivers bridged lines (to prog.Send in
// production, or the injected sink under test) — single-writer discipline.
func (h *fanoutHandler) forwarder() {
	for msg := range h.ch {
		if h.sink != nil {
			h.sink(msg)
			continue
		}
		h.prog.Send(msg) // goroutine-safe
	}
}

// WithAttrs / WithGroup preserve the always-on text path's grouping/attrs while
// keeping the TUI bridge attached, so the fan-out survives logger derivation.
func (h *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &fanoutHandler{Handler: h.Handler.WithAttrs(attrs), prog: h.prog, ch: h.ch, sink: h.sink}
}

func (h *fanoutHandler) WithGroup(name string) slog.Handler {
	return &fanoutHandler{Handler: h.Handler.WithGroup(name), prog: h.prog, ch: h.ch, sink: h.sink}
}
