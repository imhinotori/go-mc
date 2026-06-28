package host

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.starlark.net/starlark"
)

// reload_test.go covers the FULL hot-reload watcher (Plan 22-02): a fsnotify watcher rebuilds a
// fresh Manager off-tick on a .star/.toml change and publishes it on the swap channel for the tick
// owner to install. TestHotReloadSwap proves the new hook map replaces the old; TestReloadDuringDispatch
// proves a reload concurrent with Emit is race-clean because the SWAP lands on the owner goroutine
// (the sole writer of the live pointer), never mutating a live map mid-dispatch (TICK-05 / T-22-05).

// writeStar writes a plugin dir (plugin.toml + main.star) under root/name with the given hook body.
func writeStar(t *testing.T, root, name, mainStar string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	toml := "name=\"" + name + "\"\nversion=\"1\"\nentrypoint=\"main.star\"\nruntime=\"starlark\"\n"
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(toml), 0o600); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.star"), []byte(mainStar), 0o600); err != nil {
		t.Fatalf("write star: %v", err)
	}
}

// TestHotReloadSwap: a watcher over a plugins root rebuilds a fresh Manager when a plugin file
// changes. After a change that REMOVES the on_block_break hook and ADDS an on_player_join hook, the
// newly-published Manager reflects the new hook map (the removed hook is gone, the new one is live).
func TestHotReloadSwap(t *testing.T) {
	root := t.TempDir()
	// v1 plugin: subscribes on_block_break only.
	writeStar(t, root, "p",
		"def f(x,y,z,s,p):\n    count(1)\nregister(\"on_block_break\", f)\n")

	c := &counter{}
	extra := starlark.StringDict{"count": countBuiltin(c)}

	// Initial build (the Manager the server starts with).
	initial := New()
	if err := initial.LoadDirWith(root, extra); err != nil {
		t.Fatalf("initial LoadDirWith: %v", err)
	}
	if initial.HookCount(EventBlockBreak) != 1 || initial.HookCount(EventPlayerJoin) != 0 {
		t.Fatalf("initial hooks: break=%d join=%d, want 1/0",
			initial.HookCount(EventBlockBreak), initial.HookCount(EventPlayerJoin))
	}

	// The swap channel the "owner" drains (buffered 1 with replace-latest, mirroring TickLoop.pluginSwap).
	swap := make(chan *Manager, 1)
	w, err := NewWatcher(root, swap, extra)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	// v2 plugin: REMOVES on_block_break, ADDS on_player_join.
	writeStar(t, root, "p",
		"def g(name, eid):\n    count(1)\nregister(\"on_player_join\", g)\n")

	// Wait for the watcher to publish a rebuilt Manager on the swap channel.
	var rebuilt *Manager
	select {
	case rebuilt = <-swap:
	case <-time.After(5 * time.Second):
		t.Fatal("no hot-reload swap published within 5s after the plugin change")
	}

	if got := rebuilt.HookCount(EventBlockBreak); got != 0 {
		t.Fatalf("after reload: HookCount(on_block_break) = %d, want 0 (hook removed in v2)", got)
	}
	if got := rebuilt.HookCount(EventPlayerJoin); got != 1 {
		t.Fatalf("after reload: HookCount(on_player_join) = %d, want 1 (hook added in v2)", got)
	}

	// The new Manager's hook actually fires; the removed hook does not.
	rebuilt.Emit(EventBlockBreak, BlockBreakEvent{}) // no subscriber in v2 -> no-op
	if got := c.n.Load(); got != 0 {
		t.Fatalf("on_block_break fired %d times on the v2 Manager, want 0 (removed)", got)
	}
	rebuilt.Emit(EventPlayerJoin, PlayerJoinEvent{Name: "steve", EntityID: 1})
	if got := c.n.Load(); got != 1 {
		t.Fatalf("on_player_join fired %d times on the v2 Manager, want 1 (added)", got)
	}
}

