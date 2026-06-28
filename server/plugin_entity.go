package server

import (
	"fmt"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"go.starlark.net/starlark"
)

// plugin_entity.go — the Phase-23 entity/world HANDLE bridge (Option A, LOCKED in CONTEXT: the
// handles live in `server` because they need *TickLoop/*Entity/ChunkManager; server already imports
// plugin/starlark + plugin/host, one direction, no cycle).
//
// A handle is a THIN, tick-owned-safe starlark.Value: it carries ONLY an entity id (int32) +
// *TickLoop (NEVER a live *Entity pointer). Every read RE-RESOLVES t.entities.get(id) on the tick
// goroutine — the same id-carry / owner-re-resolve discipline pathReady.applyTo uses (server/async.go)
// — so a read of a removed entity returns a clean Starlark error, never a stale-pointer deref.
// Freeze() is a no-op (the handle holds no mutable Starlark state; the live entity is governed by
// TICK-05, not Starlark's frozen flag). Reads go through HasAttrs.Attr; mutates are bound *Builtin
// methods routing through the EXISTING tick-owned seams (setWantTarget/requestPath, direct vx/vy/vz,
// attribute.Map, ChunkManager.SetBlock + broadcastBlockUpdate). Each op enforces the owning plugin's
// capabilities at the handle-op boundary (plugin_capability.go).

// ----------------------------------------------------------------------------------------------
// entityHandle
// ----------------------------------------------------------------------------------------------

// entityHandle is the thin Starlark handle over a live entity. It stores the entity id (re-resolved
// each access) + the tick loop + the owning plugin's capability set — NEVER a *Entity field.
type entityHandle struct {
	t    *TickLoop
	id   int32
	caps capSet
}

// newEntityHandle builds an entity handle for an id with the given capability set. Wave 2 threads the
// real manifest-derived capSet through buildAIFromDecl; tests construct handles directly.
func newEntityHandle(t *TickLoop, id int32, caps capSet) *entityHandle {
	return &entityHandle{t: t, id: id, caps: caps}
}

// Compile-time interface assertions.
var (
	_ starlark.Value    = (*entityHandle)(nil)
	_ starlark.HasAttrs = (*entityHandle)(nil)
	_ starlark.Value    = (*worldHandle)(nil)
	_ starlark.HasAttrs = (*worldHandle)(nil)
)

func (h *entityHandle) String() string        { return fmt.Sprintf("<entity %d>", h.id) }
func (h *entityHandle) Type() string          { return "entity" }
func (h *entityHandle) Freeze()               {} // no mutable Starlark state -> no-op (Pitfall 1)
func (h *entityHandle) Truth() starlark.Bool  { return starlark.True }
func (h *entityHandle) Hash() (uint32, error) { return uint32(h.id), nil }

// bound binds a handle method as a *starlark.Builtin receiver-method (the entity.move_to(...) form).
func (h *entityHandle) bound(name string,
	fn func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error),
) starlark.Value {
	return starlark.NewBuiltin(name, fn).BindReceiver(h)
}

// Attr is the READ + mutate-method dispatch. READS re-resolve the entity on the tick goroutine and
// return frozen scalars (gated on capEntitiesRead); a removed entity returns a clean error (Pitfall
// 4). MUTATES return bound *Builtin methods (their own capability check fires when CALLED).
func (h *entityHandle) Attr(name string) (starlark.Value, error) {
	// Mutate methods are returned WITHOUT a read-capability check — the method enforces its own
	// write capability when invoked, so a write-only plugin can still fetch the method.
	switch name {
	case "move_to":
		return h.bound("move_to", h.moveTo), nil
	case "set_velocity":
		return h.bound("set_velocity", h.setVelocity), nil
	case "set_attribute":
		return h.bound("set_attribute", h.setAttribute), nil
	case "attribute":
		return h.bound("attribute", h.attribute), nil
	}

	// Everything below is a READ — require capEntitiesRead, then re-resolve the entity.
	if !h.caps.has(capEntitiesRead) {
		return nil, capError("entities.read")
	}
	e, ok := h.t.entities.get(h.id)
	if !ok {
		return nil, fmt.Errorf("entity %d no longer exists", h.id)
	}
	switch name {
	case "x":
		return starlark.Float(e.x), nil
	case "y":
		return starlark.Float(e.y), nil
	case "z":
		return starlark.Float(e.z), nil
	case "yaw":
		return starlark.Float(float64(e.yaw)), nil
	case "pitch":
		return starlark.Float(float64(e.pitch)), nil
	case "on_ground":
		return starlark.Bool(e.onGround), nil
	case "type":
		return starlark.String(entityTypeName(e.typ)), nil
	case "velocity":
		return starlark.Tuple{starlark.Float(e.vx), starlark.Float(e.vy), starlark.Float(e.vz)}, nil
	case "health":
		// Health is attribute-derived in Sulfur (no separate health field yet) — the max-health
		// attribute fold, nil-safe via getAttributeValue.
		return starlark.Float(e.getAttributeValue(attribute.MaxHealth)), nil
	}
	// HasAttrs contract: (nil, nil) == "no such field".
	return nil, nil
}

