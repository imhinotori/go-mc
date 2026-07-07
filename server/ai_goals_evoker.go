package server

// ai_goals_evoker.go — RAIDER (Task): the Evoker's spell-casting goals, PORTED 1:1 from the unobfuscated
// 26.2 jar (net.minecraft.world.entity.monster.illager.Evoker + SpellcasterIllager, CFR/javap this
// session). The Evoker registers FOUR spell goals:
//
//   @1 EvokerCastingSpellGoal (SpellcasterCastingSpellGoal): the mid-cast lock. flags {MOVE, LOOK}.
//       canUse: getSpellCastingTime() > 0. start: navigation.stop(). stop: setIsCastingSpell(NONE).
//       tick: look at target/wololoTarget.
//   @4 EvokerSummonSpellGoal (SUMMON_VEX): castingTime 100, interval 340, warmup 20. canUse: super.canUse()
//       && nextInt(8)+1 > nearbyVexCount. performSpellCasting: 3x { offset(-2+nextInt(5), +1, -2+nextInt(5));
//       spawn Vex; setLimitedLife(20*(30+nextInt(90))) }.
//   @5 EvokerAttackSpellGoal (FANGS): castingTime 40, interval 100, warmup 20. NO RNG. performSpellCasting:
//       distSqr<9 -> 5-fang arc (r1.5, step PI*0.4) + 8-fang arc (r2.5, step 2PI/8, offset 2PI/5, warmup 3);
//       else 16-fang line (dist 1.25*(i+1), warmup i). Each spawns an EvokerFangs at a sturdy floor.
//   @6 EvokerWololoSpellGoal (WOLOLO): castingTime 60, interval 140, warmup 40. canUse: no target &&
//       !casting && tickCount>=nextAttackTickCount && mobGriefing && a BLUE Sheep in range -> pick nextInt(
//       list.size()); performSpellCasting: setColor(RED).
//
// Base SpellcasterUseSpellGoal.canUse: target alive && !isCastingSpell() && tickCount >= nextAttackTickCount.
// start: attackWarmupDelay = adjustedTickDelay(warmup); spellCastingTickCount = castingTime; nextAttackTick
// Count = tickCount + interval; setIsCastingSpell(spell). tick: --attackWarmupDelay; at 0 -> performSpell
// Casting(). canContinueToUse: target alive && attackWarmupDelay > 0.
//
// v1 DEFERRALS (cited, NEVER silently dropped):
//   - VEX entity is unbuilt: EvokerSummonSpellGoal spawns NO vex, but CONSUMES the exact per-iteration RNG
//     draws (nextInt(5) X, nextInt(5) Z, nextInt(90) life) x3 so the evoker's stream stays in lockstep.
//     nearbyVexCount is always 0 (no vex exists), so nextInt(8)+1 > 0 is always true -> the summon fires
//     its RNG faithfully. The vex spawn + finalizeSpawn-internal draws land when the Vex entity is built.
//   - EVOKER FANGS entity is unbuilt: EvokerAttackSpellGoal computes the EXACT fang geometry (arc/line) but
//     spawns NO fangs. NO RandomSource draws in FANGS, so deferring the spawn does not desync the stream.
//   - WOLOLO: now LIVE. wololoCanUse scans BLUE sheep in getBoundingBox().inflate(16,4,16), and on a
//     non-empty list picks one via random.nextInt(list.size()) (the evoker mob stream) as the wololoTarget;
//     performSpellCasting (evokerWololoRecolor) does wololoTarget.setColor(RED). RNG-faithful (the ONE
//     nextInt(size) pick fires only on a non-empty BLUE-sheep list). Cite Evoker$EvokerWololoSpellGoal.
//   - getCurrentDifficultyAt / the tickCount gate use the entity's tickCount proxy (gametime-based).

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// IllagerSpell ids (VERIFIED CFR SpellcasterIllager.IllagerSpell).
const (
	illagerSpellNone    = 0
	illagerSpellSummon  = 1  // SUMMON_VEX
	illagerSpellFangs   = 2  // FANGS
	illagerSpellWololo  = 3  // WOLOLO
	spellCastWarmupBase = 20 // SpellcasterUseSpellGoal.getCastWarmupTime() base (wololo overrides to 40)
)

