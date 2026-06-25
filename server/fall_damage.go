package server

import "math"

// fall_damage.go — GAMEPLAY-04 (the environmental half). OVERWRITES the 17-01 stub. The call
// site (tick_phases.go tickEntities -> t.tickFallDamage()) and the tickPlayer fall-damage
// fields (fallDistance/wasOnGround/lastY, declared in tick.go) are owned by 17-01 and NOT
// touched here — this plan edits ONLY this file. Damage routes through the existing, tested
// applyDamage->die flow (combat.go).
//
// PORTED FROM THE JAR (javap -c -p from temp/cache/26.2-inner.jar this session — idiomatic Go,
// no GPL paste, structure cited):
//
//	net.minecraft.world.entity.Entity.checkFallDamage(double deltaY, boolean onGround, …):
//	    if (!isInWater() && deltaY < 0) fallDistance -= (float) deltaY;   // accumulate descent
//	    if (onGround) {                                                   // landing edge
//	        if (fallDistance > 0) { block.fallOn(…) -> causeFallDamage(fallDistance, …); }
//	        resetFallDistance();                                          // fallDistance = 0
//	    }
//	net.minecraft.world.entity.LivingEntity.calculateFallDamage(double d, float mul):
//	    return Mth.floor(calculateFallPower(d) * mul * FALL_DAMAGE_MULTIPLIER);
//	net.minecraft.world.entity.LivingEntity.calculateFallPower(double d):
//	    return (d + 1.0E-6) - SAFE_FALL_DISTANCE;     // SAFE_FALL_DISTANCE attribute base = 3.0
//
// With the default attributes (damageMul = 1.0, FALL_DAMAGE_MULTIPLIER = 1.0) the landing
// damage is floor(fallDistance - 3.0) HP. The 1.0E-6 epsilon is the jar's float-equality guard;
// it never changes the floored integer for whole-block falls, so it is omitted here.
//
// safeFallDistance is the SAFE_FALL_DISTANCE attribute base (3.0 blocks): a fall of 3 blocks or
// less deals no damage. fallDamageEpsilon mirrors the jar's calculateFallPower 1.0E-6 guard.
const (
	safeFallDistance = 3.0
	fallDamageEpsilon = 1.0e-6
)

// tickFallDamage is the GAMEPLAY-04 environmental-damage pass, called from tickEntities each
// tick (the 17-01-wired call site; this file overwrites the 17-01 no-op stub — the SIGNATURE is
// unchanged so the call site compiles untouched). For every connected player it:
//
//  1. accumulates this tick's airborne descent into fallDistance (Entity.checkFallDamage: while
//     not onGround, add the positive drop lastY-y);
//  2. on the onGround false->true LANDING edge, applies floor(fallDistance - safeFallDistance)
//     damage (when positive) through applyDamage — the same server-authoritative path attacks
//     use — then resets fallDistance (resetFallDistance);
//  3. records this tick's onGround/y into wasOnGround/lastY so the next tick's descent delta and
//     landing edge are computed correctly.
//
// Runs on the tick goroutine over tick-owned state (TICK-05) — no locking. A dead player is
// skipped (applyDamage already no-ops a corpse, but skipping avoids spurious SetHealth churn);
// a nil player is skipped defensively. NOTE: water/lava cushioning, slow-falling, and the
// per-block fallOn multiplier (hay bales, etc.) are deferred — fall damage is the minimum
// environmental damage for Phase 17 (A2); other environmental sources (fire, drowning, lava,
// void) are explicitly out of scope for this plan.
func (t *TickLoop) tickFallDamage() {
	for _, p := range t.players {
		if p == nil || p.dead {
			// A nil fixture or a corpse: still keep the bookkeeping current so a respawned
			// player does not inherit a stale landing edge.
			if p != nil {
				p.wasOnGround = p.onGround
				p.lastY = p.y
			}
			continue
		}

		// (1) Accumulate airborne descent. lastY is the previous tick's y; a positive
		// (lastY - y) is a drop. Only descent counts (an ascent does not reduce fallDistance —
		// the jar guards deltaY < 0). math.Max(0, …) clamps an upward step to zero.
		if !p.onGround {
			p.fallDistance += math.Max(0, p.lastY-p.y)
		}

		// (2) Landing edge: onGround transitioned false->true this tick. Apply the floored
		// damage past the safe distance, then reset the accumulator (resetFallDistance).
		if !p.wasOnGround && p.onGround {
			dmg := math.Floor(p.fallDistance + fallDamageEpsilon - safeFallDistance)
			if dmg > 0 {
				t.applyDamage(p, float32(dmg))
			}
			p.fallDistance = 0
		}

		// (3) End-of-tick bookkeeping for the next tick's delta + edge detection.
		p.wasOnGround = p.onGround
		p.lastY = p.y
	}
}
