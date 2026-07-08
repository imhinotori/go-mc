package server

// zombie_variants_test.go -- deterministic pins for the 4 zombie/skeleton variants (Drowned/Stray/Bogged/
// ZombieVillager, 1:1 javap this session). Each proves: the plugin boot-loads to the right wire type with
// the jar attributes, and the per-type SIGNATURE:
//   - Stray:          the fired arrow carries SLOWNESS 600, applied to a hit player (Stray.getArrow).
//   - Bogged:         MAX_HEALTH 16; the fired arrow carries POISON 100; shearing drops a RED_MUSHROOM.
//   - Drowned:        holding a TRIDENT, it THROWS a ThrownTrident at a target in range (performRangedAttack).
//   - ZombieVillager: the golden-apple + WEAKNESS cure starts a conversion that finishes into a Villager.
// The pig oracle (TestPluginPigEqualsGoNativePig) is UNTOUCHED (separate mobs, additive hooks).

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/plugin/host"
	"go.starlark.net/starlark"
)

// loadVanillaVariantRegistry materializes a repo-root plugins/mobs/<name> plugin into a temp dir and loads
// it through the host with the server-built declare_mob/goal builtins injected. Mirrors
// loadVanillaSkeletonRegistry. Tests run with cwd=server/, so the repo-root copy sits at ../plugins/mobs/<name>.
func loadVanillaVariantRegistry(t *testing.T, name string) *mobRegistry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s plugin dir: %v", name, err)
	}
	for _, f := range []string{"plugin.toml", "main.star"} {
		data, err := os.ReadFile(filepath.Join("..", "plugins", "mobs", name, f))
		if err != nil {
			t.Fatalf("read repo-root %s/%s: %v", name, f, err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), data, 0o644); err != nil {
			t.Fatalf("write temp %s %s: %v", name, f, err)
		}
	}
	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := m.LoadDirWith(root, extra); err != nil {
		t.Fatalf("LoadDirWith(%s): %v", name, err)
	}
	if _, ok := r.byName[name]; !ok {
		t.Fatalf("%s declaration not captured after load", name)
	}
	return r
}

// variantLoop builds a physics loop with a one-chunk stone floor and the named variant registry installed.
func variantLoop(t *testing.T, name string) (*TickLoop, int, *fakeClock) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaVariantRegistry(t, name))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())
	return loop, floorY, clock
}

func spawnVariant(loop *TickLoop, name string, x, y, z float64) *Entity {
	return loop.spawnDeclaredMob(loop.mobRegistry.byName[name], x, y, z)
}

// --- Drowned -----------------------------------------------------------------------------------

// TestDrownedSpawnDefaults: the plugin boot-loads, spawns a Drowned rendering as entity.Drowned.ID with the
// Zombie attributes (FOLLOW_RANGE 35, MOVEMENT_SPEED 0.23, ATTACK_DAMAGE 3, ARMOR 2 -- Drowned.createAttributes
// adds only STEP_HEIGHT, no combat change) and is marked isDrowned. MAX_HEALTH is the Zombie base 20.
func TestDrownedSpawnDefaults(t *testing.T) {
	loop, floorY, _ := variantLoop(t, vanillaDrownedMobName)
	d := spawnVariant(loop, vanillaDrownedMobName, 8.5, float64(floorY+1), 8.5)
	if d.typ != entity.Drowned.ID {
		t.Fatalf("drowned typ = %d, want entity.Drowned.ID %d", d.typ, entity.Drowned.ID)
	}
	if !d.isDrowned {
		t.Fatal("drowned not marked isDrowned")
	}
	if got := d.getAttributeValue(attribute.FollowRange); math.Abs(got-35.0) > 1e-9 {
		t.Fatalf("drowned FOLLOW_RANGE = %v, want 35.0", got)
	}
	if got := d.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.23) > 1e-9 {
		t.Fatalf("drowned MOVEMENT_SPEED = %v, want 0.23", got)
	}
	if got := d.getAttributeValue(attribute.AttackDamage); math.Abs(got-3.0) > 1e-9 {
		t.Fatalf("drowned ATTACK_DAMAGE = %v, want 3.0", got)
	}
	if got := d.getAttributeValue(attribute.MaxHealth); math.Abs(got-20.0) > 1e-9 {
		t.Fatalf("drowned MAX_HEALTH = %v, want 20.0 (Zombie base)", got)
	}
}