// evokerVexSummonCount is EvokerSummonSpellGoal.performSpellCasting's loop count (3 vexes).
const evokerVexSummonCount = 3

// spellcasterCastingSpellGoal ports SpellcasterIllager$SpellcasterCastingSpellGoal (the prio-1 mid-cast
// lock, subclassed as EvokerCastingSpellGoal). flags {MOVE, LOOK}. It runs while a spell is being cast
// (spellCastingTickCount > 0), parking the nav and facing the target.
type spellcasterCastingSpellGoal struct {
	baseGoal
}

func newEvokerCastingSpellGoal() *spellcasterCastingSpellGoal {
	return &spellcasterCastingSpellGoal{baseGoal: newBaseGoal(flagMove | flagLook)}
}

// canUse: getSpellCastingTime() > 0 (spellCastingTickCount).
func (g *spellcasterCastingSpellGoal) canUse(t *TickLoop, e *Entity) bool {
	return e.spellCastingTickCount > 0
}

func (g *spellcasterCastingSpellGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	return g.canUse(t, e)
}

// start: navigation.stop().
func (g *spellcasterCastingSpellGoal) start(t *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
}

// stop: setIsCastingSpell(NONE).
func (g *spellcasterCastingSpellGoal) stop(t *TickLoop, e *Entity) {
	e.currentSpell = illagerSpellNone
}

// tick: look at the combat target (or the wololo target) at max head yaw. Head-only turn.
func (g *spellcasterCastingSpellGoal) tick(t *TickLoop, e *Entity) {
	var tx, ty, tz float64
	var ok bool
	if id := mobTarget(e); id != 0 {
		if p := t.playerByEntityID(id); p != nil {
			tx, ty, tz, ok = p.x, p.y, p.z, true
		}
	}
	if !ok && e.evokerWololoTarget != 0 {
		if s, found := t.cur().entities.get(e.evokerWololoTarget); found {
			tx, ty, tz, ok = s.x, s.y, s.z, true
		}
	}
	if ok {
		yRotD := yawTowardDeg(tx-e.x, tz-e.z)
		e.headYaw = rotlerpDeg(e.headYaw, yRotD, evokerMaxHeadYaw)
		_ = ty
	}
}

// evokerMaxHeadYaw is Mob.getMaxHeadYRot() (the head-turn clamp the casting goal uses; default 75, but the
// v1 look uses the shared 30-step lerp cap for a smooth turn — a cited reduction, LOOK visual only).
const evokerMaxHeadYaw = 30.0

// spellKind identifies which concrete spell a useSpellGoal drives (routes the per-spell timings + effect).
type spellKind int

const (
	spellKindSummon spellKind = iota
	spellKindFangs
	spellKindWololo
)

// evokerSpellTiming holds a concrete spell goal's castWarmupTime/castingTime/castingInterval (VERIFIED CFR).
type evokerSpellTiming struct {
	warmup   int
	casting  int
	interval int
	spellID  int32
}

func evokerTimingFor(k spellKind) evokerSpellTiming {
	switch k {
	case spellKindSummon:
		return evokerSpellTiming{warmup: 20, casting: 100, interval: 340, spellID: illagerSpellSummon}
	case spellKindFangs:
		return evokerSpellTiming{warmup: 20, casting: 40, interval: 100, spellID: illagerSpellFangs}
	default: // wololo
		return evokerSpellTiming{warmup: 40, casting: 60, interval: 140, spellID: illagerSpellWololo}
	}
}

// evokerUseSpellGoal ports SpellcasterIllager$SpellcasterUseSpellGoal (the abstract base of the 3 concrete
// spell goals). NO flags. attackWarmupDelay counts the cast warmup; nextAttackTickCount is the absolute
// gametime the next cast becomes eligible (the tickCount gate proxy).
type evokerUseSpellGoal struct {
	baseGoal
	kind                spellKind
	timing              evokerSpellTiming
	attackWarmupDelay   int
	nextAttackTickCount int64
}

