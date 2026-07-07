package server

// entity_collision.go — B-A3: the 1:1 port of entity-entity collision (the push-apart shove +
// entity cramming), the divergence-audit gap where mobs/players never pushed each other and a
// crowd never suffocated. A LITERAL port of the unobfuscated 26.2 jar (javap -c -p this session):
//
//	net.minecraft.world.entity.LivingEntity.pushEntities()  — the neighbour scan + cramming + doPush loop
//	net.minecraft.world.entity.Entity.push(Entity other)    — the 0.05 normalized shove (doPush)
//	net.minecraft.world.level.Level.getPushableEntities     — getEntities(this, box, EntitySelector.pushableBy)
//	net.minecraft.world.entity.EntitySelector.pushableBy    — the isPushable() + team-collision filter
//	net.minecraft.world.level.gamerules.GameRules.MAX_ENTITY_CRAMMING (registerInteger default 24)
//
// It is called from serverAiStep (ai_mob.go) at the LivingEntity.aiStep pushEntities() slot — a
// single hook t.pushNearbyEntities(e) — so the crowd shove + cramming land in the vanilla tick
// order (after the AI/movement step, before the tick physics integrate the new velocity).
//
// THE PIG ORACLE (TestPluginPigEqualsGoNativePig): pushEntities() returns IMMEDIATELY when the
// pushable-neighbour list is empty (bytecode: if (list.isEmpty()) return;), and the ONLY new RNG
// draw (random.nextInt(4) for the cramming roll) is reached ONLY when list.size() > cramming-1
// (> 23). A lone pig has NO overlapping pushable entity, so the scan early-outs with ZERO new RNG
// draws and ZERO impulse — byte-identical to the pre-B-A3 behaviour. The oracle is protected BY
// CONSTRUCTION (the empty-list gate).
//
// Runs on the tick goroutine over tick-owned state (TICK-05): the entityStore.near broad phase, the
// e.vx/vy/vz velocity writes, and the cramming applyDamageEntity are all single-owner.

import (
	"math"
	"sort"
)

// entityPushForce is Entity.push(Entity)'s 0.05 shove magnitude — the ldc2_w constant
// 0.05000000074505806d (the exact double that (float)0.05F widens to). CITE: javap
// net.minecraft.world.entity.Entity.push(Entity) offset 110/117.
const entityPushForce = 0.05000000074505806

// entityPushEpsilon is the 0.01 lower bound Entity.push(Entity) tests |absMax(dx,dz)| against
// before normalizing — the ldc2_w constant 0.009999999776482582d ((float)0.01F widened). Below it
// the two entities are effectively co-located and NO shove is applied. CITE: javap Entity.push
// offset 55.
const entityPushEpsilon = 0.009999999776482582

// crammingDamage is the 6.0 damage LivingEntity.pushEntities deals via
// damageSources().cramming() when a crowd exceeds maxEntityCramming. CITE: javap
// LivingEntity.pushEntities offset 150 (ldc_w float 6.0f).
const crammingDamage float32 = 6.0

// isPushableEntity is the port of the LivingEntity.isPushable() override plus the base
// Entity.isPushable() (which returns false). Base Entity (items/orbs/arrows/potions/fangs and every
// other non-LivingEntity) is NEVER pushable; a LivingEntity is pushable when
// isAlive() && !isSpectator() && !onClimbable(). v1 models neither a spectator flag nor a climb
// state on a store Entity (attack_dispatch.go keeps onClimbable a documented const false; a mob is
// never a spectator), so those two guards are the vanilla defaults (false) — leaving isAlive() as
// the live gate, exactly as vanilla evaluates it for every non-spectator, non-climbing mob.
// CITE: javap LivingEntity.isPushable (isAlive && !isSpectator && !onClimbable); Entity.isPushable
// (iconst_0 — base false).
func isPushableEntity(e *Entity) bool {
	if !isLivingMob(e) { // base Entity.isPushable() == false (items/orbs/arrows/potions/fangs)
		return false
	}
	// LivingEntity.isPushable(): isAlive() && !isSpectator() && !onClimbable().
	// isSpectator / onClimbable are not modeled on a store Entity in v1 (both vanilla-default false).
	return e.isAlive()
}

// rootVehicleID is Entity.getRootVehicle() reduced to the root entity id: walk the vehicle chain
// (each hop is the thin vehicle-id the ride subsystem records) up to the topmost entity, returning
// ITS id. An un-ridden entity (vehicle == 0, the oracle pig) is its own root, so this returns e.id.
// The bounded walk cannot loop (the ride subsystem never forms a cycle) but is length-capped as a
// defensive guard. CITE: javap Entity.getRootVehicle (while getVehicle()!=null follow the chain).
func (t *TickLoop) rootVehicleID(e *Entity) int32 {
	cur := e
	for i := 0; i < 32 && cur.vehicle != 0; i++ {
		next, ok := t.cur().entities.get(cur.vehicle)
		if !ok {
			break
		}
		cur = next
	}
	return cur.id
}

