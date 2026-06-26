package server

import "math"

// food.go (Plan 17-19) is the 1:1 port of the vanilla hunger / food / saturation / exhaustion
// system from net.minecraft.world.food.FoodData, with the two off-FoodData call sites it depends
// on: ServerPlayer.checkMovementStatistics (movement exhaustion) and Player.causeFoodExhaustion
// (the attack/damage exhaustion entry point, in attack_dispatch.go). ALL constants and the EXACT
// branch order are VERIFIED via `javap -c -p` on temp/cache/26.2-inner.jar this session; each is
// cited at its use site. The single owner is the tick goroutine (tickFood runs inside
// tickEntities), so the food fields are -race clean by the same single-owner discipline as the
// rest of tickPlayer (TICK-05), exactly like breath.go.
//
// Cited bytecode (paths relative to temp/cache/26.2-inner.jar):
//
//	net.minecraft.world.food.FoodData fields:        foodLevel:int=20, saturationLevel:float=5.0,
//	                                                 exhaustionLevel:float=0.0, tickTimer:int=0
//	net.minecraft.world.food.FoodData.addExhaustion: exhaustionLevel = Math.min(exhaustionLevel+v, 40.0f)
//	net.minecraft.world.food.FoodData.add(int,float): foodLevel = Mth.clamp(food+foodLevel, 0, 20);
//	                                                  saturationLevel = Mth.clamp(sat+saturationLevel, 0.0f, (float)foodLevel)
//	net.minecraft.world.food.FoodData.eat(int,float): add(food, FoodConstants.saturationByModifier(food, mod))
//	net.minecraft.world.food.FoodConstants.saturationByModifier(int,float): nutrition * modifier * 2.0f
//	net.minecraft.world.food.FoodData.tick(ServerPlayer): the exhaustion-drain block, then the
//	    fast-regen / slow-regen / starvation / else if/else-if chain (see tickFood).
//	net.minecraft.world.entity.player.Player.isHurt():   getHealth() > 0.0F && getHealth() < getMaxHealth()
//	net.minecraft.world.entity.LivingEntity.heal(float): if (getHealth() > 0.0F) setHealth(getHealth()+amount)
//	net.minecraft.world.entity.LivingEntity.setHealth(float): Mth.clamp(value, 0.0F, getMaxHealth())
//	net.minecraft.server.level.ServerPlayer.checkMovementStatistics(dx,dy,dz): the movement
//	    exhaustion ladder (didNotMove early-return, swim / eye-in-water / in-water / climb / onGround).

// Food / exhaustion constants (VERIFIED via javap — see file header).
const (
	// exhaustionDrainThreshold is FoodData.tick's drain gate: `exhaustionLevel > 4.0f`
	// (`ldc 4.0f; fcmpl; ifle`). Above it, 4.0 is subtracted and converted to 1.0 saturation
	// (or 1 food) loss.
	exhaustionDrainThreshold float32 = 4.0

	// exhaustionDrainStep is the amount subtracted from exhaustionLevel per drain (`fsub 4.0f`).
	exhaustionDrainStep float32 = 4.0

	// maxExhaustion is the addExhaustion cap (`Math.min(exhaustionLevel+v, 40.0f)`).
	maxExhaustion float32 = 40.0

	// foodMax / foodMin are the Mth.clamp bounds in FoodData.add for foodLevel (`iconst_0; bipush 20`).
	// foodMax == maxFood (20) is reused from tick.go; foodMin is 0.
	foodMin int32 = 0

	// satRegenTickTimer is the FAST saturated-regen gate: tickTimer >= 10 (`bipush 10; if_icmplt`).
	satRegenTickTimer int32 = 10

	// foodRegenTickTimer is the SLOW regen / STARVATION gate: tickTimer >= 80 (`bipush 80; if_icmplt`).
	foodRegenTickTimer int32 = 80

	// satRegenFoodFull is the foodLevel threshold for FAST saturated regen: foodLevel >= 20
	// (`bipush 20; if_icmplt`).
	satRegenFoodFull int32 = 20

	// slowRegenFoodThreshold is the foodLevel threshold for SLOW regen: foodLevel >= 18
	// (`bipush 18; if_icmplt`).
	slowRegenFoodThreshold int32 = 18

	// satRegenHealCap is the per-fast-regen heal/exhaust cap: Math.min(saturationLevel, 6.0f)
	// (`ldc 6.0f; Math.min`). The heal is heal/6.0f, the exhaust is the full heal value.
	satRegenHealCap float32 = 6.0

	// slowRegenHeal / slowRegenExhaust are the SLOW regen heal (`fconst_1` -> 1.0) and exhaustion
	// (`ldc 6.0f` -> 6.0) per 80-tick fire.
	slowRegenHeal    float32 = 1.0
	slowRegenExhaust float32 = 6.0

	// starveDamage is the per-80-tick starvation damage (`fconst_1` -> hurtServer(starve(), 1.0)).
	starveDamage float32 = 1.0

	// starveHealthHardGate is the starvation health gate: getHealth() > 10.0f (`ldc 10.0f; fcmpl; ifgt`).
	starveHealthHardGate float32 = 10.0

	// starveHealthNormalGate is the NORMAL-difficulty starvation floor: getHealth() > 1.0f
	// (`fconst_1; fcmpl; ifle`).
	starveHealthNormalGate float32 = 1.0
)

