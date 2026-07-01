package server

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/packetid"
)

// witch_idle_particle_test.go verifies the wired Witch.aiStep idle-particle path: on the 7.5E-4 roll
// the witch broadcasts ClientboundEntityEvent status 15 (entityEventWitchIdleParticles) to trackers;
// otherwise it broadcasts nothing. This is the faithful vanilla path (broadcastEntityEvent(this,15)),
// NOT a ClientboundLevelParticles packet — see the citation in ai_goals_witch.go / particles.go.
//
// The witch is NOT the pig oracle, so these draws are witch-gated; the pig stream is untouched.

// buildIdleWitch makes a live, non-drinking witch at the origin with a hand-controlled RNG source so
// a test can seed the drink-ladder + idle draws deterministically. The four drink-ladder rungs each
// short-circuit at their nextFloat() compare when the draw is >= the threshold (the largest is 0.5),
// so a source whose first four draws are all >= 0.5 starts NO drink and reaches the idle roll without
// any level access — exactly the idle branch we want to exercise.
func buildIdleWitch(seed uint64) *Entity {
	e := &Entity{id: 7777, typ: entity.Witch.ID, health: 26}
	e.ai = &mobAI{rng: newEntityRandom(seed)}
	return e
}

// firstFiveDraws returns the first five nextFloat() draws a source with `seed` yields (the four
// drink-ladder rungs + the idle roll), without disturbing the entity's own source.
func firstFiveDraws(seed uint64) [5]float32 {
	r := newEntityRandom(seed)
	var d [5]float32
	for i := range d {
		d[i] = r.nextFloat()
	}
	return d
}

// TestWitchIdleParticleEventFires: find a seed whose first four draws are all >= 0.5 (no drink starts)
// and whose fifth draw is < 7.5E-4 (the idle roll WINS), then assert witchAiStep broadcasts exactly one
// ClientboundEntityEvent with status byte 15 to a tracking viewer.
func TestWitchIdleParticleEventFires(t *testing.T) {
	var seed uint64
	found := false
	for s := uint64(1); s < 20_000_000 && !found; s++ {
		d := firstFiveDraws(s)
		if d[0] >= 0.5 && d[1] >= 0.5 && d[2] >= 0.5 && d[3] >= 0.5 && float64(d[4]) < witchIdleEventChance {
			seed, found = s, true
		}
	}
	if !found {
		t.Skip("no seed produced the (no-drink, idle-roll-win) draw pattern in the search budget")
	}

	loop := &TickLoop{}
	viewer := &tickPlayer{client: captureClient(64), entityID: 1, tracked: map[int32]bool{7777: true}}
	loop.players = append(loop.players, viewer)

	w := buildIdleWitch(seed)
	loop.witchAiStep(w)

	got := drainPackets(viewer.client)
	if len(got) != 1 {
		t.Fatalf("idle-roll-win: viewer received %d packets, want 1 (the EntityEvent 15)", len(got))
	}
	if got[0].ID != int32(packetid.ClientboundEntityEvent) {
		t.Fatalf("packet id = %d, want ClientboundEntityEvent (%d)", got[0].ID, int32(packetid.ClientboundEntityEvent))
	}
	// encodeEntityEvent body = Int(entityID) + Byte(status). Assert the trailing status byte is 15.
	want := encodeEntityEvent(7777, entityEventWitchIdleParticles)
	if !bytes.Equal(got[0].Data, want.Data) {
		t.Fatalf("idle-particle event body mismatch:\n got %x\nwant %x", got[0].Data, want.Data)
	}
	if entityEventWitchIdleParticles != 15 {
		t.Fatalf("entityEventWitchIdleParticles = %d, want 15 (Witch.aiStep broadcastEntityEvent(this,15))", entityEventWitchIdleParticles)
	}
}

// TestWitchIdleParticleEventSkipped: a seed whose first four draws are all >= 0.5 (no drink) and whose
// fifth draw is >= 7.5E-4 (the idle roll LOSES) broadcasts NOTHING — the draw still fires (order
// fidelity) but no EntityEvent is sent.
func TestWitchIdleParticleEventSkipped(t *testing.T) {
	var seed uint64
	found := false
	for s := uint64(1); s < 1_000_000 && !found; s++ {
		d := firstFiveDraws(s)
		if d[0] >= 0.5 && d[1] >= 0.5 && d[2] >= 0.5 && d[3] >= 0.5 && float64(d[4]) >= witchIdleEventChance {
			seed, found = s, true
		}
	}
	if !found {
		t.Fatal("no seed produced the (no-drink, idle-roll-loss) pattern — implausible")
	}

	loop := &TickLoop{}
	viewer := &tickPlayer{client: captureClient(64), entityID: 1, tracked: map[int32]bool{7777: true}}
	loop.players = append(loop.players, viewer)

	w := buildIdleWitch(seed)
	// Record the pre-step draw count is honored: the source must advance by exactly 5 (4 rungs + idle).
	before := newEntityRandom(seed)
	loop.witchAiStep(w)

	if got := drainPackets(viewer.client); len(got) != 0 {
		t.Fatalf("idle-roll-loss: viewer received %d packets, want 0", len(got))
	}
	// Order fidelity: witchAiStep drew exactly 5 floats, so the entity source now matches a reference
	// that has drawn 5. Compare the NEXT draw.
	for i := 0; i < 5; i++ {
		before.nextFloat()
	}
	if got, want := w.ai.rng.nextFloat(), before.nextFloat(); got != want {
		t.Fatalf("RNG draw order broken: next draw = %v, want %v (witchAiStep must consume exactly 5 floats)", got, want)
	}
}