// isPassengerOfSameVehicle is Entity.isPassengerOfSameVehicle(Entity):
// getRootVehicle() == other.getRootVehicle(). Two entities riding the same root (or the same lone
// entity) share a root id. CITE: javap Entity.isPassengerOfSameVehicle (getRootVehicle==getRootVehicle).
func (t *TickLoop) isPassengerOfSameVehicle(a, b *Entity) bool {
	return t.rootVehicleID(a) == t.rootVehicleID(b)
}

// isVehicleEntity is Entity.isVehicle(): !getPassengers().isEmpty() — the entity carries at least
// one passenger. CITE: javap Entity.isVehicle (getPassengers isEmpty negated).
func isVehicleEntity(e *Entity) bool { return len(e.passengers) > 0 }

// doPushEntity is the 1:1 port of net.minecraft.world.entity.Entity.push(Entity other) (the
// collision shove, renamed doPush in the mapped bytecode). It shoves a and other apart along
// the horizontal axis of greatest separation with the 0.05 normalized impulse, symmetrically —
// each gets pushed away from the other by the SAME magnitude, gated on isVehicle()/isPushable().
//
// Bytecode trace (javap Entity.push(Entity), verified this session):
//
//	if (a.isPassengerOfSameVehicle(other)) return;
//	if (other.noPhysics || a.noPhysics) return;          // noPhysics unmodeled in v1 (default false)
//	double dx = other.getX() - a.getX();
//	double dz = other.getZ() - a.getZ();
//	double d  = Mth.absMax(dx, dz);                        // max(|dx|,|dz|)
//	if (d >= 0.01) {                                       // (double)0.01F
//	    d = Math.sqrt(d);
//	    dx /= d; dz /= d;
//	    double inv = 1.0 / d; if (inv > 1.0) inv = 1.0;
//	    dx *= inv; dz *= inv;
//	    dx *= 0.05; dz *= 0.05;                            // (double)0.05F
//	    if (!a.isVehicle()     && a.isPushable())     a.push(-dx, 0.0, -dz);
//	    if (!other.isVehicle() && other.isPushable()) other.push(dx, 0.0, dz);
//	}
//
// noPhysics is not modeled on a store Entity in v1 (no entity sets it — the vanilla default is
// false), so the noPhysics guard is the constant-false vanilla default (a documented deferral, like
// collision.go spectator/climbable defaults). Every numeric op mirrors the bytecode exactly.
func (t *TickLoop) doPushEntity(a, other *Entity) {
	if t.isPassengerOfSameVehicle(a, other) {
		return
	}
	// noPhysics guard (other.noPhysics || a.noPhysics): unmodeled in v1 (default false) — no-op.
	dx := other.x - a.x
	dz := other.z - a.z
	d := math.Max(math.Abs(dx), math.Abs(dz)) // Mth.absMax(dx, dz)
	if d >= entityPushEpsilon {
		d = math.Sqrt(d)
		dx /= d
		dz /= d
		inv := 1.0 / d
		if inv > 1.0 {
			inv = 1.0
		}
		dx *= inv
		dz *= inv
		dx *= entityPushForce
		dz *= entityPushForce
		if !isVehicleEntity(a) && isPushableEntity(a) {
			pushEntityImpulse(a, -dx, 0.0, -dz)
		}
		if !isVehicleEntity(other) && isPushableEntity(other) {
			pushEntityImpulse(other, dx, 0.0, dz)
		}
	}
}

// pushEntityImpulse is Entity.push(double, double, double): add the finite impulse to the entity
// deltaMovement (velocity). CITE: javap Entity.push(DDD): if all three are finite,
// setDeltaMovement(getDeltaMovement().add(x, y, z)); needsSync = true. The finite guard mirrors
// Double.isFinite on each component (a degenerate NaN/Inf impulse is dropped, exactly as vanilla).
// needsSync is the tracker dirty flag; v1 tracker re-reads velocity every tick, so the impulse
// itself is the observable effect (the flag is a cited no-op).
func pushEntityImpulse(e *Entity, x, y, z float64) {
	if !isFiniteD(x) || !isFiniteD(y) || !isFiniteD(z) {
		return
	}
	e.vx += x
	e.vy += y
	e.vz += z
}

