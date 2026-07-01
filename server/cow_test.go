package server

// cow_test.go — MOB-PASS-01 (Plan 34-01): the headless behavior + interact tests for the SECOND new
// passive mob, the 1:1 vanilla Cow re-expressed as the vanilla_cow Starlark plugin (the 8-goal
// AbstractCow.registerGoals subset) plus the cow-only milking interact (AbstractCow.mobInteract: an
// empty BUCKET on an ADULT -> a MILK_BUCKET + the entity.cow.milk sound). The cow reuses the pig's
// proven goal runtime, so these are LIGHTER per-mob gates (a spawn + goal-set + behavior + the milk
// interact), NOT the pig's bit-exact Go-vs-plugin oracle (which stays untouched). The cow has NO new
// RNG goal — the milking is HOST-side and draws NO RNG (AbstractCow.mobInteract has no RNG).

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/plugin/host"
	"go.starlark.net/starlark"
)

// --- cow registry test harness -----------------------------------------------------------------

// loadVanillaCowRegistry materializes the repo-root plugins/vanilla_cow plugin into a temp dir and
// loads it through the host with the server-built declare_mob/goal builtins injected, returning the
// registry holding the captured "vanilla_cow" declaration. It mirrors the pig boot-load harness
// (loadVanillaPigRegistry) without touching the pig embed: the cow plugin is read from the canonical
// repo-root copy (the byte-identical sibling of the server/assets embed). Tests run with cwd=server/,
// so the repo-root copy sits at ../plugins/vanilla_cow.
func loadVanillaCowRegistry(t *testing.T) *mobRegistry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "vanilla_cow")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir cow plugin dir: %v", err)
	}
	for _, name := range []string{"plugin.toml", "main.star"} {
		data, err := os.ReadFile(filepath.Join("..", "plugins", "mobs", "vanilla_cow", name))
		if err != nil {
			t.Fatalf("read repo-root vanilla_cow/%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write temp cow %s: %v", name, err)
		}
	}

	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := m.LoadDirWith(root, extra); err != nil {
		t.Fatalf("LoadDirWith(vanilla_cow): %v", err)
	}
	if _, ok := r.byName["vanilla_cow"]; !ok {
		t.Fatal("vanilla_cow declaration not captured after load")
	}
	return r
}

// cowLoop builds a physics loop with a one-chunk stone floor and the vanilla_cow registry installed
// (replacing the default pig-only registry so spawnDeclaredMob can build a cow). Returns the loop, the
// floor Y, and the fake clock (for the behavior drive).
func cowLoop(t *testing.T) (*TickLoop, int, *fakeClock) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaCowRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())
	return loop, floorY, clock
}

// spawnCow spawns the declared vanilla_cow via the shared spawnDeclaredMob path (real Cow attrs + the
// 8 declared goals + a per-entity RNG stream).
func spawnCow(loop *TickLoop, x, y, z float64) *Entity {
	decl := loop.mobRegistry.byName["vanilla_cow"]
	return loop.spawnDeclaredMob(decl, x, y, z)
}

// cowBucketPlayer builds a tickPlayer at the cow holding `count` buckets in the selected hotbar slot,
// tracking the cow with a capturing client so the COW_MILK sound broadcast is observable.
func cowBucketPlayer(loop *TickLoop, cow *Entity, count int32) *tickPlayer {
	p := &tickPlayer{
		x: cow.x, y: cow.y, z: cow.z,
		center:   level.ChunkPos{0, 0},
		client:   captureClient(64),
		entityID: 99000, // a high id that never collides with the cow id
		tracked:  map[int32]bool{cow.id: true},
	}
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(inv.heldSlot), component.SlotData{Count: pk.VarInt(count), ItemID: pk.VarInt(item.Bucket.ID)})
	loop.players = append(loop.players, p)
	return p
}

// --- TestCowBootLoads ---------------------------------------------------------------------------

