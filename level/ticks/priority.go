// Package ticks is the 1:1 Go port of net.minecraft.world.ticks — the scheduled
// block/fluid tick subsystem (SUB-BLOCKTICK). It mirrors LevelTicks (the level-wide
// queue manager), LevelChunkTicks (the per-chunk container), ScheduledTick / SavedTick
// (the live / on-disk tick records) and TickPriority, ported method-for-method from the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar).
//
// The package is generic over the tick payload type T (the vanilla `T` of
// LevelTicks<T> / ScheduledTick<T>): the block-tick queue is parameterized with a block
// identity, the fluid-tick queue with a fluid identity. T must be comparable so the
// per-position uniqueness set (UNIQUE_TICK_HASH in vanilla — keyed by (type, pos)) can
// use it as a map key.
//
// CITE: net.minecraft.world.ticks.{TickPriority, ScheduledTick, SavedTick,
// LevelChunkTicks, LevelTicks}.
package ticks

// TickPriority is net.minecraft.world.ticks.TickPriority — the drain-order tier of a
// scheduled tick. The enum's declaration order IS its ordinal, and the comparator that
// orders ticks within a target game-time uses Enum.compareTo (the ORDINAL), so
// EXTREMELY_HIGH (ordinal 0) drains before NORMAL (ordinal 3) before EXTREMELY_LOW
// (ordinal 6). Each constant also carries an int `value` used for the on-disk codec
// (TickPriority.CODEC = Codec.INT.xmap(byValue, getValue)); here the ordinal and the
// value happen to coincide monotonically (value = ordinal - 3), so byValue/getValue and
// the ordinal ordering agree — but both are ported faithfully.
//
// CITE: TickPriority static initializer — EXTREMELY_HIGH(-3), VERY_HIGH(-2), HIGH(-1),
// NORMAL(0), LOW(1), VERY_LOW(2), EXTREMELY_LOW(3); $values() ordering.
type TickPriority int

const (
	PriorityExtremelyHigh TickPriority = iota // ordinal 0, value -3
	PriorityVeryHigh                          // ordinal 1, value -2
	PriorityHigh                              // ordinal 2, value -1
	PriorityNormal                            // ordinal 3, value  0
	PriorityLow                               // ordinal 4, value  1
	PriorityVeryLow                           // ordinal 5, value  2
	PriorityExtremelyLow                      // ordinal 6, value  3
)

// priorityValues is the int `value` field per constant, indexed by ordinal — the vanilla
// TickPriority.value used by the codec. value == ordinal - 3.
var priorityValues = [...]int{-3, -2, -1, 0, 1, 2, 3}

// Value returns the TickPriority's int value (TickPriority.getValue) — the number written
// to disk by the `p` codec field. CITE: TickPriority.getValue (return this.value).
func (p TickPriority) Value() int {
	if p < PriorityExtremelyHigh || p > PriorityExtremelyLow {
		return 0 // defensive: an out-of-range ordinal maps to NORMAL's value, never panics
	}
	return priorityValues[p]
}

// PriorityByValue is net.minecraft.world.ticks.TickPriority.byValue(int): the inverse of
// Value, used when LOADING a saved tick. It scans the constants for a matching `value`;
// if none matches it clamps — a value below EXTREMELY_HIGH's (-3) returns EXTREMELY_HIGH,
// otherwise (above EXTREMELY_LOW's +3) EXTREMELY_LOW. CITE: TickPriority.byValue:
//
//	for (TickPriority p : values()) if (p.value == v) return p;
//	return v < EXTREMELY_HIGH.value ? EXTREMELY_HIGH : EXTREMELY_LOW;
func PriorityByValue(v int) TickPriority {
	for p := PriorityExtremelyHigh; p <= PriorityExtremelyLow; p++ {
		if priorityValues[p] == v {
			return p
		}
	}
	if v < priorityValues[PriorityExtremelyHigh] {
		return PriorityExtremelyHigh
	}
	return PriorityExtremelyLow
}