// isFiniteD is Double.isFinite(double): not NaN and not infinite.
func isFiniteD(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// pushNearbyEntities is the 1:1 port of net.minecraft.world.entity.LivingEntity.pushEntities(),
// the crowd shove + cramming driver called from the aiStep pushEntities() slot. It gathers every
// PUSHABLE entity whose AABB overlaps this mob (the getPushableEntities broad phase), returns
// immediately when that set is empty (the pig-oracle gate — zero new RNG, zero impulse), applies
// the cramming damage when a large enough non-passenger crowd overlaps, then doPush every
// neighbour to shove the pile apart.
//
// Bytecode trace (javap LivingEntity.pushEntities, verified this session):
//
//	List<Entity> list = level.getPushableEntities(this, getBoundingBox());
//	if (list.isEmpty()) return;
//	if (level instanceof ServerLevel serverLevel) {
//	    int max = serverLevel.getGameRules().getInt(MAX_ENTITY_CRAMMING);   // default 24
//	    if (max > 0 && list.size() > max - 1 && this.random.nextInt(4) == 0) {
//	        int count = 0;
//	        for (Entity e : list) if (!e.isPassenger()) count++;
//	        if (count > max - 1) this.hurtServer(serverLevel, damageSources().cramming(), 6.0F);
//	    }
//	}
//	for (Entity e : list) this.doPush(e);
//
// The neighbour set is collected over the adjacent chunk columns (near range 1 — a mob AABB is far
// smaller than a 16-block column, so its box can only overlap entities in its own or an immediately
// adjacent column) and filtered by getPushableEntities pushableBy predicate: isPushable() AND not
// self AND the AABB actually overlaps this mob box (Level.getEntities box filter). The team-
// collision half of pushableBy (Team.CollisionRule) is the vanilla ALWAYS default — teams are not a
// v1 subsystem (getTeam() == null -> ALWAYS), so the predicate reduces to isPushable + box overlap,
// cited to the default. The list is sorted by entity id so the doPush order (and the cramming
// count) is deterministic run-to-run (the movement broadcast reproducibility contract; each doPush
// is order-independent for the impulse math, but a stable order keeps a multi-mob trace comparable).
//
// this.random is the mob per-entity seeded stream (mobRandom) — the SAME source the goals draw
// from, exactly as vanilla LivingEntity.random is the entity single random. The nextInt(4) draw
// is reached ONLY inside the cramming branch (list.size() > 23), so it never perturbs the oracle.
func (t *TickLoop) pushNearbyEntities(e *Entity) {
	list := t.pushableEntities(e)
	if len(list) == 0 {
		return // Entity list empty: return immediately (the pig-oracle gate — zero RNG, zero push).
	}

	// The ServerLevel cramming branch. The server is always a ServerLevel, so the instanceof holds.
	max := t.gameRuleInt(ruleMaxEntityCramming) // MAX_ENTITY_CRAMMING (default 24)
	if max > 0 && len(list) > max-1 && mobRandom(e).nextInt(4) == 0 {
		count := 0
		for _, other := range list {
			if !isPassengerEntity(other) { // !e.isPassenger()
				count++
			}
		}
		if count > max-1 {
			t.applyDamageEntity(e, damageSourceCramming(), crammingDamage)
		}
	}

	// doPush every pushable neighbour: the symmetric shove.
	for _, other := range list {
		t.doPushEntity(e, other)
	}
}

// isPassengerEntity is Entity.isPassenger(): getVehicle() != null — the entity is riding
// something. Reduced to the thin vehicle id (0 == not riding). CITE: javap Entity.isPassenger.
func isPassengerEntity(e *Entity) bool { return e.vehicle != 0 }

// pushableEntities is the port of Level.getPushableEntities(this, box) ==
// getEntities(this, box, EntitySelector.pushableBy(this)): every OTHER entity whose AABB overlaps
// e bounding box and passes the pushableBy predicate. It walks the tick-owned entityStore
// broad phase (near, range 1 column — a mob box cannot reach beyond an adjacent column) and filters
// to (not self) && isPushable() && box overlap. The result is sorted by id for a deterministic
// doPush order. The team-collision half of pushableBy is the ALWAYS default (no teams in v1). Runs
// on the tick goroutine (TICK-05). CITE: javap Level.getPushableEntities / EntitySelector.pushableBy.
func (t *TickLoop) pushableEntities(e *Entity) []*Entity {
	box := entityBoxOf(e)
	cands := t.cur().entities.near(e.x, e.z, 1)
	var out []*Entity
	for _, other := range cands {
		if other == e || other.id == e.id {
			continue // getEntities excludes the query entity itself
		}
		if !isPushableEntity(other) { // pushableBy: other.isPushable()
			continue
		}
		// Level.getEntities AABB box filter: the candidate box must intersect e box.
		ob := entityBoxOf(other)
		if box.Intersects(ob.MinX, ob.MinY, ob.MinZ, ob.MaxX, ob.MaxY, ob.MaxZ) {
			out = append(out, other)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}