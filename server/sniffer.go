// sniffer.go -- the Sniffer (net.minecraft.world.entity.animal.sniffer.Sniffer), a 1:1 port from the
// unobfuscated 26.2 jar. Sniffer walks a DIG state machine: it scents, sniffs, searches, digs at a valid
// ground block, and drops an ancient seed (torchflower / pitcher pod). Vanilla drives it with a BRAIN + a
// DATA_STATE enum (IDLING/FEELING_HAPPY/SCENTING/SNIFFING/SEARCHING/DIGGING/RISING) and a
// DATA_DROP_SEED_AT_TICK dig timer. The full brain (sensors, memories, activity scheduler) is DEFERRED;
// this port re-expresses the OBSERVABLE state machine + timers + seed drop as a code-driven per-tick step
// in snifferAiStep (per-type-gated on typ == entity.Sniffer.ID).
//
// VANILLA (verified javap Sniffer + SnifferAi + the SnifferAi behavior inner classes this session):
//   createAttributes: Animal.createAnimalAttributes + MOVEMENT_SPEED 0.10000000149011612 + MAX_HEALTH 14.0.
//   State enum ids (Sniffer.State static init): IDLING=0 FEELING_HAPPY=1 SCENTING=2 SNIFFING=3 SEARCHING=4
//     DIGGING=5 RISING=6.
//   Behavior durations (SnifferAi.initSniffingActivity ctor args): Scenting(40,80), Sniffing(40,80),
//     Searching(max 600), Digging(160,180), FinishedDigging/RISING(40), FeelingHappy(40,100).
//   onDiggingStart: DATA_DROP_SEED_AT_TICK = tickCount + 120 (DIGGING_DROP_SEED_OFFSET_TICKS = 120).
//   tick(): while DIGGING -> emitDiggingParticles + dropSeed(); dropSeed drops SNIFFER_DIGGING loot
//     (torchflower_seeds OR pitcher_pod, gift table 1 pool / 1 roll / 2 equal-weight entries) + plays
//     SNIFFER_DROP_SEED, but ONLY on the exact tick tickCount == DATA_DROP_SEED_AT_TICK.
//   onDiggingComplete(true): storeExploredPosition(getOnPos) -> SNIFFER_EXPLORED_POSITIONS, limit(20) then
//     add(0, pos) (cap 20, evict oldest). SNIFFING_COOLDOWN_TICKS = 9600 between cycles.
//
// v1 STUBS (cited): the SnifferAi BRAIN machinery (sensors/memories/activity scheduler + the pathfinding to
// the dig position) is DEFERRED. This port supplies the bounded passive goal walk (newSnifferAI) PLUS the
// OBSERVABLE dig cycle (SCENTING -> SNIFFING -> SEARCHING -> DIGGING(drop seed at +120) -> RISING -> IDLING,
// then a 9600-tick cooldown). The SNIFFER_DIGGING loot pick is modeled directly (50/50 seed) on the mob
// OWN RNG stream, citing BuiltInLootTables.SNIFFER_DIGGING. Sounds are DEFERRED (no sound seam on the mob
// dig path yet) -- cited at each transition where vanilla plays one.

