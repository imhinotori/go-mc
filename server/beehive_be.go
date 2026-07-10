package server

// beehive_be.go -- the BEEHIVE BLOCK-ENTITY (BEEHIVE-01): a 1:1 port of
// net.minecraft.world.level.block.entity.BeehiveBlockEntity over the 26.2 jar (temp/cache/26.2-inner.jar,
// javap -c -p this session). A beehive/bee_nest holds up to 3 stored bees (Occupants). Each stored bee ages
// one tick per serverTick; once its ticksInHive exceeds its minTicksInHive (2400 with nectar, 600 without)
// it is RELEASED out the hive FACING front -- and a nectar-carrying bee (HONEY_DELIVERED) bumps the block
// HONEY_LEVEL by +1 (or +2 on a 1/100 roll), capped at 5. Adding a bee (addOccupant, driven from the
// enter-hive goal in bee_hive.go) stores an Occupant and discards the flying bee. Registers on first access
// and ticks passively (the tickBells twin -- t.beehives keyed by world position).
//
// 1:1 net.minecraft.world.level.block.entity.BeehiveBlockEntity, VERIFIED javap this session:
//   MAX_OCCUPANTS = 3; MIN_OCCUPATION_TICKS_NECTAR = 2400; MIN_OCCUPATION_TICKS_NECTARLESS = 600.
//   Occupant.of(bee): minTicksInHive = HasNectar ? 2400 : 600; ticksInHive = 0.
//   BeeData.tick(): POST-increment ticksInHive; return (PRE-increment) > minTicksInHive (strict greater).
//   addOccupant(bee): if size>=3 return; storeBee(Occupant.of(bee)); flower-pos adopt (nextBoolean ONLY
//     when both have a flower pos); discard bee. serverTick: tickOccupants; if(!empty && nextDouble<0.005)
//     BEEHIVE_WORK. releaseOccupant: front-block collision gate; createEntity; nextFloat(<0.9) flower
//     restore; HONEY_DELIVERED -> dropOffNectar + nextInt(100) honey bump capped at 5; addFreshEntity.
//
// v1 STUBS (cited, each == vanilla default): BEES_STAY_IN_HIVE == false (release early-return skipped);
//   createEntity NBT round-trip -> native Bee via spawnHiveBee (setNoGravity/setHivePos/nectar) with the
//   identical observable outcome; sounds + gameEvent are cite-deferred cosmetics (the RNG draws that GATE
//   them are preserved in lockstep); NBT persistence deferred (register-on-place, no round-trip yet).

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// Beehive constants (BeehiveBlockEntity static fields, VERIFIED javap this session).
const (
	beehiveMaxOccupants               = 3    // MAX_OCCUPANTS
	beehiveMinOccupationTicksNectar   = 2400 // MIN_OCCUPATION_TICKS_NECTAR
	beehiveMinOccupationTicksNectarNo = 600  // MIN_OCCUPATION_TICKS_NECTARLESS
	beehiveMaxHoneyLevel              = 5    // BeehiveBlock.HONEY_LEVEL max (IntegerProperty 0..5)
)

// beeReleaseStatus mirrors BeehiveBlockEntity.BeeReleaseStatus. HONEY_DELIVERED (had nectar -> bumps
// HONEY_LEVEL), BEE_RELEASED (no nectar), EMERGENCY (bypasses the blocked-front + stay-in-hive guards).
type beeReleaseStatus int

const (
	beeHoneyDelivered beeReleaseStatus = iota
	beeReleased
	beeEmergency
)

// beehiveOccupant is BeehiveBlockEntity.Occupant narrowed to the fields the hive loop reads. The full
// TypedEntityData tag round-trip is deferred (spawnHiveBee reconstructs a native bee). CITE Occupant.
type beehiveOccupant struct {
	hasNectar      bool
	ticksInHive    int
	minTicksInHive int
	savedFlowerPos *pk.Position
}

// beehiveOccupantOf ports Occupant.of(bee): minTicksInHive = HasNectar ? 2400 : 600; ticksInHive = 0.
func beehiveOccupantOf(bee *Entity) beehiveOccupant {
	minTicks := beehiveMinOccupationTicksNectarNo
	if bee.beeHasNectar {
		minTicks = beehiveMinOccupationTicksNectar // HasNectar ? 2400 : 600
	}
	return beehiveOccupant{
		hasNectar:      bee.beeHasNectar,
		ticksInHive:    0,
		minTicksInHive: minTicks,
		savedFlowerPos: bee.beeSavedFlowerPos,
	}
}

