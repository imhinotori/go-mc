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

// ----------------------------------------------------------------------------------------------
// Task 3: capability enforcement at the handle-op boundary
// ----------------------------------------------------------------------------------------------

// callExpectDenied asserts a method call returns a capability-denied Starlark error naming `want`.
func callExpectDenied(t *testing.T, h starlark.HasAttrs, name, want string, args ...starlark.Value) {
	t.Helper()
	_, err := callMethod(t, h, name, args...)
	if err == nil {
		t.Fatalf("%s without %q capability: got no error, want a denial", name, want)
	}
	if !strings.Contains(err.Error(), "capability denied") || !strings.Contains(err.Error(), want) {
		t.Errorf("%s denial error = %q, want a 'capability denied: ... %q' message", name, err.Error(), want)
	}
}

// TestCapabilityEnforced_WorldWrite: set_block without world.write errors and leaves the block
// UNCHANGED; with world.write it succeeds and the block changes.
func TestCapabilityEnforced_WorldWrite(t *testing.T) {
	loop, mgr := newBlockLoop()
	pos := pk.Position{X: 4, Y: 64, Z: 4}
	stone := block.StateID(1)

	// Denied: a world handle with everything EXCEPT world.write.
	denied := newWorldHandle(loop, capAll&^capWorldWrite)
	callExpectDenied(t, denied, "set_block", "world.write",
		starlark.MakeInt(4), starlark.MakeInt(64), starlark.MakeInt(4), starlark.MakeInt(int(stone)))
	if got, _ := mgr.GetBlock(pos, dimMinY); got == stone {
		t.Errorf("block changed despite denied set_block (got %v)", got)
	}

	// Allowed: with world.write.
	allowed := newWorldHandle(loop, capWorldWrite)
	if _, err := callMethod(t, allowed, "set_block",
		starlark.MakeInt(4), starlark.MakeInt(64), starlark.MakeInt(4), starlark.MakeInt(int(stone))); err != nil {
		t.Fatalf("set_block with world.write error: %v", err)
	}
	if got, ok := mgr.GetBlock(pos, dimMinY); !ok || got != stone {
		t.Errorf("block after allowed set_block = %v (ok %v), want %v", got, ok, stone)
	}
}

// TestCapabilityEnforced_WorldRead: block_at requires world.read.
func TestCapabilityEnforced_WorldRead(t *testing.T) {
	loop, _ := newBlockLoop()
	denied := newWorldHandle(loop, capAll&^capWorldRead)
	callExpectDenied(t, denied, "block_at", "world.read",
		starlark.MakeInt(0), starlark.MakeInt(64), starlark.MakeInt(0))
	callExpectDenied(t, denied, "entities_near", "world.read",
		starlark.Float(0), starlark.Float(0), starlark.MakeInt(1))

	allowed := newWorldHandle(loop, capWorldRead)
	if _, err := callMethod(t, allowed, "block_at", starlark.MakeInt(0), starlark.MakeInt(64), starlark.MakeInt(0)); err != nil {
		t.Errorf("block_at with world.read error: %v", err)
	}
}

// TestCapabilityEnforced_EntitiesWrite: set_velocity/set_attribute require entities.write; move_to
// requires entities.write AND nav.
func TestCapabilityEnforced_EntitiesWrite(t *testing.T) {
	loop, _ := newBlockLoop()
	e := spawnTestEntity(loop, 1.0, 64.0, 1.0)

	// Denied: no entities.write.
	denied := newEntityHandle(loop, e.id, capAll&^capEntitiesWrite)
	callExpectDenied(t, denied, "set_velocity", "entities.write",
		starlark.Float(1), starlark.Float(0), starlark.Float(0))
	if e.vx != 0 {
		t.Errorf("velocity changed despite denied set_velocity (vx=%v)", e.vx)
	}
	callExpectDenied(t, denied, "set_attribute", "entities.write",
		starlark.String(attribute.MaxHealth.Name()), starlark.Float(99))

	// move_to denied when nav is absent (even with entities.write).
	noNav := newEntityHandle(loop, e.id, capEntitiesWrite)
	callExpectDenied(t, noNav, "move_to", "nav",
		starlark.Float(5), starlark.Float(64), starlark.Float(5))
	if e.ai.hasTarget {
		t.Error("move_to set a target despite missing nav capability")
	}

	// Allowed: with entities.write + nav.
	allowed := newEntityHandle(loop, e.id, capEntitiesWrite|capNav)
	if _, err := callMethod(t, allowed, "set_velocity", starlark.Float(2), starlark.Float(0), starlark.Float(0)); err != nil {
		t.Fatalf("set_velocity with entities.write error: %v", err)
	}
	if e.vx != 2 {
		t.Errorf("velocity after allowed set_velocity vx=%v, want 2", e.vx)
	}
	if _, err := callMethod(t, allowed, "move_to", starlark.Float(5), starlark.Float(64), starlark.Float(5)); err != nil {
		t.Fatalf("move_to with entities.write+nav error: %v", err)
	}
	if !e.ai.hasTarget {
		t.Error("move_to with the right capabilities did not set a target")
	}
}

// TestCapabilityEnforced_EntitiesRead: entity reads require entities.read.
func TestCapabilityEnforced_EntitiesRead(t *testing.T) {
	loop, _ := newBlockLoop()
	e := spawnTestEntity(loop, 1.0, 64.0, 1.0)

	denied := newEntityHandle(loop, e.id, capAll&^capEntitiesRead)
	if _, err := denied.Attr("x"); err == nil || !strings.Contains(err.Error(), "entities.read") {
		t.Errorf("read x without entities.read = %v, want a denial", err)
	}
	// The 'attribute' bound method also enforces entities.read.
	callExpectDenied(t, denied, "attribute", "entities.read", starlark.String(attribute.MaxHealth.Name()))

	allowed := newEntityHandle(loop, e.id, capEntitiesRead)
	if got := attrFloat(t, allowed, "x"); got != 1.0 {
		t.Errorf("read x with entities.read = %v, want 1.0", got)
	}
}

// TestCapabilityVocab: parseCapabilities yields exactly the requested bits; an unknown capability
// string is rejected LOUDLY at parse, not silently ignored.
func TestCapabilityVocab(t *testing.T) {
	got, err := parseCapabilities([]string{"entities.read", "world.write"})
	if err != nil {
		t.Fatalf("parseCapabilities error: %v", err)
	}
	if got != (capEntitiesRead | capWorldWrite) {
		t.Errorf("parseCapabilities bits = %b, want %b", got, capEntitiesRead|capWorldWrite)
	}
	if got.has(capEntitiesWrite) || got.has(capWorldRead) || got.has(capNav) {
		t.Error("parseCapabilities granted a bit that was not requested")
	}

	// Empty -> zero capSet.
	if z, err := parseCapabilities(nil); err != nil || z != 0 {
		t.Errorf("parseCapabilities(nil) = %b (err %v), want 0", z, err)
	}

	// Unknown -> loud error naming the bad string.
	if _, err := parseCapabilities([]string{"entities.read", "world.destroy"}); err == nil {
		t.Fatal("parseCapabilities(unknown) returned no error, want a rejection")
	} else if !strings.Contains(err.Error(), "world.destroy") {
		t.Errorf("unknown-capability error = %q, want it to name 'world.destroy'", err.Error())
	}
}