package server

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// Sniffer constants (VERIFIED javap Sniffer + SnifferAi + the behavior inner-class ctor args this session).
const (
	snifferMaxHealth     = 14.0                // createAttributes MAX_HEALTH 14.0
	snifferMovementSpeed = 0.10000000149011612 // createAttributes MOVEMENT_SPEED (float-widened)
	snifferTemptSpeed    = 1.25                // FollowTemptation speed (brain-deferred; the common tempt pace)
	snifferFollowSpeed   = 1.25                // BabyFollowAdult speed (brain-deferred; the common follow pace)
	snifferBreedSpeed    = 1.0                 // AnimalMakeLove/breed speed (brain-deferred; the common breed pace)
	snifferStrollSpeed   = 1.0                 // RandomStroll speed (brain-deferred; the common stroll pace)
	snifferLookDistance  = 8.0                 // LookAtTargetSink distance (the common animal look range)
	snifferFoodTag       = "sniffer_food"      // SNIFFER_FOOD (torchflower seeds): the tempt predicate

	// State enum ids (Sniffer.State static init: id == ordinal).
	snifferStateIdling       = 0 // IDLING
	snifferStateFeelingHappy = 1 // FEELING_HAPPY
	snifferStateScenting     = 2 // SCENTING
	snifferStateSniffing     = 3 // SNIFFING
	snifferStateSearching    = 4 // SEARCHING
	snifferStateDigging      = 5 // DIGGING
	snifferStateRising       = 6 // RISING

	// Behavior durations (SnifferAi.initSniffingActivity ctor args). The brain schedules each behavior for a
	// UniformInt(min,max) budget; re-expressed here as a per-mob timer (min + nextInt(max-min+1)).
	snifferScentingMin  = 40 // Scenting(40, 80)
	snifferScentingMax  = 80
	snifferSniffingMin  = 40 // Sniffing(40, 80)
	snifferSniffingMax  = 80
	snifferSearchingMax = 600 // Searching: max 600 ticks (MoveToTargetSink budget)
	snifferDiggingMin   = 160 // Digging(160, 180)
	snifferDiggingMax   = 180
	snifferRisingTicks  = 40 // FinishedDigging(40) -> RISING then IDLING

	snifferDropSeedOffset = 120 // DIGGING_DROP_SEED_OFFSET_TICKS = 120 (Sniffer.onDiggingStart)

	snifferDigCooldownTicks = 9600 // SnifferAi.SNIFFING_COOLDOWN_TICKS = 9600

	snifferExploredCap = 20 // storeExploredPosition: limit(20) then add(0, pos) -> cap 20, evict oldest
)

// newSnifferAI builds the Sniffer bounded passive AI. Sniffer is a BRAIN mob in vanilla; the OBSERVABLE
// dig-for-seeds DATA_STATE machine now lives in snifferAiStep (below). This supplies the visibly-alive
// classic-goal stand-in (Float/Panic/Breed/Tempt/Follow/Stroll/Look), mirroring newFrogAI shape. Cite
// Sniffer.makeBrain (the brain deferral note).
func newSnifferAI() *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	m.wantSpeedMod = 1.0
	m.navigation.speed = m.wantSpeedMod * snifferMovementSpeed // seed with MOVEMENT_SPEED (0.1)
	m.navigation.canFloat = true                               // Swim brain behavior: navigation may float
	applyAnimalPathfindingMalus(m)                             // Animal.<init> FIRE malus (no-op on a fire-free world)
	m.goals.addGoal(0, newFloatGoal())
	m.goals.addGoal(1, newPanicGoal(panicSpeedModifier))
	m.goals.addGoal(3, newBreedGoal(snifferBreedSpeed))
	m.goals.addGoal(4, newTemptGoal(snifferTemptSpeed, func(id int32) bool { return itemInTag(id, snifferFoodTag) }, false, nil))
	m.goals.addGoal(5, newFollowParentGoal(snifferFollowSpeed))
	m.goals.addGoal(6, newWaterAvoidingRandomStrollGoal(snifferStrollSpeed))
	m.goals.addGoal(7, newLookAtPlayerGoal(snifferLookDistance))
	m.goals.addGoal(8, newRandomLookAroundGoal())
	return m
}

// spawnSniffer creates a Sniffer at (x,y,z) with the jar attributes and the passive goal AI, then adds it
// to the owner region store. baby toggles the AgeableMob baby age + half-scale box. initSpawnHealth seeds
// health from MAX_HEALTH (14.0). Cite Sniffer.createAttributes + Sniffer(EntityType, Level). A freshly
// spawned adult starts IDLING (snifferState zero value) on a fresh dig cooldown (0).
func (t *TickLoop) spawnSniffer(x, y, z float64, baby bool) *Entity {
	s := NewEntity(t.idAlloc.AllocID(), entity.Sniffer, x, y, z)
	s.isSniffer = true
	if baby {
		s.breedAge = babyStartAge
		s.refreshDimensions() // AgeableMob baby half-scale box (getDefaultDimensions baby-scale)
	}
	initSpawnHealth(s) // setHealth(getMaxHealth()) -> 14.0
	s.ai = newSnifferAI()
	reseedMobAI(s.ai, s.id)
	owner := t.regionForEntity(s)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(s)
	return s
}