// AttrNames lists the read + mutate names (the full handle surface).
func (h *entityHandle) AttrNames() []string {
	return []string{
		"x", "y", "z", "yaw", "pitch", "on_ground", "type", "velocity", "health",
		"attribute", "move_to", "set_velocity", "set_attribute",
	}
}

// resolve re-resolves the entity for a mutate method, returning a clean Starlark error if it is gone.
func (h *entityHandle) resolve() (*Entity, error) {
	e, ok := h.t.entities.get(h.id)
	if !ok {
		return nil, fmt.Errorf("entity %d no longer exists", h.id)
	}
	return e, nil
}

// attribute(name) READS a named attribute's folded value (entities.read).
func (h *entityHandle) attribute(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capEntitiesRead) {
		return nil, capError("entities.read")
	}
	var name string
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &name); err != nil {
		return nil, err
	}
	e, err := h.resolve()
	if err != nil {
		return nil, err
	}
	if e.attributes == nil {
		return starlark.Float(0.0), nil
	}
	return starlark.Float(e.attributes.GetValue(name)), nil
}

// moveTo(x,y,z) MUTATES through the nav seam: setWantTarget -> groundNavigation.requestPath (the
// async A* already wired via pathPool). It sets a TARGET only — a POSITION change happens later via
// navigation.tick -> moveEntity (Pitfall 5: never a raw e.x write). Requires entities.write AND nav.
func (h *entityHandle) moveTo(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capEntitiesWrite) {
		return nil, capError("entities.write")
	}
	if !h.caps.has(capNav) {
		return nil, capError("nav")
	}
	var x, y, z float64
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 3, &x, &y, &z); err != nil {
		return nil, err
	}
	e, err := h.resolve()
	if err != nil {
		return nil, err
	}
	if e.ai == nil {
		return nil, fmt.Errorf("entity %d has no AI (cannot move_to)", h.id)
	}
	e.ai.setWantTarget(x, y, z)
	return starlark.None, nil
}

// setVelocity(vx,vy,vz) MUTATES vx/vy/vz DIRECTLY — the LOCKED faithful exception (CONTEXT decision
// 2). Entity.setDeltaMovement is a direct field write (jar bytecode); LivingEntity.knockback (the
// canonical external push) calls it directly; the next tickPhysics integrates it (gravity -> drag ->
// friction -> moveEntity swept collision -> onGround). The fields are tick-owned and collision-
// integrated next tick, so a direct set is correct. Requires entities.write.
func (h *entityHandle) setVelocity(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capEntitiesWrite) {
		return nil, capError("entities.write")
	}
	var vx, vy, vz float64
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 3, &vx, &vy, &vz); err != nil {
		return nil, err
	}
	e, err := h.resolve()
	if err != nil {
		return nil, err
	}
	e.vx, e.vy, e.vz = vx, vy, vz
	return starlark.None, nil
}

// setAttribute(name,val) MUTATES through attribute.Map: GetInstance materializes the entity's local
// instance, SetBaseValue overrides the base (the value fold/clamp stays vanilla-faithful). An
// attribute the entity does not have errors. Requires entities.write.
func (h *entityHandle) setAttribute(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capEntitiesWrite) {
		return nil, capError("entities.write")
	}
	var name string
	var val float64
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 2, &name, &val); err != nil {
		return nil, err
	}
	e, err := h.resolve()
	if err != nil {
		return nil, err
	}
	if e.attributes == nil {
		return nil, fmt.Errorf("entity %d has no attributes", h.id)
	}
	inst := e.attributes.GetInstance(name)
	if inst == nil {
		return nil, fmt.Errorf("entity %d has no attribute %q", h.id, name)
	}
	inst.SetBaseValue(val)
	return starlark.None, nil
}