// beehiveBeeData is BeehiveBlockEntity.BeeData: an Occupant plus a live ticksInHive counter.
type beehiveBeeData struct {
	occupant    beehiveOccupant
	ticksInHive int
}

// newBeehiveBeeData ports the BeeData ctor: ticksInHive = occupant.ticksInHive.
func newBeehiveBeeData(o beehiveOccupant) *beehiveBeeData {
	return &beehiveBeeData{occupant: o, ticksInHive: o.ticksInHive}
}

// tick ports BeeData.tick EXACTLY: POST-increment ticksInHive, then return (PRE-increment ticksInHive) >
// minTicksInHive. The dup_x1/iadd/putfield leaves the PRE-increment value on the stack for if_icmple.
func (d *beehiveBeeData) tick() bool {
	pre := d.ticksInHive
	d.ticksInHive++
	return pre > d.occupant.minTicksInHive
}

// hasNectar ports BeeData.hasNectar.
func (d *beehiveBeeData) hasNectar() bool { return d.occupant.hasNectar }

// toOccupant ports BeeData.toOccupant: new Occupant(entityData, this.ticksInHive, minTicksInHive).
func (d *beehiveBeeData) toOccupant() beehiveOccupant {
	return beehiveOccupant{
		hasNectar:      d.occupant.hasNectar,
		ticksInHive:    d.ticksInHive,
		minTicksInHive: d.occupant.minTicksInHive,
		savedFlowerPos: d.occupant.savedFlowerPos,
	}
}

// beehiveBE is the tick-owned state of one beehive/bee_nest block-entity (stored-bee list + savedFlowerPos).
type beehiveBE struct {
	stored         []*beehiveBeeData
	savedFlowerPos *pk.Position
}

func (b *beehiveBE) isEmpty() bool          { return len(b.stored) == 0 }
func (b *beehiveBE) isFull() bool           { return len(b.stored) == beehiveMaxOccupants }
func (b *beehiveBE) occupantCount() int     { return len(b.stored) }
func (b *beehiveBE) hasSavedFlowerPos() bool { return b.savedFlowerPos != nil }

// storeBee ports BeehiveBlockEntity.storeBee: stored.add(new BeeData(occupant)).
func (b *beehiveBE) storeBee(o beehiveOccupant) {
	b.stored = append(b.stored, newBeehiveBeeData(o))
}

// beehiveAddOccupant ports BeehiveBlockEntity.addOccupant(bee): NO-OP when full (>=3); else store an
// Occupant.of(bee), maybe adopt the bee flower pos (nextBoolean draw ONLY when BOTH have a flower pos --
// the || short-circuit), then discard the flying bee. Sounds + gameEvent cite-deferred. CITE addOccupant.
func (t *TickLoop) beehiveAddOccupant(pos pk.Position, b *beehiveBE, bee *Entity) {
	_ = pos
	// if (stored.size() >= 3) return;
	if len(b.stored) >= beehiveMaxOccupants {
		return
	}
	// stopRiding/ejectPassengers/dropLeash: no-ops on v1 bee state (cited). storeBee(Occupant.of(bee)):
	b.storeBee(beehiveOccupantOf(bee))
	if t.world() != nil {
		// if (bee.hasSavedFlowerPos() && (!this.hasSavedFlowerPos() || level.random.nextBoolean()))
		//     savedFlowerPos = bee.getSavedFlowerPos();
		if bee.beeSavedFlowerPos != nil {
			adopt := !b.hasSavedFlowerPos()
			if !adopt {
				// nextBoolean() drawn ONLY when the hive ALREADY has a flower pos (the || RHS): exact order.
				if r := t.cur(); r != nil && r.levelRandom != nil {
					adopt = r.levelRandom.NextBoolean()
				}
			}
			if adopt {
				fp := *bee.beeSavedFlowerPos
				b.savedFlowerPos = &fp
			}
		}
		// playSound(BEEHIVE_ENTER) + gameEvent(BLOCK_CHANGE): cite-deferred cosmetics.
	}
	bee.dead = true // bee.discard()
	// setChanged(): persistence deferred.
}

