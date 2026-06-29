package server

// jump.go — MOB-SUB-04 (Phase 30-02): the LivingEntity.aiStep JUMP branch + the two jump impulses
// (jumpInLiquid / jumpFromGround). Consumed by the serverAiStep JUMP slot (ai_mob.go) AFTER
// jumpControl.tick has pushed the goal-armed flag into e.jumping. The impulse mutates e.vy and lands
// in tickAI, BEFORE tickPhysics integrates gravity (tick_phases.go: tickAI → tickPhysics), so the
// jump is not cancelled the same tick.
//
// PORTED 1:1 from the unobfuscated 26.2 jar (javap -c -p over temp/cache/26.2-inner.jar, this
// session). Every constant carries its jar citation. RNG-FREE: the whole branch is pure
// integer/float math + the fluid predicate reads — it draws no random, so it never perturbs the pig
// oracle's per-mob RNG stream (the only new RNG in this subsystem is FloatGoal.tick in Plan 03).

import "math"

const (
	// fluidJumpImpulse is the EXACT double LivingEntity.jumpInLiquid adds to delta-movement Y. The
	// jar constant is 0.03999999910593033 (NOT a clean 0.04) — the ldc2_w in the bytecode — so the
	// port matches the vanilla numeric op bit-for-bit (the 1:1 mandate: no paraphrasing the literal).
	//	[VERIFIED javap LivingEntity.jumpInLiquid: getDeltaMovement().add(0.0, ldc2_w 0.03999999910593033, 0.0)
	//	 then setDeltaMovement(...). The literal is double 0.03999999910593033d.]
	fluidJumpImpulse = 0.03999999910593033

	// baseJumpPower is LivingEntity.getJumpPower() for a default living entity: (float)
	// getAttributeValue(JUMP_STRENGTH) * 1.0f * getBlockJumpFactor() + getJumpBoostPower(). With the
	// JUMP_STRENGTH registration default 0.41999998688697815 (RangedAttribute "jump_strength"), a
	// blockJumpFactor of 1.0 (no honey/slime/soul-soil under the mob) and jumpBoost 0 (no potion), the
	// folded float value is 0.42f. This is a CITED CONSTANT equal to the vanilla default (CLAUDE.md):
	// there is no per-type JUMP_STRENGTH read yet (no JumpStrength attribute var), so it is structured
	// to become `(float)e.getAttributeValue(attribute.JumpStrength) * blockJumpFactor + jumpBoost` once
	// that attribute + the block-factor read land — never baked away.
	//	[VERIFIED javap Attributes.<clinit>: JUMP_STRENGTH = RangedAttribute("jump_strength",
	//	 0.41999998688697815, 0.0, 32.0); (float)0.41999998688697815 == 0.42f. And javap
	//	 LivingEntity.getJumpPower(float): (float)getAttributeValue(JUMP_STRENGTH) * f * getBlockJumpFactor()
	//	 + getJumpBoostPower().]
	baseJumpPower = 0.42

	// jumpPowerEpsilon is the LivingEntity.jumpFromGround early-out gate: `if (f <= 1.0E-5f) return;`.
	//	[VERIFIED javap LivingEntity.jumpFromGround: f = getJumpPower(); ldc_w 1.0E-5f; fcmpg; ifgt …; return.]
	jumpPowerEpsilon = 1.0e-5

	// landJumpDelay is the noJumpDelay value the aiStep jump branch sets after a successful land jump
	// (jumpFromGround) — the per-mob land-jump rate-limiter (~once per 10 ticks).
	//	[VERIFIED javap LivingEntity.aiStep: after jumpFromGround(), bipush 10; putfield noJumpDelay.]
	landJumpDelay = 10
)

// entityJumpStep is the LivingEntity.aiStep JUMP branch — the `if (jumping && isAffectedByFluids())`
// block that turns a one-tick e.jumping into the real vy impulse. Called from the serverAiStep JUMP
// slot AFTER jumpControl.tick (ai_mob.go). PURE (no RNG). The impulse precedes gravity integration.
//
//	[VERIFIED javap LivingEntity.aiStep (the jump branch, bytecode 301-476):
//	   if (jumping) {                                                                 // getfield jumping; ifeq 474
//	     if (isAffectedByFluids()) {                                                  // invokevirtual; ifeq 474
//	       double fluidHeight = isInLava() ? getFluidHeight(LAVA) : getFluidHeight(WATER);
//	       boolean inWaterAndHasFluidHeight = isInWater() && fluidHeight > 0.0;       // var 11
//	       double threshold = getFluidJumpThreshold();                               // var 12
//	       if (inWaterAndHasFluidHeight && (!onGround() || fluidHeight > threshold))  // 370-387
//	         jumpInLiquid(WATER);
//	       else if (isInLava() && (!onGround() || !isInShallowFluid(LAVA)))           // 400-421
//	         jumpInLiquid(LAVA);
//	       else if ((onGround() || (inWaterAndHasFluidHeight && fluidHeight <= threshold))
//	                && noJumpDelay == 0) {                                            // 434-458
//	         jumpFromGround();
//	         noJumpDelay = 10;
//	       }
//	     } else noJumpDelay = 0;                                                       // 474-476
//	   } else noJumpDelay = 0;                                                         // 474-476
//	 (Both the `!jumping` and the `!isAffectedByFluids` paths fall through to noJumpDelay = 0.)]
func (t *TickLoop) entityJumpStep(e *Entity) {
	if e == nil {
		return
	}
	// `if (jumping && isAffectedByFluids())` — both falsey paths set noJumpDelay = 0.
	if !e.jumping || !e.isAffectedByFluids() {
		if e.ai != nil {
			e.ai.noJumpDelay = 0
		}
		return
	}

	inLava := t.mobInLava(e)

	// fluidHeight = isInLava() ? getFluidHeight(LAVA) : getFluidHeight(WATER).
	var fluidHeight float64
	if inLava {
		fluidHeight = t.mobFluidHeight(e, fluidLava)
	} else {
		fluidHeight = t.mobFluidHeight(e, fluidWater)
	}

	// inWaterAndHasFluidHeight = isInWater() && fluidHeight > 0.0.
	inWaterAndHasFluidHeight := t.mobInWater(e) && fluidHeight > 0.0

	threshold := t.getFluidJumpThreshold(e)

	switch {
	case inWaterAndHasFluidHeight && (!e.onGround || fluidHeight > threshold):
		// Water swim impulse.
		jumpInLiquid(e) // setDeltaMovement Y += 0.03999999910593033 (LAVA uses the same impulse below)
	case inLava && (!e.onGround || !t.isInShallowFluid(e, fluidLava)):
		// Lava swim impulse — the SAME setDeltaMovement(y + 0.04) primitive (jumpInLiquid is tag-agnostic
		// in its math: it only reads getDeltaMovement and adds the constant; the TagKey arg gates the
		// CALLER's branch selection, not the impulse magnitude).
		jumpInLiquid(e)
	case (e.onGround || (inWaterAndHasFluidHeight && fluidHeight <= threshold)) && (e.ai == nil || e.ai.noJumpDelay == 0):
		// Land jump — gated by the per-mob delay.
		jumpFromGround(e)
		if e.ai != nil {
			e.ai.noJumpDelay = landJumpDelay
		}
	}
}

