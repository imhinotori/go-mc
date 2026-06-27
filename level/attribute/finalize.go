package attribute

// finalize.go — the port of the ATTRIBUTE + RNG slice of net.minecraft.world.entity.Mob.finalizeSpawn
// (the per-mob spawn initialization vanilla runs after placement). Verified against Mob.finalizeSpawn
// bytecode (javap -c -p, temp/cache/26.2-inner.jar) this session.
//
// FAITHFUL BYTECODE TRACE (Mob.finalizeSpawn, the v1-relevant slice):
//
//	RandomSource random = level.getRandom();                          // 0..6
//	AttributeInstance follow = getAttribute(FOLLOW_RANGE);            // 8..21  (Objects.requireNonNull)
//	if (!follow.hasModifier(RANDOM_SPAWN_BONUS_ID)) {                 // 23..31
//	    follow.addPermanentModifier(new AttributeModifier(           // 34..60
//	        RANDOM_SPAWN_BONUS_ID,
//	        random.triangle(0.0, 0.11485000000000001),               //   <-- TWO nextDouble() draws
//	        ADD_MULTIPLIED_BASE));
//	}
//	setLeftHanded(random.nextFloat() < 0.05F);                       // 63..83  (ONE nextFloat() draw)
//	return groupData;                                                 // 86..88  (unchanged)
//
// THE RNG DRAW ORDER IS LOAD-BEARING and must be reproduced EXACTLY (a wrong draw order desyncs every
// subsequent RNG-driven spawn decision on the same RandomSource):
//
//  1. random.triangle(0.0, 0.11485000000000001) draws nextDouble() TWICE — the MINUEND first:
//     `d + d2 * (nextDouble() - nextDouble())` (RandomSource.triangle default method). So the spawn
//     bonus consumes exactly two nextDouble() draws, minuend-before-subtrahend.
//     -- BUT only when FOLLOW_RANGE does NOT already carry the RANDOM_SPAWN_BONUS_ID modifier (the
//        re-finalize guard). On a fresh spawn it never does, so the two draws happen.
//  2. random.nextFloat() draws ONCE for the left-handed roll (< 0.05F == 5% left-handed).
//
// The bonus is a PERMANENT modifier on FOLLOW_RANGE with operation ADD_MULTIPLIED_BASE, amount in
// roughly [-0.11485, +0.11485] (triangular about 0), id "minecraft:random_spawn_bonus" — so a mob's
// effective follow range is jittered ~±11.5% per spawn. Permanent so it persists across a save (the
// CITED entity-NBT-persistence TODO).

// randomSpawnBonusID is the port of Mob.RANDOM_SPAWN_BONUS_ID =
// Identifier.withDefaultNamespace("random_spawn_bonus") -> "minecraft:random_spawn_bonus". It is the
// modifier id finalizeSpawn attaches to FOLLOW_RANGE and the id the re-finalize guard checks.
const randomSpawnBonusID = "minecraft:random_spawn_bonus"

// randomSpawnBonusSpread is the second triangle() argument, the EXACT double literal 0.11485000000000001
// from the bytecode (ldc2_w #1509). It is the half-spread of the triangular follow-range jitter.
const randomSpawnBonusSpread = 0.11485000000000001

// leftHandedChance is the nextFloat() threshold for the left-handed roll: `random.nextFloat() < 0.05F`
// (ldc_w #1527 == float 0.05f). A mob is left-handed with 5% probability.
const leftHandedChance float32 = 0.05

// RandomSource is the minimal subset of net.minecraft.util.RandomSource finalizeSpawn consumes: the
// two draw primitives, in the exact order Mob.finalizeSpawn calls them. Any of the project's RNG
// implementations (world/levelgen Xoroshiro / LegacyRandomSource) satisfies this structurally — it is
// declared locally (not imported from world/levelgen) to keep the attribute package free of a
// world-gen dependency, while remaining drop-in compatible with those sources.
//
// NextDouble is RandomSource.nextDouble() in [0,1); NextFloat is RandomSource.nextFloat() in [0,1).
type RandomSource interface {
	NextDouble() float64
	NextFloat() float32
}

