package server

// ai_random.go — AI-24 (Phase 24, Task 1): the per-entity seeded RandomSource (the
// net.minecraft.util.RandomSource / Mob.getRandom() analogue) that makes the ported mob AI
// 1:1-faithful AND deterministic.
//
// THE 1:1 ANCHOR (jar-verified this session via javap -c -p over temp/cache/26.2-inner.jar):
// vanilla mobs draw from a PER-ENTITY RandomSource (Mob.getRandom()), and the goal logic depends
// on the EXACT draw ORDER, confirmed from bytecode:
//
//   - net.minecraft.world.entity.ai.goal.RandomStrollGoal.canUse:
//       getRandom().nextInt(reducedTickDelay(interval))   // DRAW 1 (the gate)
//       then getPosition() -> DefaultRandomPos.getPos(mob, 10, 7) which draws again.
//   - net.minecraft.world.entity.ai.goal.LookAtPlayerGoal.canUse: getRandom().nextFloat() < probability;
//       LookAtPlayerGoal.start: lookTime = 40 + getRandom().nextInt(40).
//   - net.minecraft.world.entity.ai.goal.RandomLookAroundGoal.canUse: getRandom().nextFloat() < 0.02f;
//       RandomLookAroundGoal.start: d = (2π) * getRandom().nextDouble(); relX=cos(d); relZ=sin(d);
//       lookTime = 20 + getRandom().nextInt(20).
//
// WHY A SEEDED stdlib SOURCE (not a bit-exact LegacyRandomSource port): per 24-RESEARCH Open-Q §4
// (A4: no current test asserts bit-exact vanilla sequences), a per-entity seeded math/rand/v2.Rand
// reproduces the faithful draw ORDER and is fully deterministic for a fixed seed — which is all the
// 1:1 mandate requires here (the draw order is the observable behavior; the exact bit-stream is not
// asserted). It is far simpler than a LegacyRandomSource java.util.Random port and carries zero new
// deps. If a future phase asserts bit-exact vanilla sequences, swap the backing Source here without
// touching any caller (every goal draws through these three methods only).
//
// SINGLE-OWNER (TICK-05): an entityRandom is created at AI-build time (newPigAI / buildAIFromDecl)
// and drawn ONLY inside a running goal's canUse/start/tick on the tick goroutine. It REPLACES the
// shared package math/rand/v2 the AI goals used before — removing both the determinism debt
// (STATE.md: TestTickAIDrivesMobs flaky, "needs a seeded source per the 1:1 mandate") AND the
// shared-global-rand data-race source. No goroutine, no lock — plain tick-owned state.

import (
	"math/rand/v2"
)

// entityRandom is the per-mob seeded random source — the Mob.getRandom() analogue. It wraps a
// seeded *rand.Rand so two sources built with the same seed produce IDENTICAL draw streams.
type entityRandom struct {
	r *rand.Rand
}

// defaultEntityRandomSeed is the deterministic seed newEntityRandom uses when no per-entity seed is
// derived. A fixed seed makes the AI reproducible out of the box (the determinism fix for
// TestTickAIDrivesMobs); spawn sites may reseed per entity id via reseed for per-mob variety while
// staying deterministic for that id.
const defaultEntityRandomSeed uint64 = 0x9E3779B97F4A7C15 // a fixed nothing-up-my-sleeve constant

// newEntityRandom builds a per-entity seeded source. Same seed -> identical nextInt/nextFloat/
// nextDouble streams (the determinism contract the goal-draw-order test pins). Backed by PCG (the
// math/rand/v2 default generator), seeded from the single seed split into the two PCG words so a
// seed of 0 is still a valid, non-degenerate stream.
func newEntityRandom(seed uint64) *entityRandom {
	// Split the one seed into PCG's two 64-bit words deterministically (a SplitMix64-style mix on
	// the second word) so distinct seeds give well-separated streams and seed 0 is non-degenerate.
	src := rand.NewPCG(seed, seed^defaultEntityRandomSeed)
	return &entityRandom{r: rand.New(src)}
}

// reseed re-initializes the source from a new seed in place (used to derive a per-entity seed from
// the entity id at spawn so each mob has its own deterministic stream). Tick-owned; never called
// off the tick goroutine.
func (er *entityRandom) reseed(seed uint64) {
	er.r = rand.New(rand.NewPCG(seed, seed^defaultEntityRandomSeed))
}

