package server

import (
	"sync"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/plugin/host"
	"github.com/imhinotori/sulfur/world"
	"github.com/imhinotori/sulfur/world/structure"
	"go.starlark.net/starlark"
)

// plugin_events_test.go is the PLUGIN-02 server-wiring gate (Plan 22-02): it proves the 8 discrete
// gameplay seams emit their event EXACTLY ONCE per occurrence, that on_damage carries the FINAL
// post-mitigation value, and — THE GATE — that a hook fires once per real block break with a count
// INDEPENDENT of entity count (event-driven, not a per-tick-per-entity scan). The fixture
// server/testdata/plugins/events registers one counting hook per event; the host-injected
// count(event_name)/record_damage(amount) builtins make each hook fire observable from the test.

// eventCounts records per-event hook fire counts + the last on_damage amount the seam emitted. It
// is mutex-guarded so a (future) concurrent emit cannot race the test's reads — the seam emits run
// on the tick goroutine in production, but the test drives them inline.
type eventCounts struct {
	mu     sync.Mutex
	counts map[string]int
	damage float64
}

func newEventCounts() *eventCounts { return &eventCounts{counts: make(map[string]int)} }

func (e *eventCounts) get(name string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.counts[name]
}

func (e *eventCounts) lastDamage() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.damage
}

// countBuiltin returns a count(event_name) builtin that increments the named counter.
func (e *eventCounts) countBuiltin() *starlark.Builtin {
	return starlark.NewBuiltin("count", func(th *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name string
		if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &name); err != nil {
			return nil, err
		}
		e.mu.Lock()
		e.counts[name]++
		e.mu.Unlock()
		return starlark.None, nil
	})
}

// recordDamageBuiltin returns a record_damage(amount) builtin that captures the FINAL amount the
// on_damage seam emitted, so TestDamageIsPostMitigation can assert it.
func (e *eventCounts) recordDamageBuiltin() *starlark.Builtin {
	return starlark.NewBuiltin("record_damage", func(th *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var amount float64
		if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &amount); err != nil {
			return nil, err
		}
		e.mu.Lock()
		e.damage = amount
		e.mu.Unlock()
		return starlark.None, nil
	})
}

// loadEventsManager builds a host.Manager from the events fixture with the test builtins injected,
// returning it + the observable counters. It uses the public LoadDirWith seam (the same one Plan
// 22-01 exposed for tests + the Plan 22-02 watcher) so no unexported host internals are touched.
func loadEventsManager(t *testing.T) (*host.Manager, *eventCounts) {
	t.Helper()
	ec := newEventCounts()
	m := host.New()
	extra := starlark.StringDict{
		"count":         ec.countBuiltin(),
		"record_damage": ec.recordDamageBuiltin(),
	}
	if err := m.LoadDirWith("testdata/plugins", extra); err != nil {
		t.Fatalf("LoadDirWith(testdata/plugins): %v", err)
	}
	// Sanity: all 8 hooks captured at load (register-once).
	for _, evt := range []host.EventType{
		host.EventTick, host.EventPlayerJoin, host.EventPlayerLeave, host.EventBlockBreak,
		host.EventBlockPlace, host.EventEntitySpawn, host.EventEntityDeath, host.EventDamage,
	} {
		if m.HookCount(evt) != 1 {
			t.Fatalf("HookCount(%s) = %d, want 1 (fixture register-once)", evt, m.HookCount(evt))
		}
	}
	return m, ec
}

// TestBlockBreakEventFiresOnce is THE GATE (PLUGIN-02's load-bearing proof): a counting
// on_block_break hook, wired into a TickLoop with N entities present, fires EXACTLY ONCE when ONE
// block is broken through the break funnel — the count is N-independent (1, NOT N, NOT N×ticks).
// This is the concrete "event-driven, not per-tick-per-entity scan" proof: the emit hangs off the
// discrete destroyBlock occurrence, not the O(entities×ticks) tick loops.
func TestBlockBreakEventFiresOnce(t *testing.T) {
	loop, mgr := newBlockLoop()
	m, ec := loadEventsManager(t)
	loop.SetPlugins(m)

	// Populate the entity store with N entities. If the break emit were (wrongly) hung off a
	// per-entity loop, the counter would scale with N; the GATE asserts it does NOT.
	const n = 50
	for i := 0; i < n; i++ {
		e := NewEntity(loop.idAlloc.AllocID(), entity.Witch, float64(i), 64, 0)
		loop.entities.add(e)
	}

	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeCreative // creative breaks on START (one real break through destroyBlock)

	target := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(target, block.ToStateID[block.Stone{}], dimMinY)

	pa := playerActionPacket(0 /*START_DESTROY_BLOCK*/, target, 1, 42)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: pa})

	if got := ec.get("on_block_break"); got != 1 {
		t.Fatalf("on_block_break fired %d times for ONE break with %d entities present, want EXACTLY 1 "+
			"(event-driven seam, NOT a per-entity-per-tick scan)", got, n)
	}
	// on_tick must NOT have fired — the break was driven directly, no tickOnce ran.
	if got := ec.get("on_tick"); got != 0 {
		t.Fatalf("on_tick fired %d times during a direct break (no tickOnce run), want 0", got)
	}
}

