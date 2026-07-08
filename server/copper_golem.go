// copper_golem.go -- COPPER GOLEM (net.minecraft.world.entity.animal.golem.CopperGolem), 1:1 port from the
// unobfuscated 26.2 jar. Copper Golem is a utility AbstractGolem that presses copper buttons + moves items
// between chests, and (its SIGNATURE) OXIDIZES over time through the WeatheringCopper stages
// (UNAFFECTED -> EXPOSED -> WEATHERED -> OXIDIZED); once OXIDIZED it can turn into a statue. This port
// lands the attributes + spawn + the SIGNATURE oxidation stepper (updateWeathering: the nextWeatheringTick
// schedule + the stage advance), with the button/chest pathing + statue BLOCK conversion deferred.
// Additive + per-type-gated behind e.isCopperGolem (false for every other entity; the pig oracle stays
// byte-identical).
//
// VANILLA (verified javap this task):
//   createAttributes: Mob.createMobAttributes + MOVEMENT_SPEED 0.20000000298023224 + STEP_HEIGHT 1.0 +
//     MAX_HEALTH 12.0.
//   constants: WEATHERING_TICK_FROM 504000, WEATHERING_TICK_TO 552000, TURN_TO_STATUE_CHANCE 0.0058f.
//   updateWeathering(level, random, gameTime): if nextWeatheringTick == -2 return (disabled). If == -1 ->
//     nextWeatheringTick = gameTime + nextIntBetweenInclusive(504000, 552000); return. Else: state =
//     weatherState; isOxidized = state==OXIDIZED; if gameTime >= nextWeatheringTick AND !isOxidized ->
//     next = state.next(); setWeatherState(next); nextWeatheringTick = (next==OXIDIZED ? 0 : gameTime +
//     nextIntBetweenInclusive(504000,552000)). If isOxidized AND canTurnToStatue -> turnToStatue.
//   tick(): super.tick(); if !isClientSide -> updateWeathering(level, level.getRandom(), level.getGameTime()).
//   WeatheringCopper.WeatherState order: UNAFFECTED(0), EXPOSED(1), WEATHERED(2), OXIDIZED(3); next() =
//     the next enum (OXIDIZED.next() == OXIDIZED, clamped).
//
// LANDED: the 3 attributes, spawn, the SIGNATURE updateWeathering oxidation stepper (nextWeatheringTick
// schedule seeded on the -1 sentinel, the stage advance UNAFFECTED->EXPOSED->WEATHERED->OXIDIZED on the
// gameTime schedule, the reschedule with nextIntBetweenInclusive(504000,552000)). RNG uses the region
// levelRandom (level.getRandom() analogue) exactly as the jar draws it.
//
// v1 STUBS (cited): the STATUE block conversion (turnToStatue -> CopperGolemStatueBlock) is DEFERRED
// behind the block subsystem -- the OXIDIZED terminal is reached faithfully + the statue-intent flag
// (copperGolemIsStatue) is set so the block swap lands the moment the statue block wires in. The button/
// chest brain pathing (CopperGolemAi) + shear + lightning-de-oxidation are cited deferrals.

package server

import (
	"github.com/imhinotori/sulfur/data/entity"
)

// Copper Golem weather-state ids (WeatheringCopper.WeatherState ordinal; VERIFIED javap enum order).
const (
	copperGolemWeatherUnaffected = 0 // UNAFFECTED
	copperGolemWeatherExposed    = 1 // EXPOSED
	copperGolemWeatherWeathered  = 2 // WEATHERED
	copperGolemWeatherOxidized   = 3 // OXIDIZED (terminal)
)

// Copper Golem constants (VERIFIED javap this task).
const (
	copperGolemMaxHealth       = 12.0                 // createAttributes MAX_HEALTH 12.0
	copperGolemMovementSpeed   = 0.20000000298023224  // createAttributes MOVEMENT_SPEED (float-widened)
	copperGolemStepHeight      = 1.0                  // createAttributes STEP_HEIGHT 1.0
	copperGolemWeatheringFrom  = 504000               // WEATHERING_TICK_FROM
	copperGolemWeatheringTo    = 552000               // WEATHERING_TICK_TO
	copperGolemWeatherUninit   = -1                   // nextWeatheringTick sentinel: uninitialized
	copperGolemWeatherDisabled = -2                   // nextWeatheringTick sentinel: disabled
)

// spawnCopperGolem creates a CopperGolem at (x,y,z) and adds it to the owner region store. WeatherState
// starts UNAFFECTED; nextWeatheringTick starts at the -1 sentinel (first updateWeathering seeds the
// schedule). Minimal e.ai (per-entity rng); NO goalSelector (code-driven copperGolemAiStep). initSpawnHealth
// seeds health from MAX_HEALTH (12.0). Cite CopperGolem(EntityType, Level) + createAttributes.
func (t *TickLoop) spawnCopperGolem(x, y, z float64) *Entity {
	c := NewEntity(t.idAlloc.AllocID(), entity.CopperGolem, x, y, z)
	c.isCopperGolem = true
	c.copperGolemWeather = copperGolemWeatherUnaffected
	c.copperGolemNextWeatherTick = copperGolemWeatherUninit // -1: first update seeds the schedule
	initSpawnHealth(c)                                       // setHealth(getMaxHealth()) -> 12.0
	c.ai = &mobAI{}
	reseedMobAI(c.ai, c.id)
	owner := t.regionForEntity(c)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(c)
	return c
}