// difficulty is the v1 stand-in for net.minecraft.world.Difficulty. Sulfur has no difficulty
// system yet, so FoodData.tick's diff reads resolve to a CITED constant equal to the vanilla
// default (NORMAL). The enum is modeled in full (PEACEFUL/EASY/NORMAL/HARD, javap-verified order
// on net.minecraft.world.Difficulty) so a real ServerLevel.getDifficulty() read slots in later
// with no branch change.
type difficulty int

const (
	difficultyPeaceful difficulty = iota // net.minecraft.world.Difficulty.PEACEFUL
	difficultyEasy                        // net.minecraft.world.Difficulty.EASY
	difficultyNormal                      // net.minecraft.world.Difficulty.NORMAL
	difficultyHard                        // net.minecraft.world.Difficulty.HARD
)

// serverDifficulty is the CITED stub for ServerLevel.getDifficulty(): Sulfur has no difficulty
// system, so it is fixed to the vanilla default NORMAL. Structured so a real read replaces this
// constant later. NORMAL means: starvation stops at 1.0 HP (the getHealth() > 1.0f && NORMAL gate),
// and the exhaustion-drain food-loss branch fires (PEACEFUL would skip it).
const serverDifficulty = difficultyNormal

// naturalHealthRegeneration is the CITED stub for
// GameRules.get(NATURAL_HEALTH_REGENERATION) in FoodData.tick: the gamerule's vanilla default is
// true. Sulfur has no gamerule system yet; this constant equals the default so the regen branches
// fire faithfully, and a real GameRules read slots in later with no branch change.
const naturalHealthRegeneration = true

// mthClampI mirrors net.minecraft.util.Mth.clamp(int value, int min, int max):
// `value < min ? min : (value > max ? max : value)`. Ported for FoodData.add's foodLevel clamp
// (combat.go already provides the float form mthClampF, reused for the saturation clamp).
func mthClampI(value, min, max int32) int32 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// addExhaustion is the port of net.minecraft.world.food.FoodData.addExhaustion(float):
// `exhaustionLevel = Math.min(exhaustionLevel + v, 40.0f)`. Adds action cost to the accumulator,
// capped at 40.0. It is the single mutator the attack/damage/movement exhaustion sites funnel
// through. Tick-owned (TICK-05): called only on the tick goroutine.
func (p *tickPlayer) addExhaustion(v float32) {
	p.exhaustion = float32(math.Min(float64(p.exhaustion+v), float64(maxExhaustion)))
}

// foodAdd is the port of net.minecraft.world.food.FoodData.add(int food, float sat):
//
//	foodLevel = Mth.clamp(food + foodLevel, 0, 20);
//	saturationLevel = Mth.clamp(sat + saturationLevel, 0.0f, (float)foodLevel);
//
// The saturation clamp's upper bound is the JUST-updated foodLevel (cast to float), exactly as
// vanilla — saturation can never exceed the current food. NOT wired to any consume handler in this
// plan (eating items is the next plan's concern); ported so foodEat exists for it. Tick-owned.
func (p *tickPlayer) foodAdd(food int32, sat float32) {
	p.food = mthClampI(food+p.food, foodMin, maxFood)
	p.saturation = mthClampF(sat+p.saturation, 0.0, float32(p.food))
}

