package server

// lightning.go — the LIGHTNING BOLT ON THUNDER subsystem, ported 1:1 from the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar, read via CFR / javap -c -p this session). It layers on top of the
// world-global weather cycle (weather.go — isRaining/isThundering) already merged.
//
// TWO HALVES:
//
//  1. THE STRIKE (net.minecraft.server.level.ServerLevel.tickThunder(LevelChunk)): the per-chunk
//     probability gate the chunk cache runs each tick for every entity-ticking chunk. It draws the
//     GLOBAL/coordinator levelRandom (`this.random`) — nextInt(100000)==0 — and, on a hit, heightmaps a
//     random column to a strike position, rolls the skeleton-trap chance, and spawns a LightningBolt
//     (and, on a trap, a trapped SkeletonHorse — DEFERRED, see below).
//
//  2. THE BOLT (net.minecraft.world.entity.LightningBolt): the code-spawned visual+damage entity the
//     strike creates. Its tick is the START_LIFE=2 life/flashes lifecycle — at life==2 it starts ground
//     fire (doFireTick + difficulty), and while life>=0 (and not visual-only) it deals 5.0 lightning
//     damage + fire to every LivingEntity in a 3.0-radius box, then re-flashes flashes-1 more times and
//     discards. The sibling of the EvokerFangs entity (evoker_fangs.go) — a code-spawned, tick-owned
//     projectile-like entity, NOT a plugin-declared mob.
//
// VANILLA CALL CHAIN (all verified against the jar this session):
//
//	net.minecraft.server.level.ServerChunkCache.tickSpawningChunk(chunk, ...):
//	    if (distanceManager.inEntityTickingRange(chunkPos.pack())) level.tickThunder(chunk);
//
//	net.minecraft.server.level.ServerLevel.tickThunder(LevelChunk chunk):
//	    boolean raining = isRaining();
//	    int minX = chunkPos.getMinBlockX(); int minZ = chunkPos.getMinBlockZ();
//	    if (raining && isThundering() && this.random.nextInt(100000) == 0) {
//	        BlockPos pos = findLightningTargetAround(getBlockRandomPos(minX, 0, minZ, 15));
//	        if (isRainingAt(pos)) {
//	            DifficultyInstance difficulty = getCurrentDifficultyAt(pos);
//	            boolean isTrap = getGameRules().get(SPAWN_MOBS)
//	                          && this.random.nextDouble() < difficulty.getEffectiveDifficulty() * 0.01
//	                          && !getBlockState(pos.below()).is(BlockTags.LIGHTNING_RODS);
//	            if (isTrap) { SkeletonHorse ...; addFreshEntity(horse); }          // DEFERRED
//	            LightningBolt bolt = LIGHTNING_BOLT.create(this, EVENT);
//	            bolt.snapTo(Vec3.atBottomCenterOf(pos)); bolt.setVisualOnly(isTrap); addFreshEntity(bolt);
//	        }
//	    }
//
//	net.minecraft.server.level.ServerLevel.findLightningTargetAround(BlockPos pos):
//	    BlockPos center = getHeightmapPos(MOTION_BLOCKING, pos);
//	    Optional<BlockPos> rod = findLightningRod(center);              // DEFERRED (no lightning-rod POI)
//	    if (rod.isPresent()) return rod.get();
//	    AABB search = AABB.encapsulatingFullBlocks(center, center.atY(getMaxY()+1)).inflate(3.0);
//	    List<LivingEntity> hits = getEntitiesOfClass(LivingEntity, search, e -> e.isAlive() && canSeeSky(e));
//	    if (!hits.isEmpty()) return hits.get(random.nextInt(hits.size())).blockPosition();
//	    if (center.getY() == getMinY() - 1) center = center.above(2);
//	    return center;
//
// PIG-ORACLE SAFETY (mandate — the byte-identical gate must stay green): tickThunder's nextInt(100000)
// gate, the skeleton-trap nextDouble(), and findLightningTargetAround's entity-pick nextInt draw the
// GLOBAL/coordinator levelRandom (t.only().levelRandom == the globalRegion levelRandom == the ServerLevel
// `this.random` analogue), NEVER a per-entity stream. getBlockRandomPos draws the SEPARATE randValue int
// LCG (random_tick.go). The bolt's OWN lifecycle draws (flashes, the re-flash nextInt(10), spawnFire's
// nearby-pos nextInt(3)) use the bolt's per-entity random (mobRandom). The pig oracle
// (TestPluginPigEqualsGoNativePig) drives serverAiStep directly — it never runs tickThunder or the bolt
// tick — so the pinned per-entity streams are untouched regardless of which random this subsystem draws.
//
// SCOPE / CITED DEFERRALS (each a not-yet-built subsystem, structured to become real later):
//   - SkeletonHorse trap spawn: the trap CHANCE roll (nextDouble draw) is REAL (it must be consumed in
//     order so the levelRandom stream matches the jar), and it drives the bolt's visualOnly flag; but the
//     actual trapped-horse SPAWN is deferred (no SkeletonHorse type / trap-charge subsystem in v1). The
//     bolt-side observable (visualOnly => no damage/fire) is faithful. CITE ServerLevel.tickThunder.
//   - Lightning rod redirection (findLightningRod POI scan): deferred — no lightning_rod block / POI
//     manager wired. The heightmap target is REAL. CITE ServerLevel.findLightningRod.
//   - powerLightningRod / clearCopperOnLightningStrike (LightningBolt.tick): deferred — no lightning-rod
//     / weathering-copper blocks. CITE LightningBolt.powerLightningRod / clearCopperOnLightningStrike.
//   - gameEvent(LIGHTNING_STRIKE) + the LIGHTNING_STRIKE/CHANNELED_LIGHTNING advancement triggers:
//     deferred — no GameEvent (sculk) / advancement subsystem. CITE LightningBolt.tick.
//   - doFireTick gamerule: CITED-CONSTANT true (the same pattern as randomTickSpeed/doWeatherCycle) —
//     no gamerule store. The difficulty gate (NORMAL/HARD) is REAL (serverDifficulty == NORMAL).
//   - Player fire on a bolt hit: v1 players carry no remainingFireTicks field, so thunderHit's fire is
//     applied to MOBS (which have the field) and DEFERRED for players; the 5.0 damage is REAL for both.

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// LightningBolt / tickThunder constants (VERIFIED CFR — the compile-time-inlined static finals + the
// tickThunder literals).
const (
	// thunderStrikeBound is ServerLevel.tickThunder's `this.random.nextInt(100000)` bound — the per-chunk
	// strike probability gate (1 in 100000 per entity-ticking chunk per tick while thundering).
	thunderStrikeBound = 100000
	// boltStartLife is LightningBolt.START_LIFE — the initial life value (2); tick spawns the flash at
	// life==2 and counts down.
	boltStartLife = 2
	// boltDamageRadius is LightningBolt.DAMAGE_RADIUS — the ±X/±Z (and the +3 top of the +6-tall) box the
	// bolt's per-tick strike damages LivingEntities within.
	boltDamageRadius = 3.0
	// boltDamageTopExtra is the extra +6.0 vertical the damage box extends ABOVE the bolt (AABB y-hi ==
	// y + 6.0 + 3.0). LightningBolt.tick: new AABB(x-3, y-3, z-3, x+3, y+6+3, z+3).
	boltDamageTopExtra = 6.0
	// boltDamage is Entity.thunderHit's hurtServer(lightningBolt(), 5.0F) — the lightning-strike damage.
	boltDamage = 5.0
	// boltIgniteSeconds is Entity.thunderHit's igniteForSeconds(8.0F) when the victim was not already on
	// fire (remainingFireTicks == 0 after the +1).
	boltIgniteSeconds = 8.0
	// boltFireSourcesAtStrike is LightningBolt.tick's spawnFire(4) at life==2 — the primary strike scatters
	// up to 4 additional nearby fire sources (plus the one at the strike cell).
	boltFireSourcesAtStrike = 4
	// boltReflashMaxIdle is the nextInt(10) bound in `life < -random.nextInt(10)` — the re-flash idle gap
	// between the primary flash and each additional flash.
	boltReflashMaxIdle = 10
	// boltFireScatterSpan is the nextInt(3)-1 offset span for spawnFire's additional-source scatter (a
	// [-1,+1] block offset on each axis around the strike cell).
	boltFireScatterSpan = 3
)