// nextInt returns a pseudo-random int in [0, n) — the RandomSource.nextInt(int) analogue. n must be
// > 0 (vanilla's contract); a non-positive n returns 0 (a defensive clamp — vanilla would throw, but
// the ported goals never pass n<=0: the stroll interval is 120 and the radii are positive).
func (er *entityRandom) nextInt(n int) int {
	if n <= 0 {
		return 0
	}
	return er.r.IntN(n)
}

// nextFloat returns a pseudo-random float32 in [0, 1) — the RandomSource.nextFloat() analogue (the
// probability roll LookAtPlayerGoal/RandomLookAroundGoal use).
func (er *entityRandom) nextFloat() float32 {
	return er.r.Float32()
}

// nextDouble returns a pseudo-random float64 in [0, 1) — the RandomSource.nextDouble() analogue (the
// heading draw RandomLookAroundGoal.start uses: d = 2π * nextDouble()).
func (er *entityRandom) nextDouble() float64 {
	return er.r.Float64()
}

// nextBoolean returns a pseudo-random bool — the RandomSource.nextBoolean() analogue. The breed
// path's Pig.getBreedOffspring draws it ONCE to pick which parent's PigVariant the baby inherits
// (`nextBoolean() ? this.getVariant() : partner.getVariant()`). Drawn ONLY mid-breeding (inside
// breedGoal's breed()), so it is dormant on the un-fed oracle pig and never perturbs the pinned
// oracle stream. Like the other draws it is draw-ORDER-faithful (not bit-exact vanilla), backed by
// math/rand/v2's IntN(2)==0; the draw being CONSUMED (and its order vs the XP nextInt) is the
// observable contract the lockstep rule pins, not the bit value.
//
//	[VERIFIED javap Pig.getBreedOffspring: getRandom().nextBoolean() ? getVariant() : partner.getVariant().]
func (er *entityRandom) nextBoolean() bool {
	return er.r.IntN(2) == 0
}

// nextLong returns a pseudo-random int64 across the full 64-bit range — the RandomSource.nextLong()
// analogue. Raid.playSound draws it ONCE (`this.random.nextLong()`) to seed the RAID_HORN's client
// pitch-variation playback; the draw is on the raid's own stream (Raid.random == r.rng here) and MUST
// be consumed in order so the raid stream stays draw-order-faithful. Like the other draws it is
// draw-ORDER-faithful (not bit-exact vanilla), backed by math/rand/v2's Uint64. The seed only affects
// CLIENT-side sound-variant selection, never gameplay.
//
//	[VERIFIED javap Raid.playSound: `aload_0; getfield random; RandomSource.nextLong()` -> the
//	 ClientboundSoundPacket seed arg.]
func (er *entityRandom) nextLong() int64 {
	return int64(er.r.Uint64())
}

// nextGaussian returns a normally-distributed float64 (mean 0, stddev 1) — the
// RandomSource.nextGaussian() analogue. Animal.aiStep's in-love heart branch draws it three times
// (xd/yd/zd = nextGaussian() * 0.02) as the per-heart particle velocity. Like the other draws this is
// draw-ORDER-faithful (not bit-exact vanilla), backed by math/rand/v2's NormFloat64 — sufficient for
// the 1:1 mandate (the draw order is the observable behavior; on a dedicated server the heart's
// addParticle is a no-op so the velocity value never reaches a client, but the THREE draws must still
// be consumed from the mob stream in lockstep so a future bit-exact swap and the breed/in-love
// scenario RNG stay aligned). Drawn ONLY when a pig is in love (inLove>0 && inLove%10==0) — dormant on
// the un-fed oracle pig (inLove 0), so it never perturbs the pinned oracle stream.
//
//	[VERIFIED javap Animal.aiStep: 3× getRandom().nextGaussian() each * 0.02d -> xd/yd/zd, fed to
//	 Level.addParticle(HEART, getRandomX(1), getRandomY()+0.5, getRandomZ(1), xd, yd, zd).]
func (er *entityRandom) nextGaussian() float64 {
	return er.r.NormFloat64()
}