// beehiveServerTick ports BeehiveBlockEntity.serverTick: tickOccupants, then -- ONLY when non-empty -- roll
// nextDouble()<0.005 for BEEHIVE_WORK (cite-deferred; the RNG draw preserved so an empty hive draws NOTHING).
func (t *TickLoop) beehiveServerTick(pos pk.Position, b *beehiveBE) {
	if t.world() == nil {
		return
	}
	t.beehiveTickOccupants(pos, b)
	if !b.isEmpty() {
		if r := t.cur(); r != nil && r.levelRandom != nil {
			if r.levelRandom.NextDouble() < 0.005 {
				// BEEHIVE_WORK sound: cite-deferred cosmetic. The nextDouble() draw is preserved.
				_ = pos
			}
		}
	}
}

// beehiveTickOccupants ports BeehiveBlockEntity.tickOccupants: for each BeeData, tick() it, and when true
// release it (HONEY_DELIVERED with nectar, else BEE_RELEASED); a successful release removes it. CITE tickOccupants.
func (t *TickLoop) beehiveTickOccupants(pos pk.Position, b *beehiveBE) {
	changed := false
	kept := b.stored[:0] // iterator.remove() analogue: rebuild the list in-place, dropping released bees
	for _, d := range b.stored {
		released := false
		if d.tick() {
			status := beeReleased
			if d.hasNectar() {
				status = beeHoneyDelivered
			}
			if t.beehiveReleaseOccupant(pos, d.toOccupant(), status, b.savedFlowerPos) {
				changed = true
				released = true
			}
		}
		if !released {
			kept = append(kept, d)
		}
	}
	b.stored = kept
	_ = changed // if (changed) setChanged(...): persistence deferred
}

