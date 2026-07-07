package server

// mob_skills.go — SKILLS-01 (the MythicMobs-class runtime, Slice 1): the Go HOT-PATH interpreter for
// the skill declarations plugin_skill_decl.go captured at load. A skill is PURE DATA — this file
// burns ZERO starlark.Calls, idle or active (stronger than the goal path, which calls back into a
// frozen callable while running). Triggers:
//
//   - timer   — tickMobSkills, called from the tick_phases per-mob loop gated `e.skills != nil`
//     (one nil-check per skill-less mob — the pig oracle pays nothing else)
//   - spawn   — spawnDeclaredMob, once, after the store add
//   - damaged — applyDamageEntity tail, SURVIVOR only (the MythicMobs ~onDamaged analogue)
//   - death   — dieEntity, after the loot roll + status-3 broadcast (~onDeath)
//
// Mechanics route through the EXISTING jar-faithful subsystems — damage through the ported
// applyDamage (player victim, combat.go) / applyDamageEntity (mob victim, combat_mob.go) with
// damageSourceMobAttack(caster) so armor/i-frames/knock-on effects stay vanilla-exact; effects
// through addPlayerEffect / addEntityEffect (mob_effect.go). The skill layer is NEW Sulfur surface
// (free of the 1:1 mandate — design doc); the math underneath is the untouched vanilla port.
//
// RNG DISCIPLINE: the ONLY draw is the optional `chance` gate, drawn on the CASTER's per-entity
// seeded stream (mobRandom(e)) and ONLY when chance < 1.0 — a chance-1.0 skill draws nothing. No
// vanilla mob declares skills, so no vanilla stream (the pig oracle) ever gains a draw.
//
// SINGLE-OWNER (TICK-05): every entry point runs on the owning region's goroutine (the mob tick
// loop / the hurt+death paths, all region-scoped). Targeters resolve against the OWNING region's
// store (regionForEntity, the handle discipline) and the loop-level t.players (the same reads
// nearestPlayerAt / nearestPlayerWithin already perform from goal callbacks).

import "github.com/imhinotori/sulfur/level/attribute"

// skillRunner is the per-mob mutable skill state: the countdowns for the decl's timer skills + the
// reentrancy guard. The decl (and its skills slice) is SHARED immutable data; only this struct is
// per-mob (the skillDecl/starlarkGoal split, Pattern 4's data analogue). Tick-owned.
type skillRunner struct {
	decl *mobDecl
	// timers holds one countdown per decl.skills entry (indexed in parallel). Only the timer-trigger
	// entries are ever read; the rest stay 0. A timer fires when its countdown reaches 0, then re-arms
	// to the declared interval — the first fire lands `interval` ticks after spawn.
	timers []int
	// firing guards against re-entrant trigger cascades: a skill mechanic that damages the CASTER
	// itself would re-enter fireMobSkillTrigger(damaged) mid-execution (damage -> applyDamageEntity ->
	// damaged trigger -> damage -> ...). While a skill executes, nested trigger fires on the same mob
	// are dropped (the MythicMobs skill-tree analogue would queue; slice 1 drops — documented).
	firing bool
}

// newSkillRunner builds the per-mob runner over a shared declaration, arming each timer skill to its
// declared interval. Called from spawnDeclaredMob (tick goroutine, before the mob ticks).
func newSkillRunner(decl *mobDecl) *skillRunner {
	r := &skillRunner{decl: decl, timers: make([]int, len(decl.skills))}
	for i := range decl.skills {
		if decl.skills[i].trigger == triggerTimer {
			r.timers[i] = decl.skills[i].interval
		}
	}
	return r
}

// tickMobSkills advances the mob's timer skills one tick and fires any that come due. Called once
// per live skill-bearing mob per tick from the tick_phases mob loop (gated `e.skills != nil` at the
// call site, so a skill-less mob pays exactly one nil-check).
func (t *TickLoop) tickMobSkills(e *Entity) {
	r := e.skills
	if r == nil || e.dead {
		return
	}
	for i := range r.decl.skills {
		s := &r.decl.skills[i]
		if s.trigger != triggerTimer {
			continue
		}
		r.timers[i]--
		if r.timers[i] > 0 {
			continue
		}
		r.timers[i] = s.interval // re-arm BEFORE executing (a mechanic that kills the caster stops future ticks via e.dead)
		t.execMobSkill(e, s)
	}
}

// fireMobSkillTrigger fires every declared skill bound to a DISCRETE trigger (spawn/damaged/death).
// Cheap no-op for a mob without a runner; reentrancy-guarded (see skillRunner.firing).
func (t *TickLoop) fireMobSkillTrigger(e *Entity, trig skillTrigger) {
	r := e.skills
	if r == nil || r.firing {
		return
	}
	for i := range r.decl.skills {
		s := &r.decl.skills[i]
		if s.trigger != trig {
			continue
		}
		t.execMobSkill(e, s)
	}
}

// execMobSkill runs one skill for one caster: conditions (AND-ed) -> chance gate -> targeter
// resolve -> mechanics in declared order per target. The firing guard wraps the whole execution so
// a mechanic's side effects (self-damage) cannot cascade back into this mob's triggers.
func (t *TickLoop) execMobSkill(e *Entity, s *skillDecl) {
	r := e.skills
	if r == nil {
		return
	}
	r.firing = true
	defer func() { r.firing = false }()

	for i := range s.conditions {
		if !t.skillConditionHolds(e, &s.conditions[i]) {
			return
		}
	}
	// The chance gate: drawn on the CASTER's own per-entity stream, and ONLY when < 1.0 (a sure skill
	// draws nothing — no vanilla stream is ever perturbed by an undeclared feature).
	if s.chance < 1.0 {
		if float64(mobRandom(e).nextFloat()) >= s.chance {
			return
		}
	}

	players, mobs := t.resolveSkillTargets(e, &s.targeter)
	for i := range s.mechanics {
		m := &s.mechanics[i]
		for _, p := range players {
			t.applySkillMechanicToPlayer(e, m, p)
		}
		for _, victim := range mobs {
			t.applySkillMechanicToMob(e, m, victim)
		}
	}
}