// foodEat is the port of net.minecraft.world.food.FoodData.eat(int food, float satModifier):
// `add(food, FoodConstants.saturationByModifier(food, satModifier))`. saturationByModifier is
// `nutrition * modifier * 2.0f` (FoodConstants.saturationByModifier, javap-verified). Provided so
// the consume path the NEXT plan wires has its FoodData entry point ready; NO consume handler is
// wired in this plan. Tick-owned.
func (p *tickPlayer) foodEat(food int32, satModifier float32) {
	p.foodAdd(food, saturationByModifier(food, satModifier))
}

// saturationByModifier is the port of FoodConstants.saturationByModifier(int nutrition, float
// modifier): `nutrition * modifier * 2.0f` (`i2f; fmul; fconst_2; fmul`).
func saturationByModifier(nutrition int32, modifier float32) float32 {
	return float32(nutrition) * modifier * 2.0
}

// foodNeedsFood is the port of FoodData.needsFood(): `foodLevel < 20`. (Provided for the consume
// path; unused this plan.)
func (p *tickPlayer) foodNeedsFood() bool { return p.food < maxFood }

// heal is the port of net.minecraft.world.entity.LivingEntity.heal(float):
//
//	if (getHealth() > 0.0F) setHealth(getHealth() + amount);
//
// where setHealth clamps to [0.0F, getMaxHealth()] (LivingEntity.setHealth: Mth.clamp(value, 0,
// getMaxHealth())). A dead player (health <= 0) is NOT healed, exactly as vanilla. getMaxHealth()
// reads the MAX_HEALTH attribute whose base is 20.0 (== maxHealth); v1 has no max-health modifiers,
// so maxHealth is the faithful ceiling. Marks the player dirty so tickFood re-sends SetHealth when
// the heal changed health. Tick-owned (TICK-05).
func (t *TickLoop) heal(p *tickPlayer, amount float32) {
	if p.health <= 0.0 {
		return // LivingEntity.heal: the `getHealth() > 0.0F` guard — no heal while dead
	}
	// setHealth(getHealth() + amount), clamped to [0, getMaxHealth()].
	p.health = mthClampF(p.health+amount, 0.0, maxHealth)
}

// isHurt is the port of net.minecraft.world.entity.player.Player.isHurt():
// `getHealth() > 0.0F && getHealth() < getMaxHealth()`. FoodData.tick gates the regen branches on
// it (only a player who is alive AND below full health regenerates). Tick-owned read.
func (p *tickPlayer) isHurt() bool {
	return p.health > 0.0 && p.health < maxHealth
}

// tickFood is the per-tick hunger step: the 1:1 port of net.minecraft.world.food.FoodData.tick
// (run for every ServerPlayer each tick), preceded by the movement-exhaustion port
// (ServerPlayer.checkMovementStatistics), run inside tickEntities (the same fixed phase as
// tickBreath, no phase reorder — TestTickPhaseOrder stays green).
//
// Order within the tick (faithful to vanilla's per-tick player flow):
//  1. checkMovementStatistics over the per-tick position delta (movement exhaustion) — vanilla
//     runs this on the ServerPlayer tick BEFORE FoodData.tick; the delta is getX()-xo etc., which
//     Sulfur computes as (x - prevX, y - prevY, z - prevZ) since p.x/y/z are updated from the
//     client move packets between ticks (subtick.go), the client-authoritative position model.
//  2. FoodData.tick: the exhaustion-drain block, then the fast-regen / slow-regen / starvation /
//     else if/else-if chain.
//  3. prevX/prevY/prevZ = x/y/z (vanilla's xo=getX() at the tick tail) so next tick's delta is
//     correct.
//  4. dirty-send SetHealth when food/saturation/health changed (the hunger bar reads them off the
//     wire; the client does not locally simulate hunger).
func (t *TickLoop) tickFood() {
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}

		// (1) Movement exhaustion over this tick's position delta (ServerPlayer.checkMovementStatistics).
		t.checkMovementStatistics(p, p.x-p.prevX, p.y-p.prevY, p.z-p.prevZ)

		// (2) FoodData.tick: the core hunger simulation.
		t.foodDataTick(p)

		// (3) xo/yo/zo <- getX()/getY()/getZ() (vanilla's per-tick position-of-record update), so the
		// next tick's checkMovementStatistics delta is computed against THIS tick's end position.
		p.prevX, p.prevY, p.prevZ = p.x, p.y, p.z

		// (4) Push the authoritative SetHealth when food/saturation/health changed since the last send.
		t.syncFood(p)
	}
}

