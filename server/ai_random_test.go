package server

// ai_random_test.go — Phase 24 Task 1 (+ Phase 30.1 stroll rewrite): the per-entity seeded
// RandomSource determinism + the faithful goal-draw-ORDER regression. These pin that (a) a seed yields
// an identical stream, and (b) each ported goal draws from the per-entity source in the EXACT bytecode
// order. Jar-verified this session: WaterAvoidingRandomStrollGoal.canUse = nextInt(reducedTickDelay(120)
// == 60) GATE, THEN getPosition's probability nextFloat(), THEN RandomPos.generateRandomPos's
// UNCONDITIONAL 10× generateRandomDirection (each = nextInt(2h+1)-h, nextInt(2v+1)-v, nextInt(2h+1)-h
// in x/y/z order) == 31 draws; LookAtPlayerGoal = nextFloat roll then 40+nextInt(40); RandomLookAroundGoal
// = nextFloat roll then nextDouble heading then 20+nextInt(20).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// TestSeededAIRandom: two sources with the SAME seed produce IDENTICAL nextInt/nextFloat/nextDouble
// streams, and DIFFERENT seeds diverge. This is the determinism contract the AI relies on (the
// TestTickAIDrivesMobs flake fix).
func TestSeededAIRandom(t *testing.T) {
	a := newEntityRandom(12345)
	b := newEntityRandom(12345)
	for i := 0; i < 32; i++ {
		if got, want := a.nextInt(100), b.nextInt(100); got != want {
			t.Fatalf("nextInt diverged at draw %d for equal seeds: %d != %d", i, got, want)
		}
	}
	a = newEntityRandom(12345)
	b = newEntityRandom(12345)
	for i := 0; i < 32; i++ {
		if got, want := a.nextFloat(), b.nextFloat(); got != want {
			t.Fatalf("nextFloat diverged at draw %d for equal seeds: %v != %v", i, got, want)
		}
	}
	a = newEntityRandom(12345)
	b = newEntityRandom(12345)
	for i := 0; i < 32; i++ {
		if got, want := a.nextDouble(), b.nextDouble(); got != want {
			t.Fatalf("nextDouble diverged at draw %d for equal seeds: %v != %v", i, got, want)
		}
	}

	// Distinct seeds must NOT produce the same first draw (a sanity check the seed actually feeds
	// the stream — not a strong statistical claim, just that the seeds are wired through).
	if newEntityRandom(1).nextInt(1<<30) == newEntityRandom(2).nextInt(1<<30) {
		t.Fatal("distinct seeds produced the same first nextInt — seed not wired into the stream")
	}

	// reseed resets the stream: a reseeded source equals a freshly-seeded one.
	r := newEntityRandom(999)
	r.nextInt(50) // advance it
	r.reseed(12345)
	fresh := newEntityRandom(12345)
	for i := 0; i < 8; i++ {
		if got, want := r.nextInt(1000), fresh.nextInt(1000); got != want {
			t.Fatalf("reseeded stream != freshly-seeded stream at draw %d: %d != %d", i, got, want)
		}
	}
}