// doFireTickGameRule is the GameRules.FIRE_DAMAGE / doFireTick read that gates whether a lightning strike
// (and fire spread generally) sets blocks alight. No gamerule store exists in v1 (weather.go's
// advanceWeatherCycleGameRule + random_tick.go's randomTickSpeed are the same cited-constant pattern), so
// this is the vanilla default true. The difficulty gate (NORMAL/HARD) is applied separately in tickBolt.
// Becomes a real gamerule read once the store lands.
//
//	[VERIFIED javap GameRules: RULE_DOFIRETICK ("doFireTick") default true. DEFERRED: real gamerule store;
//	 default true is the vanilla registerBoolean("doFireTick", true).]
func (t *TickLoop) doFireTickGameRule() bool { return true }

// spawnMobsGameRule is the GameRules.SPAWN_MOBS ("doMobSpawning") read the skeleton-trap gate reads
// (getGameRules().get(SPAWN_MOBS)). Cited-constant true (vanilla default) — same pattern as the other
// gamerule stubs. Becomes a real read once the store lands.
//
//	[VERIFIED javap GameRules: SPAWN_MOBS ("doMobSpawning") default true.]
func (t *TickLoop) spawnMobsGameRule() bool { return true }

// tickThunder is the WORLD-GLOBAL thunder-strike driver — the 1:1 port of the
// ServerChunkCache.tickSpawningChunk -> ServerLevel.tickThunder(chunk) pass. For every loaded (Ready)
// column it runs the tickThunder body: the isRaining && isThundering && nextInt(100000)==0 gate, then the
// target selection + bolt spawn. It runs on the COORDINATOR, once per tick, as a sibling of
// tickRandomBlocks (both are world-global per-chunk passes over the SHARED ChunkManager — the world is
// not yet per-region-sharded). A nil world (bare test loops) is a cheap no-op.
//
// VANILLA gate placement: tickThunder is called per SPAWNING chunk (the entity-ticking-range chunks); v1
// has no distance-manager entity-ticking ring, so every loaded column is treated as entity-ticking (the
// same simplification tickRandomBlocks makes over forEachBlockTickingChunk). CITE:
// ServerChunkCache.tickSpawningChunk -> ServerLevel.tickThunder.
func (t *TickLoop) tickThunder() {
	if t.world() == nil {
		return // no world wired: cheap no-op (pre-SetWorld / bare test loops)
	}
	// Cheap pre-gate: isRaining && isThundering are WORLD-GLOBAL (one weather cycle), so if the world is
	// not thundering NO column can strike — skip the whole per-column loop (vanilla evaluates the gate per
	// chunk, but the raining/thundering reads are chunk-invariant, so hoisting them is a pure optimization
	// that draws ZERO random and preserves identical observable behavior). The nextInt(100000) draw stays
	// per-column below (it is NOT chunk-invariant — each column that clears the flag gate draws once).
	if !t.isRaining() || !t.isThundering() {
		return
	}
	t.world().ForEachReady(func(pos level.ChunkPos, ch *level.Chunk) {
		t.tickThunderChunk(pos)
	})
}