// foodDataTick is the body of net.minecraft.world.food.FoodData.tick(ServerPlayer). EXACT branch
// order (verified against the bytecode this session): the exhaustion-drain block FIRST, then the
// fast-regen / slow-regen / starvation / else if/else-if-else chain. The diff and the
// naturalRegeneration gamerule are CITED stubs (serverDifficulty == NORMAL, naturalHealthRegeneration
// == true — the vanilla defaults), structured to become real reads later.
func (t *TickLoop) foodDataTick(p *tickPlayer) {
	const diff = serverDifficulty // ServerLevel.getDifficulty(): CITED stub == NORMAL (vanilla default)

	// --- EXHAUSTION DRAIN (`if (exhaustionLevel > 4.0f)`) ---
	if p.exhaustion > exhaustionDrainThreshold {
		// exhaustionLevel -= 4.0f.
		p.exhaustion -= exhaustionDrainStep
		if p.saturation > 0.0 {
			// saturationLevel = Math.max(saturationLevel - 1.0f, 0.0f).
			p.saturation = float32(math.Max(float64(p.saturation-1.0), 0.0))
		} else if diff != difficultyPeaceful {
			// foodLevel = Math.max(foodLevel - 1, 0). (PEACEFUL skips the food drain.)
			p.food = maxI32(p.food-1, 0)
		}
	}

	// --- REGEN / STARVATION (gated by the naturalRegeneration gamerule) ---
	// naturalRegen = level.getGameRules().get(NATURAL_HEALTH_REGENERATION): CITED stub == true.
	const naturalRegen = naturalHealthRegeneration

	if naturalRegen && p.saturation > 0.0 && p.isHurt() && p.food >= satRegenFoodFull {
		// FAST saturated regen: every 10 ticks heal saturation/6 (capped at 6), exhaust by that heal.
		p.foodTickTimer++
		if p.foodTickTimer >= satRegenTickTimer {
			heal := float32(math.Min(float64(p.saturation), float64(satRegenHealCap)))
			t.heal(p, heal/6.0)
			p.addExhaustion(heal)
			p.foodTickTimer = 0
		}
	} else if naturalRegen && p.food >= slowRegenFoodThreshold && p.isHurt() {
		// SLOW regen: every 80 ticks heal 1.0, exhaust 6.0.
		p.foodTickTimer++
		if p.foodTickTimer >= foodRegenTickTimer {
			t.heal(p, slowRegenHeal)
			p.addExhaustion(slowRegenExhaust)
			p.foodTickTimer = 0
		}
	} else if p.food <= 0 {
		// STARVATION: every 80 ticks, 1.0 starve damage gated by health + difficulty.
		p.foodTickTimer++
		if p.foodTickTimer >= foodRegenTickTimer {
			// Apply damage if getHealth() > 10.0f OR diff == HARD OR (getHealth() > 1.0f && diff ==
			// NORMAL); otherwise the timer resets with no damage (PEACEFUL/low-health skip).
			if p.health > starveHealthHardGate ||
				diff == difficultyHard ||
				(p.health > starveHealthNormalGate && diff == difficultyNormal) {
				// hurtServer(damageSources().starve(), 1.0f): routed through applyDamage (Sulfur's
				// hurtServer port) so the starve hit obeys i-frames exactly as vanilla.
				t.applyDamage(p, starveDamage)
			}
			p.foodTickTimer = 0
		}
	} else {
		// No regen/starve condition holds: reset the shared timer (vanilla's final `else`).
		p.foodTickTimer = 0
	}
}

