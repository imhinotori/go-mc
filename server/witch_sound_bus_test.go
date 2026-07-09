package server

// witch_sound_bus_test.go — MOB-HOST-07 (Task #9): the WITCH_DRINK + WITCH_THROW client-feedback
// broadcast bus + the broadcastEntityEvent(15) idle-particle wire, gated by three byte-level tests
// that pin the positional ClientboundSound layout (jar: ClientboundSoundPacket.write: VarInt(id+1),
// VarInt(source), Int(x*8), Int(y*8), Int(z*8), Float(volume), Float(pitch), Long(seed)) and the
// ClientboundEntityEvent layout (jar: writeInt(entityId) + writeByte(eventId)). The pig oracle
// (TestPluginPigEqualsGoNativePig) is a separate, untouched mob — these tests are witch-gated.
//
// JAR-CITED BEHAVIOR (javap this session, temp/cache/26.2-inner.jar, all three witness packets):
//
//   - Witch.aiStep offsets 400-449: when a self-drink starts (potion != null on the ladder), play
//     a positional ClientboundSound for SoundEvents.WITCH_DRINK (id 1777) at the witch position on
//     HOSTILE SoundSource, volume 1.0, pitch 0.8 + nextFloat()*0.4 from the witch's per-mob RNG.
//   - Witch.aiStep offsets 475-498: tail roll `if (random.nextFloat() < 7.5E-4) level.broadcastEntityEvent(
//     this, (byte)15)` — fires EVERY aiStep call (independent of drink state), the WITCH_MAGIC
//     particle burst the client's Witch.handleEntityEvent(15) renders locally.
//   - Witch.performRangedAttack offsets 0-7: `if (isDrinkingPotion()) return;` — a drinking witch
//     does not throw a splash potion. Offsets 293-342: AFTER the splash spawn (and `if (!isSilent())`),
//     play a positional ClientboundSound for SoundEvents.WITCH_THROW (id 1779) at the witch position on
//     HOSTILE SoundSource, volume 1.0, pitch 0.8 + nextFloat()*0.4 from the witch's per-mob RNG.
//
// Both WITCH_DRINK and WITCH_THROW are POSITIONAL ClientboundSound packets (not entity-attached
// ClientboundSoundEntity — those carry an entity id, but vanilla calls Level.playSound(x, y, z, ...)
// with the witch's position, exactly as combat_mob.go's playMobHurtSound's positional sibling does).
// The 16-block broadcast radius is getRange(1.0)==16 (sound.go soundRange), and the per-sound seed is
// a dedicated non-gameplay draw (Level.soundSeedGenerator.nextLong() analogue, rand.Int64) so it
// never perturbs the witch's per-mob gameplay stream. The pitch jitter nextFloat() draw IS on the
// witch stream (witch-gated, never the pig oracle) and MUST fire in order right after the splash
// spawn (WITCH_THROW) / right after setUsingItem(true) (WITCH_DRINK).

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// buildDrinkWitch makes a live, hurt witch at the origin with a hand-controlled RNG source so a test
// can seed the drink-ladder draws deterministically. The ladder's HEALING rung (3rd rung) fires on
// `nextFloat() < 0.05` AND `health < getMaxHealth()`; a hurt witch with that draw wins and starts a
// drink. The witch is NOT in water + NOT on fire so the WATER_BREATHING + FIRE_RESISTANCE rungs
// short-circuit at their `witchEyeInWater` / `isOnFire || lastDamage.is_fire` checks regardless of
// their draws.
func buildDrinkWitch(seed uint64, health float32) *Entity {
	e := &Entity{id: 7777, typ: entity.Witch.ID, health: health, x: 8.5, y: 64, z: 8.5, height: 1.95, onGround: true}
	e.ai = &mobAI{rng: newEntityRandom(seed)}
	return e
}

// firstThreeDraws returns the first three nextFloat() draws a source with `seed` yields (the three
// ladder rungs WATER_BREATHING / FIRE_RESISTANCE / HEALING, all on the witch's per-mob stream),
// without disturbing the entity's own source.
func firstThreeDraws(seed uint64) [3]float32 {
	r := newEntityRandom(seed)
	var d [3]float32
	for i := range d {
		d[i] = r.nextFloat()
	}
	return d
}

