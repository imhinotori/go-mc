package server

// ai_goals_silverfish.go — MOB-HOST-05 (infest goals): the two Silverfish inner-class goals, ported 1:1
// from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p this session):
//
//   - net.minecraft.world.entity.monster.Silverfish$SilverfishMergeWithStoneGoal (extends
//     RandomStrollGoal(this, 1.0, 10)) — kind "silverfish_merge_stone" {MOVE}. canUse: null-target +
//     navigation.isDone gates, then (mobGriefing && nextInt(reducedTickDelay(10))==0) pick a random
//     Direction, and if the block at BlockPos.containing(x, y+0.5, z).relative(dir) isCompatibleHostBlock
//     set doMerge=true → return true; else doMerge=false → super.canUse() (bare RandomStroll). start():
//     if !doMerge → super.start(); else convert the block via infestedStateByHost + spawnAnim + discard.
//   - net.minecraft.world.entity.monster.Silverfish$SilverfishWakeUpFriendsGoal (extends Goal, NO flags) —
//     kind "silverfish_wake_friends" {}. notifyHurt() arms lookForFriends = adjustedTickDelay(20) (once).
//     canUse: lookForFriends > 0. tick: --lookForFriends; at <= 0 do the ±5(y)/±10(x)/±10(z) spiral search
//     — the FIRST InfestedBlock found is destroyed (mobGriefing) or de-infested (hostStateByInfested), then
//     if nextBoolean() STOP (only one wake per hurt), else keep spiralling.
//
// The wake goal's counter lives on mobAI (silverfishLookForFriends) — the per-mob analogue of the goal's
// `int lookForFriends` field — so the hurt-path hook (silverfishNotifyHurt, combat_mob.go) can arm it
// without reaching the goal instance (the same per-mob-state pattern the other native goals use). The
// merge goal reuses the shared DefaultRandomPos stroll machinery (generateRandomDirection + the runtime
// snapStrollWant) for its bare-RandomStroll fallback. Both goals draw ONLY from the per-mob rng
// (mobRandom(e)), and are silverfish-gated (only the vanilla_silverfish decl declares them), so every
// non-silverfish mob — including the pig oracle — gains ZERO new RNG draws.