// TestEventSeams exercises each discrete seam once and asserts its hook fired exactly once per
// occurrence (no double-fire, no per-tick multiplication).
func TestEventSeams(t *testing.T) {
	// --- on_block_break + on_block_place (one break, one place) ---
	t.Run("break_and_place", func(t *testing.T) {
		loop, mgr := newBlockLoop()
		m, ec := loadEventsManager(t)
		loop.SetPlugins(m)

		p := blockPlayer(loop, 1.5, 65.0, 1.5)
		p.gameMode = gameModeCreative

		// Break a stone block at (1,64,1).
		target := pk.Position{X: 1, Y: 64, Z: 1}
		mgr.SetBlock(target, block.ToStateID[block.Stone{}], dimMinY)
		loop.applyInput(p, SubtickInput{At: loop.clock.Now(),
			Packet: playerActionPacket(0 /*START*/, target, 1, 1)})

		if got := ec.get("on_block_break"); got != 1 {
			t.Fatalf("on_block_break = %d, want 1", got)
		}
		// A break must NOT fire on_block_place (Pitfall 2: shared broadcaster double-fire).
		if got := ec.get("on_block_place"); got != 0 {
			t.Fatalf("on_block_place = %d after a BREAK, want 0 (no double-fire via the shared broadcaster)", got)
		}

		// Place a stone block onto the +Y face of a block at (3,64,3). Give the player a held stone.
		setHeldItem(p, item.Stone.ID, 5)
		floor := pk.Position{X: 3, Y: 64, Z: 3}
		mgr.SetBlock(floor, block.ToStateID[block.Stone{}], dimMinY)
		// UseItemOn the TOP face (direction 1 = up) so the placement lands at (3,65,3) which is air.
		ui := useItemOnPacket(0, floor, 1, 0.5, 1.0, 0.5, false, false, 2)
		loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

		if got := ec.get("on_block_place"); got != 1 {
			t.Fatalf("on_block_place = %d after a PLACE, want 1", got)
		}
		// The place must NOT have re-fired on_block_break.
		if got := ec.get("on_block_break"); got != 1 {
			t.Fatalf("on_block_break = %d after a place, want still 1 (no place->break double-fire)", got)
		}
	})

	// --- on_player_join + on_player_leave (one join, one leave) ---
	t.Run("join_and_leave", func(t *testing.T) {
		loop := NewTickLoop(newFakeClock())
		m, ec := loadEventsManager(t)
		loop.SetPlugins(m)

		p := &tickPlayer{
			client:   captureClient(64),
			entityID: loop.idAlloc.AllocID(),
			name:     "steve",
			gameMode: gameModeSurvival,
		}
		// Drive the join seam through the register channel + drainRegistrations (the owner path).
		loop.register <- p
		loop.drainRegistrations()
		if got := ec.get("on_player_join"); got != 1 {
			t.Fatalf("on_player_join = %d, want 1", got)
		}

		// Drive the leave seam through the unregister channel + drainRegistrations.
		loop.unregister <- p.client
		loop.drainRegistrations()
		if got := ec.get("on_player_leave"); got != 1 {
			t.Fatalf("on_player_leave = %d, want 1", got)
		}
	})

	// --- on_entity_spawn (one structure spawn) ---
	t.Run("entity_spawn", func(t *testing.T) {
		loop := NewTickLoop(newFakeClock())
		m, ec := loadEventsManager(t)
		loop.SetPlugins(m)

		loop.drainStructureSpawns(chunkResultWithSpawn("minecraft:witch", 10, 64, 10))
		if got := ec.get("on_entity_spawn"); got != 1 {
			t.Fatalf("on_entity_spawn = %d for one structure spawn, want 1", got)
		}
	})

	// --- on_entity_death (one death) ---
	t.Run("entity_death", func(t *testing.T) {
		loop, _ := newBlockLoop()
		m, ec := loadEventsManager(t)
		loop.SetPlugins(m)
		p := combatPlayer(loop, 7)

		loop.die(p)
		if got := ec.get("on_entity_death"); got != 1 {
			t.Fatalf("on_entity_death = %d, want 1", got)
		}
	})

	// --- on_tick (one tick) ---
	t.Run("tick", func(t *testing.T) {
		loop, _ := newBlockLoop()
		m, ec := loadEventsManager(t)
		loop.SetPlugins(m)

		loop.tickOnce()
		if got := ec.get("on_tick"); got != 1 {
			t.Fatalf("on_tick = %d after ONE tickOnce, want exactly 1 (the single per-tick emit)", got)
		}
	})
}

// TestDamageIsPostMitigation: the on_damage seam emits the FINAL post-mitigation value from
// actuallyHurt (after armor/magic/absorption) — NOT the raw applyDamage input. With v1's
// pass-through armor (ARMOR attribute base 0), the emitted amount equals the landed health delta.
func TestDamageIsPostMitigation(t *testing.T) {
	loop, _ := newBlockLoop()
	m, ec := loadEventsManager(t)
	loop.SetPlugins(m)
	p := combatPlayer(loop, 7)

	const raw float32 = 6
	hpBefore := p.health
	loop.applyDamage(p, raw)
	landed := float64(hpBefore - p.health)

	if got := ec.get("on_damage"); got != 1 {
		t.Fatalf("on_damage = %d, want 1 (emitted from actuallyHurt, NOT applyDamage)", got)
	}
	// With v1 pass-through armor the post-mitigation amount == the raw amount == the landed delta.
	if got := ec.lastDamage(); got != landed {
		t.Fatalf("on_damage amount = %v, want the FINAL landed delta %v (post-mitigation)", got, landed)
	}
	if got := ec.lastDamage(); got != float64(raw) {
		t.Fatalf("on_damage amount = %v, want %v (v1 pass-through armor: post == raw)", got, raw)
	}
}

// chunkResultWithSpawn builds a minimal world.ChunkResult carrying one structure SpawnRequest so a
// test can drive drainStructureSpawns (the on_entity_spawn seam).
func chunkResultWithSpawn(entityType string, x, y, z float64) world.ChunkResult {
	return world.ChunkResult{Spawns: []structure.SpawnRequest{{EntityType: entityType, X: x, Y: y, Z: z}}}
}
