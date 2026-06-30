package server

// ai_goals_sit.go — MOB-NEUT-01 (Phase 36-01): the Go-native SitWhenOrderedToGoal, the wolf's @2
// goalSelector goal (the SIT subsystem). PORTED 1:1 (the STANDING MANDATE, idiomatic non-1:1 Go, no GPL
// paste) from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p this session):
//
//   - net.minecraft.world.entity.ai.goal.SitWhenOrderedToGoal: flags {JUMP, MOVE}; NO RNG. canUse
//     parks an ordered/tamed wolf (it claims MOVE/JUMP so a sitting wolf does not wander); start()
//     stops the navigation + sets the in-sitting-pose DATA_FLAGS bit (broadcast to trackers); stop()
//     clears it.
//
// THE PIG ORACLE IS UNTOUCHED: a passive pig declares no sit goal (and isTame()/isOrderedToSit() are
// the zero-value false for it), so this goal never ticks on it — no draw reaches the pinned pig stream
// (it has none — NO RNG here anyway). Every field read is a wolf field (tame/orderedToSit/owner),
// zero for the pig.

// sitWhenOrderedToOwnerNearSqr is the SitWhenOrderedToGoal.canUse owner-near guard distance: a tamed
// wolf within 144.0 (== 12² exact) of an owner who is being attacked does NOT sit (it stays ready to
// fight). The 144.0 is the literal double in the bytecode (ldc2_w 144.0d).
//
//	[VERIFIED javap SitWhenOrderedToGoal.canUse: distanceToSqr(owner) ldc2_w 144.0d dcmpg ifge;
//	 owner.getLastHurtByMob() ifnull → continue, else return false.]
const sitWhenOrderedToOwnerNearSqr = 144.0

// sitWhenOrderedToGoal ports net.minecraft.world.entity.ai.goal.SitWhenOrderedToGoal (flags
// {JUMP, MOVE}). It holds only the mob back-reference in the jar (supplied by the (t, e) params here),
// so it carries no fields beyond baseGoal. NO RNG.
type sitWhenOrderedToGoal struct {
	baseGoal
}

// newSitWhenOrderedToGoal builds the goal with the JUMP+MOVE flags (SitWhenOrderedToGoal ctor:
// setFlags(EnumSet.of(JUMP, MOVE))) — it claims both so a sitting wolf neither wanders (MOVE) nor jumps
// (JUMP), parking it in place. Mirrors newLeapAtTargetGoal's ctor shape.
//
//	[VERIFIED javap SitWhenOrderedToGoal.<init>: EnumSet.of(Goal$Flag.JUMP, Goal$Flag.MOVE); setFlags.]
func newSitWhenOrderedToGoal() *sitWhenOrderedToGoal {
	return &sitWhenOrderedToGoal{baseGoal: newBaseGoal(flagJump | flagMove)}
}

// canUse ports SitWhenOrderedToGoal.canUse (bytecode-verified this session), NO RNG:
//
//	boolean orderedToSit = mob.isOrderedToSit();
//	if (!orderedToSit && !mob.isTame()) return false;
//	if (mob.isInWater()) return false;
//	if (!mob.onGround()) return false;
//	LivingEntity owner = mob.getOwner();
//	if (owner == null || owner.level() != mob.level()) return true;
//	if (mob.distanceToSqr(owner) < 144.0 && owner.getLastHurtByMob() != null) return false;
//	return orderedToSit;
//
// CITE-DEFERRED sub-behavior (recorded, NEVER silently dropped):
//   - owner.level() != mob.level(): v1 has no multi-dimension owner/wolf split in this path; the owner
//     is resolved on the same loop, so the level-mismatch escape (return true) collapses to "owner
//     present" — a cited no-op equal to "same level". The owner-null escape is the live one.
//   - owner.getLastHurtByMob(): players carry no lastHurtByMob bookkeeping in v1 (only mobs do — the
//     inbound combat store-point). So "the owner is being attacked nearby" is a cited constant-FALSE
//     (the owner is treated as not-currently-fighting), and the 144.0 guard never short-circuits the
//     sit. Upgrade: read the player's lastHurtByMob when the player inbound-combat bookkeeping lands.
func (g *sitWhenOrderedToGoal) canUse(t *TickLoop, e *Entity) bool {
	orderedToSit := e.orderedToSit
	if !orderedToSit && !e.tame { // !orderedToSit && !isTame()
		return false
	}
	if t.mobInWater(e) { // isInWater()
		return false
	}
	if !e.onGround { // !onGround()
		return false
	}
	owner := t.playerByEntityID(e.ownerUUID) // getOwner()
	if owner == nil {
		// owner == null || owner.level() != mob.level() → return true. v1 resolves the owner on the same
		// loop (same level), so the only live escape is owner-absent; a missing owner returns true (the
		// wolf still sits if ordered — the jar's "no owner to guard" branch).
		return true
	}
	// distanceToSqr(owner) < 144.0 && owner.getLastHurtByMob() != null → return false. The owner-being-
	// attacked half is a cited constant-false (players have no lastHurtByMob in v1), so this guard never
	// fires and the sit proceeds. The distance term is computed for the faithful trace.
	dx, dy, dz := owner.x-e.x, owner.y-e.y, owner.z-e.z
	if dx*dx+dy*dy+dz*dz < sitWhenOrderedToOwnerNearSqr && ownerLastHurtByMob(owner) {
		return false
	}
	return orderedToSit
}

