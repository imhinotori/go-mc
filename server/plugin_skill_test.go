package server

// plugin_skill_test.go — SKILLS-01 (Slice 1): the declared-skill layer. Covers the load-time
// capture (skill/mechanic/targeter/condition parsed into pure data), the load-time LOUD rejections
// (unknown kinds, the capability gate), and the OBSERVABLE runtime effects: a timer skill damages +
// poisons a player in radius through the ported hurt/effect paths, and a damaged-trigger skill
// retaliates. The runtime is pure Go over decl data — no starlark.Call fires after load (the
// skills-are-data invariant), which the capture tests prove structurally (no callable is captured).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/plugin/host"
)

// skillpluginsRoot is the isolated testdata root holding ONLY the zapmob plugin (separate from
// testdata/mobplugins for the same reason wandermob is separate from testdata/plugins: each root
// loads under its own builtin set).
const skillpluginsRoot = "testdata/skillplugins"

// loadSkillRegistry loads the zapmob plugin through the FULL declaration vocabulary (builtinsDict —
// the exact seam the embed loaders use) into a fresh registry with capAll (the registry default).
func loadSkillRegistry(t *testing.T, root string) *mobRegistry {
	t.Helper()
	r := newMobRegistry()
	m := host.New()
	if err := m.LoadDirWith(root, r.builtinsDict()); err != nil {
		t.Fatalf("LoadDirWith(%s): %v", root, err)
	}
	return r
}

// loadSkillPluginExpectErr loads a one-off skill plugin body under the given capability grant and
// asserts the load FAILED loudly. Reuses writeMobPlugin (plugin_mob_test.go) for the dir layout.
func loadSkillPluginExpectErr(t *testing.T, star string, caps capSet) error {
	t.Helper()
	root := writeMobPlugin(t, star)
	r := newMobRegistry()
	r.setLoadCaps(caps)
	m := host.New()
	err := m.LoadDirWith(root, r.builtinsDict())
	if err == nil {
		t.Fatalf("expected a load error, got nil (the bad skill declaration was accepted)")
	}
	return err
}

// --- load-time capture ---------------------------------------------------------------------------

// TestDeclareSkillCaptures: loading zapmob captures the "zapper" decl with TWO skills parsed into
// pure data — the timer damage+poison skill and the damaged-retaliation skill.
func TestDeclareSkillCaptures(t *testing.T) {
	r := loadSkillRegistry(t, skillpluginsRoot)

	decl, ok := r.byName["zapper"]
	if !ok {
		t.Fatalf("no mobDecl captured under %q", "zapper")
	}
	if decl.baseType.ID != entity.Pig.ID {
		t.Fatalf("baseType = %v, want pig (%d)", decl.baseType.ID, entity.Pig.ID)
	}
	if len(decl.skills) != 2 {
		t.Fatalf("captured %d skills, want 2", len(decl.skills))
	}

	zap := decl.skills[0]
	if zap.trigger != triggerTimer || zap.interval != 10 {
		t.Fatalf("skill[0] = trigger %d interval %d, want timer/10", zap.trigger, zap.interval)
	}
	if zap.chance != 1.0 {
		t.Fatalf("skill[0] chance = %v, want the 1.0 default (no RNG draw)", zap.chance)
	}
	if zap.targeter.kind != "players_in_radius" || zap.targeter.radius != 10.0 {
		t.Fatalf("skill[0] targeter = %+v, want players_in_radius r=10", zap.targeter)
	}
	if len(zap.mechanics) != 2 {
		t.Fatalf("skill[0] has %d mechanics, want 2", len(zap.mechanics))
	}
	if m := zap.mechanics[0]; m.kind != "damage" || m.amount != 5.0 {
		t.Fatalf("skill[0].mechanics[0] = %+v, want damage 5.0", m)
	}
	if m := zap.mechanics[1]; m.kind != "effect" || m.effect != effectPoison || m.duration != 100 || m.amplifier != 0 {
		t.Fatalf("skill[0].mechanics[1] = %+v, want poison/100/0 (id normalized to minecraft:poison)", m)
	}

	ret := decl.skills[1]
	if ret.trigger != triggerDamaged {
		t.Fatalf("skill[1] trigger = %d, want damaged", ret.trigger)
	}
	if ret.targeter.kind != "nearest_player" || ret.targeter.radius != 16.0 {
		t.Fatalf("skill[1] targeter = %+v, want nearest_player r=16", ret.targeter)
	}
}

