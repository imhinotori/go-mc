package server

// ai_goals_cat.go — the cat comfort goals (MOB-NEUT-03), Go-native ports of the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar, CFR this session), unblocked by the SLEEP-01 player-sleep subsystem +
// the BlockTags runtime query (block_tags.go). Two goals + the shared MoveToBlockGoal base:
//
//   - net.minecraft.world.entity.animal.feline.Cat$CatRelaxOnOwnerGoal  -> kind="cat_relax_on_owner"
//       (@3) lie on the owner sleeping in bed; rolls the morning-gift on stop (never fires at the
//       vanilla default CAT_WAKING_UP_GIFT_CHANCE == 0.0f).
//   - net.minecraft.world.entity.ai.goal.CatLieOnBedGoal (extends MoveToBlockGoal) -> kind="cat_lie_on_bed"
//       (@5) walk to + lie on any #minecraft:beds block.
//
// Both set the synched IS_LYING (catLying) / RELAX_STATE_ONE (catRelaxStateOne) pose bits, broadcast to
// trackers so the client renders the comfort poses. All RNG draws from the mob's per-entity source
// (mobRandom(e) == e.ai.rng) EXCEPT the morning-gift chance, which draws the LEVEL rng (t.cur().
// levelRandom) exactly as the jar (level.getRandom().nextFloat()).

import (
	"math"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// setCatLying ports Cat.setLying(b) == entityData.set(IS_LYING, b): flip the server-side catLying and
// PUSH the DATA value to trackers (dirty-only, matching SynchedEntityData). Tick-owned.
//
//	[VERIFIED javap Cat.setLying: entityData.set(IS_LYING, value).]
func (t *TickLoop) setCatLying(e *Entity, lying bool) {
	if e.catLying == lying {
		return
	}
	e.catLying = lying
	t.broadcastToTrackers(e.id, encodeSetEntityDataByID(e.id, catLyingDataEntry(lying)))
}

// setCatRelaxStateOne ports Cat.setRelaxStateOne(b) == entityData.set(RELAX_STATE_ONE, b): flip the
// server-side catRelaxStateOne and PUSH it to trackers (dirty-only). Tick-owned.
//
//	[VERIFIED javap Cat.setRelaxStateOne: entityData.set(RELAX_STATE_ONE, value).]
func (t *TickLoop) setCatRelaxStateOne(e *Entity, relax bool) {
	if e.catRelaxStateOne == relax {
		return
	}
	e.catRelaxStateOne = relax
	t.broadcastToTrackers(e.id, encodeSetEntityDataByID(e.id, catRelaxDataEntry(relax)))
}

// setCatCollarColor ports Cat.setCollarColor(color) == entityData.set(DATA_COLLAR_COLOR, color.getId()):
// flip the server-side catCollarColor (the DyeColor id) and PUSH the new INT to trackers (dirty-only,
// matching SynchedEntityData — no broadcast when the value is unchanged). Tick-owned. The caller (the
// mobInteract dye branch) has already gated on color != current, so the guard here is belt-and-braces.
//
//	[VERIFIED CFR Cat.setCollarColor: entityData.set(DATA_COLLAR_COLOR, color.getId()).]
func (t *TickLoop) setCatCollarColor(e *Entity, colorID int) {
	if e.catCollarColor == colorID {
		return
	}
	e.catCollarColor = colorID
	t.broadcastToTrackers(e.id, encodeSetEntityDataByID(e.id, catCollarDataEntry(colorID)))
}

// blockPosOf floors a world position to its integer block coordinate (Entity.blockPosition() /
// BlockPos.containing). The same math the other goals use (int(math.Floor)).
func blockPosOf(x, y, z float64) pk.Position {
	return pk.Position{X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z))}
}

// isEmptyBlockAt ports LevelReader.isEmptyBlock(pos) == getBlockState(pos).isAir(). An unloaded column
// is treated as non-empty (false) so a bed-lie target never resolves into ungenerated space.
//
//	[VERIFIED javap CatLieOnBedGoal.isValidTarget: level.isEmptyBlock(pos.above()) == getBlockState.isAir.]
func (t *TickLoop) isEmptyBlockAt(pos pk.Position) bool {
	if t.world() == nil {
		return false
	}
	s, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return false
	}
	return block.IsAir(s)
}

