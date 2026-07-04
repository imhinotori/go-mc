package server

// legacy_random.go — a bit-exact net.minecraft.util.RandomSource / LegacyRandomSource
// (java.util.Random LCG) for the ENCHANTMENT subsystem (the anvil break roll uses the level
// random; the enchantment TABLE offer + apply rolls use the menu's per-player enchantmentSeed).
//
// WHY BIT-EXACT (not the PCG-backed entityRandom): the enchantment-table offers are OBSERVABLE to
// the client — the three offered enchantments + their level costs are computed by seeding a
// RandomSource with the player's enchantmentSeed and drawing a FIXED sequence
// (EnchantmentHelper.getEnchantmentCost + selectEnchantment). For a given seed the exact draw
// stream (not merely its order) determines which enchantments a client sees, so parity here
// requires the bit-exact java.util.Random LCG, not a draw-order-faithful PCG. This mirrors the
// carver's legacyRandom (world/levelgen/carver/random.go) — the SAME algorithm, ported once more
// in the server package so no cross-package export is needed.
//
// Sources (javap -c, temp/cache/26.2-inner.jar):
//   - net.minecraft.util.RandomSource.create(long) -> new LegacyRandomSource(seed)
//   - net.minecraft.world.level.levelgen.LegacyRandomSource (setSeed / next)
//   - net.minecraft.world.level.levelgen.BitRandomSource (nextInt / nextInt(bound) / nextFloat)

// legacyRandom ports LegacyRandomSource: a 48-bit LCG. Every operation mirrors the bytecode
// constant-for-constant (algorithmic port, not a copy of Mojang source).
type legacyRandom struct {
	seed uint64 // the 48-bit LCG state
}

// LCG constants (LegacyRandomSource ConstantValue attributes).
const (
	legacyLCGMultiplier = uint64(0x5DEECE66D)   // 25214903917
	legacyLCGIncrement  = uint64(0xB)           // 11
	legacyLCGMask       = uint64((1 << 48) - 1) // 281474976710655
)

// newLegacyRandom mirrors RandomSource.create(seed) -> LegacyRandomSource(seed) -> setSeed(seed).
func newLegacyRandom(seed int64) *legacyRandom {
	r := &legacyRandom{}
	r.setSeed(seed)
	return r
}

// setSeed ports LegacyRandomSource.setSeed: seed = (s ^ MULTIPLIER) & MASK.
func (r *legacyRandom) setSeed(s int64) {
	r.seed = (uint64(s) ^ legacyLCGMultiplier) & legacyLCGMask
}

// next ports BitRandomSource.next(bits) via the LCG step: seed = (seed*MUL + INC) & MASK;
// return (int)(seed >>> (48 - bits)) as a signed 32-bit int.
func (r *legacyRandom) next(bits int) int32 {
	r.seed = (r.seed*legacyLCGMultiplier + legacyLCGIncrement) & legacyLCGMask
	return int32(r.seed >> (48 - bits))
}

// nextInt ports BitRandomSource.nextInt() = next(32) — a full-range signed int (used by the
// enchantmentSeed re-roll: player.enchantmentSeed = random.nextInt()).
func (r *legacyRandom) nextInt() int32 { return r.next(32) }

// nextIntN ports BitRandomSource.nextInt(bound) (java.util.Random.nextInt): power-of-two fast
// path, otherwise the rejection loop. bound must be > 0.
func (r *legacyRandom) nextIntN(bound int32) int32 {
	if bound <= 0 {
		return 0 // vanilla throws; the ported callers never pass bound<=0.
	}
	// Power-of-two fast path: (bound * (long)next(31)) >> 31.
	if bound&(bound-1) == 0 {
		return int32((int64(bound) * int64(r.next(31))) >> 31)
	}
	for {
		bits := r.next(31)
		val := bits % bound
		if bits-val+(bound-1) >= 0 {
			return val
		}
	}
}

// nextFloat ports BitRandomSource.nextFloat = next(24) * 0x1.0p-24.
func (r *legacyRandom) nextFloat() float32 {
	return float32(r.next(24)) * 5.9604645e-8
}
