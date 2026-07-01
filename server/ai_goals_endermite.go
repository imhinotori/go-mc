package server

// ai_goals_endermite.go - MOB-PREY (Task #9): the Endermite's server-side despawn timer, ported 1:1 from
// the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p this session):
//
//	net.minecraft.world.entity.monster.Endermite.aiStep():
//	    super.aiStep();
//	    if (level().isClientSide()) { <spawn PORTAL particles> }        // CLIENT-only visual - not ported
//	    else {
//	        if (!isPersistenceRequired()) { ++this.life; }
//	        if (this.life >= 2400) { this.discard(); }                  // MAX_LIFE == 2400 (~2 min @ 20 TPS)
//	    }
//
// The endermite is a SHORT-LIVED mob: naturally it appears only from an EnderMan teleport (a 5% roll) and
// then despawns after 2 minutes unless a name-tag / bucket makes it persistent. We port the server branch
// exactly; the client-side PORTAL particle burst is a pure visual (no particle subsystem) and is cited-out.
//
// The endermite's COMBAT (float / powder-snow climb / melee / stroll / look / around + hurt_by + nearest-
// player target) is the .star declaration (vanilla_endermite/main.star) over the shared Go-native combat
// kinds; THIS file is only the despawn extra (the sibling of endermanAiStep / creeperAiStep).

// endermiteMaxLife is Endermite.MAX_LIFE - the despawn threshold (life >= 2400 -> discard). Pinned as a
// named const (not a magic literal) so the 1:1 value is legible. Cite Endermite.MAX_LIFE (javap: sipush 2400).
const endermiteMaxLife = 2400

// endermiteAiStep is the port of Endermite.aiStep's server branch (the per-type hook, sibling of
// endermanAiStep / creeperAiStep). A non-persistent endermite ages one tick and is discarded once its life
// reaches MAX_LIFE. Called from tickAI for a live endermite (typ == entity.Endermite.ID), AFTER serverAiStep.
//
// isPersistenceRequired is a cited constant FALSE here: v1 has no persistence subsystem (no name-tag /
// bucket / setPersistenceRequired path), so every endermite ages - which is the faithful outcome for a
// naturally-spawned (non-persistent) endermite. When a persistence flag lands, this becomes a real read.
func (t *TickLoop) endermiteAiStep(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	// !isPersistenceRequired() -> ++life. (isPersistenceRequired == false, cited: no persistence subsystem.)
	e.life++
	// life >= 2400 -> discard(): mark dead + remove from the owning region's store (the tracker's near()
	// stops returning it -> RemoveEntities to every tracker next tick). The SAME discard seam the creeper
	// explosion / silverfish merge use.
	if e.life >= endermiteMaxLife {
		e.dead = true
		t.regionForEntity(e).entities.remove(e.id)
	}
}