// --- MoveToBlockGoal base (net.minecraft.world.entity.ai.goal.MoveToBlockGoal) --------------------
//
// The abstract goal that finds the nearest matching block in a ring scan, walks to it, and tracks
// whether it reached it (isReachedTarget). CatLieOnBedGoal extends it. Ported 1:1 (CFR this session).
// The abstract isValidTarget is supplied as a callback (validTarget) the concrete goal sets -- Go has no
// abstract-method dispatch, so the base holds the predicate.
//
//	[VERIFIED CFR MoveToBlockGoal: GIVE_UP_TICKS/STAY_TICKS 1200, INTERVAL_TICKS 200; canUse nextStartTick
//	 countdown then findNearestBlock; nextStartTick reducedTickDelay(200+nextInt(200)); start moveMobToBlock
//	 + tryTicks=0 + maxStayTicks = nextInt(nextInt(1200)+1200)+1200; canContinueToUse tryTicks in
//	 [-maxStayTicks,1200] && isValidTarget; tick ring-approach + reachedTarget; shouldRecalculatePath
//	 tryTicks%40==0; findNearestBlock the deterministic spiral. requiresUpdateEveryTick true.]
type moveToBlockGoal struct {
	baseGoal
	speedModifier       float64
	searchRange         int
	verticalSearchRange int
	verticalSearchStart int

	nextStartTick int
	tryTicks      int
	maxStayTicks  int
	blockPos      pk.Position
	reachedTarget bool

	// validTarget is the concrete goal's isValidTarget(level,pos) (the abstract method). Never nil.
	validTarget func(t *TickLoop, pos pk.Position) bool
	// startHook / stopHook / tickHook are the concrete goal's start()/stop()/tick() extensions run
	// around the base behavior (CatLieOnBedGoal overrides all three). nil == no extension.
	startHook func(t *TickLoop, e *Entity)
	stopHook  func(t *TickLoop, e *Entity)
	tickHook  func(t *TickLoop, e *Entity)
	// canUseHook is the concrete goal's extra canUse gate ANDed BEFORE super.canUse (CatLieOnBedGoal:
	// isTame && !isOrderedToSit && !isLying). nil == no extra gate (bare super.canUse).
	canUseHook func(t *TickLoop, e *Entity) bool
	// nextStartTickFn overrides nextStartTick(mob) (CatLieOnBedGoal returns a constant 40). nil == the
	// base reducedTickDelay(200 + nextInt(200)).
	nextStartTickFn func(t *TickLoop, e *Entity) int
}

// newMoveToBlockGoal builds the base with the MoveToBlockGoal ctor: flags {MOVE, JUMP}, blockPos = ZERO,
// verticalSearchStart = 0 (a concrete goal may override it, e.g. CatLieOnBed sets -2). Mirrors the
// 4-arg ctor MoveToBlockGoal(mob, speedModifier, searchRange, verticalSearchRange).
//
//	[VERIFIED CFR MoveToBlockGoal.<init>: setFlags(EnumSet.of(MOVE, JUMP)); verticalSearchStart=0.]
func newMoveToBlockGoal(speedModifier float64, searchRange, verticalSearchRange int) moveToBlockGoal {
	return moveToBlockGoal{
		baseGoal:            newBaseGoal(flagMove | flagJump),
		speedModifier:       speedModifier,
		searchRange:         searchRange,
		verticalSearchRange: verticalSearchRange,
	}
}

func (g *moveToBlockGoal) requiresUpdateEveryTick() bool { return true }

// nextStartTickValue ports MoveToBlockGoal.nextStartTick(mob) = reducedTickDelay(200 + nextInt(200)),
// unless the concrete goal overrides it (CatLieOnBed -> 40, no draw).
func (g *moveToBlockGoal) nextStartTickValue(t *TickLoop, e *Entity) int {
	if g.nextStartTickFn != nil {
		return g.nextStartTickFn(t, e)
	}
	return reducedTickDelay(200 + mobRandom(e).nextInt(200))
}

// canUse ports MoveToBlockGoal.canUse (with the concrete canUseHook ANDed first, matching CatLieOnBed's
// isTame && !isOrderedToSit && !isLying && super.canUse()): if nextStartTick > 0 decrement + false; else
// nextStartTick = nextStartTick(mob) and return findNearestBlock().
//
//	[VERIFIED CFR MoveToBlockGoal.canUse + CatLieOnBedGoal.canUse.]
func (g *moveToBlockGoal) canUse(t *TickLoop, e *Entity) bool {
	if g.canUseHook != nil && !g.canUseHook(t, e) {
		return false
	}
	if g.nextStartTick > 0 {
		g.nextStartTick--
		return false
	}
	g.nextStartTick = g.nextStartTickValue(t, e)
	return g.findNearestBlock(t, e)
}

// canContinueToUse ports MoveToBlockGoal.canContinueToUse: tryTicks in [-maxStayTicks, 1200] &&
// isValidTarget(level, blockPos). CatLieOnBedGoal does NOT override it.
//
//	[VERIFIED CFR MoveToBlockGoal.canContinueToUse: tryTicks >= -maxStayTicks && tryTicks <= 1200 &&
//	 isValidTarget(mob.level(), blockPos).]
func (g *moveToBlockGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	return g.tryTicks >= -g.maxStayTicks && g.tryTicks <= 1200 && g.validTarget(t, g.blockPos)
}

// start ports MoveToBlockGoal.start: moveMobToBlock(); tryTicks = 0; maxStayTicks =
// nextInt(nextInt(1200)+1200)+1200 (INNER nextInt drawn first). Then the concrete startHook.
//
//	[VERIFIED CFR MoveToBlockGoal.start.]
func (g *moveToBlockGoal) start(t *TickLoop, e *Entity) {
	g.moveMobToBlock(e)
	g.tryTicks = 0
	inner := mobRandom(e).nextInt(1200) + 1200
	g.maxStayTicks = mobRandom(e).nextInt(inner) + 1200
	if g.startHook != nil {
		g.startHook(t, e)
	}
}

// stop is a no-op at the base (MoveToBlockGoal has no stop() override); the concrete stopHook runs.
func (g *moveToBlockGoal) stop(t *TickLoop, e *Entity) {
	if g.stopHook != nil {
		g.stopHook(t, e)
	}
}