// triangle is the port of RandomSource.triangle(double d, double d2) (the interface DEFAULT method):
//
//	return d + d2 * (nextDouble() - nextDouble());
//
// The MINUEND nextDouble() is drawn FIRST, then the subtrahend — this draw ORDER is the load-bearing
// detail (two consecutive nextDouble() calls, first minus second). The result is triangularly
// distributed about d with half-spread d2. Exported is unnecessary; kept lowercase as it is only used
// by FinalizeSpawn here, but the draw order is asserted directly in the tests.
func triangle(r RandomSource, d, d2 float64) float64 {
	// fload/dload order in the bytecode: d, d2, then nextDouble() (minuend), then nextDouble()
	// (subtrahend), then dsub, dmul, dadd. We draw the minuend first to match.
	minuend := r.NextDouble()
	subtrahend := r.NextDouble()
	return d + d2*(minuend-subtrahend)
}

// FinalizeSpawn is the port of the attribute + RNG slice of Mob.finalizeSpawn(ServerLevelAccessor,
// DifficultyInstance, EntitySpawnReason, SpawnGroupData). It runs the EXACT two-step RNG draw on a
// freshly spawned mob's AttributeMap:
//
//  1. If FOLLOW_RANGE has no RANDOM_SPAWN_BONUS_ID modifier, add a permanent ADD_MULTIPLIED_BASE
//     modifier with amount = triangle(0.0, 0.11485000000000001) — consuming TWO nextDouble() draws.
//  2. Roll left-handedness: nextFloat() < 0.05F — consuming ONE nextFloat() draw. The boolean is
//     returned (the caller sets the mob's left-handed flag / SynchedEntityData bit when that lands;
//     v1 has no left-handed metadata consumer, so the bool is returned for the caller to apply and
//     the draw is preserved for RNG-stream fidelity — CITED: the setLeftHanded consumer is wired when
//     left-handed metadata exists).
//
// The mob's AttributeMap MUST register FOLLOW_RANGE (every Mob supplier does, via createMobAttributes)
// — GetInstance(follow_range) returns a non-nil instance for any mob built from a Mob supplier. If a
// caller passes a map WITHOUT follow_range (a non-mob), the bonus step is skipped (no panic), since
// that entity is not a Mob and finalizeSpawn would not apply to it; the left-handed draw still runs to
// keep callers uniform.
//
// Returns leftHanded so the caller can apply it (and so the test can assert the deterministic roll).
func FinalizeSpawn(m *Map, r RandomSource) (leftHanded bool) {
	// AttributeInstance follow = getAttribute(FOLLOW_RANGE) (Objects.requireNonNull). GetInstance
	// materializes the entity-local instance from the supplier template so the permanent modifier is
	// recorded on the entity, exactly as getAttribute(FOLLOW_RANGE) returns the live per-entity
	// instance. A nil instance means the entity has no follow_range (not a mob) — skip the bonus.
	if m != nil {
		follow := m.GetInstance(FollowRange.Name())
		if follow != nil && !follow.HasModifier(randomSpawnBonusID) {
			// addPermanentModifier(new AttributeModifier(RANDOM_SPAWN_BONUS_ID,
			//   random.triangle(0.0, 0.11485000000000001), ADD_MULTIPLIED_BASE))
			//
			// triangle draws nextDouble() TWICE here, BEFORE the nextFloat() below — the order is
			// fixed by the bytecode.
			amount := triangle(r, 0.0, randomSpawnBonusSpread)
			follow.AddPermanentModifier(AttributeModifier{
				ID:        randomSpawnBonusID,
				Amount:    amount,
				Operation: AddMultipliedBase,
			})
		}
	}

	// setLeftHanded(random.nextFloat() < 0.05F): the ONE nextFloat() draw, AFTER the triangle draws.
	leftHanded = r.NextFloat() < leftHandedChance
	return leftHanded
}