func newEvokerUseSpellGoal(k spellKind) *evokerUseSpellGoal {
	return &evokerUseSpellGoal{baseGoal: newBaseGoal(0), kind: k, timing: evokerTimingFor(k)}
}

// hasCombatTarget reports whether the evoker has a live player target (getTarget() != null && alive).
func (g *evokerUseSpellGoal) hasCombatTarget(t *TickLoop, e *Entity) bool {
	id := mobTarget(e)
	if id == 0 {
		return false
	}
	p := t.playerByEntityID(id)
	return p != nil && !p.dead
}

// canUse ports SpellcasterUseSpellGoal.canUse: target alive && !isCastingSpell() && tickCount >=
// nextAttackTickCount. The concrete summon/wololo goals override this to add their extra gate.
func (g *evokerUseSpellGoal) canUse(t *TickLoop, e *Entity) bool {
	if g.kind == spellKindWololo {
		return g.wololoCanUse(t, e)
	}
	if !g.baseCanUse(t, e) {
		return false
	}
	if g.kind == spellKindSummon {
		// EvokerSummonSpellGoal.canUse: super.canUse() && nextInt(8)+1 > vexCount. vexCount is always 0 in
		// v1 (no Vex entity), so nextInt(8)+1 > 0 is always true. DRAW: nextInt(8) (faithful, order-exact).
		_ = mobRandom(e).nextInt(8)
		return true
	}
	return true
}

// baseCanUse is the shared SpellcasterUseSpellGoal.canUse gate (target alive, not casting, cooldown ready).
func (g *evokerUseSpellGoal) baseCanUse(t *TickLoop, e *Entity) bool {
	return g.hasCombatTarget(t, e) && e.currentSpell == illagerSpellNone && t.gametime >= g.nextAttackTickCount
}

// canContinueToUse: target alive && attackWarmupDelay > 0.
func (g *evokerUseSpellGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	if g.kind == spellKindWololo {
		return e.evokerWololoTarget != 0 && g.attackWarmupDelay > 0
	}
	return g.hasCombatTarget(t, e) && g.attackWarmupDelay > 0
}

// start ports SpellcasterUseSpellGoal.start: seed the warmup, the spellCastingTickCount, the next-cast
// gametime, and set the casting spell id.
func (g *evokerUseSpellGoal) start(t *TickLoop, e *Entity) {
	g.attackWarmupDelay = adjustedTickDelay(g.timing.warmup)
	e.spellCastingTickCount = int32(g.timing.casting)
	g.nextAttackTickCount = t.gametime + int64(g.timing.interval)
	e.currentSpell = g.timing.spellID // setIsCastingSpell(getSpell())
}

// tick ports SpellcasterUseSpellGoal.tick: --attackWarmupDelay; at 0 -> performSpellCasting().
func (g *evokerUseSpellGoal) tick(t *TickLoop, e *Entity) {
	g.attackWarmupDelay--
	if g.attackWarmupDelay == 0 {
		g.performSpellCasting(t, e)
	}
}

// stop clears the wololo target (WololoSpellGoal.stop -> setWololoTarget(null)); the others no-op.
func (g *evokerUseSpellGoal) stop(t *TickLoop, e *Entity) {
	if g.kind == spellKindWololo {
		e.evokerWololoTarget = 0
	}
}

// performSpellCasting dispatches to the concrete spell effect (SpellcasterUseSpellGoal.performSpellCasting,
// overridden per spell). The getCastSound after is a cite-deferred client sound.
func (g *evokerUseSpellGoal) performSpellCasting(t *TickLoop, e *Entity) {
	switch g.kind {
	case spellKindSummon:
		t.evokerSummonVexes(e)
	case spellKindFangs:
		t.evokerAttackFangs(e)
	case spellKindWololo:
		t.evokerWololoRecolor(e)
	}
}

