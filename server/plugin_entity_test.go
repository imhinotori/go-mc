package server

import (
	"strings"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"go.starlark.net/starlark"
)

// plugin_entity_test.go — Phase 23 (Task 2 + Task 3): the thin entity/world HANDLE bridge and the
// capability enforcement. Tests reuse the newBlockLoop harness (a TickLoop with a tick-owned
// ChunkManager + entityStore) so the handles re-resolve a real store and mutate a real world.

// spawnTestEntity adds a Pig (a living, supplier-backed type) at (x,y,z) with an AI, and returns it.
func spawnTestEntity(loop *TickLoop, x, y, z float64) *Entity {
	e := NewEntity(1, entity.Pig, x, y, z)
	e.ai = newPigAI()
	loop.entities.add(e)
	return e
}

// attrFloat is a small helper: read a handle Attr and assert it is a starlark.Float.
func attrFloat(t *testing.T, h starlark.HasAttrs, name string) float64 {
	t.Helper()
	v, err := h.Attr(name)
	if err != nil {
		t.Fatalf("Attr(%q) error: %v", name, err)
	}
	f, ok := v.(starlark.Float)
	if !ok {
		t.Fatalf("Attr(%q) = %T, want starlark.Float", name, v)
	}
	return float64(f)
}

// callMethod fetches a bound method off a handle and calls it with the given args.
func callMethod(t *testing.T, h starlark.HasAttrs, name string, args ...starlark.Value) (starlark.Value, error) {
	t.Helper()
	m, err := h.Attr(name)
	if err != nil {
		t.Fatalf("Attr(%q) error: %v", name, err)
	}
	b, ok := m.(*starlark.Builtin)
	if !ok {
		t.Fatalf("Attr(%q) = %T, want *starlark.Builtin", name, m)
	}
	th := &starlark.Thread{Name: "test"}
	return starlark.Call(th, b, starlark.Tuple(args), nil)
}

// TestEntityHandleReads: a handle over a live entity reads x/y/z/health/on_ground/type/velocity via
// Attr, matching the entity's tick-owned fields.
func TestEntityHandleReads(t *testing.T) {
	loop, _ := newBlockLoop()
	e := spawnTestEntity(loop, 10.5, 64.0, -3.25)
	e.vx, e.vy, e.vz = 0.1, -0.2, 0.3
	e.onGround = true
	h := newEntityHandle(loop, e.id, capAll)

	if got := attrFloat(t, h, "x"); got != 10.5 {
		t.Errorf("x = %v, want 10.5", got)
	}
	if got := attrFloat(t, h, "y"); got != 64.0 {
		t.Errorf("y = %v, want 64.0", got)
	}
	if got := attrFloat(t, h, "z"); got != -3.25 {
		t.Errorf("z = %v, want -3.25", got)
	}
	og, err := h.Attr("on_ground")
	if err != nil || og != starlark.Bool(true) {
		t.Errorf("on_ground = %v (err %v), want True", og, err)
	}
	typ, _ := h.Attr("type")
	if typ != starlark.String("pig") {
		t.Errorf("type = %v, want \"pig\"", typ)
	}
	// health = max_health attribute (pig 10.0 from the SUB-ATTRIB coverage fix).
	if got := attrFloat(t, h, "health"); got != 10.0 {
		t.Errorf("health = %v, want 10.0 (pig max_health)", got)
	}
	// velocity is a 3-tuple.
	vel, err := h.Attr("velocity")
	if err != nil {
		t.Fatalf("velocity error: %v", err)
	}
	tup, ok := vel.(starlark.Tuple)
	if !ok || len(tup) != 3 {
		t.Fatalf("velocity = %v, want a 3-tuple", vel)
	}
	if tup[0] != starlark.Float(0.1) || tup[1] != starlark.Float(-0.2) || tup[2] != starlark.Float(0.3) {
		t.Errorf("velocity = %v, want (0.1,-0.2,0.3)", tup)
	}
}

// TestEntityHandleStale: after the entity is removed from the store, Attr("x") returns a non-nil
// error ("entity N no longer exists"), NOT a panic and NOT a stale value.
func TestEntityHandleStale(t *testing.T) {
	loop, _ := newBlockLoop()
	e := spawnTestEntity(loop, 1.0, 64.0, 1.0)
	h := newEntityHandle(loop, e.id, capAll)
	loop.entities.remove(e.id)

	v, err := h.Attr("x")
	if err == nil {
		t.Fatalf("Attr(x) on a removed entity = %v, want an error", v)
	}
	if !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("stale error = %q, want a 'no longer exists' message", err.Error())
	}
}