// TestDrownedThrowsTrident: a Drowned holding a TRIDENT with a target in trident range + line-of-sight throws
// a ThrownTrident (Drowned.performRangedAttack). We force the trident into its mainhand, set the target, and
// drive one drownedAiStep; a trident projectile must appear.
func TestDrownedThrowsTrident(t *testing.T) {
	loop, floorY, _ := variantLoop(t, vanillaDrownedMobName)
	by := float64(floorY + 1)
	d := spawnVariant(loop, vanillaDrownedMobName, 8.5, by, 8.5)
	d.setItemSlot(eqSlotMainHand, itemStackOf(item.Trident)) // guarantee it holds a trident
	if !drownedHoldsTrident(d) {
		t.Fatal("drowned should hold a trident after setItemSlot")
	}
	p := combatTestPlayer(loop, 12.5, by, 8.5, 8801) // ~4 blocks away, within trident radius 10
	d.ai.attackTargetID = p.entityID
	d.drownedTridentTime = 0 // ready to throw

	loop.drownedAiStep(d)

	sawTrident := false
	for _, e := range loop.only().entities.byID {
		if e.isTrident {
			sawTrident = true
		}
	}
	if !sawTrident {
		t.Fatal("drowned did NOT throw a ThrownTrident (drownedPerformRangedAttack did not fire)")
	}
	if d.drownedTridentTime != drownedTridentAttackInterval {
		t.Fatalf("drowned trident cooldown = %d, want %d after a throw", d.drownedTridentTime, drownedTridentAttackInterval)
	}
}

// TestDrownedNoTridentNoThrow: a Drowned NOT holding a trident never throws (the ranged goal is inactive).
func TestDrownedNoTridentNoThrow(t *testing.T) {
	loop, floorY, _ := variantLoop(t, vanillaDrownedMobName)
	by := float64(floorY + 1)
	d := spawnVariant(loop, vanillaDrownedMobName, 8.5, by, 8.5)
	d.setItemSlot(eqSlotMainHand, itemStackOf(item.Stick)) // NOT a trident
	p := combatTestPlayer(loop, 12.5, by, 8.5, 8802)
	d.ai.attackTargetID = p.entityID
	d.drownedTridentTime = 0

	loop.drownedAiStep(d)
	for _, e := range loop.only().entities.byID {
		if e.isTrident {
			t.Fatal("a drowned without a trident threw one (should not)")
		}
	}
}

// --- Stray -------------------------------------------------------------------------------------

// TestStraySpawnDefaults: the plugin boot-loads, spawns a Stray rendering as entity.Stray.ID with the
// Skeleton attributes (MOVEMENT_SPEED 0.25, MAX_HEALTH 20 base) holding a BOW (populateMonsterEquipment),
// marked isStray, and its variantArrowEffects carry SLOWNESS 600.
func TestStraySpawnDefaults(t *testing.T) {
	loop, floorY, _ := variantLoop(t, vanillaStrayMobName)
	s := spawnVariant(loop, vanillaStrayMobName, 8.5, float64(floorY+1), 8.5)
	if s.typ != entity.Stray.ID {
		t.Fatalf("stray typ = %d, want entity.Stray.ID %d", s.typ, entity.Stray.ID)
	}
	if !s.isStray {
		t.Fatal("stray not marked isStray")
	}
	if got := s.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.25) > 1e-12 {
		t.Fatalf("stray MOVEMENT_SPEED = %v, want 0.25", got)
	}
	if got := s.getAttributeValue(attribute.MaxHealth); math.Abs(got-20.0) > 1e-9 {
		t.Fatalf("stray MAX_HEALTH = %v, want 20.0 (Skeleton base)", got)
	}
	if !s.isHoldingItem(int32(item.Bow.ID)) {
		t.Fatal("stray does not hold a BOW (populateMonsterEquipment skeleton branch)")
	}
	fx := variantArrowEffects(s)
	if len(fx) != 1 || fx[0].id != effectSlowness || fx[0].duration != strayArrowSlownessDuration {
		t.Fatalf("stray arrow effects = %+v, want SLOWNESS %d", fx, strayArrowSlownessDuration)
	}
}

// TestStrayArrowSlowness: a Stray-fired arrow (tagged by performRangedAttack) applies SLOWNESS 600 to the
// player it hits (Stray.getArrow -> Arrow.doPostHurtEffects). We spawn a stray-tagged arrow directly into a
// player's flight path and tick until it lands.
func TestStrayArrowSlowness(t *testing.T) {
	loop, floorY, _ := variantLoop(t, vanillaStrayMobName)
	by := float64(floorY + 1)
	s := spawnVariant(loop, vanillaStrayMobName, 8.5, by, 8.5)
	p := combatTestPlayer(loop, 14.5, by, 8.5, 8810)
	p.health = 20.0

	// Fire the arrow via the real performRangedAttack (it tags the arrow with the stray's SLOWNESS effect).
	s.ai.attackTargetID = p.entityID
	loop.performRangedAttack(s, p, 1.0)

	// Tick the arrows until the arrow either hits or despawns.
	hit := false
	for i := 0; i < 200 && !hit; i++ {
		loop.tickArrows()
		if playerHasEffect(p, effectSlowness) {
			hit = true
		}
	}
	if !hit {
		t.Fatal("stray arrow did NOT apply SLOWNESS on hit (Arrow.doPostHurtEffects missing)")
	}
	e := p.activeEffects[effectSlowness]
	if e == nil || e.duration != strayArrowSlownessDuration {
		t.Fatalf("SLOWNESS effect = %+v, want duration %d", e, strayArrowSlownessDuration)
	}
}