// evokerSummonVexes ports EvokerSummonSpellGoal.performSpellCasting: loop 3 times, each drawing the offset
// (nextInt(5) X, nextInt(5) Z), SPAWNING a real Vex at that offset, and setting its limited life
// (nextInt(90)). The three EVOKER-stream draws (nextInt(5), nextInt(5), nextInt(90)) fire IN ORDER so the
// evoker's stream stays in lockstep with the jar; the Vex's OWN finalizeSpawn-internal draws are on the
// VEX's per-entity stream (independent), so the evoker never sees them.
//
//	[VERIFIED CFR EvokerSummonSpellGoal.performSpellCasting: for i<3 { offset = blockPosition().offset(
//	 -2 + nextInt(5), 1, -2 + nextInt(5)); vex = VEX.create(...); vex.snapTo(offset,0,0); vex.finalizeSpawn(...);
//	 vex.setOwner(this); vex.setBoundOrigin(offset); vex.setLimitedLife(20*(30 + nextInt(90))); addFreshEntity }.]
func (t *TickLoop) evokerSummonVexes(e *Entity) {
	r := mobRandom(e)
	// blockPosition() == floor of the evoker's position (the origin the +/-2..+2 offsets are relative to).
	baseX := int(math.Floor(e.x))
	baseY := int(math.Floor(e.y))
	baseZ := int(math.Floor(e.z))
	for i := 0; i < evokerVexSummonCount; i++ {
		// offset = blockPosition().offset(-2 + nextInt(5), 1, -2 + nextInt(5)). The X draw precedes the Z draw
		// (the offset(...) argument evaluation order), and both are on the EVOKER's stream.
		offX := baseX + (-2 + int(r.nextInt(5))) // DRAW: nextInt(5) X
		offZ := baseZ + (-2 + int(r.nextInt(5))) // DRAW: nextInt(5) Z
		offY := baseY + 1                        // the +1 Y in offset(dx, 1, dz)
		// setLimitedLife(20*(30 + nextInt(90))): the life draw on the EVOKER's stream (drawn AFTER the vex's
		// finalizeSpawn in the jar, but finalizeSpawn draws are on the vex's own stream, so the evoker order
		// is exactly nextInt(5), nextInt(5), nextInt(90) -- preserved).
		life := 20 * (30 + int(r.nextInt(90))) // DRAW: nextInt(90) limited-life
		// vex.snapTo(offset, 0, 0): the vex spawns at the block CENTER (x+0.5, y, z+0.5). setOwner(this) +
		// setBoundOrigin(offset) + setLimitedLife(life) are folded into spawnVex.
		t.spawnVex(e.id, float64(offX)+0.5, float64(offY), float64(offZ)+0.5, offX, offY, offZ, life)
	}
}

// evokerAttackFangs ports EvokerAttackSpellGoal.performSpellCasting: NO RandomSource draws. It computes the
// EvokerFangs geometry (a 5+8-fang double arc within 3 blocks, else a 16-fang line toward the target) and
// spawns each fang at a sturdy floor. The EvokerFangs entity is unbuilt -> the SPAWN is cite-deferred, but
// the geometry is computed faithfully (no RNG, so no desync). Cite EvokerAttackSpellGoal.performSpellCasting.
func (t *TickLoop) evokerAttackFangs(e *Entity) {
	id := mobTarget(e)
	if id == 0 {
		return
	}
	target := t.playerByEntityID(id)
	if target == nil {
		return
	}
	minY := math.Min(target.y, e.y)
	maxY := math.Max(target.y, e.y) + 1.0
	baseAngle := math.Atan2(target.z-e.z, target.x-e.x)
	distSqr := distanceToSqrPlayer(target, e)
	if distSqr < 9.0 {
		// Inner arc: 5 fangs, radius 1.5, step PI*0.4, warmup 0.
		for i := 0; i < 5; i++ {
			ang := baseAngle + float64(i)*math.Pi*0.4
			t.evokerCreateFang(e, e.x+math.Cos(ang)*1.5, e.z+math.Sin(ang)*1.5, minY, maxY, ang, 0)
		}
		// Outer arc: 8 fangs, radius 2.5, step 2PI/8, offset 2PI/5, warmup 3.
		for i := 0; i < 8; i++ {
			ang := baseAngle + float64(i)*(2.0*math.Pi/8.0) + 1.2566371
			t.evokerCreateFang(e, e.x+math.Cos(ang)*2.5, e.z+math.Sin(ang)*2.5, minY, maxY, ang, 3)
		}
	} else {
		// Line: 16 fangs, dist 1.25*(i+1), warmup i.
		for i := 0; i < 16; i++ {
			dist := 1.25 * float64(i+1)
			t.evokerCreateFang(e, e.x+math.Cos(baseAngle)*dist, e.z+math.Sin(baseAngle)*dist, minY, maxY, baseAngle, i)
		}
	}
}