// skillConditionHolds evaluates one condition against the CASTER. Unknown kinds cannot reach here
// (rejected at load); the switch is exhaustive over the slice-1 set.
func (t *TickLoop) skillConditionHolds(e *Entity, c *conditionDecl) bool {
	switch c.kind {
	case "health_below":
		max := float32(entityMaxHealth(e))
		if max <= 0 {
			return false
		}
		return e.health < float32(c.value)*max
	default:
		return false // unreachable (load-validated); fail closed
	}
}

// resolveSkillTargets resolves a targeter to its player + mob target lists. Players come from the
// loop-level t.players (the same tick-owned read nearestPlayerAt performs); mobs from the OWNING
// region's store (the handle discipline: regionForEntity, falling back to the current region).
// Dead/removed targets are excluded; the caster never targets itself except via "self".
func (t *TickLoop) resolveSkillTargets(e *Entity, tg *targeterDecl) ([]*tickPlayer, []*Entity) {
	switch tg.kind {
	case "self":
		return nil, []*Entity{e}
	case "nearest_player":
		if p := t.nearestLivePlayer(e.x, e.y, e.z, tg.radius); p != nil {
			return []*tickPlayer{p}, nil
		}
		return nil, nil
	case "players_in_radius":
		return t.livePlayersWithin(e.x, e.y, e.z, tg.radius), nil
	case "mobs_in_radius":
		return nil, t.livingMobsWithin(e, tg.radius)
	default:
		return nil, nil // unreachable (load-validated); fail closed
	}
}

// nearestLivePlayer returns the nearest non-dead player within maxDist of (cx,cy,cz), or nil — the
// skill-target sibling of nearestPlayerAt (which returns a position, not the player).
func (t *TickLoop) nearestLivePlayer(cx, cy, cz, maxDist float64) *tickPlayer {
	best := maxDist * maxDist
	var found *tickPlayer
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		dx, dy, dz := p.x-cx, p.y-cy, p.z-cz
		if d2 := dx*dx + dy*dy + dz*dz; d2 <= best {
			best = d2
			found = p
		}
	}
	return found
}

// livePlayersWithin returns every non-dead player within radius of (cx,cy,cz).
func (t *TickLoop) livePlayersWithin(cx, cy, cz, radius float64) []*tickPlayer {
	r2 := radius * radius
	var out []*tickPlayer
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		dx, dy, dz := p.x-cx, p.y-cy, p.z-cz
		if dx*dx+dy*dy+dz*dz <= r2 {
			out = append(out, p)
		}
	}
	return out
}

// livingMobsWithin returns every LIVING mob (attribute-backed, non-dead, non-item) within radius of
// the caster, excluding the caster itself. Resolved against the caster's OWNING region's store —
// the same single-owner discipline entities_near enforces (a region tick must not touch another
// region's store).
func (t *TickLoop) livingMobsWithin(e *Entity, radius float64) []*Entity {
	region := t.regionForEntity(e)
	if region == nil {
		region = t.cur()
	}
	if region == nil {
		return nil
	}
	r2 := radius * radius
	var out []*Entity
	for _, v := range region.entities.byID {
		if v == nil || v.id == e.id || v.dead || v.attributes == nil {
			continue
		}
		dx, dy, dz := v.x-e.x, v.y-e.y, v.z-e.z
		if dx*dx+dy*dy+dz*dz <= r2 {
			out = append(out, v)
		}
	}
	return out
}

// applySkillMechanicToPlayer applies one mechanic to a PLAYER target through the ported vanilla
// paths: damage -> applyDamage (the full hurtServer port: i-frames, armor, absorption), attributed
// to the caster via damageSourceMobAttack (the same source Mob.doHurtTarget builds for a no-weapon
// mob); effect -> addPlayerEffect (full modifier/tick semantics, owner-attributed).
func (t *TickLoop) applySkillMechanicToPlayer(caster *Entity, m *mechanicDecl, p *tickPlayer) {
	switch m.kind {
	case "damage":
		t.applyDamage(p, damageSourceMobAttack(caster.id), float32(m.amount))
	case "effect":
		t.addPlayerEffect(p, caster.id, m.effect, m.duration, m.amplifier, 1.0)
	}
}

// applySkillMechanicToMob applies one mechanic to a MOB target (including the caster via "self"):
// damage -> applyDamageEntity (the mob hurtServer port); effect -> addEntityEffect (add + instant
// semantics; duration TICKING for arbitrary mobs is design-doc P6 — today only the witch lane ticks
// mobEffects, so a duration effect on a mob target registers but does not yet count down).
func (t *TickLoop) applySkillMechanicToMob(caster *Entity, m *mechanicDecl, victim *Entity) {
	switch m.kind {
	case "damage":
		t.applyDamageEntity(victim, damageSourceMobAttack(caster.id), float32(m.amount))
	case "effect":
		t.addEntityEffect(victim, m.effect, m.duration, m.amplifier)
	}
}

// entityMaxHealth reads the mob's folded MaxHealth (nil-safe via getAttributeValue): the
// health_below denominator.
func entityMaxHealth(e *Entity) float64 {
	if e == nil {
		return 0
	}
	return e.getAttributeValue(attribute.MaxHealth)
}