// snifferNextIntBetween ports RandomSource.nextIntBetweenInclusive(min, max) on the sniffer OWN mob rng
// stream (mobRandom): min + nextInt(max - min + 1). The brain schedules each behavior for a UniformInt
// (min,max) budget; this reproduces that draw on the per-entity stream (Sniffer-gated; the pig oracle never
// runs this). Cite UniformInt.sample / RandomSource.nextIntBetweenInclusive.
func snifferNextIntBetween(e *Entity, min, max int) int {
	if max <= min {
		return min
	}
	return min + mobRandom(e).nextInt(max-min+1)
}

// snifferCanDig ports Sniffer.canDig() (the guard subset available without the full brain/pathfinding):
// !isBaby && onGround && !isInWater && !isPassenger (isPanicking / isTempted are DEFERRED brain reads that
// default to their vanilla false while the sniffer walks the passive goals). The per-position
// SNIFFER_DIGGABLE_BLOCK + explored-position + pathability checks are DEFERRED (no dig-position search yet);
// the OBSERVABLE cycle proceeds on the timer. Cite Sniffer.canDig().
func (t *TickLoop) snifferCanDig(e *Entity) bool {
	if e.isBaby() {
		return false // isBaby() gate
	}
	if !e.onGround {
		return false // onGround() gate
	}
	if t.mobInWater(e) {
		return false // isInWater() gate (mobInWater)
	}
	if isPassengerEntity(e) {
		return false // isPassenger() gate
	}
	// DEFERRED: isPanicking() / isTempted() (brain memory reads) default false while walking passive goals;
	// the per-position canDig(BlockPos) (SNIFFER_DIGGABLE_BLOCK tag + explored + pathability) is DEFERRED.
	return true
}

// snifferSetState transitions DATA_STATE and (for DIGGING) runs onDiggingStart. Mirrors
// Sniffer.transitionTo(state) -> setState(state) [+ onScentingStart / onDiggingStart / sounds]. The
// per-transition sounds (SNIFFER_HAPPY/SCENTING/SNIFFING/DIGGING_STOP) are DEFERRED (no sound seam on the
// mob dig path). Cite Sniffer.transitionTo + Sniffer.onDiggingStart.
func snifferSetState(e *Entity, state int) {
	e.snifferState = state
	if state == snifferStateDigging {
		// Sniffer.onDiggingStart: DATA_DROP_SEED_AT_TICK = tickCount + 120. aiTickCount is the mob
		// Entity.tickCount proxy (incremented at the top of serverAiStep, exactly like baseTick).
		e.snifferDropSeedAtTick = e.ai.aiTickCount + snifferDropSeedOffset
	}
}

// snifferStoreExploredPosition ports Sniffer.storeExploredPosition(pos): take the explored list, limit to
// 20, then insert the new pos at index 0 (cap 20, evict oldest). We store packed BlockPos longs (the
// SNIFFER_EXPLORED_POSITIONS memory is GlobalPos in vanilla; the dimension is implicit here as the dig
// position search is DEFERRED). Cite Sniffer.storeExploredPosition.
func snifferStoreExploredPosition(e *Entity, packed int64) {
	list := e.snifferExplored
	if len(list) > snifferExploredCap {
		list = list[:snifferExploredCap] // limit(20)
	}
	// list.add(0, pos): prepend, evicting the oldest so the length never exceeds the cap.
	updated := make([]int64, 0, snifferExploredCap)
	updated = append(updated, packed)
	updated = append(updated, list...)
	if len(updated) > snifferExploredCap {
		updated = updated[:snifferExploredCap]
	}
	e.snifferExplored = updated
}

