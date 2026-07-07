package server

// ai_goals_target_test.go — MOB-SUB-10 (Phase 35-01): the focused RNG/cooldown tests for the shared
// targetSelector + melee goals (NearestAttackableTargetGoal, HurtByTargetGoal, MeleeAttackGoal). These
// are the LOCKSTEP gates the plan pins:
//
//   - TestNearestAttackableTargetGateUsesTen — the canUse RNG gate draws EXACTLY one nextInt with bound
//     reducedTickDelay(10) == 5 (the jar ctor's halved DEFAULT_RANDOM_INTERVAL; the selector is now
//     decimated, C-1), and the gate outcome matches nextInt(5) == 0. This is the LOCKSTEP-CRITICAL bound.
//   - TestNearestAttackableTargetAcquiresPlayer — a player in FOLLOW_RANGE is acquired as the target
//     (attackTargetID set); start() commits it to mobAI.getTarget().
//   - TestHurtByTargetNoRNG — hurtByTargetGoal.canUse draws ZERO RNG and gates on the
//     lastHurtByMob/timestamp bookkeeping (false when unset / same timestamp; true on a fresh attacker).
//   - TestLastHurtByMob — applyDamageEntity records lastHurtByMob = src.attacker + the timestamp.
//   - TestMeleeAttackCooldown — meleeAttackGoal.canUse honors the 20-tick lastCanUseCheck gate and
//     draws ZERO RNG; false when getTarget() == 0.
//
// The per-entity rng is seeded deterministically (reseedMobAI), so a parallel reference entityRandom
// with the SAME seed reproduces the goal's draws — that is how a "draw count + bound" assertion is made
// without a draw counter (the goal's rng and the reference advance in lockstep iff the goal drew the
// asserted call).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

// targetTestMob builds a live mob with a goal-bearing AI + a deterministic per-entity rng (reseeded by
// id, exactly as the spawn path does), so a reference entityRandom seeded identically reproduces its draws.
func targetTestMob(id int32, x, y, z float64) *Entity {
	e := NewEntity(id, entity.Zombie, x, y, z)
	e.ai = &mobAI{}
	e.ai.rng = newEntityRandom(defaultEntityRandomSeed)
	reseedMobAI(e.ai, e.id)
	e.health = 20.0
	return e
}

// referenceRng builds an entityRandom with the SAME seed the mob's reseedMobAI derives for `id`, so it
// reproduces the mob's draw stream in lockstep (the draw-count/bound assertion vehicle).
func referenceRng(id int32) *entityRandom {
	seed := uint64(uint32(id)) ^ defaultEntityRandomSeed
	return newEntityRandom(seed)
}

// addTestPlayer registers a player at (x,y,z) with the given entity id on the loop (the t.players seam
// the target scans read).
func addTestPlayer(loop *TickLoop, entityID int32, x, y, z float64) *tickPlayer {
	p := &tickPlayer{x: x, y: y, z: z, entityID: entityID}
	loop.players = append(loop.players, p)
	return p
}

// --- TestNearestAttackableTargetGateUsesTen ----------------------------------------------------