// tickThunderChunk is ServerLevel.tickThunder(chunk)'s body for one column: draw the global levelRandom
// nextInt(100000) gate, and on a hit select the target + spawn the bolt. The isRaining && isThundering
// pre-conditions are established by the caller (hoisted, RNG-free); the nextInt(100000) draw is per-column
// (each column that reaches here draws exactly once), matching the jar where the && short-circuit means
// the draw happens once per chunk that passes the raining+thundering flags. CITE: ServerLevel.tickThunder.
func (t *TickLoop) tickThunderChunk(pos level.ChunkPos) {
	// The strike draws the GLOBAL/coordinator levelRandom (ServerLevel `this.random`), exactly like the
	// weather cycle (weather.go uses t.regions[globalRegion].levelRandom). t.only() resolves to the
	// globalRegion on the coordinator. A nil levelRandom (a bare test loop that never seeded it) is a
	// no-op — no random means no strike.
	r := t.only()
	if r == nil || r.levelRandom == nil {
		return
	}
	minX := int(pos[0]) << 4
	minZ := int(pos[1]) << 4

	// `this.random.nextInt(100000) == 0` — the 1-in-100000 per-chunk gate.
	if r.levelRandom.NextIntN(thunderStrikeBound) != 0 {
		return
	}

	// pos = findLightningTargetAround(getBlockRandomPos(minX, 0, minZ, 15)). getBlockRandomPos draws the
	// SEPARATE randValue int LCG (random_tick.go) — NOT levelRandom. yo == 0, yMask == 15 (the jar call).
	seed := r.nextBlockRandomPos(minX, 0, minZ, 15)
	target := t.findLightningTargetAround(seed)

	// if (isRainingAt(pos)) { ... } — the strike only lands where rain actually reaches (open sky + at or
	// above the surface top, weather.go/crop_block.go's isRainingAt). No further work otherwise.
	if !t.isRainingAt(target) {
		return
	}

	// boolean isTrap = getGameRules().get(SPAWN_MOBS)
	//               && this.random.nextDouble() < difficulty.getEffectiveDifficulty() * 0.01
	//               && !getBlockState(pos.below()).is(BlockTags.LIGHTNING_RODS);
	// The && is short-circuit: the nextDouble() draw happens ONLY when SPAWN_MOBS is true (it is, cited
	// constant). getEffectiveDifficulty() at the strike pos == effectiveDifficulty(NORMAL, gametime, 0, 0)
	// (getCurrentDifficultyAt: localTime=0, moonBrightness=0 when the inhabited-time/moon are unwired), the
	// same DifficultyInstance the spawn-armor roll builds (entity_equipment.go). The lightning-rod-below
	// check is a cited constant false (no lightning_rod block), so the trap gate is (nextDouble < eff*0.01).
	isTrap := false
	if t.spawnMobsGameRule() {
		eff := effectiveDifficulty(serverDifficulty, t.gametime, 0, 0.0)
		// !getBlockState(pos.below()).is(LIGHTNING_RODS): no lightning_rod block in v1 -> always true (the
		// block below is never a rod), so the third conjunct never suppresses the trap. Cited deferral.
		if r.levelRandom.NextDouble() < float64(eff)*0.01 {
			isTrap = true
		}
	}

	// if (isTrap) { SkeletonHorse ...; addFreshEntity(horse); } — DEFERRED (no SkeletonHorse type / trap
	// subsystem). The trap CHANCE roll above is REAL (consumed in order so the levelRandom stream matches
	// the jar) and drives visualOnly below; only the trapped-horse spawn is omitted. CITE
	// ServerLevel.tickThunder skeleton-trap branch.

	// LightningBolt bolt = LIGHTNING_BOLT.create(this, EVENT);
	// bolt.snapTo(Vec3.atBottomCenterOf(pos)); bolt.setVisualOnly(isTrap); addFreshEntity(bolt);
	t.spawnLightningBolt(target, isTrap)
}

