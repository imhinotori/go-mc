package server

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/data/registryid"
)

// particles_test.go exercises the spawnParticle broadcast subsystem: the ServerLevel.sendParticles
// per-player distance gate (32.0 default / 512.0 long-distance from the player's BLOCK-pos center),
// the unknown-key no-op, and the wire body (delegated to encodeLevelParticles). The pig oracle draws
// ZERO particles (no mob calls spawnParticle), so these tests add no RNG draws to any mob stream.

// newParticleViewer builds a tickPlayer at (x,y,z) with a capture client so drainPackets can read
// whatever spawnParticle sent it.
func newParticleViewer(x, y, z float64) *tickPlayer {
	return &tickPlayer{x: x, y: y, z: z, client: captureClient(64)}
}

// TestSpawnParticleBroadcast: a player inside the 32.0 radius receives the packet; a player outside
// it does not. Distance is measured from the player's block-pos CENTER (floor+0.5) to the particle
// point, exactly as ServerLevel.sendParticles -> Vec3i.closerToCenterThan.
func TestSpawnParticleBroadcast(t *testing.T) {
	loop := &TickLoop{}
	near := newParticleViewer(5, 64, 5)   // block center (5.5,64.5,5.5); ~well within 32 of (10,64,10)
	far := newParticleViewer(100, 64, 100) // > 32 blocks away on X and Z
	loop.players = append(loop.players, near, far)

	loop.spawnParticle("minecraft:portal", false, false, 10, 64, 10, 0, 0, 0, 0, 5)

	if got := drainPackets(near.client); len(got) != 1 {
		t.Fatalf("near player received %d packets, want 1", len(got))
	} else if got[0].ID != int32(packetid.ClientboundLevelParticles) {
		t.Fatalf("near packet id = %d, want ClientboundLevelParticles (%d)", got[0].ID, int32(packetid.ClientboundLevelParticles))
	}
	if got := drainPackets(far.client); len(got) != 0 {
		t.Fatalf("far player (>32) received %d packets, want 0", len(got))
	}
}

// TestSpawnParticleRadiusBoundary: a player whose block-pos center is just OUTSIDE 32.0 is excluded,
// and the same player IS included when overrideLimiter (512.0 radius) is set. Pins the < radius^2
// comparison and the long-distance branch.
func TestSpawnParticleRadiusBoundary(t *testing.T) {
	loop := &TickLoop{}
	// particle at (0.5,64.5,0.5) == a block center; player block center at (40.5,64.5,0.5) -> dx = 40 > 32.
	p := newParticleViewer(40, 64, 0)
	loop.players = append(loop.players, p)

	loop.spawnParticle("minecraft:witch", false, false, 0.5, 64.5, 0.5, 0, 0, 0, 0, 1)
	if got := drainPackets(p.client); len(got) != 0 {
		t.Fatalf("default radius: player at dx=40 received %d packets, want 0", len(got))
	}

	p2 := newParticleViewer(40, 64, 0)
	loop.players = []*tickPlayer{p2}
	loop.spawnParticle("minecraft:witch", true, false, 0.5, 64.5, 0.5, 0, 0, 0, 0, 1) // overrideLimiter -> 512.0
	if got := drainPackets(p2.client); len(got) != 1 {
		t.Fatalf("long-distance radius: player at dx=40 received %d packets, want 1", len(got))
	}
}

// TestSpawnParticleUnknownKey: an unknown particle key is a no-op (menuTypeID returns -1) — nothing
// is sent to anyone. Guards the defensive sentinel.
func TestSpawnParticleUnknownKey(t *testing.T) {
	loop := &TickLoop{}
	p := newParticleViewer(0, 64, 0)
	loop.players = append(loop.players, p)

	loop.spawnParticle("minecraft:not_a_real_particle", false, false, 0, 64, 0, 0, 0, 0, 0, 1)
	if got := drainPackets(p.client); len(got) != 0 {
		t.Fatalf("unknown particle key sent %d packets, want 0", len(got))
	}
}

// TestSpawnParticleWireBody: the broadcast packet body is exactly encodeLevelParticles's — same field
// order + the trailing particle-type VarInt. Rebuilds the expected body and byte-compares.
func TestSpawnParticleWireBody(t *testing.T) {
	loop := &TickLoop{}
	p := newParticleViewer(0, 64, 0)
	loop.players = append(loop.players, p)

	poofID := menuTypeID(registryid.ParticleType, "minecraft:poof")
	if poofID < 0 {
		t.Fatal("minecraft:poof not found in registryid.ParticleType")
	}
	loop.spawnParticle("minecraft:poof", true, false, 0.5, 64.5, 0.5, 0.1, 0.2, 0.3, 0.05, 7)

	got := drainPackets(p.client)
	if len(got) != 1 {
		t.Fatalf("received %d packets, want 1", len(got))
	}
	want := encodeLevelParticles(poofID, true, false, 0.5, 64.5, 0.5, 0.1, 0.2, 0.3, 0.05, 7)
	if !bytes.Equal(got[0].Data, want.Data) {
		t.Fatalf("spawnParticle body mismatch:\n got %x\nwant %x", got[0].Data, want.Data)
	}
}
