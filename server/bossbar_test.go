package server

// bossbar_test.go — the ClientboundBossEvent WIRE (bossbar.go) + the raid boss-bar emission. Asserts
// the ADD / UPDATE_PROGRESS / REMOVE wire bodies byte-for-byte against the jar-derived layout (VERIFIED
// CFR ClientboundBossEventPacket), and that starting a raid ADDs the bar to a nearby player. The pig
// oracle is untouched (no raid in the pig test -> zero draws).

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	pk "github.com/imhinotori/sulfur/net/packet"

	"github.com/google/uuid"
)

// TestBossEventAddWire pins encodeBossEventAdd: UUID id, VarInt ADD(0), Component name (a non-empty NBT
// body), Float progress, VarInt color, VarInt overlay, Byte flags. VERIFIED CFR AddOperation.write.
func TestBossEventAddWire(t *testing.T) {
	id := uuid.New()
	p := encodeBossEventAdd(id, bossBarName("event.minecraft.raid"), 0.5, bossBarColorRed, bossBarOverlayNotched10, false, false, false)
	if p.ID != int32(packetid.ClientboundBossEvent) {
		t.Fatalf("BossEvent id = %d, want %d", p.ID, packetid.ClientboundBossEvent)
	}
	r := bytes.NewReader(p.Data)

	var gotID pk.UUID
	if _, err := gotID.ReadFrom(r); err != nil {
		t.Fatalf("read uuid: %v", err)
	}
	if uuid.UUID(gotID) != id {
		t.Fatalf("id = %v, want %v", uuid.UUID(gotID), id)
	}

	var op pk.VarInt
	if _, err := op.ReadFrom(r); err != nil {
		t.Fatalf("read op: %v", err)
	}
	if int32(op) != int32(bossEventOpAdd) {
		t.Fatalf("op = %d, want %d (ADD)", op, bossEventOpAdd)
	}

	// The Component is an NBT network body (TRUSTED_STREAM_CODEC). We do not re-decode the NBT here —
	// the chat.Message codec is exercised by the chat tests — but we consume it by reading the trailing
	// fixed-width fields from the END so the framing is asserted. Instead, decode forward through the
	// component using the chat decoder is overkill; assert the tail fields by scanning from the back.
	rest := make([]byte, r.Len())
	if _, err := r.Read(rest); err != nil {
		t.Fatalf("read rest: %v", err)
	}
	// Tail layout (fixed width): Float progress(4) + VarInt color + VarInt overlay + Byte flags(1).
	// color=RED(2) and overlay=NOTCHED_10(2) are single-byte VarInts, so the tail is exactly 4+1+1+1 = 7
	// bytes; the component is everything before it.
	if len(rest) < 7 {
		t.Fatalf("ADD payload too short: %d bytes (need >=7 for the tail)", len(rest))
	}
	tail := rest[len(rest)-7:]
	comp := rest[:len(rest)-7]
	if len(comp) == 0 {
		t.Fatal("ADD component body is empty (expected an NBT-encoded translate component)")
	}
	tr := bytes.NewReader(tail)
	var prog pk.Float
	if _, err := prog.ReadFrom(tr); err != nil {
		t.Fatalf("read progress: %v", err)
	}
	if float32(prog) != 0.5 {
		t.Fatalf("progress = %v, want 0.5", float32(prog))
	}
	var color, overlay pk.VarInt
	if _, err := color.ReadFrom(tr); err != nil {
		t.Fatalf("read color: %v", err)
	}
	if int32(color) != int32(bossBarColorRed) {
		t.Fatalf("color = %d, want %d (RED)", color, bossBarColorRed)
	}
	if _, err := overlay.ReadFrom(tr); err != nil {
		t.Fatalf("read overlay: %v", err)
	}
	if int32(overlay) != int32(bossBarOverlayNotched10) {
		t.Fatalf("overlay = %d, want %d (NOTCHED_10)", overlay, bossBarOverlayNotched10)
	}
	var flags pk.Byte
	if _, err := flags.ReadFrom(tr); err != nil {
		t.Fatalf("read flags: %v", err)
	}
	if int8(flags) != 0 {
		t.Fatalf("flags = %d, want 0 (no darken/music/fog)", flags)
	}
}

// TestBossEventPropertiesFlags pins bossEventProperties: FLAG_DARKEN 1 | FLAG_MUSIC 2 | FLAG_FOG 4
// (VERIFIED CFR encodeProperties).
func TestBossEventPropertiesFlags(t *testing.T) {
	cases := []struct {
		darken, music, fog bool
		want               int8
	}{
		{false, false, false, 0},
		{true, false, false, 1},
		{false, true, false, 2},
		{false, false, true, 4},
		{true, true, true, 7},
		{true, false, true, 5},
	}
	for _, c := range cases {
		if got := bossEventProperties(c.darken, c.music, c.fog); got != c.want {
			t.Fatalf("bossEventProperties(%v,%v,%v) = %d, want %d", c.darken, c.music, c.fog, got, c.want)
		}
	}
}