// findLightningTargetAround is the 1:1 port of ServerLevel.findLightningTargetAround(BlockPos): heightmap
// the seed column to the surface top (MOTION_BLOCKING), look for a nearby lightning rod (DEFERRED), then
// pick a random sky-exposed living entity in the tall search box above the column, else return the
// heightmap top. It draws the GLOBAL levelRandom for the entity pick (random.nextInt(hits.size())) — in
// draw order after the nextInt(100000) gate, matching the jar. CITE: ServerLevel.findLightningTargetAround.
func (t *TickLoop) findLightningTargetAround(seed pk.Position) pk.Position {
	// BlockPos center = getHeightmapPos(MOTION_BLOCKING, pos): the first-available cell ABOVE the surface
	// top at (x,z) — getFirstAvailable == ghastMotionBlockingTop + 1 (the same MOTION_BLOCKING top the
	// precipitation/heightmap gates use, crop_block.go/ai_goals_happy_ghast.go).
	centerY := t.ghastMotionBlockingTop(seed.X, seed.Z) + 1
	center := pk.Position{X: seed.X, Y: centerY, Z: seed.Z}

	// Optional<BlockPos> rod = findLightningRod(center); if present return it. DEFERRED — no lightning_rod
	// block / POI manager in v1, so findLightningRod is always empty. The heightmap target below is REAL.
	// CITE ServerLevel.findLightningRod.

	// AABB search = AABB.encapsulatingFullBlocks(center, center.atY(getMaxY()+1)).inflate(3.0);
	// The box spans from the heightmap top UP to just above the build ceiling, inflated 3 on every axis.
	// getMaxY() == maxBuildHeightY (block_break.go; the overworld getMinY()+getHeight()-1 == 319), so the
	// top is maxBuildHeightY + 1.
	loX := float64(center.X) - boltDamageRadius
	hiX := float64(center.X+1) + boltDamageRadius
	loZ := float64(center.Z) - boltDamageRadius
	hiZ := float64(center.Z+1) + boltDamageRadius
	loY := float64(center.Y) - boltDamageRadius
	hiY := float64(maxBuildHeightY+1) + boltDamageRadius

	// List<LivingEntity> hits = getEntitiesOfClass(LivingEntity, search, e -> e.isAlive() && canSeeSky(e));
	// v1 LivingEntities near the column are the store mobs (players are not in the entity store — the same
	// convention as fangsDealDamageInBox scans separately; but findLightningTargetAround in vanilla ALSO
	// scans players. v1 players ARE LivingEntities, so include them too, exactly as the jar's
	// getEntitiesOfClass(LivingEntity.class) hits every LivingEntity in the box, mob or player).
	var candidates []pk.Position
	// Mob LivingEntities in the tall box.
	r := t.only()
	if r != nil && r.entities != nil {
		for _, m := range r.entities.byID {
			if m == nil || !isLivingMob(m) || !m.isAlive() {
				continue // e.isAlive() && instanceof LivingEntity
			}
			if !aabbContainsEntity(m, loX, loY, loZ, hiX, hiY, hiZ) {
				continue
			}
			if !t.canSeeSkyAt(int(math.Floor(m.y))) {
				continue // canSeeSky(input.blockPosition())
			}
			candidates = append(candidates, entityBlockPos(m))
		}
	}
	// Player LivingEntities in the tall box.
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		if p.x < loX || p.x >= hiX || p.z < loZ || p.z >= hiZ || p.y < loY || p.y >= hiY {
			continue
		}
		if !t.canSeeSkyAt(int(math.Floor(p.y))) {
			continue
		}
		candidates = append(candidates, pk.Position{X: int(math.Floor(p.x)), Y: int(math.Floor(p.y)), Z: int(math.Floor(p.z))})
	}

	// if (!entities.isEmpty()) return entities.get(random.nextInt(entities.size())).blockPosition();
	// The entity pick draws the GLOBAL levelRandom (the ServerLevel `this.random`), in order after the
	// strike gate — a load-bearing draw for the levelRandom stream.
	if len(candidates) > 0 && r != nil && r.levelRandom != nil {
		idx := int(r.levelRandom.NextIntN(int32(len(candidates))))
		return candidates[idx]
	}

	// if (center.getY() == getMinY() - 1) center = center.above(2); — a wholly-air column heightmap can
	// land at getMinY()-1 (dimMinY-1); nudge it up 2 so the bolt is not below the world. getMinY() ==
	// dimMinY.
	if center.Y == dimMinY-1 {
		center.Y += 2
	}
	return center
}