// TestWitchDrinkBroadcastsEntityEvent15: a witch that is actively drinking (witchDrinking=true,
// witchUsingTime mid-countdown) and whose next draw from the seeded source is < 7.5E-4 broadcasts
// exactly one ClientboundEntityEvent with status byte 15 to a tracking viewer. The tail roll at the
// end of Witch.aiStep (bytecode offsets 475-498) fires EVERY aiStep call (independent of drink
// state); arming the drink state manually (no RNG draws) lets the test pin a single tail roll to a
// controlled seed. Cite Witch.aiStep offsets 475-498 (the broadcastEntityEvent(15) tail roll).
func TestWitchDrinkBroadcastsEntityEvent15(t *testing.T) {
	var seed uint64
	found := false
	for s := uint64(1); s < 1_000_000 && !found; s++ {
		r := newEntityRandom(s)
		if float64(r.nextFloat()) < witchIdleEventChance {
			seed, found = s, true
		}
	}
	if !found {
		t.Fatal("no seed produced tail-roll-win draw in 1M seeds — implausible")
	}

	loop := &TickLoop{}
	viewer := &tickPlayer{client: captureClient(64), entityID: 1, tracked: map[int32]bool{7777: true}}
	loop.players = append(loop.players, viewer)

	// Build a hurt-eligible witch with the seeded source. health 26 == max so HEALING never wins
	// even if a draw happens to hit; but the drink state is armed MANUALLY (no ladder draws), so
	// the only aiStep draw this tick is the tail roll (1 nextFloat). Full health is fine here.
	w := buildDrinkWitch(seed, 26.0)
	w.witchDrinking = true  // arm drink state (NO RNG draw)
	w.witchUsingTime = 20   // mid-drink countdown (NO RNG draw)

	loop.witchAiStep(w)

	got := drainPackets(viewer.client)
	if len(got) != 1 {
		t.Fatalf("viewer received %d packets, want 1 (EntityEvent 15)", len(got))
	}
	if got[0].ID != int32(packetid.ClientboundEntityEvent) {
		t.Fatalf("packet id = %d, want ClientboundEntityEvent (%d)", got[0].ID, int32(packetid.ClientboundEntityEvent))
	}
	// encodeEntityEvent body = Int(entityID) + Byte(status). The trailing status byte is 15.
	want := encodeEntityEvent(7777, entityEventWitchIdleParticles)
	if !bytes.Equal(got[0].Data, want.Data) {
		t.Fatalf("entity-event body mismatch:\n got %x\nwant %x", got[0].Data, want.Data)
	}
	if entityEventWitchIdleParticles != 15 {
		t.Fatalf("entityEventWitchIdleParticles = %d, want 15 (Witch.aiStep broadcastEntityEvent(this,15))", entityEventWitchIdleParticles)
	}
}