// --- load-time rejections --------------------------------------------------------------------------

// TestSkillRejectsUnknownMechanic: an unknown mechanic kind is a loud LOAD error.
func TestSkillRejectsUnknownMechanic(t *testing.T) {
	star := `
declare_mob(name="x", base_type="pig", skills=[
    skill(trigger="timer", interval=5, targeter=targeter("self"),
          mechanics=[mechanic("lightning", amount=1.0)]),
])
`
	err := loadSkillPluginExpectErr(t, star, capAll)
	if !contains(err.Error(), "unknown kind") {
		t.Fatalf("error %q does not mention the unknown mechanic kind", err.Error())
	}
}

// TestSkillRejectsMissingCapability: mechanic("damage") under a grant WITHOUT skills.damage is a
// loud LOAD error naming the missing capability — the load-time LOCKED-pattern enforcement.
func TestSkillRejectsMissingCapability(t *testing.T) {
	star := `
declare_mob(name="x", base_type="pig", skills=[
    skill(trigger="timer", interval=5, targeter=targeter("self"),
          mechanics=[mechanic("damage", amount=1.0)]),
])
`
	err := loadSkillPluginExpectErr(t, star, capEntitiesRead|capEntitiesWrite|capNav)
	if !contains(err.Error(), "skills.damage") {
		t.Fatalf("error %q does not name the missing skills.damage capability", err.Error())
	}
}

// TestSkillRejectsBadTrigger: an unknown trigger + a timer without interval are loud LOAD errors.
func TestSkillRejectsBadTrigger(t *testing.T) {
	star := `
declare_mob(name="x", base_type="pig", skills=[
    skill(trigger="full_moon", targeter=targeter("self"),
          mechanics=[mechanic("damage", amount=1.0)]),
])
`
	err := loadSkillPluginExpectErr(t, star, capAll)
	if !contains(err.Error(), "unknown trigger") {
		t.Fatalf("error %q does not mention the unknown trigger", err.Error())
	}

	star2 := `
declare_mob(name="x", base_type="pig", skills=[
    skill(trigger="timer", targeter=targeter("self"),
          mechanics=[mechanic("damage", amount=1.0)]),
])
`
	err2 := loadSkillPluginExpectErr(t, star2, capAll)
	if !contains(err2.Error(), "interval") {
		t.Fatalf("error %q does not mention the missing timer interval", err2.Error())
	}
}

// TestSkillRejectsUnknownEffect: an effect outside the implemented set is a loud LOAD error (never a
// silently-inert buff).
func TestSkillRejectsUnknownEffect(t *testing.T) {
	star := `
declare_mob(name="x", base_type="pig", skills=[
    skill(trigger="timer", interval=5, targeter=targeter("self"),
          mechanics=[mechanic("effect", effect="levitation_v2", duration=10)]),
])
`
	err := loadSkillPluginExpectErr(t, star, capAll)
	if !contains(err.Error(), "unknown effect") {
		t.Fatalf("error %q does not mention the unknown effect", err.Error())
	}
}

// --- runtime: the observable effects ----------------------------------------------------------------

// spawnZapper spawns the zapmob decl on a real floor and returns the live entity.
func spawnZapper(t *testing.T) (*TickLoop, *Entity) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)

	r := loadSkillRegistry(t, skillpluginsRoot)
	decl := r.byName["zapper"]
	e := loop.spawnDeclaredMob(decl, 2.5, float64(floorY+1), 2.5)
	if e.skills == nil {
		t.Fatalf("spawnDeclaredMob did not attach a skillRunner to a skill-bearing decl")
	}
	return loop, e
}