// spawnLightningBolt is ServerLevel.tickThunder's LIGHTNING_BOLT.create + snapTo + setVisualOnly +
// addFreshEntity: build a LightningBolt entity at the strike pos (snapTo(Vec3.atBottomCenterOf(pos)) ==
// x+0.5, y, z+0.5) with the START_LIFE=2 life + a per-entity flashes=nextInt(3)+1 roll, mark it
// visual-only for a trap, and add it to the owning region's store (the tracker broadcasts its AddEntity
// next tick, exactly as the fangs/arrow/item drop rides the store-add path). Returns the bolt entity.
// CITE: ServerLevel.tickThunder bolt branch + LightningBolt.<init>.
func (t *TickLoop) spawnLightningBolt(pos pk.Position, visualOnly bool) *Entity {
	// Vec3.atBottomCenterOf(Vec3i): (x + 0.5, y, z + 0.5) — the block's bottom-center. The bolt's feet sit
	// on the target cell's floor.
	x := float64(pos.X) + 0.5
	y := float64(pos.Y)
	z := float64(pos.Z) + 0.5

	b := NewEntity(t.idAlloc.AllocID(), entity.LightningBolt, x, y, z)
	b.isBolt = true
	b.boltLife = boltStartLife // LightningBolt.life = START_LIFE (2)
	b.boltVisualOnly = visualOnly

	// LightningBolt.<init>: seed = random.nextLong(); flashes = random.nextInt(3) + 1. Both draw the bolt's
	// OWN per-entity random (Entity.random == RandomSource.create()), NOT levelRandom — so they never
	// perturb the coordinator stream. seed is a client-render sound seed only (unread server-side); flashes
	// (1..3) is the count of additional strike flashes. mobRandom(b) is the per-entity source (seeded from
	// the entity id at spawn via reseedMobAI for non-AI entities it falls back to the default seed —
	// deterministic either way). The nextLong seed draw is CONSUMED in order so the flashes draw matches the
	// jar's post-seed draw position.
	rng := mobRandom(b)
	_ = rng.nextLong()                        // seed = random.nextLong() (client sound seed; unread)
	b.boltFlashes = int32(rng.nextInt(3) + 1) // flashes = random.nextInt(3) + 1  (1..3)

	owner := t.regionForEntity(b)
	if owner == nil {
		owner = t.only()
	}
	owner.entities.add(b)
	return b
}

