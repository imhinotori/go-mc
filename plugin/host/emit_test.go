package host

import (
	"testing"

	"go.starlark.net/starlark"
)

// TestEmitFires: Emit on a subscribed event runs the greeter hook exactly once
// per occurrence (the counter increments by 1 per Emit).
func TestEmitFires(t *testing.T) {
	m, c := loadGreeter(t)
	payload := BlockBreakEvent{X: 1, Y: 64, Z: -3, State: 10, PlayerID: 7}

	m.Emit(EventBlockBreak, payload)
	if got := c.n.Load(); got != 1 {
		t.Fatalf("after 1 Emit: counter = %d, want 1", got)
	}
	m.Emit(EventBlockBreak, payload)
	m.Emit(EventBlockBreak, payload)
	if got := c.n.Load(); got != 3 {
		t.Fatalf("after 3 Emits: counter = %d, want 3 (one fire per occurrence)", got)
	}
}

// TestEmitNoSubscribers: Emit on an event nothing subscribed is a no-op — no
// panic, counter untouched.
func TestEmitNoSubscribers(t *testing.T) {
	m, c := loadGreeter(t)
	// greeter subscribes on_block_break only; on_player_join has no hooks.
	m.Emit(EventPlayerJoin, PlayerJoinEvent{Name: "steve", EntityID: 1})
	if got := c.n.Load(); got != 0 {
		t.Fatalf("Emit with zero subscribers: counter = %d, want 0", got)
	}
	// An event no plugin ever touches, with a nil-ish payload path, must also
	// short-circuit before toStarlark.
	m.Emit(EventTick, TickEvent{Tick: 99})
	if got := c.n.Load(); got != 0 {
		t.Fatalf("Emit on_tick with zero subscribers: counter = %d, want 0", got)
	}
}

// TestHookIsolation: badhook (fails) + greeter both subscribe on_block_break.
// Emit must fire both; the badhook error is isolated and greeter still runs.
func TestHookIsolation(t *testing.T) {
	c := &counter{}
	m := New()
	// Load both fixtures from a root containing greeter + badhook.
	root := multiPluginRoot(t, "greeter", "badhook")
	if err := m.LoadDirWith(root, starlark.StringDict{"count": countBuiltin(c)}); err != nil {
		t.Fatalf("LoadDirWith(greeter+badhook): %v", err)
	}
	if got := m.HookCount(EventBlockBreak); got != 2 {
		t.Fatalf("HookCount(on_block_break) = %d, want 2", got)
	}

	// Emit must not panic despite badhook's fail(); greeter must still fire.
	m.Emit(EventBlockBreak, BlockBreakEvent{X: 0, Y: 0, Z: 0, State: 1, PlayerID: 2})
	if got := c.n.Load(); got != 1 {
		t.Fatalf("after isolated Emit: greeter counter = %d, want 1 (badhook error isolated)", got)
	}
}

func multiPluginRoot(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range names {
		dst := dirJoin(t, root, name)
		copyFixture(t, name, dst)
	}
	return root
}