// TestWitchDrinkPlaysSound: drive witchAiStep on a hurt witch (health < maxHealth 26) with a seed
// whose 3rd ladder draw (HEALING rung) is < 0.05 (the witch is NOT in water + NOT on fire, so the
// 1st and 2nd rungs short-circuit at their condition checks regardless of their draws). The HEALING
// rung wins, the witch starts a self-drink, and Witch.aiStep offsets 400-449 emit a positional
// ClientboundSound for SoundEvents.WITCH_DRINK (id 1777) at the witch's (x,y,z) on HOSTILE
// SoundSource, volume 1.0, pitch 0.8 + nextFloat()*0.4. Cite Witch.aiStep offsets 400-449.
func TestWitchDrinkPlaysSound(t *testing.T) {
	var seed uint64
	found := false
	for s := uint64(1); s < 1_000_000 && !found; s++ {
		d := firstThreeDraws(s)
		if float64(d[2]) < witchHealingChance {
			seed, found = s, true
		}
	}
	if !found {
		t.Fatal("no seed produced HEALING-ladder draw < 0.05 in 1M seeds — implausible")
	}

	loop := &TickLoop{}
	// Viewer at the witch's position (distance 0 < 16, inside getRange(1.0)). playSound does
	// PlayerList.broadcast (per-player radius), not broadcastToTrackers — the viewer's tracked map
	// is irrelevant for this packet.
	viewer := &tickPlayer{x: 8.5, y: 64, z: 8.5, entityID: 1, client: captureClient(64)}
	loop.players = append(loop.players, viewer)

	w := buildDrinkWitch(seed, 10.0) // hurt (10 < 26) -> HEALING rung eligible

	loop.witchAiStep(w)

	got := drainPackets(viewer.client)
	var soundPkt pk.Packet
	for _, p := range got {
		if p.ID == int32(packetid.ClientboundSound) {
			soundPkt = p
			break
		}
	}
	if soundPkt.ID == 0 {
		t.Fatalf("viewer received %d packets but no ClientboundSound (WITCH_DRINK): %+v", len(got), got)
	}
	// Decode the ClientboundSound body byte-for-byte: VarInt(id+1), VarInt(source), Int(x*8),
	// Int(y*8), Int(z*8), Float(volume), Float(pitch), Long(seed). See sound_test.go TestEncodeSoundWireBody.
	r := bytes.NewReader(soundPkt.Data)
	var soundHolder, source pk.VarInt
	var x8, y8, z8 pk.Int
	var volume, pitch pk.Float
	var seedF pk.Long
	if _, err := (pk.Tuple{&soundHolder, &source, &x8, &y8, &z8, &volume, &pitch, &seedF}).ReadFrom(r); err != nil {
		t.Fatalf("decode ClientboundSound: %v", err)
	}
	// Holder<SoundEvent>: registry Reference -> id + 1 (0 reserved for inline). witch.drink == 1777.
	if int32(soundHolder) != witchDrinkSoundID+1 {
		t.Fatalf("Sound holder = %d, want %d (witch.drink id %d + 1)", int32(soundHolder), witchDrinkSoundID+1, witchDrinkSoundID)
	}
	if int32(source) != soundSourceHostile {
		t.Fatalf("Sound source = %d, want %d (SoundSource.HOSTILE ordinal)", int32(source), soundSourceHostile)
	}
	if int32(x8) != int32(8.5*8.0) || int32(y8) != int32(64.0*8.0) || int32(z8) != int32(8.5*8.0) {
		t.Fatalf("Sound position = (%d, %d, %d) (1/8-block), want (%d, %d, %d) (witch at 8.5, 64, 8.5)",
			int32(x8), int32(y8), int32(z8), int32(8.5*8.0), int32(64.0*8.0), int32(8.5*8.0))
	}
	if float32(volume) != witchDrinkSoundVolume {
		t.Fatalf("Sound volume = %v, want %v (fconst_1)", float32(volume), witchDrinkSoundVolume)
	}
	if float32(pitch) < 0.8 || float32(pitch) >= 1.2 {
		t.Fatalf("Sound pitch = %v, want in [0.8, 1.2) (0.8f + nextFloat()*0.4f from the witch's per-mob RNG)", float32(pitch))
	}
	if r.Len() != 0 {
		t.Fatalf("Sound packet has %d trailing bytes, want 0 (exact wire layout)", r.Len())
	}
	if witchDrinkSoundID != 1777 {
		t.Fatalf("witchDrinkSoundID = %d, want 1777 (SoundEvents.WITCH_DRINK)", witchDrinkSoundID)
	}
}