// TestEntityRandFaithfulDrawOrder: the three goals draw from the mob's per-entity source in the
// bytecode order. We prove the ORDER by giving the mob a known-seeded source and replaying the same
// seed through a reference source in the documented order, asserting the goal consumed exactly those
// draws (so any reorder/extra/missing draw — the Pitfall-1 permutation bug — fails the test).
func TestEntityRandFaithfulDrawOrder(t *testing.T) {
	const seed uint64 = 0xABCDEF

	// --- WaterAvoidingRandomStrollGoal: canUse draws the reducedTickDelay(120)==60 GATE, THEN
	// getPosition's probability nextFloat(), THEN RandomPos.generateRandomPos's UNCONDITIONAL
	// 10× generateRandomDirection (x/y/z order) == 31 draws. This is the lockstep contract this
	// phase changed; the test FAILS if the gate radix, the nextFloat draw, the candidate count, or
	// the x/y/z order is ever reverted. ---
	{
		// strollGateZeroSeed makes the first nextInt(reducedTickDelay(120)) land on 0 so the gate
		// passes and the FULL draw stream (probability + 30 offsets) is actually exercised — the old
		// test used a seed whose gate was non-zero, so canUse returned false and the draw-order body
		// never ran (vacuous). Verified: newEntityRandom(73).nextInt(60) == 0.
		const strollGateZeroSeed uint64 = 73

		loop := NewTickLoop(newFakeClock())
		e := NewEntity(1, entity.Pig, 100, 64, 200)
		m := newPigAI()
		m.rng.reseed(strollGateZeroSeed)
		e.ai = m
		stroll := findStroll(t, m)

		// Reference source replaying the SAME seed in the EXACT documented order. ANY reorder, drop,
		// or extra draw on the real path shifts the stream and makes the candidate compare below fail.
		ref := newEntityRandom(strollGateZeroSeed)
		gate := ref.nextInt(reducedTickDelay(stroll.interval)) // DRAW 1 — the reducedTickDelay gate (nextInt(60))
		if gate != 0 {
			t.Fatalf("test setup invariant broken: seed %d gate nextInt(%d) = %d, want 0 (pick a zero-gate seed)",
				strollGateZeroSeed, reducedTickDelay(stroll.interval), gate)
		}
		_ = ref.nextFloat() // DRAW 2 — the probability nextFloat() (WaterAvoidingRandomStrollGoal.getPosition)
		// DRAWS 3..32 — RandomPos.generateRandomPos's 10 unconditional generateRandomDirection draws,
		// each in x, y, z order (xt=nextInt(2h+1)-h, yt=nextInt(2v+1)-v, zt=nextInt(2h+1)-h).
		var wantCands [10][3]float64
		for i := 0; i < 10; i++ {
			xt := ref.nextInt(2*strollHorizontalRadius+1) - strollHorizontalRadius // x — order 1 of 3
			yt := ref.nextInt(2*strollVerticalRadius+1) - strollVerticalRadius     // y — order 2 of 3 (y BEFORE z)
			zt := ref.nextInt(2*strollHorizontalRadius+1) - strollHorizontalRadius // z — order 3 of 3
			wantCands[i] = [3]float64{e.x + float64(xt), e.y + float64(yt), e.z + float64(zt)}
		}

		used := stroll.canUse(loop, e)
		if !used {
			t.Fatalf("stroll.canUse=false with a zero-gate seed — the gate draw or its radix regressed")
		}
		// The goal stashes the 10 RAW candidates (getPosition's generateRandomPos supplier results,
		// pre-snap) on g.wantCandidates. They must equal the reference set EXACTLY — proving the gate
		// radix, the probability nextFloat, the 10-candidate count, and the x/y/z draw order all match.
		if len(stroll.wantCandidates) != 10 {
			t.Fatalf("stroll emitted %d candidates, want 10 (RandomPos.generateRandomPos for-i<10)", len(stroll.wantCandidates))
		}
		for i := 0; i < 10; i++ {
			got := stroll.wantCandidates[i]
			if got != wantCands[i] {
				t.Fatalf("stroll candidate %d draw-order/count mismatch: got %v ref %v\n"+
					"(a desync here means the gate radix, the probability nextFloat, or the x/y/z order regressed)",
					i, got, wantCands[i])
			}
		}
	}

	// --- LookAtPlayer: start draws 40 + nextInt(40). ---
	{
		e := NewEntity(2, entity.Pig, 0, 64, 0)
		m := newPigAI()
		m.rng.reseed(seed)
		e.ai = m
		look := findLookAtPlayer(t, m)

		ref := newEntityRandom(seed)
		wantLookTime := 40 + ref.nextInt(40)

		look.start(nil, e)
		if look.lookTime != wantLookTime {
			t.Fatalf("lookAtPlayer.start lookTime draw mismatch: got %d want %d", look.lookTime, wantLookTime)
		}
	}

	// --- RandomLookAround: start draws nextDouble (heading) THEN nextInt(20) (time), in order. ---
	{
		e := NewEntity(3, entity.Pig, 0, 64, 0)
		m := newPigAI()
		m.rng.reseed(seed)
		e.ai = m
		var around *randomLookAroundGoal
		for _, wg := range m.goals.goals {
			if g, ok := wg.g.(*randomLookAroundGoal); ok {
				around = g
			}
		}
		if around == nil {
			t.Fatal("no randomLookAroundGoal registered")
		}

		ref := newEntityRandom(seed)
		_ = ref.nextDouble()          // heading draw must come FIRST
		wantLookTime := 20 + ref.nextInt(20)

		around.start(nil, e)
		if around.lookTime != wantLookTime {
			t.Fatalf("randomLookAround.start draw order mismatch (heading before time?): lookTime got %d want %d",
				around.lookTime, wantLookTime)
		}
	}
}