// snifferDropSeed ports Sniffer.dropSeed(): fires ONLY on the tick tickCount == DATA_DROP_SEED_AT_TICK,
// dropping one stack from BuiltInLootTables.SNIFFER_DIGGING (a gift table: 1 pool, 1 roll, 2 equal-weight
// entries -> torchflower_seeds OR pitcher_pod at 50/50) at the sniffer head block, then plays
// SNIFFER_DROP_SEED (sound DEFERRED). The 50/50 pick draws on the mob OWN rng stream (Sniffer-gated). Cite
// Sniffer.dropSeed + BuiltInLootTables.SNIFFER_DIGGING.
func (t *TickLoop) snifferDropSeed(e *Entity) {
	if e.snifferDropSeedAtTick != e.ai.aiTickCount {
		return // dropSeed guard: intValue() != tickCount -> return
	}
	// SNIFFER_DIGGING gift table: 1 pool / 1 roll / two equal-weight item entries -> a uniform pick between
	// the two (weight-sum 2). Draw on the mob rng stream.
	var seedID pk.VarInt
	if mobRandom(e).nextInt(2) == 0 {
		seedID = pk.VarInt(item.TorchflowerSeeds.ID)
	} else {
		seedID = pk.VarInt(item.PitcherPod.ID)
	}
	// Drop at the sniffer head/on-position (getHeadBlock). The dig-position search is DEFERRED, so the seed
	// pops at the sniffer OWN block position (the loot BiConsumer places at the dug block).
	pos := blockPosOfEntity(e)
	stack := component.SlotData{Count: 1, ItemID: seedID}
	t.spawnSnifferItem(e, pos, stack)
	// DEFERRED: playSound(SNIFFER_DROP_SEED) -- no sound seam on the mob dig path.
}

// spawnSnifferItem drops a single seed Item entity at the given block position, mirroring the dropSeed loot
// BiConsumer / Block.popResource spawn (block center + per-axis jitter, random toss, default pickup delay).
// Reuses NewItemEntity (the shared ItemEntity.<init> port). Cite Sniffer.dropSeed lambda.
func (t *TickLoop) spawnSnifferItem(e *Entity, pos pk.Position, stack component.SlotData) {
	x := float64(pos.X) + 0.5 + mthNextDouble(-itemSpawnJitter, itemSpawnJitter)
	y := float64(pos.Y) + 0.5 + mthNextDouble(-itemSpawnJitter, itemSpawnJitter) - itemEntityHalfHeight
	z := float64(pos.Z) + 0.5 + mthNextDouble(-itemSpawnJitter, itemSpawnJitter)
	ie := NewItemEntity(t.idAlloc.AllocID(), x, y, z, stack)
	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(ie)
}