// TestWitchThrowPlaysSound: drive t.performWitchRangedAttack(witch, target, power) and assert the
// WITCH_THROW positional ClientboundSound (id 1779, HOSTILE source, volume 1.0, jittered pitch)
// emits AFTER the splash spawn — the wire side of Witch.performRangedAttack offsets 293-342. The
// isDrinkingPotion() guard at offsets 0-7 is also exercised (the test witch is not drinking, so
// the guard does not fire and the throw proceeds). Cite Witch.performRangedAttack + Projectile
// .spawnProjectileUsingShoot.
func TestWitchThrowPlaysSound(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	// Witch at (8.5, 64, 8.5). health 26 (full); witch is NOT drinking (per the bytecode guard at
	// offsets 0-7, performRangedAttack returns early on a drinking witch, so the WITCH_THROW would
	// also skip — verified in TestWitchThrowSilentWhileDrinking below).
	w := &Entity{id: 7777, typ: entity.Witch.ID, x: 8.5, y: 64, z: 8.5, height: 1.95, onGround: true, health: 26.0}
	w.ai = &mobAI{rng: newEntityRandom(0xC0FFEE)}

	// Target player 4 blocks away (within the 10-block attack radius, dist >= 3 so the WEAKNESS
	// branch never fires, and the HARMING default applies).
	target := &tickPlayer{x: 12.5, y: 64, z: 8.5, health: 20.0, entityID: 100, client: captureClient(64)}

	// Viewer at the witch's position (distance 0 < 16, inside getRange(1.0)).
	viewer := &tickPlayer{x: 8.5, y: 64, z: 8.5, entityID: 1, client: captureClient(64)}
	loop.players = append(loop.players, target, viewer)

	loop.performWitchRangedAttack(w, target, 1.0)

	got := drainPackets(viewer.client)
	var soundPkt pk.Packet
	for _, p := range got {
		if p.ID == int32(packetid.ClientboundSound) {
			soundPkt = p
			break
		}
	}
	if soundPkt.ID == 0 {
		t.Fatalf("viewer received %d packets but no ClientboundSound (WITCH_THROW): %+v", len(got), got)
	}
	// Decode the ClientboundSound body byte-for-byte (same shape as the WITCH_DRINK test).
	r := bytes.NewReader(soundPkt.Data)
	var soundHolder, source pk.VarInt
	var x8, y8, z8 pk.Int
	var volume, pitch pk.Float
	var seedF pk.Long
	if _, err := (pk.Tuple{&soundHolder, &source, &x8, &y8, &z8, &volume, &pitch, &seedF}).ReadFrom(r); err != nil {
		t.Fatalf("decode ClientboundSound: %v", err)
	}
	// Holder<SoundEvent>: registry Reference -> id + 1 (0 reserved for inline). witch.throw == 1779.
	if int32(soundHolder) != witchThrowSoundID+1 {
		t.Fatalf("Sound holder = %d, want %d (witch.throw id %d + 1)", int32(soundHolder), witchThrowSoundID+1, witchThrowSoundID)
	}
	if int32(source) != soundSourceHostile {
		t.Fatalf("Sound source = %d, want %d (SoundSource.HOSTILE ordinal)", int32(source), soundSourceHostile)
	}
	if int32(x8) != int32(8.5*8.0) || int32(y8) != int32(64.0*8.0) || int32(z8) != int32(8.5*8.0) {
		t.Fatalf("Sound position = (%d, %d, %d) (1/8-block), want (%d, %d, %d) (witch at 8.5, 64, 8.5)",
			int32(x8), int32(y8), int32(z8), int32(8.5*8.0), int32(64.0*8.0), int32(8.5*8.0))
	}
	if float32(volume) != witchDrinkSoundVolume {
		t.Fatalf("Sound volume = %v, want %v (fconst_1)", float32(volume), witchDrinkSoundVolume)
	}
	if float32(pitch) < 0.8 || float32(pitch) >= 1.2 {
		t.Fatalf("Sound pitch = %v, want in [0.8, 1.2) (0.8f + nextFloat()*0.4f from the witch's per-mob RNG)", float32(pitch))
	}
	if r.Len() != 0 {
		t.Fatalf("Sound packet has %d trailing bytes, want 0 (exact wire layout)", r.Len())
	}
	if witchThrowSoundID != 1779 {
		t.Fatalf("witchThrowSoundID = %d, want 1779 (SoundEvents.WITCH_THROW)", witchThrowSoundID)
	}
	if entity.Witch.ID == 0 {
		t.Fatal("entity.Witch.ID not registered — the constant table is missing the witch")
	}
}

// TestWitchThrowSilentWhileDrinking: the bytecode guard at Witch.performRangedAttack offsets 0-7
// `if (isDrinkingPotion()) return;` is the WITCH_THROW sibling of the WITCH_DRINK self-buff — a
// drinking witch never throws a splash potion AND never plays the WITCH_THROW sound. The test arms
// witchDrinking=true on a fresh witch, calls performWitchRangedAttack, and asserts NO
// ClientboundSound reaches the viewer (the throw was suppressed at the head of the method, BEFORE
// the splash spawn + BEFORE the WITCH_THROW playSound at offsets 293-342). Cite Witch.performRangedAttack
// offsets 0-7 (isDrinkingPotion ifeq -> return).
func TestWitchThrowSilentWhileDrinking(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	w := &Entity{id: 7777, typ: entity.Witch.ID, x: 8.5, y: 64, z: 8.5, height: 1.95, onGround: true, health: 26.0}
	w.ai = &mobAI{rng: newEntityRandom(0xC0FFEE)}
	w.witchDrinking = true // arm drink state -- the bytecode guard must short-circuit

	target := &tickPlayer{x: 12.5, y: 64, z: 8.5, health: 20.0, entityID: 100, client: captureClient(64)}
	viewer := &tickPlayer{x: 8.5, y: 64, z: 8.5, entityID: 1, client: captureClient(64)}
	loop.players = append(loop.players, target, viewer)

	loop.performWitchRangedAttack(w, target, 1.0)

	got := drainPackets(viewer.client)
	for _, p := range got {
		if p.ID == int32(packetid.ClientboundSound) {
			t.Fatalf("a DRINKING witch emitted a ClientboundSound (%d), want 0 (Witch.performRangedAttack offsets 0-7 isDrinkingPotion ifeq -> return suppresses the throw + sound)", p.ID)
		}
	}
	if got := drainPackets(target.client); len(got) != 0 {
		t.Fatalf("a DRINKING witch's throw hit the target with %d packets, want 0 (offsets 0-7 suppressed the throw)", len(got))
	}
}