// TestNearestAttackableTargetGateUsesTen pins the LOCKSTEP-CRITICAL bound: NearestAttackableTargetGoal
// .canUse draws EXACTLY ONE nextInt(reducedTickDelay(10)) == nextInt(5) — the jar ctor's halved
// DEFAULT_RANDOM_INTERVAL (the selector is now decimated, C-1) — and the gate outcome equals
// nextInt(5) == 0. Proven by lockstep with a reference rng: the reference draws ONE nextInt(5) (whose
// value decides the gate); after canUse, the mob's rng and the reference must agree on the NEXT draw iff
// the goal consumed exactly one nextInt(5). A wrong bound (the un-halved 10) would consume a DIFFERENT
// amount of the stream, desynchronizing the follow-up draw.
func TestNearestAttackableTargetGateUsesTen(t *testing.T) {
	bound := nearestTargetRandomInterval
	// The jar ctor stores randomInterval = reducedTickDelay(DEFAULT_RANDOM_INTERVAL=10) == 5; the
	// decimated selector (C-1) makes the halved value 1:1, so assert against reducedTickDelay(10).
	if bound != reducedTickDelay(10) {
		t.Fatalf("nearestTargetRandomInterval = %d, want %d (reducedTickDelay(10); the jar ctor's halved DEFAULT_RANDOM_INTERVAL, NOT the raw 10)", bound, reducedTickDelay(10))
	}

	clock := newFakeClock()
	loop := NewTickLoop(clock)

	// Place a player far outside FOLLOW_RANGE so findTarget cannot acquire — this isolates the GATE
	// (when the gate passes, findTarget runs and returns no target -> canUse false; when the gate fails,
	// findTarget never runs). Either way the gate draws exactly one nextInt(reducedTickDelay(10))=nextInt(5).
	addTestPlayer(loop, 9000, 100000, 64, 100000)

	e := targetTestMob(7001, 8.5, 64, 8.5)
	ref := referenceRng(e.id)

	// The reference draws the SAME first nextInt(bound=reducedTickDelay(10)=5) the gate draws (the gate value).
	gateRoll := ref.nextInt(bound)
	wantGatePassed := gateRoll == 0 // gate "passes" (proceeds to findTarget) iff nextInt(5) == 0

	g := newNearestAttackableTargetGoal()
	got := g.canUse(loop, e)

	// With the player out of range, a passed gate -> findTarget finds nothing -> canUse false. A failed
	// gate -> canUse false on the gate. So canUse is false either way here; the LOCKSTEP proof is the
	// follow-up draw alignment below, plus the gate-decision cross-check.
	if got {
		t.Fatalf("canUse should be false (no in-range player), got true")
	}

	// LOCKSTEP: after canUse consumed exactly one nextInt(5), the mob's rng and the reference must
	// produce the IDENTICAL next draw. If canUse had drawn nextInt(10) (the un-halved bound) OR drawn twice,
	// this would diverge.
	if a, b := e.ai.rng.nextInt(1_000_000), ref.nextInt(1_000_000); a != b {
		t.Fatalf("draw desync after canUse: mob rng next=%d, reference next=%d — canUse did NOT draw exactly one nextInt(5) "+
			"(the un-halved nextInt(10) bound or a double draw would desync this)", a, b)
	}

	// Cross-check the gate decision matched nextInt(10)==0: re-run from a fresh identical mob, this time
	// forcing the player IN range so a passed gate yields a target. The gate decision must equal wantGatePassed.
	loop2 := NewTickLoop(newFakeClock())
	addTestPlayer(loop2, 9001, 8.5, 64, 9.5) // ~1 block away, well within FOLLOW_RANGE
	e2 := targetTestMob(7001, 8.5, 64, 8.5)  // SAME id -> SAME seed -> SAME first roll
	g2 := newNearestAttackableTargetGoal()
	if got2 := g2.canUse(loop2, e2); got2 != wantGatePassed {
		t.Fatalf("gate outcome = %v, want %v (canUse must proceed to findTarget iff nextInt(5) == 0; gateRoll was %d)", got2, wantGatePassed, gateRoll)
	}
}

// --- TestNearestAttackableTargetAcquiresPlayer -------------------------------------------------

// TestNearestAttackableTargetAcquiresPlayer: with the RNG gate forced (forceTrigger), a player within
// FOLLOW_RANGE is acquired (canUse true, target set), and start() commits it to mobAI.getTarget(). A
// player OUTSIDE follow range is not acquired.
func TestNearestAttackableTargetAcquiresPlayer(t *testing.T) {
	// A world is required now that findTarget gates the acquired player on line of sight (C-4): the
	// physics loop wires a ChunkManager, and with no blocks placed the eye-to-eye ray is clear air
	// (unloaded/empty cells contribute no collider), so an in-range player is visible and acquired.
	loop, _ := newPhysicsLoop()
	e := targetTestMob(7010, 8.5, 64, 8.5)
	follow := e.getAttributeValue(attribute.FollowRange)
	if follow <= 0 {
		t.Fatalf("zombie FOLLOW_RANGE = %v, want > 0 (35.0)", follow)
	}

	// A player just inside follow range.
	p := addTestPlayer(loop, 9100, 8.5, 64, 8.5+follow-1)

	g := newNearestAttackableTargetGoal()
	g.forceTrigger = true // bypass the RNG gate so the scan runs deterministically
	if !g.canUse(loop, e) {
		t.Fatal("canUse must acquire an in-range player (forceTrigger bypasses the gate)")
	}
	if g.target != p.entityID {
		t.Fatalf("acquired target = %d, want player id %d", g.target, p.entityID)
	}
	g.start(loop, e)
	if e.ai.getTarget() != p.entityID {
		t.Fatalf("start() must commit the target to mobAI.getTarget(): got %d, want %d", e.ai.getTarget(), p.entityID)
	}

	// A player OUTSIDE follow range is not acquired (the range gate rejects before LoS is consulted).
	loop2, _ := newPhysicsLoop()
	addTestPlayer(loop2, 9101, 8.5, 64, 8.5+follow+5)
	e2 := targetTestMob(7011, 8.5, 64, 8.5)
	g2 := newNearestAttackableTargetGoal()
	g2.forceTrigger = true
	if g2.canUse(loop2, e2) {
		t.Fatal("canUse must NOT acquire a player beyond FOLLOW_RANGE")
	}
}

// --- TestHurtByTargetNoRNG ---------------------------------------------------------------------

