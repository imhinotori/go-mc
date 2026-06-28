package host

import "go.starlark.net/starlark"

// EventType is the discrete gameplay-event identifier a plugin subscribes to
// via register(event_name, fn). The string values are the Starlark-facing
// names the plugin author types — they MUST match exactly (a typo is rejected
// at load by the register builtin, never silently dropped).
type EventType string

// The 8 core events. Each fires on a DISCRETE occurrence (a block breaks, a
// player joins, a mob dies) — NEVER per-entity-per-tick. on_tick is the single
// per-tick event and is guarded by the zero-subscriber check in Emit so an
// unsubscribed server pays nothing.
const (
	EventTick        EventType = "on_tick"
	EventPlayerJoin  EventType = "on_player_join"
	EventPlayerLeave EventType = "on_player_leave"
	EventBlockBreak  EventType = "on_block_break"
	EventBlockPlace  EventType = "on_block_place"
	EventEntitySpawn EventType = "on_entity_spawn"
	EventEntityDeath EventType = "on_entity_death"
	EventDamage      EventType = "on_damage"
)

// knownEvents is the closed set the register builtin validates against. Adding
// an event means adding its const above AND its entry here.
var knownEvents = map[EventType]struct{}{
	EventTick:        {},
	EventPlayerJoin:  {},
	EventPlayerLeave: {},
	EventBlockBreak:  {},
	EventBlockPlace:  {},
	EventEntitySpawn: {},
	EventEntityDeath: {},
	EventDamage:      {},
}

// isKnownEvent reports whether evt is one of the 8 supported events. The
// register builtin uses this to reject a typo'd event name at LOAD.
func isKnownEvent(evt EventType) bool {
	_, ok := knownEvents[evt]
	return ok
}

// Event is a discrete-occurrence payload. toStarlark turns it into the frozen
// positional args a STARLARK hook is called with; toArgs turns it into the SAME
// scalars as a plain Go []any for the PYTHON off-tick lane (gopy CallGoArgs
// marshals Go scalars to python objects). Phase 22 payloads carry ONLY frozen
// scalars (ints/strings/float) — NO live *Entity/*world handles (those are the
// Phase-23 frozen-handle API). A starlark.Int/String/Float is immutable and the
// []any carries value copies, so both are inherently race-safe to read on any
// Emit thread or off-tick worker.
type Event interface {
	toStarlark() starlark.Tuple
	// toArgs returns the event's positional payload as plain Go scalars, in the
	// SAME order as toStarlark, for the python off-tick lane. The slice is a fresh
	// value copy per call (no shared backing) so an off-tick worker reading it
	// never races the tick (26-RESEARCH: only plain scalars cross to python).
	toArgs() []any
}

// TickEvent carries the current game tick. Args: (tick).
type TickEvent struct{ Tick int }

func (e TickEvent) toStarlark() starlark.Tuple {
	return starlark.Tuple{starlark.MakeInt(e.Tick)}
}

func (e TickEvent) toArgs() []any { return []any{e.Tick} }

// PlayerJoinEvent fires once per join. Args: (name, entity_id).
type PlayerJoinEvent struct {
	Name     string
	EntityID int
}

func (e PlayerJoinEvent) toStarlark() starlark.Tuple {
	return starlark.Tuple{starlark.String(e.Name), starlark.MakeInt(e.EntityID)}
}

func (e PlayerJoinEvent) toArgs() []any { return []any{e.Name, e.EntityID} }

// PlayerLeaveEvent fires once per leave. Args: (name, entity_id).
type PlayerLeaveEvent struct {
	Name     string
	EntityID int
}

func (e PlayerLeaveEvent) toStarlark() starlark.Tuple {
	return starlark.Tuple{starlark.String(e.Name), starlark.MakeInt(e.EntityID)}
}

func (e PlayerLeaveEvent) toArgs() []any { return []any{e.Name, e.EntityID} }

// BlockBreakEvent fires once per block actually removed. Args:
// (x, y, z, state, player_id).
type BlockBreakEvent struct{ X, Y, Z, State, PlayerID int }

func (e BlockBreakEvent) toStarlark() starlark.Tuple {
	return starlark.Tuple{
		starlark.MakeInt(e.X), starlark.MakeInt(e.Y), starlark.MakeInt(e.Z),
		starlark.MakeInt(e.State), starlark.MakeInt(e.PlayerID),
	}
}

func (e BlockBreakEvent) toArgs() []any { return []any{e.X, e.Y, e.Z, e.State, e.PlayerID} }

// BlockPlaceEvent fires once per placed block. Args:
// (x, y, z, state, player_id).
type BlockPlaceEvent struct{ X, Y, Z, State, PlayerID int }

func (e BlockPlaceEvent) toStarlark() starlark.Tuple {
	return starlark.Tuple{
		starlark.MakeInt(e.X), starlark.MakeInt(e.Y), starlark.MakeInt(e.Z),
		starlark.MakeInt(e.State), starlark.MakeInt(e.PlayerID),
	}
}

func (e BlockPlaceEvent) toArgs() []any { return []any{e.X, e.Y, e.Z, e.State, e.PlayerID} }

// EntitySpawnEvent fires once per spawned entity. Args:
// (entity_id, type_id, x, y, z).
type EntitySpawnEvent struct {
	EntityID int
	TypeID   int
	X, Y, Z  int
}

func (e EntitySpawnEvent) toStarlark() starlark.Tuple {
	return starlark.Tuple{
		starlark.MakeInt(e.EntityID), starlark.MakeInt(e.TypeID),
		starlark.MakeInt(e.X), starlark.MakeInt(e.Y), starlark.MakeInt(e.Z),
	}
}

func (e EntitySpawnEvent) toArgs() []any { return []any{e.EntityID, e.TypeID, e.X, e.Y, e.Z} }

// EntityDeathEvent fires once per death. Args: (entity_id, type_id).
type EntityDeathEvent struct {
	EntityID int
	TypeID   int
}

func (e EntityDeathEvent) toStarlark() starlark.Tuple {
	return starlark.Tuple{starlark.MakeInt(e.EntityID), starlark.MakeInt(e.TypeID)}
}

func (e EntityDeathEvent) toArgs() []any { return []any{e.EntityID, e.TypeID} }

// DamageEvent fires once per damage application. Amount is the FINAL
// post-mitigation value (after armor/effects/absorption) — the value most
// hooks want, matching where the jar computes final damage (locked decision).
// Args: (entity_id, amount).
type DamageEvent struct {
	EntityID int
	Amount   float64
}

func (e DamageEvent) toStarlark() starlark.Tuple {
	return starlark.Tuple{starlark.MakeInt(e.EntityID), starlark.Float(e.Amount)}
}

func (e DamageEvent) toArgs() []any { return []any{e.EntityID, e.Amount} }