// entityTypeName resolves a wire entity-type id to its registry name (data/entity.ByID). An unknown
// id yields "unknown" (a defensive fallback; the live entity's typ always comes from the table).
func entityTypeName(typ entity.ID) string {
	if e, ok := entity.ByID[typ]; ok {
		return e.Name
	}
	return "unknown"
}

// ----------------------------------------------------------------------------------------------
// worldHandle
// ----------------------------------------------------------------------------------------------

// worldHandle is the thin Starlark handle over the world (the ChunkManager). It carries no id — just
// the tick loop + the owning plugin's capability set.
type worldHandle struct {
	t    *TickLoop
	caps capSet
}

// newWorldHandle builds a world handle with the given capability set.
func newWorldHandle(t *TickLoop, caps capSet) *worldHandle {
	return &worldHandle{t: t, caps: caps}
}

func (h *worldHandle) String() string        { return "<world>" }
func (h *worldHandle) Type() string          { return "world" }
func (h *worldHandle) Freeze()               {}
func (h *worldHandle) Truth() starlark.Bool  { return starlark.True }
func (h *worldHandle) Hash() (uint32, error) { return 0, nil }

func (h *worldHandle) bound(name string,
	fn func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error),
) starlark.Value {
	return starlark.NewBuiltin(name, fn).BindReceiver(h)
}

func (h *worldHandle) Attr(name string) (starlark.Value, error) {
	switch name {
	case "block_at":
		return h.bound("block_at", h.blockAt), nil
	case "set_block":
		return h.bound("set_block", h.setBlock), nil
	case "entities_near":
		return h.bound("entities_near", h.entitiesNear), nil
	}
	return nil, nil
}

func (h *worldHandle) AttrNames() []string {
	return []string{"block_at", "set_block", "entities_near"}
}

// blockAt(x,y,z) READS the block state id at a position (world.read). Returns a tuple
// (state_id, ok) so a read of an unloaded column is observable, not a silent 0.
func (h *worldHandle) blockAt(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capWorldRead) {
		return nil, capError("world.read")
	}
	var x, y, z int
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 3, &x, &y, &z); err != nil {
		return nil, err
	}
	if h.t.world == nil {
		return starlark.Tuple{starlark.MakeInt(0), starlark.False}, nil
	}
	pos := pk.Position{X: x, Y: y, Z: z}
	state, ok := h.t.world.GetBlock(pos, dimMinY)
	return starlark.Tuple{starlark.MakeInt(int(state)), starlark.Bool(ok)}, nil
}

// setBlock(x,y,z,state) MUTATES through ChunkManager.SetBlock + broadcastBlockUpdate (world.write),
// so the change persists in the tick-owned world AND the clients see it. Returns True if the block
// changed (SetBlock reported a change on a loaded column).
func (h *worldHandle) setBlock(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capWorldWrite) {
		return nil, capError("world.write")
	}
	var x, y, z, state int
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 4, &x, &y, &z, &state); err != nil {
		return nil, err
	}
	if h.t.world == nil {
		return starlark.False, nil
	}
	pos := pk.Position{X: x, Y: y, Z: z}
	changed := h.t.world.SetBlock(pos, block.StateID(state), dimMinY)
	if changed {
		h.t.broadcastBlockUpdate(pos, block.StateID(state))
	}
	return starlark.Bool(changed), nil
}

// entitiesNear(x,z,radiusChunks) READS the entities in the per-section buckets around a position
// (world.read), returning a list of entity handles (each carrying the same capability set, so a read
// of a nearby entity still requires entities.read). radiusChunks is the chunk-column radius the
// broad-phase scans (entityStore.near).
func (h *worldHandle) entitiesNear(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capWorldRead) {
		return nil, capError("world.read")
	}
	var x, z float64
	var radiusChunks int
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 3, &x, &z, &radiusChunks); err != nil {
		return nil, err
	}
	if h.t.entities == nil {
		return starlark.NewList(nil), nil
	}
	near := h.t.entities.near(x, z, radiusChunks)
	out := make([]starlark.Value, 0, len(near))
	for _, e := range near {
		out = append(out, newEntityHandle(h.t, e.id, h.caps))
	}
	return starlark.NewList(out), nil
}