// TestCowBootLoads: the vanilla_cow plugin boot-loads, spawnCow builds a live cow that renders as
// entity.Cow.ID with a non-nil AI holding the 8 goals at the jar registerGoals indices @0..7.
func TestCowBootLoads(t *testing.T) {
	loop, floorY, _ := cowLoop(t)

	if _, ok := loop.mobRegistry.byName["vanilla_cow"]; !ok {
		t.Fatal("registry has no vanilla_cow declaration after boot-load")
	}

	cow := spawnCow(loop, 8.5, float64(floorY+1), 8.5)
	if cow.typ != entity.Cow.ID {
		t.Fatalf("cow typ = %d, want entity.Cow.ID %d (custom = behavior, not a new wire type)", cow.typ, entity.Cow.ID)
	}
	if cow.ai == nil {
		t.Fatal("cow has no AI")
	}
	if got := len(cow.ai.goals.goals); got != 8 {
		t.Fatalf("cow has %d goals, want 8 (FloatGoal@0 PanicGoal@1 BreedGoal@2 TemptGoal@3 FollowParentGoal@4 Stroll@5 LookAtPlayer@6 LookAround@7)", got)
	}
	// Assert exactly the 8 jar priorities 0..7 (no duplicates — the cow has ONE Tempt, no carrot).
	seen := map[int]int{}
	for _, wg := range cow.ai.goals.goals {
		seen[wg.priority]++
	}
	for _, want := range []int{0, 1, 2, 3, 4, 5, 6, 7} {
		if seen[want] != 1 {
			t.Fatalf("cow goal at priority %d appears %d times, want exactly 1 (priorities seen: %v)", want, seen[want], seen)
		}
	}

	// Attributes seeded from the declaration (Cow.createAttributes: max_health 10.0, movement_speed 0.2).
	if mh := cow.attributes.GetValue(attribute.MaxHealth.Name()); mh != 10.0 {
		t.Fatalf("cow max_health = %v, want 10.0 (Cow.createAttributes)", mh)
	}
	if ms := cow.attributes.GetValue(attribute.MovementSpeed.Name()); ms != 0.2 {
		t.Fatalf("cow movement_speed = %v, want 0.2 (Cow.createAttributes)", ms)
	}

	if _, ok := loop.only().entities.get(cow.id); !ok {
		t.Fatal("spawnCow did not add the cow to the tick-owned store")
	}
}

// --- TestMilkCow -------------------------------------------------------------------------------

// TestMilkCow: an ADULT cow + a player holding 1 bucket -> tryMilkCow returns true; the held slot
// becomes milk_bucket (1046), the bucket is consumed, and an entity.cow.milk sound (449) is broadcast
// to the tracking player. Ports net.minecraft.world.entity.animal.cow.AbstractCow.mobInteract (a single
// bucket: createFilledResult shrinks the bucket to empty -> the hand becomes the milk_bucket). NO RNG.
func TestMilkCow(t *testing.T) {
	loop, floorY, _ := cowLoop(t)
	cow := spawnCow(loop, 8.5, float64(floorY+1), 8.5)
	p := cowBucketPlayer(loop, cow, 1)
	inv := ensureInventory(p)

	if !loop.tryMilkCow(p, cow) {
		t.Fatal("tryMilkCow on an adult cow with an empty bucket must return true (the milk path)")
	}

	// createFilledResult on a single bucket: the bucket shrinks to empty, so the hand becomes the
	// milk_bucket (the return value is the filled stack, set into the hand by setItemInHand).
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if int32(held.ItemID) != int32(item.MilkBucket.ID) || held.Count != 1 {
		t.Fatalf("held after milk = item %d count %d, want milk_bucket %d count 1 (bucket->milk_bucket)", int32(held.ItemID), held.Count, item.MilkBucket.ID)
	}

	// The entity.cow.milk sound (449) is broadcast to the tracking player. SoundSource NEUTRAL (Animal).
	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundSoundEntity); n != 1 {
		t.Fatalf("milk broadcast %d ClientboundSoundEntity packets, want 1 (entity.cow.milk)", n)
	}
	var pkt pk.Packet
	for _, q := range got {
		if q.ID == int32(packetid.ClientboundSoundEntity) {
			pkt = q
		}
	}
	r := bytes.NewReader(pkt.Data)
	var soundHolder, source, entityID pk.VarInt
	var volume, pitch pk.Float
	var seed pk.Long
	if _, err := (pk.Tuple{&soundHolder, &source, &entityID, &volume, &pitch, &seed}).ReadFrom(r); err != nil {
		t.Fatalf("decode ClientboundSoundEntity: %v", err)
	}
	// Holder<SoundEvent>: registry Reference -> id + 1. entity.cow.milk == 449 -> 450 on the wire.
	if int32(soundHolder) != 449+1 {
		t.Fatalf("SoundEntity holder = %d, want %d (entity.cow.milk 449 + 1)", int32(soundHolder), 449+1)
	}
	if int32(source) != soundSourceNeutral {
		t.Fatalf("SoundEntity source = %d, want %d (SoundSource.NEUTRAL)", int32(source), soundSourceNeutral)
	}
	if int32(entityID) != cow.id {
		t.Fatalf("SoundEntity entityId = %d, want %d (the milked cow)", int32(entityID), cow.id)
	}
	if float32(volume) != 1.0 || float32(pitch) != 1.0 {
		t.Fatalf("SoundEntity volume/pitch = %v/%v, want 1.0/1.0 (player.playSound(COW_MILK, 1, 1) — NO RNG)", float32(volume), float32(pitch))
	}
}