// TestBossEventUpdateProgressWire pins encodeBossEventUpdateProgress: UUID id, VarInt UPDATE_PROGRESS(2),
// Float progress — no other payload. VERIFIED CFR UpdateProgressOperation.write.
func TestBossEventUpdateProgressWire(t *testing.T) {
	id := uuid.New()
	p := encodeBossEventUpdateProgress(id, 0.25)
	if p.ID != int32(packetid.ClientboundBossEvent) {
		t.Fatalf("id = %d, want %d", p.ID, packetid.ClientboundBossEvent)
	}
	r := bytes.NewReader(p.Data)
	var gotID pk.UUID
	if _, err := gotID.ReadFrom(r); err != nil {
		t.Fatalf("read uuid: %v", err)
	}
	if uuid.UUID(gotID) != id {
		t.Fatalf("id mismatch")
	}
	var op pk.VarInt
	if _, err := op.ReadFrom(r); err != nil {
		t.Fatalf("read op: %v", err)
	}
	if int32(op) != int32(bossEventOpUpdateProgress) {
		t.Fatalf("op = %d, want %d (UPDATE_PROGRESS)", op, bossEventOpUpdateProgress)
	}
	var prog pk.Float
	if _, err := prog.ReadFrom(r); err != nil {
		t.Fatalf("read progress: %v", err)
	}
	if float32(prog) != 0.25 {
		t.Fatalf("progress = %v, want 0.25", float32(prog))
	}
	if r.Len() != 0 {
		t.Fatalf("UPDATE_PROGRESS has %d trailing bytes, want 0", r.Len())
	}
}

// TestBossEventRemoveWire pins encodeBossEventRemove: UUID id, VarInt REMOVE(1), then NO payload.
// VERIFIED CFR REMOVE_OPERATION.write is empty.
func TestBossEventRemoveWire(t *testing.T) {
	id := uuid.New()
	p := encodeBossEventRemove(id)
	if p.ID != int32(packetid.ClientboundBossEvent) {
		t.Fatalf("id = %d, want %d", p.ID, packetid.ClientboundBossEvent)
	}
	r := bytes.NewReader(p.Data)
	var gotID pk.UUID
	if _, err := gotID.ReadFrom(r); err != nil {
		t.Fatalf("read uuid: %v", err)
	}
	if uuid.UUID(gotID) != id {
		t.Fatalf("id mismatch")
	}
	var op pk.VarInt
	if _, err := op.ReadFrom(r); err != nil {
		t.Fatalf("read op: %v", err)
	}
	if int32(op) != int32(bossEventOpRemove) {
		t.Fatalf("op = %d, want %d (REMOVE)", op, bossEventOpRemove)
	}
	if r.Len() != 0 {
		t.Fatalf("REMOVE has %d trailing bytes, want 0 (empty payload)", r.Len())
	}
}

// TestRaidAddsBossBarToNearbyPlayer drives a live raid and asserts a nearby player receives a
// ClientboundBossEvent ADD (the boss bar appears). A player placed at the raid center is inside
// VALID_RAID_RADIUS_SQR (9216), so bossUpdatePlayers (called on cooldown tick 300) adds it and sends ADD.
func TestRaidAddsBossBarToNearbyPlayer(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaWitchRegistry(t))
	loop.start(loop.clock.(*fakeClock).Now())

	// A player at the raid center with a capturing client (entity id 1000, distinct from raid spawns).
	p := newTrackerPlayer(loop, 1000, 8, 8)
	p.y = float64(floorY)

	rm := loop.only().ensureRaidsManager()
	raid := rm.createRaidAt(8, floorY, 8, difficultyNormal, 1)

	// One tick reaches the cooldown branch at raidCooldownTicks==300, which calls bossUpdatePlayers ->
	// the player is valid (closest active raid within radius) -> addPlayer sends ADD.
	loop.raidsTick(rm)

	if _, tracked := raid.bossEvent.players[p.entityID]; !tracked {
		t.Fatalf("nearby player not tracked by the boss bar after one raid tick")
	}
	pkts := drainPackets(p.client)
	if countID(pkts, packetid.ClientboundBossEvent) == 0 {
		t.Fatalf("nearby player received no ClientboundBossEvent (want an ADD); got %d packets", len(pkts))
	}
	// The first BossEvent the player sees must be an ADD (operation 0) for the raid bar id.
	for _, pkt := range pkts {
		if pkt.ID != int32(packetid.ClientboundBossEvent) {
			continue
		}
		r := bytes.NewReader(pkt.Data)
		var gotID pk.UUID
		if _, err := gotID.ReadFrom(r); err != nil {
			t.Fatalf("read uuid: %v", err)
		}
		if uuid.UUID(gotID) != raid.bossEvent.id {
			t.Fatalf("boss event id = %v, want raid bar id %v", uuid.UUID(gotID), raid.bossEvent.id)
		}
		var op pk.VarInt
		if _, err := op.ReadFrom(r); err != nil {
			t.Fatalf("read op: %v", err)
		}
		if int32(op) != int32(bossEventOpAdd) {
			t.Fatalf("first boss event op = %d, want %d (ADD)", op, bossEventOpAdd)
		}
		return
	}
	t.Fatal("no ClientboundBossEvent packet found in the drained stream")
}