// --- Bogged ------------------------------------------------------------------------------------

// TestBoggedSpawnDefaults: the plugin boot-loads, spawns a Bogged rendering as entity.Bogged.ID with
// MAX_HEALTH 16 (Bogged.createAttributes override) + MOVEMENT_SPEED 0.25, holding a BOW, marked isBogged,
// and its variantArrowEffects carry POISON 100.
func TestBoggedSpawnDefaults(t *testing.T) {
	loop, floorY, _ := variantLoop(t, vanillaBoggedMobName)
	b := spawnVariant(loop, vanillaBoggedMobName, 8.5, float64(floorY+1), 8.5)
	if b.typ != entity.Bogged.ID {
		t.Fatalf("bogged typ = %d, want entity.Bogged.ID %d", b.typ, entity.Bogged.ID)
	}
	if !b.isBogged {
		t.Fatal("bogged not marked isBogged")
	}
	if got := b.getAttributeValue(attribute.MaxHealth); math.Abs(got-16.0) > 1e-9 {
		t.Fatalf("bogged MAX_HEALTH = %v, want 16.0 (Bogged.createAttributes override)", got)
	}
	if math.Abs(float64(b.health)-16.0) > 1e-6 {
		t.Fatalf("bogged health = %v, want 16.0 (initSpawnHealth reads the 16 override)", b.health)
	}
	if got := b.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.25) > 1e-12 {
		t.Fatalf("bogged MOVEMENT_SPEED = %v, want 0.25", got)
	}
	if !b.isHoldingItem(int32(item.Bow.ID)) {
		t.Fatal("bogged does not hold a BOW")
	}
	fx := variantArrowEffects(b)
	if len(fx) != 1 || fx[0].id != effectPoison || fx[0].duration != boggedArrowPoisonDuration {
		t.Fatalf("bogged arrow effects = %+v, want POISON %d", fx, boggedArrowPoisonDuration)
	}
}

// TestBoggedShearDropsMushroom: shearing a ready Bogged spawns a RED_MUSHROOM item and marks it sheared;
// a second shear does nothing (already sheared).
func TestBoggedShearDropsMushroom(t *testing.T) {
	loop, floorY, _ := variantLoop(t, vanillaBoggedMobName)
	b := spawnVariant(loop, vanillaBoggedMobName, 8.5, float64(floorY+1), 8.5)
	p := combatTestPlayer(loop, 9.1, float64(floorY+1), 8.5, 8820)
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(inv.heldSlot), itemStackOf(item.Shears))

	if !loop.tryBoggedShear(p, b) {
		t.Fatal("tryBoggedShear returned false with shears in hand")
	}
	if !b.boggedSheared {
		t.Fatal("bogged not marked sheared after shear")
	}
	sawMushroom := false
	for _, e := range loop.only().entities.byID {
		if e.isItem && int32(e.itemStack.ItemID) == int32(item.RedMushroom.ID) {
			sawMushroom = true
		}
	}
	if !sawMushroom {
		t.Fatal("shearing a bogged did NOT drop a RED_MUSHROOM")
	}
	// A second shear is a bare CONSUME (already sheared) -- no new drop.
	before := len(loop.only().entities.byID)
	if !loop.tryBoggedShear(p, b) {
		t.Fatal("second tryBoggedShear returned false (should consume the shears interact)")
	}
	if got := len(loop.only().entities.byID); got != before {
		t.Fatalf("a second shear spawned a new entity (%d -> %d); an already-sheared bogged drops nothing", before, got)
	}
}

// --- ZombieVillager ----------------------------------------------------------------------------

// TestZombieVillagerSpawnDefaults: the plugin boot-loads, spawns a ZombieVillager rendering as
// entity.ZombieVillager.ID with the Zombie attributes, marked isZombieVillager and NOT converting.
func TestZombieVillagerSpawnDefaults(t *testing.T) {
	loop, floorY, _ := variantLoop(t, vanillaZombieVillagerMobName)
	z := spawnVariant(loop, vanillaZombieVillagerMobName, 8.5, float64(floorY+1), 8.5)
	if z.typ != entity.ZombieVillager.ID {
		t.Fatalf("zombie_villager typ = %d, want entity.ZombieVillager.ID %d", z.typ, entity.ZombieVillager.ID)
	}
	if !z.isZombieVillager {
		t.Fatal("zombie_villager not marked isZombieVillager")
	}
	if z.zvConverting {
		t.Fatal("a fresh zombie_villager should not be converting")
	}
	if got := z.getAttributeValue(attribute.AttackDamage); math.Abs(got-3.0) > 1e-9 {
		t.Fatalf("zombie_villager ATTACK_DAMAGE = %v, want 3.0", got)
	}
}