import (
	"math"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// silverfishDirectionCount is Direction.values().length (6): Direction.getRandom(r) == VALUES[nextInt(6)]
// in ordinal order DOWN(0),UP(1),NORTH(2),SOUTH(3),WEST(4),EAST(5) — the SAME indexing directionNormal
// (block_interact.go) uses. Cite Direction.getRandom == Util.getRandom(VALUES, r).
const silverfishDirectionCount = 6

// --- SilverfishMergeWithStoneGoal ----------------------------------------------------------------

// silverfishMergeGateInterval is the RandomStrollGoal interval the merge goal passes to its super ctor
// (RandomStrollGoal(this, 1.0, 10)) — used BOTH by the merge RNG gate (nextInt(reducedTickDelay(10)))
// AND the bare-RandomStroll fallback (nextInt(reducedTickDelay(10))). Cite Silverfish$SilverfishMergeWithStoneGoal
// ctor (dconst_1 speed, bipush 10 interval).
const silverfishMergeGateInterval = 10

// silverfishMergeSpeed is the RandomStrollGoal speedModifier the merge goal passes (1.0). The v1 nav
// consumes the mob's walk pace (declaredWalkSpeed) for the fallback stroll; 1.0 is the vanilla want
// multiplier (cited-deferred like the other stroll speedModifiers). Cite the ctor (dconst_1).
const silverfishMergeSpeed = 1.0

// silverfishMergeStoneGoal ports Silverfish$SilverfishMergeWithStoneGoal (extends RandomStrollGoal),
// flags {MOVE}. doMerge is the per-run flag canUse sets and start() reads; mergePos is the host block
// canUse selected (BlockPos.containing(x, y+0.5, z).relative(selectedDirection)) that start() converts.
type silverfishMergeStoneGoal struct {
	baseGoal
	speedModifier float64
	doMerge       bool
	mergePos      pk.Position
	// wantCandidates is the bare-RandomStroll (DefaultRandomPos.getPos(mob,10,7)) fallback carrier — the
	// 10 raw candidate offsets canUse emits when the merge condition does not hold; start() hands them to
	// the runtime snap (setWantCandidates, DefaultRandomPos mode). Mirrors randomStrollGoal.wantCandidates.
	wantCandidates [][3]float64
}

// newSilverfishMergeStoneGoal builds the merge goal (kind "silverfish_merge_stone"). speed routes from
// the declared movement_speed (declaredWalkSpeed) exactly like the melee goal — the fallback stroll
// pace. Cite Silverfish.registerGoals @5 SilverfishMergeWithStoneGoal.
func newSilverfishMergeStoneGoal(speed float64) *silverfishMergeStoneGoal {
	return &silverfishMergeStoneGoal{
		baseGoal:      newBaseGoal(flagMove),
		speedModifier: speed,
	}
}

// canUse ports SilverfishMergeWithStoneGoal.canUse (javap-verified this session):
//
//	if (mob.getTarget() != null) return false;                        // a hunting silverfish never merges
//	if (!mob.getNavigation().isDone()) return false;                  // must be idle (no active path)
//	RandomSource r = mob.getRandom();
//	if (mobGriefing && r.nextInt(reducedTickDelay(10)) == 0) {        // reducedTickDelay(10) == 5
//	    selectedDirection = Direction.getRandom(r);                   // VALUES[r.nextInt(6)]
//	    BlockPos p = BlockPos.containing(x, y+0.5, z).relative(dir);
//	    if (InfestedBlock.isCompatibleHostBlock(level.getBlockState(p))) { doMerge = true; return true; }
//	}
//	doMerge = false;
//	return super.canUse();                                            // bare RandomStrollGoal.canUse
//
// The mob.hasControllingPassenger()/getNoActionTime() gates of the super canUse are v1 no-ops (no rider,
// no noActionTime counter — a documented faithful-scope omission, like the other stroll ports). Cite
// Silverfish$SilverfishMergeWithStoneGoal.canUse.
func (g *silverfishMergeStoneGoal) canUse(t *TickLoop, e *Entity) bool {
	if e.ai == nil {
		return false
	}
	// mob.getTarget() != null → return false.
	if e.ai.getTarget() != 0 {
		return false
	}
	// !mob.getNavigation().isDone() → return false. isDone() == !hasTarget (the v1 nav "done" analog,
	// mirroring PanicGoal/FollowOwnerGoal.canContinueToUse).
	if e.ai.hasTarget {
		return false
	}
	r := mobRandom(e)
	// mobGriefing && nextInt(reducedTickDelay(10)) == 0 — the merge attempt gate.
	if silverfishMobGriefing && r.nextInt(reducedTickDelay(silverfishMergeGateInterval)) == 0 {
		// selectedDirection = Direction.getRandom(r) == VALUES[nextInt(6)] (DOWN,UP,NORTH,SOUTH,WEST,EAST).
		dir := r.nextInt(silverfishDirectionCount)
		dx, dy, dz := directionNormal(dir)
		// BlockPos.containing(getX(), getY()+0.5, getZ()).relative(selectedDirection).
		p := pk.Position{
			X: int(math.Floor(e.x)) + dx,
			Y: int(math.Floor(e.y+0.5)) + dy,
			Z: int(math.Floor(e.z)) + dz,
		}
		if t.world() != nil {
			state, _ := t.world().GetBlock(p, dimMinY)
			if isCompatibleHostBlock(state) {
				g.doMerge = true
				g.mergePos = p
				return true
			}
		}
	}
	// doMerge = false; return super.canUse() — the bare RandomStrollGoal path (DefaultRandomPos.getPos).
	g.doMerge = false
	return g.superCanUse(e, r)
}

// superCanUse ports bare RandomStrollGoal.canUse's fallback tail (the merge goal's super): roll
// nextInt(reducedTickDelay(interval)); if it hits, take DefaultRandomPos.getPos(mob,10,7) (10×
// generateRandomDirection, x/y/z order, NO probability nextFloat — that is water-avoiding only) and
// stash the 10 raw candidates. The mob.getRandom() passed in is the SAME source the merge gate already
// drew from (the jar reuses `random`), so the draw stream stays contiguous. Cite RandomStrollGoal.canUse
// + RandomStrollGoal.getPosition (DefaultRandomPos.getPos(mob, 10, 7)).
func (g *silverfishMergeStoneGoal) superCanUse(e *Entity, r *entityRandom) bool {
	// nextInt(reducedTickDelay(interval)) != 0 → return false (the 1-in-5 stroll gate).
	if r.nextInt(reducedTickDelay(silverfishMergeGateInterval)) != 0 {
		return false
	}
	// DefaultRandomPos.getPos(mob, 10, 7): for i<10, generateRandomDirection(r, 10, 7) → candidate at
	// (x+xt, y+yt, z+zt). NO probability nextFloat (bare RandomStrollGoal, not water-avoiding).
	cands := make([][3]float64, 0, 10)
	for i := 0; i < 10; i++ {
		xt, yt, zt := generateRandomDirection(r, strollHorizontalRadius, strollVerticalRadius)
		cands = append(cands, [3]float64{e.x + float64(xt), e.y + float64(yt), e.z + float64(zt)})
	}
	g.wantCandidates = cands
	return true
}

// canContinueToUse ports SilverfishMergeWithStoneGoal.canContinueToUse:
//
//	if (doMerge) return false;                        // a merging silverfish is one-shot (start() discards it)
//	return super.canContinueToUse();                 // == !navigation.isDone() (the fallback stroll continues)
//
// Cite Silverfish$SilverfishMergeWithStoneGoal.canContinueToUse.
func (g *silverfishMergeStoneGoal) canContinueToUse(_ *TickLoop, e *Entity) bool {
	if g.doMerge {
		return false
	}
	if e.ai == nil {
		return false
	}
	return e.ai.hasTarget // !navigation.isDone()
}

// start ports SilverfishMergeWithStoneGoal.start (javap-verified this session):
//
//	if (!doMerge) { super.start(); return; }         // bare RandomStroll: navigation.moveTo(want, speed)
//	Level level = mob.level();
//	BlockPos p = BlockPos.containing(x, y+0.5, z).relative(selectedDirection);
//	BlockState s = level.getBlockState(p);
//	if (InfestedBlock.isCompatibleHostBlock(s)) {
//	    level.setBlock(p, InfestedBlock.infestedStateByHost(s), 3);
//	    mob.spawnAnim();
//	    mob.discard();
//	}
//
// Cite Silverfish$SilverfishMergeWithStoneGoal.start.
func (g *silverfishMergeStoneGoal) start(t *TickLoop, e *Entity) {
	if !g.doMerge {
		// super.start(): navigation.moveTo(wantedX/Y/Z, speedModifier). Hand the raw candidates to the
		// runtime snap (DefaultRandomPos mode → wantLandMode=false: no up-snap, no water reject).
		if e.ai != nil && len(g.wantCandidates) == 10 {
			var arr [10][3]float64
			copy(arr[:], g.wantCandidates)
			e.ai.setWantCandidates(arr, false, g.speedModifier) // RandomStrollGoal speedModifier 1.0 (seam x MOVEMENT_SPEED)
		}
		return
	}
	if t.world() == nil {
		return
	}
	// Re-read + re-check the host block (the start() re-does isCompatibleHostBlock over the same pos).
	state, _ := t.world().GetBlock(g.mergePos, dimMinY)
	if !isCompatibleHostBlock(state) {
		return
	}
	infested, ok := infestedStateByHost(state)
	if !ok {
		return
	}
	// level.setBlock(p, infestedStateByHost(s), 3): convert the host into its infested variant.
	if t.world().SetBlock(g.mergePos, infested, dimMinY) {
		t.broadcastBlockUpdate(g.mergePos, infested)
	}
	// mob.spawnAnim(); mob.discard(): poof particles then remove the silverfish (it "merged" into the block).
	t.silverfishSpawnAnim(e)
	e.dead = true
	t.regionForEntity(e).entities.remove(e.id)
}

// stop ports RandomStrollGoal.stop (super): navigation.stop() — clear the fallback stroll want.
func (g *silverfishMergeStoneGoal) stop(_ *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
}

// --- SilverfishWakeUpFriendsGoal -----------------------------------------------------------------

// silverfishWakeSpiral{Y,XZ} are the wake goal's spiral search half-extents: ±5 on y (the vanilla `j`
// loop -5..5) and ±10 on x and z (the `k`/`l` loops -10..10). Cite Silverfish$SilverfishWakeUpFriendsGoal.tick.
const (
	silverfishWakeSpiralY  = 5
	silverfishWakeSpiralXZ = 10
)

// silverfishWakeLookForFriends is notifyHurt's adjustedTickDelay(20) arm value — the delay before the
// wake spiral fires. adjustedTickDelay is identity (=20) in the full-rate driver. Cite
// Silverfish$SilverfishWakeUpFriendsGoal.notifyHurt (bipush 20; adjustedTickDelay).
const silverfishWakeLookForFriends = 20

// silverfishWakeFriendsGoal ports Silverfish$SilverfishWakeUpFriendsGoal (extends Goal, EMPTY flag set —
// it claims no control flag, so it never conflicts with any other goal in the selector). Its lookForFriends
// counter lives on mobAI (silverfishLookForFriends) so the hurt hook can arm it; this struct is stateless.
type silverfishWakeFriendsGoal struct {
	baseGoal
}

// newSilverfishWakeFriendsGoal builds the wake goal (kind "silverfish_wake_friends"), flags {} (empty
// EnumSet — Silverfish$SilverfishWakeUpFriendsGoal declares NO setFlags). Cite Silverfish.registerGoals
// @3 SilverfishWakeUpFriendsGoal.
func newSilverfishWakeFriendsGoal() *silverfishWakeFriendsGoal {
	return &silverfishWakeFriendsGoal{baseGoal: newBaseGoal(0)}
}

// canUse ports SilverfishWakeUpFriendsGoal.canUse: `return lookForFriends > 0;`. Cite the method
// (getfield lookForFriends; ifle 11; iconst_1). RNG-FREE.
func (g *silverfishWakeFriendsGoal) canUse(_ *TickLoop, e *Entity) bool {
	return e.ai != nil && e.ai.silverfishLookForFriends > 0
}

// canContinueToUse: the goal runs a single tick that self-terminates (lookForFriends decrements to <=0
// and the spiral fires). Delegating to canUse keeps a still-armed counter running; once tick drives it
// to 0 the spiral fires and canUse returns false next check. Cite the goal's Goal-default lifecycle.
func (g *silverfishWakeFriendsGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	return g.canUse(t, e)
}

// tick ports SilverfishWakeUpFriendsGoal.tick (javap-verified this session):
//
//	--lookForFriends;
//	if (lookForFriends <= 0) {
//	    Level level = silverfish.level();
//	    RandomSource r = silverfish.getRandom();
//	    BlockPos base = silverfish.blockPosition();
//	    for (int j = 0; !broke && j <= 5 && j >= -5; j = j <= 0 ? 1 - j : -j) {          // y spiral ±5
//	      for (int k = 0; !broke && k <= 10 && k >= -10; k = k <= 0 ? 1 - k : -k) {      // x spiral ±10
//	        for (int l = 0; !broke && l <= 10 && l >= -10; l = l <= 0 ? 1 - l : -l) {    // z spiral ±10
//	          BlockPos p = base.offset(k, j, l);                                          // NOTE: offset(k, j, l)
//	          BlockState s = level.getBlockState(p);
//	          Block b = s.getBlock();
//	          if (b instanceof InfestedBlock) {
//	            if (mobGriefing) level.destroyBlock(p, true, silverfish);
//	            else level.setBlock(p, ((InfestedBlock)b).hostStateByInfested(level.getBlockState(p)), 3);
//	            if (r.nextBoolean()) return;                                              // one wake per hurt (usually)
//	          }
//	        }
//	      }
//	    }
//	}
//
// The triple-nested spiral order is j (y), then k (x), then l (z); the block offset is offset(k, j, l)
// — x=k, y=j, z=l. The counter (lookForFriends) lives on mobAI. Cite Silverfish$SilverfishWakeUpFriendsGoal.tick.
func (g *silverfishWakeFriendsGoal) tick(t *TickLoop, e *Entity) {
	if e.ai == nil {
		return
	}
	// --lookForFriends;
	e.ai.silverfishLookForFriends--
	if e.ai.silverfishLookForFriends > 0 {
		return
	}
	if t.world() == nil {
		return
	}
	r := mobRandom(e)
	// silverfish.blockPosition() == BlockPos.containing(x, y, z) (Mth.floor each).
	baseX := int(math.Floor(e.x))
	baseY := int(math.Floor(e.y))
	baseZ := int(math.Floor(e.z))
	// The spiral: j over ±5 (y), k over ±10 (x), l over ±10 (z) — the vanilla `i = i<=0 ? 1-i : -i` walk
	// (0, 1, -1, 2, -2, ...). Faithfully reproduce the exact iteration + break-on-nextBoolean order.
	for j := 0; j <= silverfishWakeSpiralY && j >= -silverfishWakeSpiralY; j = spiralNext(j) {
		for k := 0; k <= silverfishWakeSpiralXZ && k >= -silverfishWakeSpiralXZ; k = spiralNext(k) {
			for l := 0; l <= silverfishWakeSpiralXZ && l >= -silverfishWakeSpiralXZ; l = spiralNext(l) {
				p := pk.Position{X: baseX + k, Y: baseY + j, Z: baseZ + l} // base.offset(k, j, l)
				state, _ := t.world().GetBlock(p, dimMinY)
				if !isInfestedBlock(state) { // b instanceof InfestedBlock
					continue
				}
				if silverfishMobGriefing {
					// level.destroyBlock(p, true, silverfish) → break + spawnInfestation (summon a friend).
					t.silverfishDestroyInfestedBlock(p)
				} else {
					// level.setBlock(p, hostStateByInfested(s), 3): de-infest in place (no summon).
					if host, ok := hostStateByInfested(state); ok {
						if t.world().SetBlock(p, host, dimMinY) {
							t.broadcastBlockUpdate(p, host)
						}
					}
				}
				// if (r.nextBoolean()) return; — usually stop after the first wake (~50%).
				if r.nextBoolean() {
					return
				}
			}
		}
	}
}

// requiresUpdateEveryTick: the wake goal claims no flags, so the selector would not tick it on the
// simulate path unless it is the running goal; it self-drives its counter each tick while armed. It uses
// the baseGoal default (false) — the selector ticks a RUNNING no-flag goal each tick (tickRunningGoals),
// which is enough: canUse arms it (lookForFriends>0) and its own tick decrements + fires. No override.

// spiralNext ports the vanilla spiral index step `i = i <= 0 ? 1 - i : -i` (the 0,1,-1,2,-2,… walk the
// wake goal's three nested loops use). Cite Silverfish$SilverfishWakeUpFriendsGoal.tick (iload; ifgt;
// iconst_1; ... isub — the ternary reload).
func spiralNext(i int) int {
	if i <= 0 {
		return 1 - i
	}
	return -i
}