// evokerCreateFang ports Evoker.createSpellEntity: scan DOWN from y=maxY for a sturdy floor and, on success,
// spawn an EvokerFangs at (x, floorY, z, yRot, warmupDelay). NO RandomSource draws (the fangs geometry is
// deterministic), so this never perturbs the evoker's lockstep stream. Cite Evoker.createSpellEntity.
//
//	[VERIFIED CFR Evoker.createSpellEntity: pos = BlockPos.containing(x, maxY, z); do {
//	  below = pos.below(); if (belowState.isFaceSturdy(UP)) { if (!isEmptyBlock(pos) && !collisionShape.isEmpty)
//	    topOffset = shape.max(Y); success = true; break; } } while ((pos = pos.below()).getY() >= floor(minY)-1);
//	  if (success) level.addFreshEntity(new EvokerFangs(level, x, pos.getY()+topOffset, z, angle, delayTicks, this)).]
func (t *TickLoop) evokerCreateFang(e *Entity, x, z, minY, maxY, yRot float64, warmupDelay int) {
	// Scan DOWN from the ceiling cell (containing(x, maxY, z)) to floor(minY)-1, looking for the first cell
	// whose block BELOW is a sturdy up-face (a solid floor). v1 uses isSolidAt as the sturdy-floor stand-in
	// (the same block-solidity simplification the arrow's clip uses -- the full isFaceSturdy(UP) VoxelShape
	// face test is cite-deferred; the observable "fangs erupt from the ground under the arc" holds). The
	// vanilla topOffset (the below-block collision-shape max on Y) is 0 in the common flat-floor case.
	topPosY := int(math.Floor(maxY))
	bottomPosY := int(math.Floor(minY)) - 1
	bx := int(math.Floor(x))
	bz := int(math.Floor(z))
	for py := topPosY; py >= bottomPosY; py-- {
		// below = pos.below(); isFaceSturdy(UP) stand-in: the block one cell down is solid (a floor).
		if !t.isSolidAt(pk.Position{X: bx, Y: py - 1, Z: bz}) {
			continue
		}
		// success: the fangs erupt at this floor cell's TOP. new EvokerFangs(level, x, pos.getY()+topOffset,
		// z, angle, delayTicks, evoker) + addFreshEntity + ENTITY_PLACE (the gameEvent is a cite-deferred POI
		// signal; the spawn + AddEntity are the observable). topOffset == 0 (flat-floor v1 stand-in).
		t.spawnEvokerFangs(e.id, x, float64(py), z, yRot, warmupDelay)
		return
	}
	// No sturdy floor found in the [floor(minY)-1, maxY] column: no fangs here (vanilla success==false).
}