// TestReloadDuringDispatch is the -race proof (Docker CGO=1): a reload concurrent with Emit dispatch
// must produce NO torn map read. The design under test: the SWAP lands on the OWNER goroutine (the
// sole writer of the live *Manager pointer), and Emit reads that pointer ONLY on the owner — exactly
// the TickLoop discipline (drainRegistrations swaps t.plugins; Emit reads t.plugins; both on the tick
// goroutine). A separate "watcher" goroutine only CONSTRUCTS fresh Managers and hands the pointer
// across the swap channel; it never mutates a live map. This must be -race clean; a design that
// mutated the live hooks map mid-dispatch would fail under -race.
func TestReloadDuringDispatch(t *testing.T) {
	root := t.TempDir()
	writeStar(t, root, "p",
		"def f(x,y,z,s,p):\n    count(1)\nregister(\"on_block_break\", f)\n")

	c := &counter{}
	extra := starlark.StringDict{"count": countBuiltin(c)}

	// A pre-built pool of fresh Managers the "watcher" goroutine hands over. Building them up front
	// keeps the watcher goroutine a pure pointer-sender (it never touches a live map), isolating the
	// race surface to the pointer swap + Emit read, which is the exact production seam.
	const swaps = 20
	managers := make([]*Manager, swaps)
	for i := range managers {
		m := New()
		if err := m.LoadDirWith(root, extra); err != nil {
			t.Fatalf("build manager %d: %v", i, err)
		}
		managers[i] = m
	}

	initial := New()
	if err := initial.LoadDirWith(root, extra); err != nil {
		t.Fatalf("initial LoadDirWith: %v", err)
	}

	swap := make(chan *Manager, 1) // buffered 1, replace-latest — mirrors TickLoop.pluginSwap
	done := make(chan struct{})
	var fired atomic.Int64

	var wg sync.WaitGroup
	wg.Add(1)
	// OWNER goroutine: the SOLE writer of `live` and the SOLE caller of Emit — exactly the tick
	// goroutine's role. It drains the swap channel (installing a new pointer on-thread) and dispatches
	// Emit on the current pointer, interleaved, just like drainRegistrations + the seam emits within a
	// tick. Because one goroutine owns both the pointer write and the Emit read, there is no shared
	// mutable state crossing goroutines except the pointer handed over the channel (a safe handoff).
	go func() {
		defer wg.Done()
		live := initial
		for {
			select {
			case mgr := <-swap:
				live = mgr // owner installs the freshly-built Manager on-thread (the swap)
			case <-done:
				return
			default:
				// No pending swap: dispatch on the current Manager (the per-seam Emit).
				live.Emit(EventBlockBreak, BlockBreakEvent{X: 1, Y: 2, Z: 3, State: 4, PlayerID: 5})
				fired.Add(1)
			}
		}
	}()

	// WATCHER goroutine: hands each pre-built Manager across the swap channel (the off-tick rebuild
	// publishing a fresh pointer). It never reads or mutates a live hook map — it only sends pointers,
	// which the owner installs. The send is non-blocking-with-drain so a stalled owner never parks it.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < swaps; i++ {
			select {
			case swap <- managers[i]:
			default:
				// Replace-latest: drop the stale pending swap, install this fresher one.
				select {
				case <-swap:
				default:
				}
				swap <- managers[i]
			}
			time.Sleep(time.Millisecond)
		}
	}()

	// Let the owner dispatch + swap interleave for a bit, then stop.
	time.Sleep(50 * time.Millisecond)
	close(done)
	wg.Wait()

	// Every Emit that ran must have fired the on_block_break hook (each Manager subscribes it). The
	// exact count is timing-dependent; the assertion is that dispatch happened and the counter tracks
	// it 1:1 (no lost/torn fires) — the real proof is -race reporting clean.
	if fired.Load() == 0 {
		t.Fatal("owner never dispatched any Emit")
	}
	if got := c.n.Load(); got != fired.Load() {
		t.Fatalf("hook fired %d times but %d Emits ran — torn dispatch (each Emit must fire exactly one hook)",
			c.n.Load(), fired.Load())
	}
}