// snifferAiStep is the Sniffer per-tick extra (Sniffer.customServerAiStep -> the SnifferAi brain + the
// tick() DIGGING branch). The full brain (sensors/memories/activity scheduler + dig-position pathfinding)
// is DEFERRED; this re-expresses the OBSERVABLE DATA_STATE machine + the DATA_DROP_SEED_AT_TICK dig timer
// on the mob own timer/rng: IDLING (on cooldown) -> SCENTING -> SNIFFING -> SEARCHING -> DIGGING (dropSeed
// at tickCount == DROP_SEED_AT_TICK, i.e. +120) -> RISING -> IDLING (reset 9600 cooldown). All RNG is on
// the sniffer OWN stream (Sniffer-gated; the pig oracle stream is untouched). Cite
// Sniffer.customServerAiStep + Sniffer.tick + SnifferAi (the behavior durations + SNIFFING_COOLDOWN_TICKS).
func (t *TickLoop) snifferAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	if e.ai == nil {
		return
	}

	// Sniffer.tick(): while DIGGING, dropSeed() runs EVERY tick (dropSeed self-guards to the exact
	// DROP_SEED_AT_TICK). Run the drop attempt before the timer decrement so the +120 offset lands exactly.
	if e.snifferState == snifferStateDigging {
		t.snifferDropSeed(e)
	}

	// The behavior timer counts down the current scheduled behavior.
	if e.snifferStateTimer > 0 {
		e.snifferStateTimer--
		return
	}

	switch e.snifferState {
	case snifferStateIdling:
		// IDLING: gated by the dig cooldown. Count it down; when it hits 0 and the sniffer can dig, begin the
		// cycle at SCENTING (transitionTo(SCENTING) -> onScentingStart).
		if e.snifferCooldown > 0 {
			e.snifferCooldown--
			return
		}
		if !t.snifferCanDig(e) {
			return // stay IDLING until the sniffer is a dig-capable adult on the ground
		}
		snifferSetState(e, snifferStateScenting)
		e.snifferStateTimer = snifferNextIntBetween(e, snifferScentingMin, snifferScentingMax)
	case snifferStateScenting:
		// SCENTING done -> SNIFFING (transitionTo(SNIFFING) plays SNIFFER_SNIFFING; sound DEFERRED).
		snifferSetState(e, snifferStateSniffing)
		e.snifferStateTimer = snifferNextIntBetween(e, snifferSniffingMin, snifferSniffingMax)
	case snifferStateSniffing:
		// SNIFFING done -> SEARCHING (walk to the dig position). The dig-position search is DEFERRED, so hold
		// SEARCHING for the MoveToTargetSink budget then proceed to DIGGING if still dig-capable.
		if !t.snifferCanDig(e) {
			snifferSetState(e, snifferStateIdling)
			e.snifferCooldown = snifferDigCooldownTicks
			return
		}
		snifferSetState(e, snifferStateSearching)
		e.snifferStateTimer = snifferSearchingMax
	case snifferStateSearching:
		// SEARCHING done -> DIGGING (Digging.start: canDig -> transitionTo(DIGGING) -> onDiggingStart).
		if !t.snifferCanDig(e) {
			snifferSetState(e, snifferStateIdling)
			e.snifferCooldown = snifferDigCooldownTicks
			return
		}
		snifferSetState(e, snifferStateDigging) // onDiggingStart: DROP_SEED_AT_TICK = tickCount + 120
		e.snifferStateTimer = snifferNextIntBetween(e, snifferDiggingMin, snifferDiggingMax)
	case snifferStateDigging:
		// DIGGING done -> RISING (FinishedDigging.start: transitionTo(RISING) + onDiggingComplete(true) ->
		// storeExploredPosition(getOnPos)). The dropSeed already fired at DROP_SEED_AT_TICK above.
		snifferSetState(e, snifferStateRising)
		pos := blockPosOfEntity(e)
		snifferStoreExploredPosition(e, snifferPackPos(pos)) // onDiggingComplete(true) -> storeExploredPosition
		e.snifferStateTimer = snifferRisingTicks
	case snifferStateRising:
		// RISING done -> IDLING, start the dig cooldown (FinishedDigging.stop -> transitionTo(IDLING)).
		snifferSetState(e, snifferStateIdling)
		e.snifferCooldown = snifferDigCooldownTicks
	default:
		// FEELING_HAPPY (brain-triggered) or any unexpected state: return to IDLING. FEELING_HAPPY is not
		// entered by this passive cycle (no brain trigger).
		snifferSetState(e, snifferStateIdling)
	}
}

// snifferPackPos packs a BlockPos into a single int64 (the Sniffer explored-positions key). Mirrors
// BlockPos.asLong bit-packing (26 bits X, 26 bits Z, 12 bits Y) so an explored spot round-trips uniquely.
func snifferPackPos(pos pk.Position) int64 {
	return (int64(pos.X&0x3FFFFFF) << 38) | (int64(pos.Y&0xFFF) << 26) | int64(pos.Z&0x3FFFFFF)
}
