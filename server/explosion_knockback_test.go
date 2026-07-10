package server

import (
	"bytes"
	"io"
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// explosion_knockback_test.go covers the Fable explosion audit fixes A1/A2/A3:
//   A2 — a mob in blast range is PUSHED away from the center (not just damaged).
//   A3 — a primed TNT in range is PUSHED (the chain-reaction impulse) even though it takes no damage.
//   A1 — a nearby player (within 64 blocks) receives a ClientboundExplode carrying its knockback Optional.
// The pig oracle draws RNG only in calculateExplodedPositions (unchanged) and never explodes, so these
// add no draws to any mob gameplay stream (TestPluginPigEqualsGoNativePig stays byte-identical).

// TestExplosionKnocksBackMob (A2): a creeper standing off-center from a blast is launched away from the
// center — its velocity gains a component pointing from the center toward the mob. Before the A2 fix the
// mob loop applied damage but never touched e.vx/vy/vz.
func TestExplosionKnocksBackMob(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaCreeperRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	// A victim creeper 2 blocks to +X of the blast center (well within a radius-3 blast, doubleRadius 6).
	decl := loop.mobRegistry.byName["vanilla_creeper"]
	const cx, cy, cz = 8.5, float64(floorY + 1), 8.5
	victim := loop.spawnDeclaredMob(decl, cx+2.0, cy, cz)
	victim.onGround = true
	vx0, vy0, vz0 := victim.vx, victim.vy, victim.vz

	loop.withRegion(loop.only(), func() {
		// srcID -1 (no source entity) so the victim creeper is not excluded from the hurt set.
		loop.explode(-1, cx, cy, cz, 3.0)
	})

	if victim.vx == vx0 && victim.vy == vy0 && victim.vz == vz0 {
		t.Fatalf("mob velocity unchanged after explosion: v=(%v,%v,%v) — A2 push never applied", victim.vx, victim.vy, victim.vz)
	}
	// The mob is at +X of the center, so the push must have a POSITIVE x component (away from center).
	if victim.vx <= vx0 {
		t.Fatalf("mob was not pushed away from center: vx %v <= %v (expected +X push for a +X-side mob)", victim.vx, vx0)
	}
	// getEyePosition origin is above the center's y, so the push also has an upward (+y) component.
	if victim.vy <= vy0 {
		t.Fatalf("mob push had no upward component: vy %v <= %v", victim.vy, vy0)
	}
}

// TestExplosionPushesPrimedTnt (A3): a primed TNT in blast range is pushed (its velocity changes away
// from the center) even though it takes NO explosion damage — this is the impulse that scatters a TNT
// cluster in a chain reaction. Before the A3 fix the loop only touched mobs (e.ai != nil), so a TNT
// (ai == nil) got neither damage nor push.
func TestExplosionPushesPrimedTnt(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	const cx, cy, cz = 8.5, float64(floorY + 1), 8.5
	// A primed TNT 2 blocks to +X (in range of a radius-3 blast). spawnPrimedTnt gives it an initial
	// upward pop; capture that as the baseline so we measure only the explosion's added impulse.
	var tnt *Entity
	loop.withRegion(loop.only(), func() {
		tnt = loop.spawnPrimedTnt(cx+2.0, cy, cz, 80)
	})
	vx0, vy0, vz0 := tnt.vx, tnt.vy, tnt.vz

	loop.withRegion(loop.only(), func() {
		loop.explode(-1, cx, cy, cz, 3.0)
	})

	if tnt.vx == vx0 && tnt.vy == vy0 && tnt.vz == vz0 {
		t.Fatalf("primed TNT velocity unchanged after explosion: v=(%v,%v,%v) — A3 chain impulse never applied", tnt.vx, tnt.vy, tnt.vz)
	}
	// +X-side TNT must gain a +X push.
	if tnt.vx <= vx0 {
		t.Fatalf("primed TNT not pushed away from center: vx %v <= %v (expected +X push)", tnt.vx, vx0)
	}
}

// TestExplosionSendsClientboundExplode (A1): a player within 64 blocks of the blast receives exactly one
// ClientboundExplode, and its per-player knockback Optional is PRESENT with a non-zero vector (the same
// impulse the server pushed the player by). A player beyond 64 blocks receives nothing.
func TestExplosionSendsClientboundExplode(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	const cx, cy, cz = 8.5, float64(floorY + 1), 8.5
	// A player at the far edge of a radius-3 blast (dist ≈ 0.92) so it lands in the hurt set with a
	// non-zero push but SURVIVES (a dead player is beside the point — the packet gate is distance, not
	// the hurt set). Its capture client starts empty (no registration packets), so no pre-drain — and
	// drainPackets CLOSES the outbound queue, so we must drain only AFTER the explosion.
	near := combatTestPlayer(loop, cx+5.5, cy, cz, 201)
	near.playerEntity = &Entity{id: near.entityID}
	// A player far away (> 64 blocks) — outside the 4096.0 distanceToSqr gate, gets no packet.
	far := combatTestPlayer(loop, cx+200.0, cy, cz, 202)
	far.playerEntity = &Entity{id: far.entityID}

	loop.withRegion(loop.only(), func() {
		loop.explode(-1, cx, cy, cz, 3.0)
	})

	nearPk := drainPackets(near.client)
	if n := countID(nearPk, packetid.ClientboundExplode); n != 1 {
		t.Fatalf("near player received %d ClientboundExplode packets, want 1", n)
	}
	// The near player is in the hurt set, so the Optional must be present with a non-zero knockback.
	kbx, kby, kbz, present := decodeExplodeKnockback(t, findPacket(nearPk, packetid.ClientboundExplode))
	if !present {
		t.Fatal("near player's ClientboundExplode carried an ABSENT knockback Optional, want present")
	}
	if kbx == 0 && kby == 0 && kbz == 0 {
		t.Fatalf("near player's knockback vector is zero: (%v,%v,%v)", kbx, kby, kbz)
	}
	// The push must match the velocity actually applied to the player's store entity (same vector).
	if math.Abs(kbx-near.playerEntity.vx) > 1e-9 || math.Abs(kby-near.playerEntity.vy) > 1e-9 || math.Abs(kbz-near.playerEntity.vz) > 1e-9 {
		t.Fatalf("packet knockback (%v,%v,%v) != applied velocity (%v,%v,%v)", kbx, kby, kbz, near.playerEntity.vx, near.playerEntity.vy, near.playerEntity.vz)
	}

	if n := countID(drainPackets(far.client), packetid.ClientboundExplode); n != 0 {
		t.Fatalf("far player (>64 blocks) received %d ClientboundExplode packets, want 0", n)
	}
}

// TestExplosionCreativeFlyingNoKnockback: a creative (flying) player caught in a blast takes NO knockback
// (its store-entity velocity stays zero) and is NOT recorded in the hurt set (no ClientboundExplode
// knockback Optional / no push), mirroring ServerExplosion.hurtEntities' isCreative&&flying skip. A
// survival player at the same distance IS pushed (the control). Cite ServerExplosion.hurtEntities.
func TestExplosionCreativeFlyingNoKnockback(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())

	const cx, cy, cz = 8.5, float64(floorY + 1), 8.5
	creative := combatTestPlayer(loop, cx+2.0, cy, cz, 301)
	creative.playerEntity = &Entity{id: creative.entityID}
	creative.gameMode = gameModeCreative

	loop.withRegion(loop.only(), func() {
		loop.explode(-1, cx, cy, cz, 3.0)
	})

	if creative.playerEntity.vx != 0 || creative.playerEntity.vy != 0 || creative.playerEntity.vz != 0 {
		t.Fatalf("creative-flying player was knocked back (%v,%v,%v), want zero (isCreative&&flying skip)",
			creative.playerEntity.vx, creative.playerEntity.vy, creative.playerEntity.vz)
	}
	pk := drainPackets(creative.client)
	if p := findPacket(pk, packetid.ClientboundExplode); p.ID != 0 {
		_, _, _, present := decodeExplodeKnockback(t, p)
		if present {
			t.Fatal("creative-flying player's ClientboundExplode carried a knockback Optional, want ABSENT (not in hurt set)")
		}
	}
}