// TestHurtByTargetNoRNG: hurtByTargetGoal.canUse draws ZERO RNG and gates purely on the
// lastHurtByMob/lastHurtByMobTimestamp bookkeeping — false when no attacker is recorded, false when the
// timestamp matches the goal's own (already retaliated), true on a fresh attacker. The zero-RNG proof:
// the mob's rng is byte-identical before and after canUse (a reference seeded the same way is unmoved).
func TestHurtByTargetNoRNG(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := targetTestMob(7020, 8.5, 64, 8.5)
	ref := referenceRng(e.id)

	g := newHurtByTargetGoal()

	// (1) No attacker recorded -> false, NO draw.
	if g.canUse(loop, e) {
		t.Fatal("canUse must be false when lastHurtByMob == 0 (no attacker)")
	}

	// Record a fresh attacker (a present player) + a timestamp.
	attacker := addTestPlayer(loop, 9200, 8.5, 64, 9.5)
	e.lastHurtByMob = attacker.entityID
	e.lastHurtByMobTimestamp = 100

	// (2) A fresh attacker -> true (the player resolves on the loop).
	if !g.canUse(loop, e) {
		t.Fatal("canUse must be true for a fresh, present attacker (lastHurtByMob set, timestamp != own)")
	}

	// start() stamps the goal's own timestamp + commits the target.
	g.start(loop, e)
	if e.ai.getTarget() != attacker.entityID {
		t.Fatalf("start() must setTarget(getLastHurtByMob()): got %d, want %d", e.ai.getTarget(), attacker.entityID)
	}

	// (3) Same timestamp as the goal's own -> false (already retaliated; no re-fire on the same hit).
	if g.canUse(loop, e) {
		t.Fatal("canUse must be false when timestamp == the goal's own (already retaliated this hit)")
	}

	// ZERO RNG across all three canUse calls + start: the reference rng (never drawn) and the mob's rng
	// must still produce the IDENTICAL next draw.
	if a, b := e.ai.rng.nextInt(1_000_000), ref.nextInt(1_000_000); a != b {
		t.Fatalf("hurtByTargetGoal drew RNG: mob rng next=%d, reference next=%d — canUse/start must draw ZERO randoms", a, b)
	}
}

// --- TestLastHurtByMob -------------------------------------------------------------------------

// TestLastHurtByMob: applyDamageEntity records lastHurtByMob = src.attacker + lastHurtByMobTimestamp =
// gametime at the flag2 store-point (the HurtByTargetGoal bookkeeping). A pure field write, NO RNG.
func TestLastHurtByMob(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	loop.gametime = 4242
	e := targetTestMob(7030, 8.5, 64, 8.5)
	loop.only().entities.add(e)

	const attackerID = 555
	loop.applyDamageEntity(e, damageSourcePlayerAttack(attackerID), 2.0)

	if e.lastHurtByMob != attackerID {
		t.Fatalf("lastHurtByMob = %d, want %d (src.attacker)", e.lastHurtByMob, attackerID)
	}
	if e.lastHurtByMobTimestamp != 4242 {
		t.Fatalf("lastHurtByMobTimestamp = %d, want 4242 (int32(t.gametime))", e.lastHurtByMobTimestamp)
	}
}

// --- TestMeleeAttackCooldown -------------------------------------------------------------------

// TestMeleeAttackCooldown: meleeAttackGoal.canUse honors the 20-tick lastCanUseCheck gate (false within
// 20 gameTime ticks of the last check, true after) and draws ZERO RNG; false when getTarget() == 0.
func TestMeleeAttackCooldown(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := targetTestMob(7040, 8.5, 64, 8.5)
	ref := referenceRng(e.id)

	g := newMeleeAttackGoal(1.0)

	// (1) No target -> false (getTarget() == 0). lastCanUseCheck advances (the gate passed the 20-tick
	// check on the first call at gametime 0; lastCanUseCheck initializes to 0 so time-0 < 20 is false on
	// the FIRST call... so the very first call at gametime 0 is gated). Run at a gametime past the gate.
	loop.gametime = 100
	if g.canUse(loop, e) {
		t.Fatal("canUse must be false when getTarget() == 0 (no acquired target)")
	}
	// lastCanUseCheck is now 100 (the gate passed and recorded the time before the no-target return).
	if g.lastCanUseCheck != 100 {
		t.Fatalf("lastCanUseCheck = %d, want 100 (recorded after passing the 20-tick gate)", g.lastCanUseCheck)
	}

	// Give the mob a present target.
	target := addTestPlayer(loop, 9300, 8.5, 64, 9.0)
	e.ai.setTarget(target.entityID)

	// (2) Within 20 ticks of the last check (gametime 100 -> 119) -> the gate rejects (false), regardless
	// of the target.
	loop.gametime = 119
	if g.canUse(loop, e) {
		t.Fatal("canUse must be false within 20 gameTime ticks of the last check (the lastCanUseCheck gate)")
	}

	// (3) At exactly +20 ticks (gametime 120) -> the gate passes; with a present target -> true.
	loop.gametime = 120
	if !g.canUse(loop, e) {
		t.Fatal("canUse must be true at +20 gameTime ticks with a present target (the gate elapsed)")
	}
	if g.lastCanUseCheck != 120 {
		t.Fatalf("lastCanUseCheck = %d, want 120 (recorded on the passing check)", g.lastCanUseCheck)
	}

	// ZERO RNG across every canUse: the reference rng (never drawn) and the mob's rng agree on the next draw.
	if a, b := e.ai.rng.nextInt(1_000_000), ref.nextInt(1_000_000); a != b {
		t.Fatalf("meleeAttackGoal.canUse drew RNG: mob rng next=%d, reference next=%d — the cooldown gate must be RNG-FREE", a, b)
	}
}
