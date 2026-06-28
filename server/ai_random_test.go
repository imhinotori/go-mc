package server

// ai_random_test.go — Phase 24 Task 1: the per-entity seeded RandomSource determinism + the
// faithful goal-draw-ORDER regression. These pin that (a) a seed yields an identical stream, and
// (b) each ported goal draws from the per-entity source in the EXACT bytecode order (jar-verified
// this session: RandomStrollGoal.canUse = nextInt(interval) GATE then 3× nextInt offset;
// LookAtPlayerGoal = nextFloat roll then 40+nextInt(40); RandomLookAroundGoal = nextFloat roll then
// nextDouble heading then 20+nextInt(20)).

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

	// --- RandomStroll: canUse draws nextInt(interval) [gate] then getPosition's 3× nextInt. ---
	{
		loop := NewTickLoop(newFakeClock())
		e := NewEntity(1, entity.Pig, 100, 64, 200)
		m := newPigAI()
		m.rng.reseed(seed)
		e.ai = m
		stroll := findStroll(t, m)

		// Reference source replaying the SAME seed in the documented order.
		ref := newEntityRandom(seed)
		gate := ref.nextInt(stroll.interval) // DRAW 1 — the chance gate
		dx := ref.nextInt(2*strollHorizontalRadius + 1)
		dz := ref.nextInt(2*strollHorizontalRadius + 1)
		dy := ref.nextInt(2*strollVerticalRadius + 1)

		used := stroll.canUse(loop, e)
		// canUse returns true iff the gate landed on 0; assert our reference predicted the same.
		if used != (gate == 0) {
			t.Fatalf("stroll.canUse=%v but the reference gate draw was %d (expected canUse==%v)", used, gate, gate == 0)
		}
		if used {
			// The wanted target must equal e.pos + the reference offsets (proving getPosition drew
			// exactly DX,DZ,DY in that order, right after the gate).
			wantX := e.x + float64(dx-strollHorizontalRadius)
			wantZ := e.z + float64(dz-strollHorizontalRadius)
			wantY := e.y + float64(dy-strollVerticalRadius)
			if m.wantX != wantX || m.wantZ != wantZ || m.wantY != wantY {
				t.Fatalf("stroll getPosition draw order mismatch: got want=(%v,%v,%v) ref=(%v,%v,%v)",
					m.wantX, m.wantY, m.wantZ, wantX, wantY, wantZ)
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