// tickLightning drives every LightningBolt in every region, the sibling of tickFangs/tickArrows. It runs
// on the coordinator (quiescent) and processes each region WITH that region registered (withRegion) so the
// bolt tick's t.cur() (the despawn remove) resolves to the bolt's OWN store. A per-region snapshot keeps
// the loop stable across an in-loop discard (the despawn removal). CITE: LightningBolt.tick.
func (t *TickLoop) tickLightning() {
	for _, r := range t.regions {
		if r == nil || r.entities == nil {
			continue
		}
		var snapshot []*Entity
		for _, e := range r.entities.byID {
			if e.isBolt {
				snapshot = append(snapshot, e)
			}
		}
		if snapshot == nil {
			continue
		}
		t.withRegion(r, func() {
			for _, e := range snapshot {
				t.tickBolt(e)
			}
		})
	}
}

// tickBolt ports LightningBolt.tick's SERVER branch for one bolt: the life/flashes lifecycle. At life==2
// it starts the ground fire (doFireTick && difficulty) — powerLightningRod / clearCopper / gameEvent are
// cited deferrals. It counts life down; at life<0 it discards (flashes==0) or re-flashes; while life>=0
// and not visual-only it deals 5.0 lightning damage + fire to every LivingEntity in the ±3 box. The
// isClientSide branches (sound, sky-flash, particles) are pure client render (server-authoritative here) —
// NOT ported. CITE: LightningBolt.tick.
func (t *TickLoop) tickBolt(e *Entity) {
	rng := mobRandom(e)

	// if (life == 2) { server branch }: the primary strike frame.
	if e.boltLife == 2 {
		// Difficulty difficulty = getDifficulty(); if (NORMAL || HARD) spawnFire(4). serverDifficulty is
		// the cited NORMAL stub, so the gate passes (NORMAL). spawnFire itself gates on !visualOnly +
		// doFireTick (canSpreadFireAround). CITE LightningBolt.tick life==2 fire branch.
		if serverDifficulty == difficultyNormal || serverDifficulty == difficultyHard {
			t.boltSpawnFire(e, boltFireSourcesAtStrike)
		}
		// powerLightningRod(); clearCopperOnLightningStrike(...); gameEvent(LIGHTNING_STRIKE) — DEFERRED
		// (no lightning-rod / weathering-copper blocks, no GameEvent/sculk subsystem). CITE
		// LightningBolt.powerLightningRod / clearCopperOnLightningStrike / tick gameEvent.
	}

	// --this.life;
	e.boltLife--

	// if (this.life < 0) { flashes==0 -> discard; else if (life < -random.nextInt(10)) -> re-flash }.
	if e.boltLife < 0 {
		if e.boltFlashes == 0 {
			// flashes exhausted: the LIGHTNING_STRIKE/CHANNELED_LIGHTNING advancement triggers are DEFERRED
			// (no advancement subsystem); discard(). The tracker emits RemoveEntities next tick.
			t.cur().entities.remove(e.id)
			return
		} else if e.boltLife < -int32(rng.nextInt(boltReflashMaxIdle)) {
			// A re-flash: --flashes; life = 1; seed = random.nextLong(); spawnFire(0). The seed re-roll is a
			// client sound seed (drawn in order); spawnFire(0) places only the strike-cell fire (0 scatter).
			e.boltFlashes--
			e.boltLife = 1
			_ = rng.nextLong() // seed = random.nextLong() (client sound seed; drawn in order)
			t.boltSpawnFire(e, 0)
		}
	}

	// if (this.life >= 0) { server + !visualOnly -> damage every LivingEntity in the box }.
	if e.boltLife >= 0 && !e.boltVisualOnly {
		t.boltDamageEntitiesInBox(e)
	}
}