// TestSkillTimerFiresDamageAndEffect: the ~onTimer:10 @PlayersInRadius{r=10} skill fires on the 10th
// tick — the near player takes the damage mechanic (through the FULL ported applyDamage path) AND
// carries the poison effect; a far player is untouched. Ticks 1-9 fire nothing (the countdown).
func TestSkillTimerFiresDamageAndEffect(t *testing.T) {
	loop, e := spawnZapper(t)

	near := &tickPlayer{entityID: 9001, x: 5.5, y: 65, z: 2.5, health: 20, client: captureClient(64)}
	far := &tickPlayer{entityID: 9002, x: 200.5, y: 65, z: 200.5, health: 20, client: captureClient(64)}
	loop.players = append(loop.players, near, far)

	for i := 0; i < 9; i++ {
		loop.tickMobSkills(e)
	}
	if near.health != 20 {
		t.Fatalf("timer skill fired early: near player health = %v after 9 ticks, want 20", near.health)
	}

	loop.tickMobSkills(e) // the 10th tick — the timer comes due
	if near.health != 15 {
		t.Fatalf("near player health = %v after the timer fire, want 15 (damage 5 through applyDamage)", near.health)
	}
	if _, ok := near.activeEffects[effectPoison]; !ok {
		t.Fatalf("near player does not carry %q after the timer fire", effectPoison)
	}
	if far.health != 20 || len(far.activeEffects) != 0 {
		t.Fatalf("far player was affected (health %v, %d effects) — outside the r=10 targeter", far.health, len(far.activeEffects))
	}

	// The timer re-arms: the NEXT fire is 10 ticks later, not immediate.
	loop.tickMobSkills(e)
	if _, ok := near.activeEffects[effectPoison]; !ok {
		t.Fatalf("poison expired unexpectedly")
	}
	if near.health != 15 {
		t.Fatalf("near player health = %v one tick after the fire, want 15 (the timer must re-arm)", near.health)
	}
}

// TestSkillDamagedTriggerRetaliates: hitting the zapper fires its ~onDamaged @NearestPlayer skill —
// the attacker (nearest player) takes the retaliation damage. Also proves the reentrancy guard: the
// retaliation's own damage cannot cascade (the victim is a player; and a self-damaging skill on the
// caster would be dropped by skillRunner.firing).
func TestSkillDamagedTriggerRetaliates(t *testing.T) {
	loop, e := spawnZapper(t)

	p := &tickPlayer{entityID: 9101, x: 4.5, y: 65, z: 2.5, health: 20, client: captureClient(64)}
	loop.players = append(loop.players, p)

	loop.applyDamageEntity(e, damageSourcePlayerAttack(p.entityID), 2.0)

	if e.health >= 30 {
		t.Fatalf("the zapper took no damage (health %v) — the hit itself must land first", e.health)
	}
	if p.health != 18 {
		t.Fatalf("player health = %v after hitting the zapper, want 18 (the damaged-trigger retaliation, damage 2)", p.health)
	}
}

// TestSkillDeathTriggerSurvivorGate: a LETHAL hit must NOT fire the "damaged" trigger (the survivor
// gate) — the zapper declares no death skill, so the player only ever takes the retaliation from
// NON-lethal hits.
func TestSkillDeathTriggerSurvivorGate(t *testing.T) {
	loop, e := spawnZapper(t)

	p := &tickPlayer{entityID: 9201, x: 4.5, y: 65, z: 2.5, health: 20, client: captureClient(64)}
	loop.players = append(loop.players, p)

	// One overwhelming hit: 100 >= max_health 30 — the mob dies on this hit.
	loop.applyDamageEntity(e, damageSourcePlayerAttack(p.entityID), 100.0)

	if !e.dead {
		t.Fatalf("the zapper survived a 100-damage hit (health %v)", e.health)
	}
	if p.health != 20 {
		t.Fatalf("player health = %v after the lethal hit, want 20 (the damaged trigger must NOT fire on a kill)", p.health)
	}
}

// TestVanillaMobHasNoSkillRunner: a skill-less declaration (the wander mob) spawns WITHOUT a
// skillRunner — the zero-cost contract every vanilla mob (and the pig oracle) relies on.
func TestVanillaMobHasNoSkillRunner(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, 64)

	r := loadMobRegistry(t, mobpluginsRoot) // the wander mob — declares goals, NO skills
	decl := r.byName["wanderer"]
	if decl.skills != nil {
		t.Fatalf("the wander mob captured %d skills, want none", len(decl.skills))
	}
	e := loop.spawnDeclaredMob(decl, 2.5, 65, 2.5)
	if e.skills != nil {
		t.Fatalf("a skill-less declaration spawned WITH a skillRunner — the zero-cost gate is broken")
	}
}
