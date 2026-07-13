package server

// bed_explode.go -- the nether/end BED-EXPLOSION branch of BedBlock.useWithoutItem, ported 1:1 from the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p / CFR this session). It is the BedRule
// .explodes() half of the bed right-click: in a dimension whose gameplay/bed_rule EnvironmentAttribute
// carries explodes=true (the_nether + the_end -- see dimension_type data), clicking a bed removes the
// bed and detonates a radius-5.0, fire-carrying, BLOCK-interaction explosion instead of sleeping.
//
// VANILLA CALL CHAIN (BedBlock.useWithoutItem, verified CFR/javap):
//
//	BedRule rule = level.environmentAttributes().getValue(BED_RULE, pos);
//	if (rule.explodes()) {
//	    rule.errorMessage().ifPresent(player::displayClientMessage);      // overlay (DEFERRED: no overlay)
//	    level.removeBlock(pos, false);                                    // remove the HEAD
//	    BlockPos foot = pos.relative(state.getValue(FACING).getOpposite());
//	    if (level.getBlockState(foot).is(this)) level.removeBlock(foot, false);   // remove the FOOT
//	    Vec3 center = Vec3.atCenterOf(pos);                               // HEAD center (x+.5,y+.5,z+.5)
//	    level.explode(null, level.damageSources().badRespawnPointExplosion(center), null, center,
//	                  5.0F, true, Level.ExplosionInteraction.BLOCK);
//	    return SUCCESS_SERVER;
//	}
//
// ServerLevel.explode maps ExplosionInteraction.BLOCK -> getDestroyType(BLOCK_EXPLOSION_DROP_DECAY)
// (vanilla default true -> DESTROY_WITH_DECAY), NOT KEEP -- so a BLOCK explosion ALWAYS destroys terrain
// (independent of MOB_GRIEFING, unlike the creeper MOB case). fire=true so ServerExplosion.explode()
// runs createFire(toBlow) after interactWithBlocks. Draw order (level.random): calculateExplodedPositions
// (nextFloat per shell ray) -> hurtEntities (no RNG) -> interactWithBlocks (Util.shuffle + per-drop decay
// rolls) -> createFire (nextInt(3) per toBlow pos). CITE: BedBlock.useWithoutItem + ServerLevel.explode +
// ServerExplosion.explode/createFire.
//
// PIG-ORACLE SAFETY: the bed explosion is reached only from useBed (a PLAYER right-click), never from
// serverAiStep -- the pig oracle drives serverAiStep directly and never opens a bed. Its RNG draws use
// t.cur().levelRandom (the region ServerLevel this.random), never a per-entity stream.

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// bedExplodeRadius is the BedBlock.useWithoutItem explode radius (5.0F). Cite BedBlock.useWithoutItem.
const bedExplodeRadius = 5.0

// bedCreateFireChance is the ServerExplosion.createFire level.random.nextInt(3) bound -- a 1-in-3 chance
// per toBlow position to place fire (fire lands only on nextInt(3)==0). Cite ServerExplosion.createFire.
const bedCreateFireChance = 3

// bedRule is the resolved net.minecraft.world.attribute.BedRule for a dimension: the explodes flag the
// useBed branch reads. v1 has no EnvironmentAttribute datapack engine, so the two vanilla constants are
// hard-mapped by dimension (the gameplay/bed_rule attribute values from the dimension_type data):
// overworld == CAN_SLEEP_WHEN_DARK (canSleep WHEN_DARK, canSetSpawn ALWAYS, explodes false), nether+end
// == EXPLODES (canSleep NEVER, canSetSpawn NEVER, explodes true). Becomes a real
// environmentAttributes().getValue(BED_RULE, pos) read once that engine lands.
//
//	[VERIFIED javap BedRule.<clinit>: CAN_SLEEP_WHEN_DARK = (WHEN_DARK, ALWAYS, false, "no_sleep");
//	 EXPLODES = (NEVER, NEVER, true, empty). Data: the_nether/the_end gameplay/bed_rule explodes=true;
//	 overworld gameplay/bed_rule can_sleep=when_dark/can_set_spawn=always.]
type bedRule struct {
	explodes bool
}

// bedRuleFor resolves the BedRule for the player dimension. Only explodes is needed at the useBed seam
// (the overworld canSleep/canSetSpawn sub-gates live in startSleepInBed, reached only when explodes is
// false). Cite EnvironmentAttributes.BED_RULE per-dimension assignment.
func bedRuleFor(dimension int) bedRule {
	switch dimension {
	case dimNether, dimEnd:
		return bedRule{explodes: true} // BedRule.EXPLODES
	default:
		return bedRule{explodes: false} // BedRule.CAN_SLEEP_WHEN_DARK
	}
}