// boltDamageEntitiesInBox is LightningBolt.tick's damage loop: getEntities(this, AABB(x-3, y-3, z-3, x+3,
// y+6+3, z+3), Entity::isAlive) -> thunderHit each, then hitEntities.addAll(entities). An entity already
// in hitEntities (struck on an earlier tick of THIS bolt) is NOT re-hit — the box loop uses Entity::isAlive
// (no hitEntities filter) but hitEntities.addAll accumulates, and vanilla re-hits across the multi-tick
// window are prevented by the fresh spawn each flash resetting nothing (the SAME hitEntities set persists);
// v1 mirrors vanilla exactly: every alive entity in the box is thunderHit each tick life>=0, and the set is
// tracked for the findLightningTargetAround-side exclusion (the discard-frame viewer list). NOTE the box is
// asymmetric: -3 below, +6+3 above. CITE: LightningBolt.tick damage branch + Entity.thunderHit.
func (t *TickLoop) boltDamageEntitiesInBox(e *Entity) {
	loX := e.x - boltDamageRadius
	hiX := e.x + boltDamageRadius
	loZ := e.z - boltDamageRadius
	hiZ := e.z + boltDamageRadius
	loY := e.y - boltDamageRadius
	hiY := e.y + boltDamageTopExtra + boltDamageRadius

	if e.boltHitEntities == nil {
		e.boltHitEntities = make(map[int32]bool)
	}

	// Mob LivingEntities (and any alive store entity) in the box. Vanilla's filter is Entity::isAlive (NOT
	// LivingEntity-only) — thunderHit is on Entity, so ANY alive entity in range is hit. But thunderHit's
	// effect (fire + 5.0 hurt) only matters for hurtable entities; v1 store non-living props (items/orbs/
	// arrows/potions/fangs) have no health path, so restrict the hit to mobs to avoid nonsensical "hurt an
	// item". Players are handled below. near() is the broad phase; the AABB test is the narrow phase.
	for _, m := range t.cur().entities.near(e.x, e.z, 1) {
		if m == nil || m == e || !isLivingMob(m) || !m.isAlive() {
			continue
		}
		if !aabbContainsEntity(m, loX, loY, loZ, hiX, hiY, hiZ) {
			continue
		}
		t.boltThunderHitMob(e, m)
		e.boltHitEntities[m.id] = true
	}

	// Player LivingEntities in the box.
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		if boxIntersectsPlayer(p, loX, loY, loZ, hiX, hiY, hiZ) {
			t.boltThunderHitPlayer(e, p)
			e.boltHitEntities[p.entityID] = true
		}
	}
}

// boltThunderHitMob ports Entity.thunderHit for a MOB victim EXACTLY:
//
//	setRemainingFireTicks(this.remainingFireTicks + 1);
//	if (this.remainingFireTicks == 0) igniteForSeconds(8.0F);
//	hurtServer(damageSources().lightningBolt(), 5.0F);
//
// The +1 is a plain field write (setRemainingFireTicks is a bare setter); the `== 0` guard then reads the
// JUST-incremented field, so igniteForSeconds(8) fires ONLY when the field was -1 before the +1. A mob at
// the vanilla default 0 (Entity.remainingFireTicks is a plain int, default 0) becomes 1 — NOT 0 — so a
// single strike does NOT give the 8s burn; instead the mob is on fire for 1 tick, re-bumped each tick the
// bolt is alive (life 2->0 gives ~3 hits). This is the exact jar behavior; do NOT "improve" it to an
// unconditional 8s ignite. The 5.0 damage routes through applyDamageEntity (the mob hurt path). CITE:
// Entity.thunderHit + setRemainingFireTicks (a bare field write, verified this session).
func (t *TickLoop) boltThunderHitMob(e *Entity, m *Entity) {
	m.remainingFireTicks++
	if m.remainingFireTicks == 0 {
		t.igniteForSeconds(m, boltIgniteSeconds)
	} else {
		// remainingFireTicks just became > 0: the mob is now on fire (baseTick renders + burns). Refresh the
		// client flame flag so the strike's brief burn is visible, mirroring Entity.setSharedFlagOnFire in
		// baseTick (the fire.go tick would also do this next tick, but broadcast now so the flag is prompt).
		t.broadcastEntityFireFlag(m)
	}
	t.applyDamageEntity(m, damageSourceLightning(), boltDamage)
}

// boltThunderHitPlayer ports Entity.thunderHit for a PLAYER victim: the 5.0 lightning hurt. The fire +1 /
// igniteForSeconds is a CITED deferral for players (v1 players carry no remainingFireTicks field — the
// player fire subsystem is not wired); the 5.0 damage is REAL. Routes through applyDamage (the player hurt
// path). CITE: Entity.thunderHit.
func (t *TickLoop) boltThunderHitPlayer(e *Entity, p *tickPlayer) {
	t.applyDamage(p, damageSourceLightning(), boltDamage)
}