// checkMovementStatistics is the v1 port of
// net.minecraft.server.level.ServerPlayer.checkMovementStatistics(double dx, double dy, double dz):
// the movement-exhaustion ladder. The stats awards (awardStat) are visual/scoreboard bookkeeping
// Sulfur does not model; only the causeFoodExhaustion sites are ported (the gameplay-observable
// part). EXACT branch order + constants verified against the bytecode this session:
//
//	if (didNotMove(dx,dy,dz)) return;                        // dx==0 && dy==0 && dz==0 (exact)
//	if (isSwimming())      { cm = round(sqrt(dx²+dy²+dz²)*100); if (cm>0) exhaust(0.01f*cm*0.01f); }
//	else if (isEyeInFluid(WATER)) { cm = round(sqrt(dx²+dy²+dz²)*100); if (cm>0) exhaust(0.01f*cm*0.01f); }
//	else if (isInWater())  { cm = round(sqrt(dx²+dz²)*100);    if (cm>0) exhaust(0.01f*cm*0.01f); }  // x,z only
//	else if (onClimbable()) { /* climb stat only, NO exhaustion */ }
//	else if (onGround())   { cm = round(sqrt(dx²+dz²)*100);    if (cm>0) {                          // x,z only
//	                            if (isSprinting())  exhaust(0.1f*cm*0.01f);
//	                            else if (isCrouching()) exhaust(0.0f*cm*0.01f);                      // zero
//	                            else                exhaust(0.0f*cm*0.01f); } }                      // walk: zero
//	// (isFallFlying / vehicle branches: elytra + vehicles are out of v1 scope — omitted with this note.)
//
// NET RESULT: only SPRINTING on the ground and ANY in-water movement cost food; plain walking and
// crouching cost 0.0 (the `fconst_0` multiplier — verified). The `isPassenger()` guard is folded
// into didNotMove for v1 (no vehicles), so it is omitted.
//
// CITED STUBS for flags Sulfur does not track yet — each equals the vanilla default that makes the
// branch faithful, structured so a real read slots in later:
//   - isSwimming():   false (no swim-pose decode). cited stub.
//   - isCrouching():  false (no sneak-pose decode). cited stub.
//   - isSprinting():  p.sprinting (combat.go's existing field — false in v1, the faithful default).
//   - isInWater():    t.playerInWater(p) (fluid_physics.go AABB water check — REAL read).
//   - isEyeInFluid(WATER): t.eyeInWater(p) (breath.go eye-block water sample — REAL read).
//   - onClimbable():  false (no ladder/vine detection). cited stub.
//   - onGround():     p.onGround (REAL read — the physics/movement onGround flag).
func (t *TickLoop) checkMovementStatistics(p *tickPlayer, dx, dy, dz float64) {
	// didNotMove(dx,dy,dz): dx==0.0 && dy==0.0 && dz==0.0 (exact-zero compare, javap-verified).
	if dx == 0.0 && dy == 0.0 && dz == 0.0 {
		return
	}

	// CITED stubs for the un-decoded pose flags (see method doc).
	const isSwimming = false  // ServerPlayer.isSwimming(): no swim pose in v1
	const isCrouching = false // ServerPlayer.isCrouching(): no sneak pose in v1
	const onClimbable = false // ServerPlayer.onClimbable(): no ladder/vine detection in v1

	switch {
	case isSwimming:
		// cm = round(sqrt(dx²+dy²+dz²)*100); if (cm>0) causeFoodExhaustion(0.01f*cm*0.01f).
		if cm := round3D(dx, dy, dz); cm > 0 {
			t.causeFoodExhaustion(p, 0.01*float32(cm)*0.01)
		}
	case t.eyeInWater(p): // isEyeInFluid(FluidTags.WATER) — REAL read (breath.go)
		// cm = round(sqrt(dx²+dy²+dz²)*100); if (cm>0) causeFoodExhaustion(0.01f*cm*0.01f).
		if cm := round3D(dx, dy, dz); cm > 0 {
			t.causeFoodExhaustion(p, 0.01*float32(cm)*0.01)
		}
	case t.playerInWater(p): // isInWater() — REAL read (fluid_physics.go); HORIZONTAL (x,z) only
		// cm = round(sqrt(dx²+dz²)*100); if (cm>0) causeFoodExhaustion(0.01f*cm*0.01f).
		if cm := round2D(dx, dz); cm > 0 {
			t.causeFoodExhaustion(p, 0.01*float32(cm)*0.01)
		}
	case onClimbable:
		// Climb stat only — NO exhaustion (vanilla's onClimbable() branch awards CLIMB_ONE_CM and
		// does not call causeFoodExhaustion). Faithful no-op in v1.
	case p.onGround: // onGround() — REAL read; HORIZONTAL (x,z) only
		// cm = round(sqrt(dx²+dz²)*100).
		if cm := round2D(dx, dz); cm > 0 {
			switch {
			case p.sprinting: // isSprinting() — p.sprinting (combat.go field; false default in v1)
				t.causeFoodExhaustion(p, 0.1*float32(cm)*0.01)
			case isCrouching:
				// isCrouching(): multiplier is fconst_0 (0.0f) — costs nothing.
				t.causeFoodExhaustion(p, 0.0*float32(cm)*0.01)
			default:
				// walk: multiplier is fconst_0 (0.0f) — costs nothing.
				t.causeFoodExhaustion(p, 0.0*float32(cm)*0.01)
			}
		}
	}
	// (isFallFlying / vehicle branches omitted — elytra and vehicles are out of v1 scope.)
}