// moveMobToBlock ports MoveToBlockGoal.moveMobToBlock: navigation.moveTo(blockPos.x+0.5, blockPos.y+1,
// blockPos.z+0.5, speedModifier). SET the want (the goal sets, the async nav steps the mob).
//
//	[VERIFIED CFR MoveToBlockGoal.moveMobToBlock.]
func (g *moveToBlockGoal) moveMobToBlock(e *Entity) {
	if e.ai != nil {
		e.ai.setWantTargetSpeed(float64(g.blockPos.X)+0.5, float64(g.blockPos.Y+1), float64(g.blockPos.Z)+0.5, g.speedModifier)
	}
}

// getMoveToTarget ports MoveToBlockGoal.getMoveToTarget() = blockPos.above().
func (g *moveToBlockGoal) getMoveToTarget() pk.Position {
	return pk.Position{X: g.blockPos.X, Y: g.blockPos.Y + 1, Z: g.blockPos.Z}
}

// tick ports MoveToBlockGoal.tick: if the moveToTarget is NOT within acceptedDistance (1.0) of the mob:
// reachedTarget=false, ++tryTicks, and (every 40 ticks) re-issue navigation.moveTo(moveToTarget,
// speedModifier). Else reachedTarget=true, --tryTicks. Then the concrete tickHook. acceptedDistance ==
// 1.0; closerToCenterThan compares to the block CENTER (x+0.5,y+0.5,z+0.5).
//
//	[VERIFIED CFR MoveToBlockGoal.tick + acceptedDistance()==1.0 + shouldRecalculatePath tryTicks%40==0.]
func (g *moveToBlockGoal) tick(t *TickLoop, e *Entity) {
	mt := g.getMoveToTarget()
	cx, cy, cz := float64(mt.X)+0.5, float64(mt.Y)+0.5, float64(mt.Z)+0.5
	dx, dy, dz := cx-e.x, cy-e.y, cz-e.z
	if dx*dx+dy*dy+dz*dz > 1.0 { // !closerToCenterThan(pos, acceptedDistance()==1.0)
		g.reachedTarget = false
		g.tryTicks++
		if g.tryTicks%40 == 0 { // shouldRecalculatePath
			if e.ai != nil {
				e.ai.setWantTargetSpeed(float64(mt.X)+0.5, float64(mt.Y), float64(mt.Z)+0.5, g.speedModifier)
			}
		}
	} else {
		g.reachedTarget = true
		g.tryTicks--
	}
	if g.tickHook != nil {
		g.tickHook(t, e)
	}
}

func (g *moveToBlockGoal) isReachedTarget() bool { return g.reachedTarget }

// findNearestBlock ports MoveToBlockGoal.findNearestBlock: the deterministic ring/spiral scan (NO RNG)
// over y in [verticalSearchStart, verticalSearchRange], r in [0, searchRange), x/z in the alternating
// ring pattern; the first pos where isValidTarget(level, pos) holds is stored in blockPos. isWithinHome
// (the home-restriction guard) is a cited constant-TRUE for v1 (a cat has no home restriction), so it is
// omitted. Returns true if a block was found.
//
//	[VERIFIED CFR MoveToBlockGoal.findNearestBlock: the y/r/x/z spiral with pos.setWithOffset(mobPos, x,
//	 y-1, z); isWithinHome(pos) && isValidTarget(level, pos) -> blockPos=pos, return true.]
func (g *moveToBlockGoal) findNearestBlock(t *TickLoop, e *Entity) bool {
	horizontalSearch := g.searchRange
	verticalSearch := g.verticalSearchRange
	mob := blockPosOf(e.x, e.y, e.z)
	for y := g.verticalSearchStart; y <= verticalSearch; y = ternaryStep(y) {
		for r := 0; r < horizontalSearch; r++ {
			for x := 0; x <= r; x = ternaryStep(x) {
				var z int
				if x < r && x > -r {
					z = r
				} else {
					z = 0
				}
				for z <= r {
					pos := pk.Position{X: mob.X + x, Y: mob.Y + y - 1, Z: mob.Z + z}
					if g.validTarget(t, pos) { // isWithinHome cited constant-true (no home restriction)
						g.blockPos = pos
						return true
					}
					if z > 0 {
						z = -z
					} else {
						z = 1 - z
					}
				}
			}
		}
	}
	return false
}

// ternaryStep reproduces the jar's v = v>0 ? -v : 1-v alternation (the outward spiral index step, used
// for both the y and the x loop variables). Pinned so the loops read like the bytecode.
func ternaryStep(v int) int {
	if v > 0 {
		return -v
	}
	return 1 - v
}