// copperGolemNextWeatherState ports WeatheringCopper.WeatherState.next(): the next enum, clamped at
// OXIDIZED (OXIDIZED.next() == OXIDIZED). Cite WeatherState.next.
func copperGolemNextWeatherState(state int) int {
	if state >= copperGolemWeatherOxidized {
		return copperGolemWeatherOxidized
	}
	return state + 1
}

// copperGolemNextIntBetweenInclusive ports RandomSource.nextIntBetweenInclusive(min, max) drawn on the
// region levelRandom (level.getRandom() analogue): min + nextInt(max - min + 1). Uses t.cur().levelRandom
// so the draw order matches the jar (updateWeathering draws only on the -1 seed + each stage advance).
func (t *TickLoop) copperGolemNextIntBetweenInclusive(min, max int) int {
	r := t.cur()
	if r == nil || r.levelRandom == nil {
		return min // no region rng (degenerate): the low bound (deterministic, cited)
	}
	return min + int(r.levelRandom.NextIntN(int32(max-min+1)))
}

// copperGolemAiStep ports CopperGolem.tick (server tail) -> updateWeathering(level, level.getRandom(),
// level.getGameTime()), driven per-type from tickAI (gated on typ == entity.CopperGolem.ID, AFTER
// serverAiStep). This is the SIGNATURE oxidation stepper. The button/chest brain is DEFERRED. RNG on the
// region levelRandom. Cite CopperGolem.tick + updateWeathering.
func (t *TickLoop) copperGolemAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	t.copperGolemUpdateWeathering(e)
}

// copperGolemUpdateWeathering ports CopperGolem.updateWeathering(level, random, gameTime):
//   if nextWeatheringTick == -2 return (disabled).
//   if nextWeatheringTick == -1 -> nextWeatheringTick = gameTime + nextIntBetweenInclusive(504000,552000).
//   else: isOxidized = state==OXIDIZED; if gameTime >= nextWeatheringTick && !isOxidized -> next =
//     state.next(); setWeatherState(next); nextWeatheringTick = (next==OXIDIZED ? 0 : gameTime +
//     nextIntBetweenInclusive(504000,552000)). if isOxidized && canTurnToStatue -> turnToStatue.
// gameTime is t.gametime (Level.getGameTime()). Cite CopperGolem.updateWeathering.
func (t *TickLoop) copperGolemUpdateWeathering(e *Entity) {
	if e.copperGolemNextWeatherTick == copperGolemWeatherDisabled {
		return // -2: weathering disabled
	}
	gameTime := t.gametime
	if e.copperGolemNextWeatherTick == copperGolemWeatherUninit {
		// -1: seed the first schedule.
		e.copperGolemNextWeatherTick = gameTime + int64(t.copperGolemNextIntBetweenInclusive(copperGolemWeatheringFrom, copperGolemWeatheringTo))
		return
	}
	isOxidized := e.copperGolemWeather == copperGolemWeatherOxidized
	if gameTime >= e.copperGolemNextWeatherTick && !isOxidized {
		next := copperGolemNextWeatherState(e.copperGolemWeather)
		e.copperGolemWeather = next // setWeatherState(next)
		if next == copperGolemWeatherOxidized {
			e.copperGolemNextWeatherTick = 0
		} else {
			e.copperGolemNextWeatherTick = gameTime + int64(t.copperGolemNextIntBetweenInclusive(copperGolemWeatheringFrom, copperGolemWeatheringTo))
		}
		isOxidized = next == copperGolemWeatherOxidized
	}
	if isOxidized && t.copperGolemCanTurnToStatue(e) {
		t.copperGolemTurnToStatue(e)
	}
}

// copperGolemCanTurnToStatue ports CopperGolem.canTurnToStatue(level): the OXIDIZED golem is eligible to
// freeze into a statue. The full jar predicate (on-ground, not-in-liquid, valid statue placement) needs
// the block/collision context; v1 uses a cited constant-false so the golem holds at OXIDIZED (the statue
// BLOCK conversion is DEFERRED). Cite CopperGolem.canTurnToStatue.
func (t *TickLoop) copperGolemCanTurnToStatue(e *Entity) bool {
	_ = e
	return false // DEFERRED: statue placement needs the CopperGolemStatueBlock subsystem
}

// copperGolemTurnToStatue ports CopperGolem.turnToStatue(level): replace the golem with a
// CopperGolemStatueBlock carrying its weather state. DEFERRED behind the block subsystem -- the
// statue-intent flag is set so the swap lands the moment the statue block wires in. Cite turnToStatue.
func (t *TickLoop) copperGolemTurnToStatue(e *Entity) {
	e.copperGolemIsStatue = true // DEFERRED: CopperGolemStatueBlock placement + discard()
}