// boltSpawnFire ports LightningBolt.spawnFire(additionalSources): if visualOnly OR !doFireTick -> nothing;
// else place a fire block at the strike cell (if air + survivable), then scatter `additionalSources` more
// in a [-1,+1] cube around it, each drawing nextInt(3)-1 per axis on the bolt's OWN random. The
// canSpreadFireAround (fire-spread-radius / nearby-player) gate is applied via doFireTick here (v1 has no
// per-player fire-spread radius; doFireTick true is the cited constant). CITE: LightningBolt.spawnFire.
func (t *TickLoop) boltSpawnFire(e *Entity, additionalSources int) {
	// if (visualOnly || !(level instanceof ServerLevel)) return; — a visual-only (trap) bolt sets no fire.
	if e.boltVisualOnly {
		return
	}
	// canSpreadFireAround(pos): spreadRadius==-1 || anyPlayerCloseEnough. v1 has no fire-spread-radius
	// gamerule / player-proximity map, so this reduces to the doFireTick gate (cited constant true). When
	// the gamerule store lands this becomes the real canSpreadFireAround read.
	if !t.doFireTickGameRule() {
		return
	}
	if t.world() == nil {
		return
	}
	rng := mobRandom(e)

	// BlockPos pos = blockPosition(); if (getBlockState(pos).isAir() && fire.canSurvive(...)) setBlock(fire).
	strike := entityBlockPos(e)
	t.boltPlaceFire(e, strike)

	// for (i = 0; i < additionalSources; ++i) { nearbyPos = pos.offset(nextInt(3)-1, nextInt(3)-1,
	// nextInt(3)-1); if air+survivable setBlock(fire). } — the nextInt(3) draws (3 per source) are on the
	// bolt's OWN random, in order. DRAW ORDER is load-bearing for the bolt's own stream.
	for i := 0; i < additionalSources; i++ {
		dx := rng.nextInt(boltFireScatterSpan) - 1
		dy := rng.nextInt(boltFireScatterSpan) - 1
		dz := rng.nextInt(boltFireScatterSpan) - 1
		t.boltPlaceFire(e, pk.Position{X: strike.X + dx, Y: strike.Y + dy, Z: strike.Z + dz})
	}
}

// boltPlaceFire is spawnFire's per-cell placement: BaseFireBlock.getState(level, pos) chosen, then placed
// only if the cell is air AND the fire can survive there. v1 uses the plain fire block (minecraft:fire
// default state) — SoulFireBlock / the FireBlock.getStateForPlacement neighbor-connectivity refinement is
// a cited simplification (the observable "lightning leaves a fire on the ground" is faithful; the exact
// fire-face bitmask is a client-render/spread detail). canSurvive is approximated by the cell-is-air +
// solid-below check. CITE: LightningBolt.spawnFire + BaseFireBlock.getState.
func (t *TickLoop) boltPlaceFire(e *Entity, pos pk.Position) {
	// if (!level.getBlockState(pos).isAir()) return; — only replace AIR.
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsAir(state) {
		return
	}
	// fire.canSurvive(level, pos): fire needs a supporting surface. v1 approximation — a solid block
	// directly below the cell (the common case: fire on the ground the bolt struck). A cell with no solid
	// support (mid-air) is skipped, matching the "fire on the ground" observable.
	if !t.isSolidAt(pk.Position{X: pos.X, Y: pos.Y - 1, Z: pos.Z}) {
		return
	}
	fire := block.DefaultStateID["minecraft:fire"]
	if t.world().SetBlock(pos, fire, dimMinY) {
		t.broadcastBlockUpdate(pos, fire)
		e.boltBlocksSetOnFire++ // ++blocksSetOnFire (diagnostic; unread in v1)
	}
}

// entityBlockPos is Entity.blockPosition() == BlockPos.containing(x, y, z) (floor on each axis) for a store
// entity — the cell the entity's feet occupy.
func entityBlockPos(e *Entity) pk.Position {
	return pk.Position{X: int(math.Floor(e.x)), Y: int(math.Floor(e.y)), Z: int(math.Floor(e.z))}
}

// aabbContainsEntity reports whether a store entity's collision box (width x height, feet at e.y) overlaps
// the given world AABB. Half-open on each axis (a shared-face touch does not count) — the same narrow-phase
// convention fangsDealDamageInBox / boxIntersectsPlayer use.
func aabbContainsEntity(e *Entity, loX, loY, loZ, hiX, hiY, hiZ float64) bool {
	hw := e.width / 2
	eLoX, eHiX := e.x-hw, e.x+hw
	eLoY, eHiY := e.y, e.y+e.height
	eLoZ, eHiZ := e.z-hw, e.z+hw
	return !(hiX <= eLoX || eHiX <= loX ||
		hiY <= eLoY || eHiY <= loY ||
		hiZ <= eLoZ || eHiZ <= loZ)
}