// --- CatLieOnBedGoal (net.minecraft.world.entity.ai.goal.CatLieOnBedGoal) -------------------------
//
// Extends MoveToBlockGoal(cat, speed, searchRange=8, verticalSearchRange=6); verticalSearchStart=-2;
// flags {JUMP, MOVE}. Walk to + lie on any #minecraft:beds block whose space above is empty.
//
//	[VERIFIED CFR CatLieOnBedGoal: ctor super(cat, speed, searchRange, 6); verticalSearchStart=-2;
//	 setFlags(EnumSet.of(JUMP, MOVE)); canUse isTame && !isOrderedToSit && !isLying && super.canUse();
//	 start super.start()+setInSittingPose(false); nextStartTick 40; stop super.stop()+setLying(false);
//	 tick super.tick()+setInSittingPose(false)+(!isReachedTarget?setLying(false):!isLying?setLying(true));
//	 isValidTarget level.isEmptyBlock(pos.above()) && getBlockState(pos).is(BlockTags.BEDS).]
type catLieOnBedGoal struct {
	moveToBlockGoal
}

// newCatLieOnBedGoal ports CatLieOnBedGoal's ctor. Cat.registerGoals @5 passes speed 1.1 and
// searchRange 8 (bipush 8); the verticalSearchRange is the fixed 6 (super(cat,speed,searchRange,6)).
// It re-sets the flags to {JUMP, MOVE} (the ctor's setFlags, same set the base already carries but in
// the jar's order) and verticalSearchStart to -2. The base start/stop/tick/canUse/nextStartTick are
// composed via the hook fields.
//
//	[VERIFIED CFR CatLieOnBedGoal.<init>(cat, speedModifier, searchRange): super(cat, speedModifier,
//	 searchRange, 6); verticalSearchStart = -2; setFlags(EnumSet.of(JUMP, MOVE)). Cat.registerGoals @5:
//	 new CatLieOnBedGoal(this, 1.1, 8).]
func newCatLieOnBedGoal(speedModifier float64) *catLieOnBedGoal {
	g := &catLieOnBedGoal{moveToBlockGoal: newMoveToBlockGoal(speedModifier, catLieOnBedSearchRange, 6)}
	g.verticalSearchStart = -2
	g.gflags = flagJump | flagMove
	// isValidTarget(level, pos): level.isEmptyBlock(pos.above()) && getBlockState(pos).is(BlockTags.BEDS).
	g.validTarget = func(t *TickLoop, pos pk.Position) bool {
		if t.world() == nil {
			return false
		}
		above := pk.Position{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
		if !t.isEmptyBlockAt(above) {
			return false
		}
		s, ok := t.world().GetBlock(pos, dimMinY)
		if !ok {
			return false
		}
		return blockInTag(s, blockTagBeds)
	}
	// nextStartTick override = 40 (constant, NOT the base 200+nextInt(200)) -> NO draw.
	g.nextStartTickFn = func(_ *TickLoop, _ *Entity) int { return 40 }
	// canUse extra gate: isTame() && !isOrderedToSit() && !isLying() (ANDed BEFORE super.canUse).
	g.canUseHook = func(_ *TickLoop, e *Entity) bool {
		return e.tame && !e.orderedToSit && !e.catLying
	}
	// start: super.start(); setInSittingPose(false).
	g.startHook = func(t *TickLoop, e *Entity) {
		t.setCatInSittingPose(e, false)
	}
	// stop: super.stop(); setLying(false).
	g.stopHook = func(t *TickLoop, e *Entity) {
		t.setCatLying(e, false)
	}
	// tick: super.tick() already ran; setInSittingPose(false); if !isReachedTarget setLying(false) else
	// if !isLying setLying(true).
	g.tickHook = func(t *TickLoop, e *Entity) {
		t.setCatInSittingPose(e, false)
		if !g.isReachedTarget() {
			t.setCatLying(e, false)
		} else if !e.catLying {
			t.setCatLying(e, true)
		}
	}
	return g
}

// catLieOnBedSearchRange is Cat.registerGoals @5 CatLieOnBedGoal(this, 1.1, 8) searchRange arg (bipush 8).
//
//	[VERIFIED javap Cat.registerGoals @5: new CatLieOnBedGoal(this, 1.1, 8).]
const catLieOnBedSearchRange = 8

// setCatInSittingPose ports Cat.setInSittingPose via TamableAnimal (the DATA_FLAGS 0x1 sit bit) — the
// SAME broadcast the wolf sit uses (setWolfInSittingPose). A cat carries the same DATA_FLAGS accessor
// (index 18) as the wolf, so the sit-pose byte + broadcast are identical. Kept as a cat-named seam so
// the cat goals read intent-clearly.
//
//	[VERIFIED javap TamableAnimal.setInSittingPose(b): DATA_FLAGS bit 0x1.]
func (t *TickLoop) setCatInSittingPose(e *Entity, sitting bool) {
	t.setWolfInSittingPose(e, sitting)
}

var _ Goal = (*catLieOnBedGoal)(nil)

// --- CatSitOnBlockGoal (net.minecraft.world.entity.ai.goal.CatSitOnBlockGoal) ---------------------
//
// Extends MoveToBlockGoal(cat, speed, searchRange=8) -- the 3-arg ctor, which forwards to the 4-arg with
// verticalSearchRange=1 (iconst_1) and verticalSearchStart=0 (base default); flags stay {MOVE, JUMP}
// (base). Walk to + sit on a valid comfort block: an unopened CHEST, a LIT FURNACE, or the FOOT of a
// bed. Ported 1:1 (javap this session).
//
//	[VERIFIED javap CatSitOnBlockGoal: ctor super(cat, speed, bipush 8) -> MoveToBlockGoal 3-arg (which
//	 calls 4-arg with verticalSearchRange=iconst_1); canUse isTame() && !isOrderedToSit() && super.canUse();
//	 start super.start()+setInSittingPose(false); stop super.stop()+setInSittingPose(false);
//	 tick super.tick()+setInSittingPose(isReachedTarget()); isValidTarget level.isEmptyBlock(pos.above())
//	 && ( is(Blocks.CHEST) ? ChestBlockEntity.getOpenCount(level,pos) < 1
//	    : is(Blocks.FURNACE) && FurnaceBlock.LIT ? true
//	    : is(BlockTags.BEDS, s -> s.getOptionalValue(BedBlock.PART).map(v -> v != HEAD).orElse(true)) ).
//	 Cat.registerGoals @7: new CatSitOnBlockGoal(this, 0.8).]
type catSitOnBlockGoal struct {
	moveToBlockGoal
}

// newCatSitOnBlockGoal ports CatSitOnBlockGoal's ctor. Cat.registerGoals @7 passes speed 0.8 (ldc2_w
// 0.8d); the searchRange is the bipush 8, the verticalSearchRange the base 3-arg-ctor default 1,
// verticalSearchStart the base default 0. The base start/stop/tick/canUse are composed via the hook
// fields (Go has no super-method dispatch).
//
//	[VERIFIED javap CatSitOnBlockGoal.<init>(cat, speed): super(cat, speed, 8) (MoveToBlockGoal 3-arg).
//	 Cat.registerGoals @7: new CatSitOnBlockGoal(this, 0.8).]
func newCatSitOnBlockGoal(speedModifier float64) *catSitOnBlockGoal {
	g := &catSitOnBlockGoal{moveToBlockGoal: newMoveToBlockGoal(speedModifier, catSitOnBlockSearchRange, 1)}
	// isValidTarget(level, pos): isEmptyBlock(pos.above()) && ( CHEST&&openCount<1 || FURNACE&&LIT ||
	// BED&&part!=HEAD ). The three-way is the exact bytecode order (CHEST first, then FURNACE, then BEDS).
	g.validTarget = func(t *TickLoop, pos pk.Position) bool {
		if t.world() == nil {
			return false
		}
		above := pk.Position{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
		if !t.isEmptyBlockAt(above) { // level.isEmptyBlock(pos.above())
			return false
		}
		s, ok := t.world().GetBlock(pos, dimMinY)
		if !ok {
			return false
		}
		if isChestBlock(s) { // s.is(Blocks.CHEST) -> getOpenCount(level,pos) < 1
			return t.chestOpenCount(pos) < 1
		}
		if isFurnaceBlock(s) { // s.is(Blocks.FURNACE) && FurnaceBlock.LIT
			return furnaceLit(s)
		}
		// s.is(BlockTags.BEDS, s -> s.getOptionalValue(BedBlock.PART).map(v -> v != HEAD).orElse(true)).
		// The predicate accepts a bed whose PART is NOT the HEAD (i.e. the FOOT), or a bed with no PART
		// property (orElse(true)); a non-bed fails the tag membership first.
		if !blockInTag(s, blockTagBeds) {
			return false
		}
		if bp, okBed := readBed(s); okBed {
			return bp.part != block.BedPartHead
		}
		return true // in BEDS but no PART property -> orElse(true)
	}
	// canUse extra gate: isTame() && !isOrderedToSit() (ANDed BEFORE super.canUse). NO isLying gate (unlike
	// CatLieOnBedGoal -- CatSitOnBlockGoal.canUse omits it).
	g.canUseHook = func(_ *TickLoop, e *Entity) bool {
		return e.tame && !e.orderedToSit
	}
	// start: super.start(); setInSittingPose(false).
	g.startHook = func(t *TickLoop, e *Entity) {
		t.setCatInSittingPose(e, false)
	}
	// stop: super.stop(); setInSittingPose(false).
	g.stopHook = func(t *TickLoop, e *Entity) {
		t.setCatInSittingPose(e, false)
	}
	// tick: super.tick() already ran; setInSittingPose(isReachedTarget()).
	g.tickHook = func(t *TickLoop, e *Entity) {
		t.setCatInSittingPose(e, g.isReachedTarget())
	}
	return g
}

// catSitOnBlockSearchRange is Cat.registerGoals @7 CatSitOnBlockGoal(this, 0.8) -> MoveToBlockGoal(cat,
// 0.8, 8) searchRange arg (bipush 8).
//
//	[VERIFIED javap CatSitOnBlockGoal.<init>: super(cat, speed, bipush 8).]
const catSitOnBlockSearchRange = 8

var _ Goal = (*catSitOnBlockGoal)(nil)

// --- CatRelaxOnOwnerGoal (net.minecraft.world.entity.animal.feline.Cat$CatRelaxOnOwnerGoal) --------
//
// The cat lies on its owner who is sleeping in a bed; on stop it rolls the morning-gift (never fires at
// the vanilla default CAT_WAKING_UP_GIFT_CHANCE == 0.0f). Ported 1:1 (CFR this session).
//
//	[VERIFIED CFR Cat$CatRelaxOnOwnerGoal: fields cat/ownerPlayer/goalPos/onBedTicks; canUse the tame +
//	 !sit + owner-instanceof-Player + owner.isSleeping + distanceToSqr<=100 + BlockTags.BEDS at ownerPos
//	 + !spaceIsOccupied chain; goalPos = ownerPos.relative(FACING.getOpposite()) (or ownerPos);
//	 spaceIsOccupied getEntitiesOfClass(Cat, AABB(goalPos).inflate(2.0)) other cat lying||relaxStateOne;
//	 start setInSittingPose(false)+nav.moveTo(goalPos,1.1); canContinueToUse tame+!sit+owner!=null+
//	 owner.isSleeping+goalPos!=null+!spaceIsOccupied; stop setLying(false)+morning-gift roll+onBedTicks=0+
//	 setRelaxStateOne(false)+nav.stop; tick setInSittingPose(false)+nav.moveTo(goalPos,1.1); if
//	 distanceToSqr(owner)<2.5 { ++onBedTicks; if >adjustedTickDelay(16) setLying(true)+setRelaxStateOne(
//	 false) else lookAt(owner)+setRelaxStateOne(true) } else setLying(false).]
type catRelaxOnOwnerGoal struct {
	baseGoal
	ownerID int32        // ownerPlayer (0 == none this run)
	goalPos *pk.Position // the bed-adjacent stand spot (nil == none)
}

// newCatRelaxOnOwnerGoal builds the goal. CatRelaxOnOwnerGoal has NO explicit setFlags in its ctor, so
// it carries the empty flag set (Goal default) — it claims no MOVE/JUMP/LOOK/TARGET control (it drives
// the navigation directly in tick without reserving the MOVE flag, matching the jar). Cat.registerGoals
// @3 passes only the cat (new CatRelaxOnOwnerGoal(this)).
//
//	[VERIFIED CFR Cat$CatRelaxOnOwnerGoal.<init>(Cat cat): this.cat = cat; (no setFlags). javap
//	 Cat.registerGoals @3: new CatRelaxOnOwnerGoal(this).]
func newCatRelaxOnOwnerGoal() *catRelaxOnOwnerGoal {
	return &catRelaxOnOwnerGoal{baseGoal: newBaseGoal(0)}
}

// catRelaxOnOwnerDistSqr is CatRelaxOnOwnerGoal.canUse's distanceToSqr(owner) > 100.0 gate (10 blocks).
//
//	[VERIFIED CFR: if (this.cat.distanceToSqr(this.ownerPlayer) > 100.0) return false.]
const catRelaxOnOwnerDistSqr = 100.0

// catRelaxOnBedNearSqr is the tick's distanceToSqr(owner) < 2.5 gate (the "close enough to settle").
//
//	[VERIFIED CFR: if (this.cat.distanceToSqr(this.ownerPlayer) < 2.5) { ++onBedTicks; ... }.]
const catRelaxOnBedNearSqr = 2.5

// catRelaxOnBedSettleTicks is adjustedTickDelay(16) == 16 (identity in the full-rate driver): once
// onBedTicks exceeds it, the cat flips from relaxStateOne to lying.
//
//	[VERIFIED CFR: if (this.onBedTicks > this.adjustedTickDelay(16)) setLying(true) else lookAt+relax.]
const catRelaxOnBedSettleTicks = 16

// canUse ports Cat$CatRelaxOnOwnerGoal.canUse (CFR), NO RNG:
//
//	if (!isTame()) return false;
//	if (isOrderedToSit()) return false;
//	LivingEntity owner = getOwner();
//	if (owner instanceof Player playerOwner) {
//	    this.ownerPlayer = playerOwner;
//	    if (!owner.isSleeping()) return false;
//	    if (distanceToSqr(ownerPlayer) > 100.0) return false;
//	    BlockPos ownerPos = ownerPlayer.blockPosition();
//	    BlockState s = level.getBlockState(ownerPos);
//	    if (s.is(BlockTags.BEDS)) {
//	        this.goalPos = s.getOptionalValue(BedBlock.FACING).map(d -> ownerPos.relative(d.getOpposite()))
//	                        .orElseGet(() -> new BlockPos(ownerPos));
//	        return !spaceIsOccupied();
//	    }
//	}
//	return false;
func (g *catRelaxOnOwnerGoal) canUse(t *TickLoop, e *Entity) bool {
	if !e.tame {
		return false
	}
	if e.orderedToSit {
		return false
	}
	owner := t.playerByEntityID(e.ownerUUID) // getOwner() (v1 owner is a Player)
	if owner == nil {
		return false
	}
	g.ownerID = owner.entityID
	if !owner.isSleeping() {
		return false
	}
	dx, dy, dz := owner.x-e.x, owner.y-e.y, owner.z-e.z
	if dx*dx+dy*dy+dz*dz > catRelaxOnOwnerDistSqr {
		return false
	}
	ownerPos := blockPosOf(owner.x, owner.y, owner.z)
	if t.world() == nil {
		return false
	}
	s, ok := t.world().GetBlock(ownerPos, dimMinY)
	if !ok || !blockInTag(s, blockTagBeds) {
		return false
	}
	// goalPos = ownerPos.relative(FACING.getOpposite()) if the bed has a FACING, else ownerPos.
	gp := ownerPos
	if bp, okBed := readBed(s); okBed {
		if dx, dz, okDir := bedFacingDelta(bedOpposite(bp.facing)); okDir {
			gp = pk.Position{X: ownerPos.X + dx, Y: ownerPos.Y, Z: ownerPos.Z + dz}
		}
	}
	g.goalPos = &gp
	return !g.spaceIsOccupied(t, e)
}

// bedOpposite returns the opposite horizontal direction (Direction.getOpposite() for the 4 bed facings).
//
//	[VERIFIED: Direction.getOpposite(): NORTH<->SOUTH, EAST<->WEST.]
func bedOpposite(d block.Direction) block.Direction {
	switch d {
	case block.North:
		return block.South
	case block.South:
		return block.North
	case block.West:
		return block.East
	case block.East:
		return block.West
	default:
		return d
	}
}

// spaceIsOccupied ports Cat$CatRelaxOnOwnerGoal.spaceIsOccupied: getEntitiesOfClass(Cat,
// AABB(goalPos).inflate(2.0)); occupied iff any OTHER cat isLying() || isRelaxStateOne(). The scan uses
// the owning-region store (t.cur().entities.near — the same broad-phase the breed/follow goals use), a
// 3-chunk radius safely covering the inflate(2.0) box; the exact AABB(goalPos).inflate(2.0) membership
// is re-checked per candidate.
//
//	[VERIFIED CFR spaceIsOccupied: for each cat in getEntitiesOfClass(Cat, AABB(goalPos).inflate(2.0)):
//	 if (otherCat == this.cat || (!otherCat.isLying() && !otherCat.isRelaxStateOne())) continue; return true.]
func (g *catRelaxOnOwnerGoal) spaceIsOccupied(t *TickLoop, e *Entity) bool {
	if g.goalPos == nil {
		return false
	}
	// AABB(goalPos).inflate(2.0): a unit box at goalPos grown by 2 on every side.
	minX, minY, minZ := float64(g.goalPos.X)-2.0, float64(g.goalPos.Y)-2.0, float64(g.goalPos.Z)-2.0
	maxX, maxY, maxZ := float64(g.goalPos.X)+1.0+2.0, float64(g.goalPos.Y)+1.0+2.0, float64(g.goalPos.Z)+1.0+2.0
	for _, other := range t.cur().entities.near(float64(g.goalPos.X), float64(g.goalPos.Z), 1) {
		if other == e || other.typ != e.typ { // == this.cat, and only Cats
			continue
		}
		if other.x < minX || other.x > maxX || other.y < minY || other.y > maxY || other.z < minZ || other.z > maxZ {
			continue // outside the inflated AABB
		}
		if !other.catLying && !other.catRelaxStateOne {
			continue
		}
		return true
	}
	return false
}

// canContinueToUse ports Cat$CatRelaxOnOwnerGoal.canContinueToUse: isTame() && !isOrderedToSit() &&
// ownerPlayer != null && ownerPlayer.isSleeping() && goalPos != null && !spaceIsOccupied().
//
//	[VERIFIED CFR canContinueToUse.]
func (g *catRelaxOnOwnerGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	if !e.tame || e.orderedToSit || g.goalPos == nil {
		return false
	}
	owner := t.playerByEntityID(e.ownerUUID)
	if owner == nil || !owner.isSleeping() {
		return false
	}
	return !g.spaceIsOccupied(t, e)
}

// start ports Cat$CatRelaxOnOwnerGoal.start: if goalPos != null { setInSittingPose(false);
// nav.moveTo(goalPos, 1.1f) }.
//
//	[VERIFIED CFR start: setInSittingPose(false); getNavigation().moveTo(goalPos.x,y,z, 1.1f).]
func (g *catRelaxOnOwnerGoal) start(t *TickLoop, e *Entity) {
	if g.goalPos == nil {
		return
	}
	t.setCatInSittingPose(e, false)
	if e.ai != nil {
		e.ai.setWantTargetSpeed(float64(g.goalPos.X), float64(g.goalPos.Y), float64(g.goalPos.Z), catRelaxMoveSpeed)
	}
}

// catRelaxMoveSpeed is the 1.1f nav.moveTo speed CatRelaxOnOwnerGoal.start/tick pass.
//
//	[VERIFIED CFR: getNavigation().moveTo(goalPos..., 1.1f).]
const catRelaxMoveSpeed = 1.1

// stop ports Cat$CatRelaxOnOwnerGoal.stop: setLying(false); THEN the morning-gift roll (below);
// onBedTicks = 0; setRelaxStateOne(false); nav.stop().
//
//	[VERIFIED CFR stop: setLying(false); if (ownerPlayer.getSleepTimer() >= 100 && level.getRandom().
//	 nextFloat() < CAT_WAKING_UP_GIFT_CHANCE) giveMorningGift(); onBedTicks = 0; setRelaxStateOne(false);
//	 getNavigation().stop().]
func (g *catRelaxOnOwnerGoal) stop(t *TickLoop, e *Entity) {
	t.setCatLying(e, false)
	// The morning-gift gate. owner.getSleepTimer() >= 100 && level.getRandom().nextFloat() <
	// CAT_WAKING_UP_GIFT_CHANCE. JAR FINDING: CAT_WAKING_UP_GIFT_CHANCE defaultValue == 0.0f, so the
	// comparison nextFloat() < 0.0f is ALWAYS false -> giveMorningGift() is NEVER reached at the vanilla
	// default (it is a datapack/dimension override knob). The nextFloat() draw is STILL taken (on the
	// LEVEL rng, t.cur().levelRandom == level.getRandom()) for RNG lockstep, exactly as the jar draws it
	// before the always-false compare. Because the branch is unreachable at the default, giveMorningGift
	// (randomTeleport + the CAT_MORNING_GIFT loot table drop) is a cited unreachable follow-up — it needs
	// the randomTeleport + loot-table subsystems, but at the vanilla chance it never runs.
	if owner := t.playerByEntityID(e.ownerUUID); owner != nil && owner.getSleepTimer() >= sleepDuration {
		if t.catWakingUpGiftRoll() < catWakingUpGiftChance {
			t.giveMorningGift(e) // UNREACHABLE at the default chance (0.0f); cited follow-up when > 0.
		}
	}
	e.catOnBedTicks = 0
	t.setCatRelaxStateOne(e, false)
	if e.ai != nil {
		e.ai.clearWantTarget() // navigation.stop()
	}
}

// catWakingUpGiftChance is EnvironmentAttributes.CAT_WAKING_UP_GIFT_CHANCE.defaultValue == 0.0f (26.2,
// verified). At this default the morning-gift never fires (nextFloat() < 0.0f is always false). Pinned
// as the cited constant that equals the vanilla default, structured so a future environment-attribute
// read replaces it (never baked away).
//
//	[VERIFIED CFR EnvironmentAttributes: CAT_WAKING_UP_GIFT_CHANCE = ...defaultValue(0.0f).]
const catWakingUpGiftChance float32 = 0.0

// catWakingUpGiftRoll draws the LEVEL rng nextFloat() (level.getRandom().nextFloat()) — the SAME draw
// the jar takes before the always-false compare. Kept as a draw (not skipped) so the level-rng stream
// stays in lockstep. A nil region random (a bare test loop) yields 0.0 (no draw), which still compares
// >= 0.0 -> false, so the gift stays unreachable.
func (t *TickLoop) catWakingUpGiftRoll() float32 {
	if t.cur() == nil || t.cur().levelRandom == nil {
		return 0.0
	}
	return t.cur().levelRandom.NextFloat()
}

// giveMorningGift is the cited UNREACHABLE-at-default follow-up (Cat$CatRelaxOnOwnerGoal.giveMorningGift):
// randomTeleport(x+nextInt(11)-5, y+nextInt(5)-2, z+nextInt(11)-5, false) then dropFromGiftLootTable(
// CAT_MORNING_GIFT). It is NEVER called at the vanilla default CAT_WAKING_UP_GIFT_CHANCE (0.0f) because
// the stop() gate (nextFloat() < 0.0f) is always false. It BLOCKS on the randomTeleport + CAT_MORNING_GIFT
// loot-table subsystems, absent in v1. Present as the cited stub so the branch is structurally complete
// and the upgrade slots in here when the chance is overridden > 0.
//
//	[VERIFIED CFR giveMorningGift: cat.getRandom() teleport offsets + dropFromGiftLootTable(
//	 BuiltInLootTables.CAT_MORNING_GIFT).]
func (t *TickLoop) giveMorningGift(_ *Entity) {
	// Unreachable at the vanilla default; the randomTeleport + loot-table drop land with those subsystems.
}

// tick ports Cat$CatRelaxOnOwnerGoal.tick: if ownerPlayer != null && goalPos != null {
// setInSittingPose(false); nav.moveTo(goalPos, 1.1f); if distanceToSqr(owner) < 2.5 { ++onBedTicks; if
// onBedTicks > adjustedTickDelay(16) { setLying(true); setRelaxStateOne(false) } else { lookAt(owner,
// 45,45); setRelaxStateOne(true) } } else setLying(false) }.  NO RNG.
//
//	[VERIFIED CFR tick.]
func (g *catRelaxOnOwnerGoal) tick(t *TickLoop, e *Entity) {
	if g.goalPos == nil {
		return
	}
	owner := t.playerByEntityID(e.ownerUUID)
	if owner == nil {
		return
	}
	t.setCatInSittingPose(e, false)
	if e.ai != nil {
		e.ai.setWantTargetSpeed(float64(g.goalPos.X), float64(g.goalPos.Y), float64(g.goalPos.Z), catRelaxMoveSpeed)
	}
	dx, dy, dz := owner.x-e.x, owner.y-e.y, owner.z-e.z
	if dx*dx+dy*dy+dz*dz < catRelaxOnBedNearSqr {
		e.catOnBedTicks++
		if e.catOnBedTicks > catRelaxOnBedSettleTicks {
			t.setCatLying(e, true)
			t.setCatRelaxStateOne(e, false)
		} else {
			// lookAt(owner, 45, 45): face the owner (headYaw, the same posture the look/follow goals use).
			e.headYaw = yawTowardDeg(owner.x-e.x, owner.z-e.z)
			t.setCatRelaxStateOne(e, true)
		}
	} else {
		t.setCatLying(e, false)
	}
}

var _ Goal = (*catRelaxOnOwnerGoal)(nil)