// bedExplode ports the BedRule.explodes() branch of BedBlock.useWithoutItem: remove the HEAD (headPos)
// and, if the block one step back along FACING.getOpposite() is the same bed, the FOOT, then detonate a
// radius-5.0 fire-carrying BLOCK-interaction explosion centered on the HEAD block center
// (Vec3.atCenterOf). headPos is the resolved HEAD position and facing its FACING (both already resolved
// by useBed FOOT->HEAD hop). Tick-owned (called from useBed on the owner goroutine). CITE:
// BedBlock.useWithoutItem explode branch.
func (t *TickLoop) bedExplode(headPos pk.Position, facing block.Direction) {
	w := t.world()
	if w == nil {
		return
	}
	air := block.DefaultStateID["minecraft:air"]

	// rule.errorMessage().ifPresent(player::displayClientMessage): the EXPLODES rule carries an EMPTY
	// error message (Optional.empty in the BedRule.EXPLODES ctor), so nothing is shown -- a faithful
	// no-op even without an overlay subsystem. CITE BedRule.EXPLODES (Optional.empty).

	// level.removeBlock(pos, false): remove the HEAD block (broadcast the air update to trackers).
	if w.SetBlock(headPos, air, dimMinY) {
		t.broadcastBlockUpdate(headPos, air)
	}

	// BlockPos foot = pos.relative(FACING.getOpposite()); the FOOT is one step BACK along FACING (the
	// HEAD is placed one step FORWARD along FACING from the FOOT, so the FOOT is FACING.getOpposite()).
	if dx, dz, ok := bedFacingDelta(facing); ok {
		footPos := pk.Position{X: headPos.X - dx, Y: headPos.Y, Z: headPos.Z - dz}
		// if (level.getBlockState(foot).is(this)) level.removeBlock(foot, false): only remove it when it
		// is actually (still) a bed -- a half-broken bed leaves the surviving half in place.
		if fs, ok := w.GetBlock(footPos, dimMinY); ok && isBedBlock(fs) {
			if w.SetBlock(footPos, air, dimMinY) {
				t.broadcastBlockUpdate(footPos, air)
			}
		}
	}

	// Vec3 center = Vec3.atCenterOf(pos): the HEAD block CENTER (x+0.5, y+0.5, z+0.5) -- NOT bottom-
	// center; the explosion is centered inside the (now-removed) head cell. CITE Vec3.atCenterOf(Vec3i).
	cx := float64(headPos.X) + 0.5
	cy := float64(headPos.Y) + 0.5
	cz := float64(headPos.Z) + 0.5

	// level.explode(null, badRespawnPointExplosion(center), null, center, 5.0F, true, BLOCK). No source
	// entity (srcID 0 == none; the excluded-id path only skips a live entity, and 0 matches no store id).
	t.bedExplodeAt(cx, cy, cz, bedExplodeRadius)
}

// bedExplodeAt is ServerLevel.explode for the ExplosionInteraction.BLOCK + fire=true case (the shape the
// bed uses): (1) calculateExplodedPositions (the shell rays, the FIRST level.random draws), (2)
// hurtEntities with the bad_respawn_point source (no RNG), (3) interactWithBlocks UNCONDITIONALLY (BLOCK
// -> DESTROY_WITH_DECAY, never gated by MOB_GRIEFING), (4) createFire(toBlow) because fire is true, then
// (5) the ClientboundExplode broadcast. This mirrors the creeper explode() but with the BLOCK interaction
// (always destroys) + the bad_respawn_point damage source + the fire tail. CITE: ServerLevel.explode
// (BLOCK case) + ServerExplosion.explode.
func (t *TickLoop) bedExplodeAt(x, y, z, radius float64) {
	toBlow := t.calculateExplodedPositions(x, y, z, radius, nil)
	hitPlayers := t.hurtEntitiesFromExplosion(0, x, y, z, radius, damageSourceOf(damageTypeBadRespawnPoint))
	// interactsWithBlocks(): a BLOCK explosion blockInteraction (DESTROY_WITH_DECAY) is != KEEP, so it
	// ALWAYS interacts -- unlike the creeper MOB case there is NO mobGriefing gate. Cite
	// ServerLevel.explode (BLOCK -> getDestroyType(BLOCK_EXPLOSION_DROP_DECAY)) + interactsWithBlocks.
	t.interactWithBlocks(toBlow, radius)
	// fire == true (BedBlock passes iconst_1): createFire(toBlow) after the block interaction.
	t.bedCreateFire(toBlow)
	t.sendExplodePackets(x, y, z, float32(radius), int32(len(toBlow)), hitPlayers)
}

// bedCreateFire ports ServerExplosion.createFire(List<BlockPos>): for each toBlow pos, roll
// level.random.nextInt(3) and (only when it is 0) place fire if the cell is air and the cell BELOW renders
// solid. The nextInt(3) draw happens for EVERY pos in draw order (even when the cell is not air / not
// supported -- the RNG is consumed before the air/solid guards short-circuit), so level.random stays in
// lockstep with the jar. v1 uses the plain minecraft:fire default state (SoulFire / fire-connectivity
// refinement is the same cited simplification as LightningBolt.spawnFire) and isSolidAt(below) as the
// isSolidRender(below) analog. CITE: ServerExplosion.createFire.
func (t *TickLoop) bedCreateFire(toBlow []pk.Position) {
	w := t.world()
	if w == nil {
		return
	}
	r := t.cur()
	if r == nil || r.levelRandom == nil {
		return
	}
	fire := block.DefaultStateID["minecraft:fire"]
	for _, pos := range toBlow {
		// if (level.random.nextInt(3) != 0) continue; -- the draw is UNCONDITIONAL (before the guards).
		if r.levelRandom.NextIntN(bedCreateFireChance) != 0 {
			continue
		}
		// if (getBlockState(pos).isAir() && getBlockState(pos.below()).isSolidRender()) setBlockAndUpdate.
		st, ok := w.GetBlock(pos, dimMinY)
		if !ok || !block.IsAir(st) {
			continue
		}
		if !t.isSolidAt(pk.Position{X: pos.X, Y: pos.Y - 1, Z: pos.Z}) {
			continue // isSolidRender(below) analog (isSolidAt)
		}
		if w.SetBlock(pos, fire, dimMinY) {
			t.broadcastBlockUpdate(pos, fire)
		}
	}
}