// TestHandleFreezeNoop: handle.Freeze() does nothing; the entity stays mutable on the tick (move it
// after Freeze, read the new pos through the handle).
func TestHandleFreezeNoop(t *testing.T) {
	loop, _ := newBlockLoop()
	e := spawnTestEntity(loop, 5.0, 64.0, 5.0)
	h := newEntityHandle(loop, e.id, capAll)
	h.Freeze() // no-op

	// Mutate the entity directly on the tick (the tracker/physics seam) AFTER Freeze.
	loop.entities.move(e, 5.0, 70.0, 5.0)
	if got := attrFloat(t, h, "y"); got != 70.0 {
		t.Errorf("after Freeze + move, y = %v, want 70.0 (Freeze must not lock the entity)", got)
	}
}

// TestHandleMutators: move_to routes to setWantTarget; set_velocity writes vx/vy/vz directly;
// set_attribute routes to attribute.Map; world.set_block routes to ChunkManager.SetBlock.
func TestHandleMutators(t *testing.T) {
	loop, mgr := newBlockLoop()
	e := spawnTestEntity(loop, 1.0, 64.0, 1.0)
	h := newEntityHandle(loop, e.id, capAll)

	// move_to -> setWantTarget.
	if _, err := callMethod(t, h, "move_to", starlark.Float(8), starlark.Float(64), starlark.Float(9)); err != nil {
		t.Fatalf("move_to error: %v", err)
	}
	if !e.ai.hasTarget || e.ai.wantX != 8 || e.ai.wantY != 64 || e.ai.wantZ != 9 {
		t.Errorf("move_to did not set the nav target: hasTarget=%v want=(%v,%v,%v)",
			e.ai.hasTarget, e.ai.wantX, e.ai.wantY, e.ai.wantZ)
	}

	// set_velocity -> direct vx/vy/vz.
	if _, err := callMethod(t, h, "set_velocity", starlark.Float(1.5), starlark.Float(2.5), starlark.Float(-3.5)); err != nil {
		t.Fatalf("set_velocity error: %v", err)
	}
	if e.vx != 1.5 || e.vy != 2.5 || e.vz != -3.5 {
		t.Errorf("set_velocity = (%v,%v,%v), want (1.5,2.5,-3.5)", e.vx, e.vy, e.vz)
	}

	// set_attribute -> attribute.Map base override (pig max_health 10 -> 30).
	if _, err := callMethod(t, h, "set_attribute", starlark.String(attribute.MaxHealth.Name()), starlark.Float(30)); err != nil {
		t.Fatalf("set_attribute error: %v", err)
	}
	if got := e.attributes.GetValue(attribute.MaxHealth.Name()); got != 30.0 {
		t.Errorf("after set_attribute, max_health = %v, want 30.0", got)
	}

	// world.set_block -> ChunkManager.SetBlock.
	wh := newWorldHandle(loop, capAll)
	stone := block.StateID(1)
	res, err := callMethod(t, wh, "set_block",
		starlark.MakeInt(2), starlark.MakeInt(64), starlark.MakeInt(3), starlark.MakeInt(int(stone)))
	if err != nil {
		t.Fatalf("set_block error: %v", err)
	}
	if res != starlark.True {
		t.Errorf("set_block returned %v, want True (block changed)", res)
	}
	got, ok := mgr.GetBlock(pk.Position{X: 2, Y: 64, Z: 3}, dimMinY)
	if !ok || got != stone {
		t.Errorf("world block after set_block = %v (ok %v), want %v", got, ok, stone)
	}

	// world.block_at -> read it back.
	rb, err := callMethod(t, wh, "block_at", starlark.MakeInt(2), starlark.MakeInt(64), starlark.MakeInt(3))
	if err != nil {
		t.Fatalf("block_at error: %v", err)
	}
	tup := rb.(starlark.Tuple)
	if tup[0] != starlark.MakeInt(int(stone)) || tup[1] != starlark.True {
		t.Errorf("block_at = %v, want (%d, True)", rb, stone)
	}
}

// TestNoRawPositionWrite: move_to must NOT write e.x/e.y/e.z directly (it sets a nav target); the
// position is unchanged immediately after move_to.
func TestNoRawPositionWrite(t *testing.T) {
	loop, _ := newBlockLoop()
	e := spawnTestEntity(loop, 1.0, 64.0, 1.0)
	h := newEntityHandle(loop, e.id, capAll)

	if _, err := callMethod(t, h, "move_to", starlark.Float(100), starlark.Float(64), starlark.Float(100)); err != nil {
		t.Fatalf("move_to error: %v", err)
	}
	if e.x != 1.0 || e.y != 64.0 || e.z != 1.0 {
		t.Errorf("move_to wrote the position directly: (%v,%v,%v), want it unchanged at (1,64,1)", e.x, e.y, e.z)
	}
	if !e.ai.hasTarget {
		t.Error("move_to did not set a nav target")
	}
}