// TestMilkCowStackShrinks: an ADULT cow + a player holding a STACK of buckets (count 2) -> the held
// bucket stack shrinks by 1 (createFilledResult: emptyStack.consume(1) leaves a non-empty bucket stack,
// so the milk is added to the inventory, the hand keeps the remaining bucket(s)).
func TestMilkCowStackShrinks(t *testing.T) {
	loop, floorY, _ := cowLoop(t)
	cow := spawnCow(loop, 8.5, float64(floorY+1), 8.5)
	p := cowBucketPlayer(loop, cow, 2)
	inv := ensureInventory(p)

	if !loop.tryMilkCow(p, cow) {
		t.Fatal("tryMilkCow on an adult cow with a bucket stack must return true")
	}

	// The held slot keeps the remaining bucket (count 2 -> 1); the milk_bucket lands elsewhere in the inv.
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if int32(held.ItemID) != int32(item.Bucket.ID) || held.Count != 1 {
		t.Fatalf("held after milk (stack) = item %d count %d, want bucket %d count 1 (consume 1)", int32(held.ItemID), held.Count, item.Bucket.ID)
	}
	// A milk_bucket must now exist somewhere in the inventory (createFilledResult's inventory.add path).
	found := false
	for i := int16(0); i < int16(playerInventorySize); i++ {
		s := inv.get(i)
		if int32(s.ItemID) == int32(item.MilkBucket.ID) && s.Count >= 1 {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("milking a bucket stack must add a milk_bucket to the inventory (createFilledResult inventory.add)")
	}
}

// --- TestMilkCowBabyFallsThrough ----------------------------------------------------------------

// TestMilkCowBabyFallsThrough: a BABY cow + a player holding a bucket -> tryMilkCow returns false
// (!isBaby gate fails), so handleInteract falls through to the feed path. The held bucket is untouched
// and NO milk sound is broadcast.
func TestMilkCowBabyFallsThrough(t *testing.T) {
	loop, floorY, _ := cowLoop(t)
	cow := spawnCow(loop, 8.5, float64(floorY+1), 8.5)
	cow.breedAge = -24000 // baby
	p := cowBucketPlayer(loop, cow, 1)
	inv := ensureInventory(p)

	if loop.tryMilkCow(p, cow) {
		t.Fatal("tryMilkCow on a BABY cow must return false (!isBaby gate -> fall through to feed)")
	}
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if int32(held.ItemID) != int32(item.Bucket.ID) || held.Count != 1 {
		t.Fatalf("a baby-milk no-op must leave the held bucket untouched: item %d count %d", int32(held.ItemID), held.Count)
	}
	if got := drainPackets(p.client); countID(got, packetid.ClientboundSoundEntity) != 0 {
		t.Fatal("a baby cow must NOT broadcast the COW_MILK sound")
	}
}

// TestMilkCowNonBucketFallsThrough: an adult cow + a player holding a non-bucket item -> tryMilkCow
// returns false (the itemStack.is(BUCKET) gate fails), falling through to the feed path.
func TestMilkCowNonBucketFallsThrough(t *testing.T) {
	loop, floorY, _ := cowLoop(t)
	cow := spawnCow(loop, 8.5, float64(floorY+1), 8.5)
	p := &tickPlayer{x: cow.x, y: cow.y, z: cow.z, center: level.ChunkPos{0, 0}, client: captureClient(64), entityID: 99001, tracked: map[int32]bool{cow.id: true}}
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(inv.heldSlot), component.SlotData{Count: 1, ItemID: pk.VarInt(item.Egg.ID)}) // a non-bucket item
	loop.players = append(loop.players, p)

	if loop.tryMilkCow(p, cow) {
		t.Fatal("tryMilkCow with a non-bucket item must return false (the is(BUCKET) gate -> fall through)")
	}
}

// --- TestCowBehavior ----------------------------------------------------------------------------

// TestCowBehavior: a spawned cow is a CREATURE (categoryOf -> CREATURE, like the pig — the jar
// EntityType.COW is MobCategory.CREATURE; data/entity Cow.Type == "creature") and its AI ticks without
// error over many ticks (the proven shared goals drive it). A smoke drive that the cow is a live,
// ticking mob.
func TestCowBehavior(t *testing.T) {
	loop, floorY, clock := cowLoop(t)

	if got := categoryOf(entity.Cow.ID); got != categoryCreature {
		t.Fatalf("categoryOf(Cow) = %v, want categoryCreature (vanilla EntityType.COW is MobCategory.CREATURE)", got)
	}

	cow := spawnCow(loop, 8.5, float64(floorY+1), 8.5)
	cow.onGround = true
	if cow.ai == nil {
		t.Fatal("cow has no AI to drive")
	}

	// Drive the AI for a stretch of ticks (the stroll/look goals fire on their RNG gates). It must not
	// panic and the cow must remain a live, in-store cow.
	for i := 0; i < 200; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
	}
	if _, ok := loop.only().entities.get(cow.id); !ok {
		t.Fatal("the cow vanished from the store after the drive")
	}
	if cow.typ != entity.Cow.ID {
		t.Fatalf("cow typ drifted to %d, want entity.Cow.ID %d", cow.typ, entity.Cow.ID)
	}
}