// TestZombieVillagerCureConversion: a golden apple on a WEAKENED zombie villager starts the cure
// (startConverting), the countdown drains, and once it reaches 0 the entity finishConversion into a Villager.
func TestZombieVillagerCureConversion(t *testing.T) {
	loop, floorY, _ := variantLoop(t, vanillaZombieVillagerMobName)
	z := spawnVariant(loop, vanillaZombieVillagerMobName, 8.5, float64(floorY+1), 8.5)
	// Give it WEAKNESS (the cure precondition) via the real entity-effect API.
	loop.addEntityEffect(z, effectWeakness, 600, 0)
	if !entityHasEffect(z, effectWeakness) {
		t.Fatal("zombie_villager should have WEAKNESS before the cure")
	}
	p := combatTestPlayer(loop, 9.1, float64(floorY+1), 8.5, 8830)
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(inv.heldSlot), itemStackOf(item.GoldenApple))

	if !loop.tryZombieVillagerCure(p, z) {
		t.Fatal("tryZombieVillagerCure returned false with a golden apple on a weakened zombie villager")
	}
	if !z.zvConverting {
		t.Fatal("zombie_villager did not start converting after the cure")
	}
	if z.zvConversionTime < zvConversionBaseTime || z.zvConversionTime >= zvConversionBaseTime+zvConversionRandCap {
		t.Fatalf("zvConversionTime = %d, want in [%d, %d)", z.zvConversionTime, zvConversionBaseTime, zvConversionBaseTime+zvConversionRandCap)
	}
	if entityHasEffect(z, effectWeakness) {
		t.Fatal("WEAKNESS should be removed on startConverting")
	}
	// Fast-forward the countdown by draining the timer to near-zero, then one aiStep finishes.
	z.zvConversionTime = 1
	loop.zombieVillagerAiStep(z)
	if z.typ != entity.Villager.ID {
		t.Fatalf("cured zombie_villager typ = %d, want entity.Villager.ID %d (finishConversion)", z.typ, entity.Villager.ID)
	}
	if z.isZombieVillager || z.zvConverting {
		t.Fatal("the cured entity should no longer be a converting zombie villager")
	}
}

// TestZombieVillagerCureNoWeakness: a golden apple WITHOUT weakness is consumed but does NOT start a cure.
func TestZombieVillagerCureNoWeakness(t *testing.T) {
	loop, floorY, _ := variantLoop(t, vanillaZombieVillagerMobName)
	z := spawnVariant(loop, vanillaZombieVillagerMobName, 8.5, float64(floorY+1), 8.5)
	p := combatTestPlayer(loop, 9.1, float64(floorY+1), 8.5, 8831)
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(inv.heldSlot), itemStackOf(item.GoldenApple))

	if !loop.tryZombieVillagerCure(p, z) {
		t.Fatal("golden apple should consume the interact even without weakness")
	}
	if z.zvConverting {
		t.Fatal("a golden apple without WEAKNESS must NOT start a cure")
	}
}

// TestZombieVillagerInfection: the Zombie.killedEntity infection helper converts a Villager to a
// ZombieVillager (NORMAL+; always on HARD). We convert a spawned villager directly through the helper.
func TestZombieVillagerInfection(t *testing.T) {
	loop, floorY, _ := variantLoop(t, vanillaZombieVillagerMobName)
	// A ZombieVillager (as the killer) + a Villager entity (mutated in place to a ZombieVillager).
	killer := spawnVariant(loop, vanillaZombieVillagerMobName, 8.5, float64(floorY+1), 8.5)
	victim := NewEntity(loop.idAlloc.AllocID(), entity.Villager, 9.5, float64(floorY+1), 9.5)
	victim.ai = &mobAI{}
	reseedMobAI(victim.ai, victim.id)

	// convertVillagerToZombieVillager (the unconditional core) turns the villager into a zombie villager.
	if !loop.convertVillagerToZombieVillager(victim) {
		t.Fatal("convertVillagerToZombieVillager returned false")
	}
	if victim.typ != entity.ZombieVillager.ID || !victim.isZombieVillager {
		t.Fatalf("infected villager typ = %d (isZombieVillager=%v), want ZombieVillager", victim.typ, victim.isZombieVillager)
	}
	// The gate helper is difficulty-driven; on NORMAL it either converts or (50%) does not, but never panics.
	_ = loop.zombieKilledVillagerInfection(killer, victim)
}