// round3D is `Math.round((float)(Math.sqrt(dx*dx+dy*dy+dz*dz)) * 100.0f)` — the 3D centimetre count
// for the swim / eye-in-water branches. The sqrt is computed in double, narrowed to float (d2f),
// scaled by 100.0f, then Math.round(float) (round-half-up to the nearest int) — exactly the
// bytecode op sequence (dsqrt; d2f; fmul 100.0f; Math.round).
func round3D(dx, dy, dz float64) int32 {
	return mathRoundF(float32(math.Sqrt(dx*dx+dy*dy+dz*dz)) * 100.0)
}

// round2D is `Math.round((float)(Math.sqrt(dx*dx+dz*dz)) * 100.0f)` — the HORIZONTAL (x,z only)
// centimetre count for the in-water / onGround branches.
func round2D(dx, dz float64) int32 {
	return mathRoundF(float32(math.Sqrt(dx*dx+dz*dz)) * 100.0)
}

// mathRoundF mirrors java.lang.Math.round(float): `(long) Math.floor(value + 0.5f)` — round half
// up. The bytecode narrows to int via the int overload (Math.round(float) returns int), so the
// result is an int32. Negative deltas cannot occur here (the argument is a non-negative sqrt*100),
// so the floor(x+0.5) form is exact for the values reached.
func mathRoundF(value float32) int32 {
	return int32(math.Floor(float64(value) + 0.5))
}

// maxI32 mirrors java.lang.Math.max(int, int) for the exhaustion-drain food-loss step
// (`Math.max(foodLevel - 1, 0)`).
func maxI32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// syncFood pushes the authoritative ClientboundSetHealth to the player's own client when food,
// saturation, OR health changed since the last send (the dirty-send tracker, mirroring breath.go's
// syncAirSupply for the bubble bar). The hunger bar reads food/saturation off the SetHealth packet
// and the client does NOT locally simulate hunger in multiplayer — so tickFood MUST push the value
// when it changes or the bar never moves (the Plan 17-19 gap this closes). A no-change tick sends
// nothing. Health is folded into the dirty check because heal()/starve both change health WITHOUT
// going through applyDamage's own SetHealth in every case (heal never does), so the food packet is
// the carrier that keeps the HUD's hearts in sync with regen too. Tick-owned (TICK-05): food /
// saturation / health / lastFoodSent / lastSaturationSent are all single-owner tick state.
func (t *TickLoop) syncFood(p *tickPlayer) {
	if p.food == p.lastFoodSent && p.saturation == p.lastSaturationSent && p.health == p.lastHealthSent {
		return // not dirty: nothing changed (vanilla re-sends only on a real change)
	}
	p.lastFoodSent = p.food
	p.lastSaturationSent = p.saturation
	p.lastHealthSent = p.health
	if p.client != nil {
		p.client.Send(setHealth(p.health, p.food, p.saturation))
	}
}