// beehiveReleaseOccupant ports BeehiveBlockEntity.releaseOccupant(...) with entitiesList == null (serverTick
// path). Returns true when released. EXACT RNG order in the Bee branch: (a) nextFloat() flower-pos restore
// [only when storedFlowerPos != null && !bee.hasSavedFlowerPos()], then (b) nextInt(100) honey bump [only
// when HONEY_DELIVERED && beehive tag && honeyLevel<5]. CITE releaseOccupant.
func (t *TickLoop) beehiveReleaseOccupant(pos pk.Position, occ beehiveOccupant, status beeReleaseStatus, storedFlowerPos *pk.Position) bool {
	if t.world() == nil {
		return false
	}
	// 1. BEES_STAY_IN_HIVE default false (EnvironmentAttributes default) -> early-return never fires (cited).
	const beesStayInHive = false
	if beesStayInHive && status != beeEmergency {
		return false
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsBeehiveBlock(state) {
		return false
	}
	// 2. dir = state.getValue(FACING); front = pos.relative(dir);
	dir, _ := block.BeehiveFacing(state)
	front := relative(pos, dir)
	frontState, fok := t.world().GetBlock(front, dimMinY)
	if !fok {
		frontState = t.airState()
	}
	// blocked = !getCollisionShape(front).isEmpty(); if (blocked && status != EMERGENCY) return false.
	blocked := !block.CollisionShape(frontState).IsEmpty()
	if blocked && status != beeEmergency {
		return false
	}
	// 3. Entity e = occupant.createEntity(level, pos); if (e == null) return false. (Never nil for a bee.)
	bee := t.spawnHiveBee(pos, occ)
	if bee == nil {
		return false
	}
	// 4. if (e instanceof Bee bee): rng = level.getRandom();
	var rng *legacyRandomFacade
	if r := t.cur(); r != nil && r.levelRandom != nil {
		rng = &legacyRandomFacade{r.levelRandom}
	}
	// (a) if (storedFlowerPos != null && !bee.hasSavedFlowerPos() && rng.nextFloat() < 0.9f) setSavedFlowerPos.
	if storedFlowerPos != nil && bee.beeSavedFlowerPos == nil {
		if rng != nil && rng.nextFloat() < 0.9 {
			fp := *storedFlowerPos
			bee.beeSavedFlowerPos = &fp
		}
	}
	// (b) if (status == HONEY_DELIVERED) { dropOffNectar(); if state.is(BEEHIVES) && hl<5 { bump } }
	if status == beeHoneyDelivered {
		bee.beeHasNectar = false // dropOffNectar(): setHasNectar(false)
		hl := block.HoneyLevel(state)
		if hl < beehiveMaxHoneyLevel {
			i := 1
			if rng != nil && rng.nextInt(100) == 0 {
				i = 2 // rng.nextInt(100) == 0 ? 2 : 1
			}
			if hl+i > beehiveMaxHoneyLevel {
				i-- // if (hl + i > 5) i--;
			}
			if ns, nok := block.WithHoneyLevel(state, hl+i); nok {
				// setBlockAndUpdate(pos, state.setValue(HONEY_LEVEL, hl+i)): SetBlock + broadcast (UPDATE_ALL).
				if t.world().SetBlock(pos, ns, dimMinY) {
					t.broadcastBlockUpdate(pos, ns)
				}
			}
		}
	}
	// entitiesList == null on serverTick: the add is skipped.
	// 5. front-of-hive position: d0 = HONEY_DELIVERED ? 0.0 : 0.55 + bbWidth/2; y -= bbHeight/2.
	d0 := 0.0
	if status != beeHoneyDelivered {
		d0 = 0.55 + bee.width/2.0
	}
	sx, sz := dirStepXZ(dir)
	bee.x = float64(pos.X) + 0.5 + d0*float64(sx)
	bee.y = float64(pos.Y) + 0.5 - bee.height/2.0
	bee.z = float64(pos.Z) + 0.5 + d0*float64(sz)
	// AABB() is derived on-demand from x/y/z/width/height (no stored box to recompute).
	// playSound(BEEHIVE_EXIT) + gameEvent: cite-deferred. addFreshEntity(e):
	owner := t.regionForEntity(bee)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(bee)
	return true
}

// legacyRandomFacade narrows the region RNG to releaseOccupant two draws (nextFloat + nextInt).
type legacyRandomFacade struct {
	r interface {
		NextFloat() float32
		NextIntN(int32) int32
	}
}

func (f *legacyRandomFacade) nextFloat() float32      { return f.r.NextFloat() }
func (f *legacyRandomFacade) nextInt(bound int) int32 { return f.r.NextIntN(int32(bound)) }

// dirStepXZ returns (getStepX, getStepZ) for a horizontal Direction. CITE Direction.getStepX/getStepZ.
func dirStepXZ(d block.Direction) (int, int) {
	switch d {
	case block.North:
		return 0, -1
	case block.South:
		return 0, 1
	case block.West:
		return -1, 0
	case block.East:
		return 1, 0
	default:
		return 0, 0
	}
}

// spawnHiveBee ports Occupant.createEntity for the Bee case: a native Bee with setNoGravity(true),
// setHivePos(pos), and the stored nectar flag, NOT yet added to the world (release step 5 positions + adds).
func (t *TickLoop) spawnHiveBee(pos pk.Position, occ beehiveOccupant) *Entity {
	b := NewEntity(t.idAlloc.AllocID(), entity.Bee, float64(pos.X), float64(pos.Y), float64(pos.Z))
	b.isBee = true
	initSpawnHealth(b)
	b.ai = newBeeAI()
	reseedMobAI(b.ai, b.id)
	// e.setNoGravity(true): the Entity has no gravity field yet (flying-bee movement is a cited follow-up);
	// a released bee is a flying mob whose observable release position is set below. CITE Entity.setNoGravity.
	hp := pos
	b.beeHivePos = &hp
	b.beeHasNectar = occ.hasNectar
	if occ.savedFlowerPos != nil {
		fp := *occ.savedFlowerPos
		b.beeSavedFlowerPos = &fp
	}
	return b
}

// resolveBeehive returns the tick-owned beehiveBE for pos, creating an EMPTY one on first access. nil when
// pos is not a beehive/bee_nest block (or world unloaded). Tick-owned (t.beehives, the t.bells twin).
func (t *TickLoop) resolveBeehive(pos pk.Position) *beehiveBE {
	if t.beehives == nil {
		t.beehives = make(map[pk.Position]*beehiveBE)
	}
	if b, ok := t.beehives[pos]; ok {
		return b
	}
	if t.world() == nil {
		return nil
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsBeehiveBlock(state) {
		return nil
	}
	b := &beehiveBE{}
	t.beehives[pos] = b
	return b
}

// tickBeehives ticks every live beehive/bee_nest once per tick (BeehiveBlockEntity.serverTick fan-out). A
// non-hive cell (broken/replaced) drops the BE. Nil world = no-op. Tick-owned. CITE BeehiveBlock.getTicker.
func (t *TickLoop) tickBeehives() {
	if len(t.beehives) == 0 {
		return
	}
	w := t.world()
	if w == nil {
		return
	}
	for pos, b := range t.beehives {
		state, ok := w.GetBlock(pos, dimMinY)
		if !ok || !block.IsBeehiveBlock(state) {
			delete(t.beehives, pos)
			continue
		}
		t.beehiveServerTick(pos, b)
	}
}