// isInShallowFluid is LivingEntity.isInShallowFluid(TagKey): true when the mob's fluid height for the
// tag is at or below the jump threshold (i.e. the fluid is too shallow to swim-jump in). Used by the
// lava branch of the jump dispatch (`!isInShallowFluid(LAVA)`). PURE read.
//
//	[VERIFIED javap LivingEntity.isInShallowFluid: return getFluidHeight(tag) <= getFluidJumpThreshold();
//	 (dcmpg; ifgt → 0; else 1).]
func (t *TickLoop) isInShallowFluid(e *Entity, kind fluidKind) bool {
	return t.mobFluidHeight(e, kind) <= t.getFluidJumpThreshold(e)
}

// jumpInLiquid is LivingEntity.jumpInLiquid(TagKey): add the fluid jump impulse to the mob's vertical
// delta-movement. The TagKey argument selects nothing inside the method — the impulse is a fixed
// constant added to getDeltaMovement().y — so the Go port takes no tag (the caller's branch already
// chose water vs lava). vx/vz are untouched (the Vec3.add is (0, impulse, 0)).
//
//	[VERIFIED javap LivingEntity.jumpInLiquid: setDeltaMovement(getDeltaMovement().add(0.0,
//	 0.03999999910593033, 0.0)) — only the Y component is incremented.]
func jumpInLiquid(e *Entity) {
	e.vy += fluidJumpImpulse
}

// jumpFromGround is LivingEntity.jumpFromGround: the land jump. f = getJumpPower() (= 0.42); if it is
// at/below the epsilon do nothing; else set vy = max(f, vy) (vanilla uses Math.max(f, dm.y), NOT a
// plain assignment — a mob already rising faster than the jump keeps its speed); then, if sprinting,
// nudge horizontal velocity by the yaw-aligned 0.2 vector; finally mark needsSync.
//
// SPRINT BRANCH: a v1 mob never sprints (there is no per-mob sprinting flag yet — isSprinting()
// would be false for a Pig). The branch is ported behind a cited const-false (isSprinting → false)
// per CONTEXT Discretion ("port the minimum the jump branch reads, cite the rest"), structured to
// become `if e.sprinting { … }` once a mob sprint flag lands. The needsSync marker (a delta-codec
// re-send hint) has no Sulfur field yet either — cited as a deferred wire detail (the tracker already
// re-sends on a velocity change, so omitting the explicit marker does not drop the jump on the wire).
//
//	[VERIFIED javap LivingEntity.jumpFromGround:
//	   float f = getJumpPower(); if (f <= 1.0E-5f) return;
//	   Vec3 dm = getDeltaMovement(); setDeltaMovement(dm.x, Math.max((double)f, dm.y), dm.z);
//	   if (isSprinting()) { float yr = getYRot() * 0.017453292f;
//	                        addDeltaMovement(new Vec3(-sin(yr)*0.2, 0.0, cos(yr)*0.2)); }
//	   this.needsSync = true;.]
func jumpFromGround(e *Entity) {
	const f = baseJumpPower // getJumpPower() == 0.42f for a default living entity (cited above)
	if f <= jumpPowerEpsilon {
		return
	}
	// setDeltaMovement(x, max(f, y), z) — vanilla preserves a faster-than-jump upward velocity.
	e.vy = math.Max(f, e.vy)

	// isSprinting() — const false for a v1 mob (no sprint flag). The yaw-aligned 0.2 horizontal nudge
	// is the vanilla sprint-jump bonus; it is a no-op until a mob sprint flag exists. Structured to
	// become `if e.sprinting { … }`:
	const isSprinting = false
	if isSprinting {
		yr := float64(e.yaw) * 0.017453292 // degrees -> radians (the jar's 0.017453292f literal)
		e.vx += -math.Sin(yr) * 0.2
		e.vz += math.Cos(yr) * 0.2
	}
	// needsSync = true — a deferred wire detail (no Sulfur field; the tracker re-sends on the velocity
	// change). Cited, not baked.
}