// findPacket returns the first packet with the given id, or a zero Packet.
func findPacket(ps []pk.Packet, id packetid.ClientboundPacketID) pk.Packet {
	for _, p := range ps {
		if p.ID == int32(id) {
			return p
		}
	}
	return pk.Packet{}
}

// decodeExplodeKnockback reads the ClientboundExplode wire body far enough to recover the playerKnockback
// Optional: it skips center (3 Double), radius (Float), blockCount (Int), then reads the present Boolean
// and (if present) the 3-Double knockback vector. Field order per the jar STREAM_CODEC composite. p.Data
// is the body WITHOUT the packet id (the id is p.ID).
func decodeExplodeKnockback(t *testing.T, p pk.Packet) (float64, float64, float64, bool) {
	t.Helper()
	if p.ID != int32(packetid.ClientboundExplode) {
		t.Fatalf("decodeExplodeKnockback: packet id %d is not ClientboundExplode", p.ID)
	}
	r := bytes.NewReader(p.Data)
	var cx, cy, cz pk.Double
	var radius pk.Float
	var blockCount pk.Int
	var present pk.Boolean
	readField(t, r, &cx, &cy, &cz, &radius, &blockCount, &present)
	if !bool(present) {
		return 0, 0, 0, false
	}
	var kx, ky, kz pk.Double
	readField(t, r, &kx, &ky, &kz)
	return float64(kx), float64(ky), float64(kz), true
}

// readField reads each pk field in order from r, failing the test on any decode error.
func readField(t *testing.T, r io.Reader, fields ...pk.FieldDecoder) {
	t.Helper()
	for i, f := range fields {
		if _, err := f.ReadFrom(r); err != nil {
			t.Fatalf("read explode field %d: %v", i, err)
		}
	}
}