// evokerWololoRecolor ports EvokerWololoSpellGoal.performSpellCasting: recolor the picked BLUE sheep to RED.
// VERBATIM (CFR): Sheep wololoTarget = getWololoTarget(); if (wololoTarget != null && wololoTarget.isAlive())
// wololoTarget.setColor(DyeColor.RED). The target id was captured by wololoCanUse; resolve it through the
// evoker's owning region (the same-region cut the other evoker scans use) and setColor(RED). NO RNG.
// Cite Evoker$EvokerWololoSpellGoal.performSpellCasting.
func (t *TickLoop) evokerWololoRecolor(e *Entity) {
	if e.evokerWololoTarget == 0 || t.cur() == nil {
		return // getWololoTarget() == null
	}
	target, ok := t.cur().entities.get(e.evokerWololoTarget)
	if !ok || !target.isAlive() || target.dead {
		return // wololoTarget == null || !isAlive()
	}
	t.sheepSetColor(target, dyeRed) // wololoTarget.setColor(DyeColor.RED)
}

// wololoCanUse ports EvokerWololoSpellGoal.canUse: false if the evoker has a combat target, is casting, or is
// on cooldown; else (mobGriefing on) pick a random in-range BLUE sheep. In v1 there are NO blue sheep (sheep
// have no color state), so the search is always empty -> false. The nextInt(list.size()) pick is faithful but
// unreachable (empty list). Cite EvokerWololoSpellGoal.canUse.
func (g *evokerUseSpellGoal) wololoCanUse(t *TickLoop, e *Entity) bool {
	if mobTarget(e) != 0 { // getTarget() != null -> won't wololo mid-combat
		return false
	}
	if e.currentSpell != illagerSpellNone { // isCastingSpell()
		return false
	}
	if t.gametime < g.nextAttackTickCount { // tickCount < nextAttackTickCount
		return false
	}
	if !t.gameRule(ruleMobGriefing) { // gamerule mobGriefing off -> no wololo
		return false
	}
	// getNearbyEntities(Sheep.class, wololoTargeting, this, getBoundingBox().inflate(16,4,16)): collect every
	// live BLUE sheep whose center is within the inflated AABB (|dx|<=16, |dy|<=4, |dz|<=16) — the
	// TargetingConditions.forNonCombat().range(16.0).selector(getColor()==BLUE) predicate. entities.near scans
	// the surrounding chunk band (16 blocks -> ceil(16/16)=1 chunk radius); we filter to Sheep + BLUE + the box.
	// If the list is EMPTY, canUse is false; else pick one via random.nextInt(list.size()) (the evoker's mob
	// stream) and set it as the wololoTarget. The list's iteration order follows entities.near (the v5
	// same-region-scan order the breed/target scans use) — the DRAW (nextInt(size)) + its bound are the
	// faithful contract. Cite Evoker$EvokerWololoSpellGoal.canUse.
	if t.cur() == nil {
		return false
	}
	blue := make([]int32, 0, 4)
	for _, other := range t.cur().entities.near(e.x, e.z, 1) {
		if other == e || other.dead || !other.isAlive() {
			continue
		}
		if other.typ != entity.Sheep.ID {
			continue
		}
		if sheepGetColor(other) != dyeBlue { // selector: (Sheep)target.getColor() == DyeColor.BLUE
			continue
		}
		if math.Abs(other.x-e.x) > 16.0 || math.Abs(other.y-e.y) > 4.0 || math.Abs(other.z-e.z) > 16.0 {
			continue // outside getBoundingBox().inflate(16,4,16)
		}
		blue = append(blue, other.id)
	}
	if len(blue) == 0 {
		return false // entities.isEmpty()
	}
	// setWololoTarget(entities.get(random.nextInt(entities.size()))): the ONE wololo pick draw (mob stream).
	e.evokerWololoTarget = blue[mobRandom(e).nextInt(len(blue))]
	return true
}

// evokerAiStep ports SpellcasterIllager.customServerAiStep's server branch (the per-type hook, sibling of
// creeperAiStep). Called from tickAI for a live evoker (typ == entity.Evoker.ID) AFTER serverAiStep. It
// decrements the spell-casting countdown each tick (SpellcasterIllager: if spellCastingTickCount>0 then
// spellCastingTickCount--). NO RNG. Cite SpellcasterIllager.customServerAiStep.
func (t *TickLoop) evokerAiStep(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	if e.spellCastingTickCount > 0 {
		e.spellCastingTickCount--
	}
}