// ownerLastHurtByMob is the CITED constant-false stub for owner.getLastHurtByMob() != null: a player
// (the v1 owner) tracks no inbound lastHurtByMob, so "the owner is currently being attacked" is always
// false. Kept as a named predicate (not an inline `false`) so the upgrade — reading the player's
// lastHurtByMob when player inbound-combat bookkeeping lands — slots in here with no canUse edit.
func ownerLastHurtByMob(_ *tickPlayer) bool { return false }

// canContinueToUse ports SitWhenOrderedToGoal.canContinueToUse = isOrderedToSit(). The wolf keeps
// sitting while it remains ordered to sit (the sit-toggle interact, Plan B, flips orderedToSit). NO RNG.
//
//	[VERIFIED javap SitWhenOrderedToGoal.canContinueToUse: mob.isOrderedToSit(); ireturn.]
func (g *sitWhenOrderedToGoal) canContinueToUse(_ *TickLoop, e *Entity) bool {
	return e.orderedToSit
}

// start ports SitWhenOrderedToGoal.start: mob.getNavigation().stop(); mob.setInSittingPose(true). It
// parks the navigation (clearWantTarget — the navigation.stop() seam) and sets the DATA_FLAGS sit bit
// (0x1), broadcasting the flip so the client renders the sit pose. NO RNG.
//
//	[VERIFIED javap SitWhenOrderedToGoal.start: getNavigation().stop(); setInSittingPose(true).]
func (g *sitWhenOrderedToGoal) start(t *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.clearWantTarget() // navigation.stop()
	}
	t.setWolfInSittingPose(e, true) // setInSittingPose(true) + the DATA_FLAGS broadcast
}

// stop ports SitWhenOrderedToGoal.stop: mob.setInSittingPose(false). Clears the sit bit + broadcasts.
// NO RNG.
//
//	[VERIFIED javap SitWhenOrderedToGoal.stop: setInSittingPose(false).]
func (g *sitWhenOrderedToGoal) stop(t *TickLoop, e *Entity) {
	t.setWolfInSittingPose(e, false)
}

// setWolfInSittingPose ports TamableAnimal.setInSittingPose(b): flip the DATA_FLAGS 0x1 bit on the wolf
// (the inSittingPose field — the host-side mirror) and PUSH the new DATA_FLAGS byte to trackers so the
// client renders/clears the sit pose. The byte is recomputed from the wolf's (inSittingPose, tame)
// state via wolfFlagsByte (so a tamed-sitting wolf carries 0x05). Mirrors the sheep setSheared broadcast
// (sheep_eat.go). Tick-owned (TICK-05). NO RNG.
//
//	[VERIFIED javap TamableAnimal.setInSittingPose(b): DATA_FLAGS = b ? (cur|1) : (cur&0xFE); the
//	 SynchedEntityData set fans the DATA_FLAGS DataValue to trackers.]
func (t *TickLoop) setWolfInSittingPose(e *Entity, sitting bool) {
	if e.inSittingPose == sitting {
		return // no change → no broadcast (idempotent, matches SynchedEntityData's dirty-only push)
	}
	e.inSittingPose = sitting
	flags := wolfFlagsByte(e.inSittingPose, e.tame)
	t.broadcastToTrackers(e.id, encodeSetEntityDataByID(e.id, wolfFlagsDataEntry(flags)))
}

// Compile-time assertion: sitWhenOrderedToGoal IS a server.Goal.
var _ Goal = (*sitWhenOrderedToGoal)(nil)
