package server

import (
	"fmt"
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
	"go.starlark.net/starlark"
)

// plugin_entity.go — the Phase-23 entity/world HANDLE bridge (Option A, LOCKED in CONTEXT: the
// handles live in `server` because they need *TickLoop/*Entity/ChunkManager; server already imports
// plugin/starlark + plugin/host, one direction, no cycle).
//
// A handle is a THIN, tick-owned-safe starlark.Value: it carries ONLY an entity id (int32) +
// *TickLoop (NEVER a live *Entity pointer). Every read RE-RESOLVES t.cur().entities.get(id) on the tick
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
	t  *TickLoop
	id int32
	// region is the Phase-27 STEP-3 OWNING region this handle resolves the entity against (approach
	// (a): the handle is region-bound at build time inside r.tick, so a callback for a mob in region R
	// resolves R's store — Pitfall 4). nil for a handle built outside a region context (a direct test
	// handle, the debug seam): store() then falls back to t.only() (the goroutine-local resolution,
	// which is globalRegion off the fan-out). NEVER a live *Entity — only the region's store pointer.
	region *region
	caps   capSet
	// scratch is the per-(mob,goal) mutable state bag the get_state/set_state seam reads/writes — the
	// frozen-Starlark-boundary fix (FIDELITY GAP 2). It is the OWNING goal's scratch map (allocated in
	// buildAIFromDecl, threaded through starlarkGoal.handles), so set_state from a goal callback
	// mutates that goal's own per-mob state (the lookAtPlayerGoal.lookTime / randomLookAroundGoal.relX
	// /relZ struct-field analogue). nil for a handle built without a goal (entities_near results, the
	// debug seam) — get_state then returns the default and set_state errors cleanly. Tick-owned.
	scratch map[string]float64
}

// store returns the entity store this handle resolves against: the bound OWNING region's store when
// the handle is region-bound (the fan-out goal-callback path — Phase-27 Pitfall 4), else t.only()
// (the goroutine-local fallback: the region currently registered, or globalRegion off the fan-out).
// Centralizing it keeps every read/mutate path resolving against the SAME store.
func (h *entityHandle) store() *entityStore {
	if h.region != nil {
		// Fall back to the entity's ACTUAL owning region when the position-bound store does not hold
		// the id — a mob that walked into another region's column is bound to the position-derived
		// region in handles(), but its store entry transfers 1+ ticks later (or never, in a direct-drive
		// test). See navHandle.store() for the full rationale (the region-crossing-mob lockstep fix).
		if _, ok := h.region.entities.get(h.id); ok {
			return h.region.entities
		}
		if r := h.t.owningRegion(h.id); r != nil {
			return r.entities
		}
		return h.region.entities
	}
	return h.t.cur().entities
}

// newEntityHandle builds an entity handle for an id with the given capability set (no goal scratch).
// Wave 2 threads the real manifest-derived capSet through buildAIFromDecl; tests construct handles
// directly. Used where there is no owning goal (entities_near results, direct test handles). The
// region is left nil (store() falls back to t.only()).
func newEntityHandle(t *TickLoop, id int32, caps capSet) *entityHandle {
	return &entityHandle{t: t, id: id, caps: caps}
}

// newEntityHandleInRegion builds a region-BOUND entity handle (Phase-27 STEP-3): the handle resolves
// the entity against `region`'s store, so a goal callback for a mob in region R finds it in R (even
// if the resolving goroutine changed). Used by starlarkGoal.handles and the cross-region
// entities_near results so the whole handle graph a callback touches is region-consistent.
func newEntityHandleInRegion(t *TickLoop, region *region, id int32, caps capSet) *entityHandle {
	return &entityHandle{t: t, region: region, id: id, caps: caps}
}

// newEntityHandleInRegionWithScratch is newEntityHandleInRegion carrying the owning goal's per-mob
// scratch (the get_state/set_state bag) — the region-bound twin of newEntityHandleWithScratch.
func newEntityHandleInRegionWithScratch(t *TickLoop, region *region, id int32, caps capSet, scratch map[string]float64) *entityHandle {
	return &entityHandle{t: t, region: region, id: id, caps: caps, scratch: scratch}
}

// newEntityHandleWithScratch builds an entity handle that also carries the OWNING goal's per-mob
// scratch map, so a goal callback's get_state/set_state reaches that goal's own mutable state. The
// scratch is allocated once per spawned mob per goal (buildAIFromDecl) and re-threaded fresh per
// callback call (starlarkGoal.handles) — the handle never outlives the call, the scratch persists on
// the goal. Tick-owned (mutated only on the tick goroutine).
func newEntityHandleWithScratch(t *TickLoop, id int32, caps capSet, scratch map[string]float64) *entityHandle {
	return &entityHandle{t: t, id: id, caps: caps, scratch: scratch}
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
	case "damage_in_tag":
		return h.bound("damage_in_tag", h.damageInTag), nil
	case "nearest_player_holding_carrot_on_a_stick":
		return h.bound("nearest_player_holding_carrot_on_a_stick", h.nearestPlayerHoldingCarrotOnAStick), nil
	case "nearest_player_holding_pig_food":
		return h.bound("nearest_player_holding_pig_food", h.nearestPlayerHoldingPigFood), nil
	case "nearest_player_holding_food":
		return h.bound("nearest_player_holding_food", h.nearestPlayerHoldingFood), nil
	case "eat_grass_block":
		return h.bound("eat_grass_block", h.eatGrassBlockHandle), nil
	case "eat_broadcast_byte10":
		return h.bound("eat_broadcast_byte10", h.eatBroadcastByte10Handle), nil
	case "nearest_breeding_partner":
		return h.bound("nearest_breeding_partner", h.nearestBreedingPartner), nil
	case "nearest_adult_parent":
		return h.bound("nearest_adult_parent", h.nearestAdultParent), nil
	case "try_breed":
		return h.bound("try_breed", h.tryBreed), nil
	case "set_look":
		return h.bound("set_look", h.setLook), nil
	case "set_look_at":
		return h.bound("set_look_at", h.setLookAt), nil
	case "rand_int":
		return h.bound("rand_int", h.randInt), nil
	case "rand_float":
		return h.bound("rand_float", h.randFloat), nil
	case "rand_double":
		return h.bound("rand_double", h.randDouble), nil
	case "get_state":
		return h.bound("get_state", h.getState), nil
	case "set_state":
		return h.bound("set_state", h.setState), nil
	}

	// Everything below is a READ — require capEntitiesRead, then re-resolve the entity.
	if !h.caps.has(capEntitiesRead) {
		return nil, capError("entities.read")
	}
	e, ok := h.store().get(h.id)
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
	case "was_hurt":
		// MOB-SUB-02 frozen scalar: whether the mob is CURRENTLY in its hurt-flash window. The predicate
		// is e.hurtTime > 0 — LivingEntity.hurtTime is set to hurtDuration (10) on a fresh hit in
		// hurtServer and decremented unconditionally each tick in baseTick (the red-flash timer); it is
		// the canonical "this mob was just hurt" signal a goal reacts to (PanicGoal in P31 pairs it with
		// last_damage_type against the panic_causes tag). It is host-COMPUTED here (the goal never holds a
		// live source) and re-resolved through the region-bound h.store() (Pitfall 7), never t.cur().
		return starlark.Bool(e.hurtTime > 0), nil
	case "last_damage_type":
		// MOB-SUB-02 frozen scalar: the damage-type id of the mob's lastDamageSource (the genuine ported
		// source set in hurtServer's flag2 block, combat_mob.go). Returned as a plain Starlark int — the
		// goal resolves the tag membership by NAME via data/tag (e.g. is the id in panic_causes), it never
		// holds a live DamageSource. Re-resolved through the region-bound h.store(), never t.cur().
		return starlark.MakeInt(int(e.lastDamageSource.typeTag)), nil
	case "has_last_damage":
		// MOB-SUB-02 / P31 frozen scalar (Decision B): the host-COMPUTED faithful
		// LivingEntity.getLastDamageSource() != null mirror — e.hasLastDamage, set TRUE in hurtServer's
		// flag2 block the first time a real source is recorded (combat_mob.go). PanicGoal.shouldPanic
		// pairs it with damage_in_tag("panic_causes"). It is a SEPARATE bool because typeTag==0
		// (minecraft:in_fire) is a real panic_causes member, so the raw id cannot represent "unset"
		// (data/tag/tags.go:152). Re-resolved through the region-bound h.store() (Pitfall 7), never
		// t.cur() — same discipline as was_hurt.
		return starlark.Bool(e.hasLastDamage), nil
	case "in_water":
		// MOB-SUB-04 frozen scalar: whether the mob's AABB intersects any water cell (the FloatGoal.canUse
		// predicate). Host-COMPUTED through the handle's owner-bound TickLoop over the entity already
		// re-resolved via h.store() (NOT t.cur() — Pitfall 7); the goal never holds a live fluid state.
		return starlark.Bool(h.t.mobInWater(e)), nil
	case "fluid_height":
		// MOB-SUB-04 frozen scalar: the WATER fluid height at the mob (Entity.getFluidHeight(WATER), the
		// MAX over the AABB). FloatGoal.canUse compares it against the jump threshold. Host-computed,
		// re-resolved via h.store().
		return starlark.Float(h.t.mobFluidHeight(e, fluidWater)), nil
	case "in_lava":
		// MOB-SUB-04 frozen scalar: whether the mob's AABB intersects any lava cell (the second
		// FloatGoal.canUse disjunct). Host-computed, re-resolved via h.store().
		return starlark.Bool(h.t.mobInLava(e)), nil
	case "is_in_love":
		// MOB-SUB-09 frozen scalar: net.minecraft.world.entity.animal.Animal.isInLove() == inLove>0.
		// BreedGoal.canUse gates on it (the FIRST line: if (!animal.isInLove()) return false) — the
		// oracle-safety contract: an un-fed adult (inLove==0) makes breed_can_use return false BEFORE the
		// partner scan, so the breed path (and its host RNG draws) never fires on the lone-adult oracle.
		// Host-COMPUTED here, re-resolved through the region-bound h.store() (Pitfall 7), never t.cur() —
		// the same discipline as was_hurt. The .star never sees the raw inLove timer, only the bool.
		return starlark.Bool(e.isInLove()), nil
	case "is_baby":
		// MOB-SUB-09 frozen scalar: net.minecraft.world.entity.AgeableMob.isBaby() == breedAge<0.
		// FollowParentGoal.canUse gates on it (only a baby follows: if (animal.getAge() >= 0) return
		// false) — the oracle-safety contract: an adult (breedAge>=0) makes follow_can_use return false,
		// so the follow path is dormant on the lone-adult oracle. Host-computed, re-resolved via h.store().
		return starlark.Bool(e.isBaby()), nil
	case "breed_age":
		// MOB-SUB-09 frozen scalar: net.minecraft.world.entity.AgeableMob.getAge() == breedAge (the
		// signed-int age machine: <0 baby ticks up, >0 cooldown ticks down, ==0 adult). Exposed for the
		// .star follow/breed callbacks to read the same age the Go-native goals read (e.breedAge), so the
		// two halves agree on the baby/adult/cooldown state. Host-computed, re-resolved via h.store().
		return starlark.MakeInt(e.breedAge), nil
	}
	// HasAttrs contract: (nil, nil) == "no such field".
	return nil, nil
}

// AttrNames lists the read + mutate names (the full handle surface).
func (h *entityHandle) AttrNames() []string {
	return []string{
		"x", "y", "z", "yaw", "pitch", "on_ground", "type", "velocity", "health",
		"was_hurt", "last_damage_type", "has_last_damage", "damage_in_tag",
		"nearest_player_holding_carrot_on_a_stick", "nearest_player_holding_pig_food",
		"nearest_player_holding_food", "eat_grass_block", "eat_broadcast_byte10",
		"nearest_breeding_partner", "nearest_adult_parent", "try_breed",
		"is_in_love", "is_baby", "breed_age",
		"in_water", "fluid_height", "in_lava",
		"attribute", "move_to", "set_velocity", "set_attribute",
		"set_look", "set_look_at", "rand_int", "rand_float", "rand_double", "get_state", "set_state",
	}
}

// resolve re-resolves the entity for a mutate method, returning a clean Starlark error if it is gone.
func (h *entityHandle) resolve() (*Entity, error) {
	e, ok := h.store().get(h.id)
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

// damageInTag(name) READS whether the mob's lastDamageSource is a member of the named damage-type tag
// (entities.read). It is the plugin analog of the Go src.is("...") read and the host-side port of
// DamageSource.is(TagKey) (damage_source.go:78) — PanicGoal's shouldPanic tests panic_causes
// membership BY NAME without duplicating the id set into Starlark (Decision C); the membership table
// stays on the Go side. Modeled on attribute(): gated on capEntitiesRead, one positional string arg,
// resolved via the region-bound h.resolve() (NOT t.cur() — Pitfall 7). An unknown tag yields false
// (the zero-value map read, exactly Holder.is over an empty tag), never a panic.
func (h *entityHandle) damageInTag(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capEntitiesRead) {
		return nil, capError("entities.read")
	}
	var tagName string
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &tagName); err != nil {
		return nil, err
	}
	e, err := h.resolve()
	if err != nil {
		return nil, err
	}
	return starlark.Bool(e.lastDamageSource.is(tagName)), nil
}

// nearestPlayerHoldingCarrotOnAStick / nearestPlayerHoldingPigFood are the host-computed
// nearest-tempt-player scans (MOB-SUB-06). They mirror damageInTag (the HOST owns the item-id set:
// the carrot_on_a_stick literal id 887 / the pig_food tag) AND worldHandle.nearestPlayer (the
// tuple-or-None return) — the .star NEVER sees an item id, only a position tuple or None. One
// positional arg: range (the TEMPT_RANGE the goal passes). Re-resolved via h.resolve() (the
// region-bound store, NOT t.cur() — Pitfall 7). Gated on capEntitiesRead (matching damageInTag).
func (h *entityHandle) nearestPlayerHoldingCarrotOnAStick(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capEntitiesRead) {
		return nil, capError("entities.read")
	}
	var maxDist float64
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &maxDist); err != nil {
		return nil, err
	}
	e, err := h.resolve()
	if err != nil {
		return nil, err
	}
	// Items.CARROT_ON_A_STICK literal (data/item/item.go:5340, id 887) — vanilla pred i.is(CARROT_ON_A_STICK).
	x, y, z, ok := nearestPlayerHolding(h.t, e, maxDist, func(id int32) bool { return id == 887 })
	if !ok {
		return starlark.None, nil
	}
	return starlark.Tuple{starlark.Float(x), starlark.Float(y), starlark.Float(z)}, nil
}

func (h *entityHandle) nearestPlayerHoldingPigFood(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capEntitiesRead) {
		return nil, capError("entities.read")
	}
	var maxDist float64
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &maxDist); err != nil {
		return nil, err
	}
	e, err := h.resolve()
	if err != nil {
		return nil, err
	}
	// ItemTags.PIG_FOOD (data/tag/tags.go:305 {1257,1258,1317}) — vanilla pred i.is(ItemTags.PIG_FOOD).
	x, y, z, ok := nearestPlayerHolding(h.t, e, maxDist, func(id int32) bool { return itemInTag(id, "pig_food") })
	if !ok {
		return starlark.None, nil
	}
	return starlark.Tuple{starlark.Float(x), starlark.Float(y), starlark.Float(z)}, nil
}

// nearestPlayerHoldingFood is the PARAMETERIZED tempt-player scan (MOB-PASS-01) the new cow/sheep/chicken
// TemptGoal callbacks use. It is nearestPlayerHoldingPigFood with the food tag PASSED IN (TWO positional
// args: tag string, range float64) instead of the pig_food literal — the Go port of
// net.minecraft.world.entity.animal.Animal.isFood = stack.is(ItemTags.<X>_FOOD), parameterized by the
// declared food tag (cow_food / sheep_food / chicken_food). The HOST still owns the itemInTag membership
// (the .star never sees an item id, only a position tuple or None); the .star only chooses WHICH food tag.
// Re-resolved via h.resolve() (the region-bound store — Pitfall 7). Gated on capEntitiesRead (matching
// nearestPlayerHoldingPigFood). Draws no RNG. The pig's nearestPlayerHoldingPigFood is left UNTOUCHED so
// the byte-identical oracle is unperturbed.
func (h *entityHandle) nearestPlayerHoldingFood(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capEntitiesRead) {
		return nil, capError("entities.read")
	}
	var tag string
	var maxDist float64
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 2, &tag, &maxDist); err != nil {
		return nil, err
	}
	e, err := h.resolve()
	if err != nil {
		return nil, err
	}
	// Animal.isFood = stack.is(ItemTags.<tag>) — the Phase-32 tag read, parameterized by the declared tag.
	x, y, z, ok := nearestPlayerHolding(h.t, e, maxDist, func(id int32) bool { return itemInTag(id, tag) })
	if !ok {
		return starlark.None, nil
	}
	return starlark.Tuple{starlark.Float(x), starlark.Float(y), starlark.Float(z)}, nil
}

// eatGrassBlockHandle is the EatBlockGoal eat seam (MOB-PASS-02) the sheep .star calls when its
// EatBlockGoal acts (the tick == adjustedTickDelay(4) eat). It does the HOST-side block write (a
// grass_block-below -> dirt swap + the sheep wool regrow + a baby ageUp) — see sheep_eat.go's
// eatGrassBlock for the 1:1 EatBlockGoal.tick + Sheep.ate port. The block write needs capWorldWrite (the
// sheep plugin.toml grants world.write); the .star passes NO position, so the seam can only ever touch the
// mob's OWN below-position (T-34-04/T-34-05). Re-resolved via h.resolve() (Pitfall 7). Draws no RNG.
func (h *entityHandle) eatGrassBlockHandle(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capWorldWrite) {
		return nil, capError("world.write")
	}
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 0); err != nil {
		return nil, err
	}
	e, err := h.resolve()
	if err != nil {
		return nil, err
	}
	h.t.eatGrassBlock(e)
	return starlark.None, nil
}

// eatBroadcastByte10Handle is the EatBlockGoal.start broadcast seam (MOB-PASS-02): it fires
// ClientboundEntityEvent byte 10 (the EatBlockGoal eat-animation status — Mob.broadcastEntityEvent(mob,
// (byte)10)) to the sheep's trackers so the client plays the head-down grass-eat animation. The .star
// calls it on EatBlockGoal.start. It is a metadata/event broadcast, so it gates on capEntitiesWrite.
// Re-resolved via h.resolve() (Pitfall 7). Draws no RNG.
func (h *entityHandle) eatBroadcastByte10Handle(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capEntitiesWrite) {
		return nil, capError("entities.write")
	}
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 0); err != nil {
		return nil, err
	}
	e, err := h.resolve()
	if err != nil {
		return nil, err
	}
	// EatBlockGoal.start: level.broadcastEntityEvent(mob, (byte)10) — the eat-animation client event.
	h.t.broadcastToTrackers(e.id, encodeEntityEvent(e.id, 10))
	return starlark.None, nil
}

// nearestBreedingPartner(range) is the host-computed BreedGoal.getFreePartner scan (MOB-SUB-09): the
// nearest same-class in-love non-panicking partner within `range` blocks. It mirrors
// nearestPlayerHoldingPigFood (the HOST owns the same-class + canMate + isPanicking filter; the .star
// NEVER sees an entity id or class set, only a position tuple or None) and routes through the EXACT
// same findFreePartner the Go-native breedGoal.getFreePartner calls — so the plugin pig and the Go pig
// pick the IDENTICAL partner (the lockstep contract, Plan 33-04). The .star breed_can_use uses the
// returned tuple only to navigate; the actual breed (and its RNG draws) goes through try_breed below.
// One positional arg: range. Re-resolved via h.resolve() (the region-bound store — Pitfall 7). Gated on
// capEntitiesRead (matching nearestPlayerHoldingPigFood). Draws no RNG.
func (h *entityHandle) nearestBreedingPartner(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capEntitiesRead) {
		return nil, capError("entities.read")
	}
	var rng float64
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &rng); err != nil {
		return nil, err
	}
	e, err := h.resolve()
	if err != nil {
		return nil, err
	}
	partner := findFreePartner(h.t, e, rng)
	if partner == nil {
		return starlark.None, nil
	}
	return starlark.Tuple{starlark.Float(partner.x), starlark.Float(partner.y), starlark.Float(partner.z)}, nil
}

// nearestAdultParent(range) is the host-computed FollowParentGoal.canUse adult scan (MOB-SUB-09): the
// nearest same-class ADULT (breedAge>=0) within the inflate(8,4,8) box that is NOT already within 3
// blocks. It routes through the EXACT same findNearestAdultParent the Go-native followParentGoal.canUse
// calls — so the plugin pig and the Go pig pick the IDENTICAL parent (the lockstep contract). The
// `range` arg is accepted for symmetry with nearestBreedingPartner (the scan box is the jar's fixed
// inflate(8,4,8), so the arg is informational — the host owns the box). Re-resolved via h.resolve();
// gated on capEntitiesRead; the .star sees only a tuple or None. Draws no RNG.
func (h *entityHandle) nearestAdultParent(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capEntitiesRead) {
		return nil, capError("entities.read")
	}
	var rng float64
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &rng); err != nil {
		return nil, err
	}
	_ = rng // the FollowParentGoal scan box is the fixed jar inflate(8,4,8); the range arg is symmetry only
	e, err := h.resolve()
	if err != nil {
		return nil, err
	}
	parent := findNearestAdultParent(h.t, e)
	if parent == nil {
		return starlark.None, nil
	}
	return starlark.Tuple{starlark.Float(parent.x), starlark.Float(parent.y), starlark.Float(parent.z)}, nil
}

// tryBreed(range) is THE LOCKSTEP host breed op (MOB-SUB-09, Plan 33-04): the .star breed_tick calls it
// at the breed threshold (loveTime>=60 && within 3 blocks), and the HOST re-finds the partner (the SAME
// findFreePartner scan) and routes through t.breed(e, partner) — the EXACT same TickLoop.breed the
// Go-native breedGoal.tick calls. This is the make-or-break lockstep: both the Go pig AND the plugin pig
// fire ONE host breed() that draws the variant nextBoolean() FIRST then the XP 1+nextInt(7) SECOND, both
// on `e`'s per-mob RNG in the jar's exact order (Plan 33-03 javap). The .star NEVER draws breed RNG
// itself — it only signals readiness, so the two halves' RNG streams are byte-identical. Returns True if
// a breed fired (a partner was still in range), False otherwise (the partner wandered off between the
// scan and the threshold). Gated on capEntitiesWrite (a breed spawns a child + mutates both parents — a
// write, like move_to). Re-resolved via h.resolve(); draws RNG ONLY through t.breed (dormant on the
// un-fed oracle, which never reaches breed_tick).
func (h *entityHandle) tryBreed(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capEntitiesWrite) {
		return nil, capError("entities.write")
	}
	var rng float64
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &rng); err != nil {
		return nil, err
	}
	e, err := h.resolve()
	if err != nil {
		return nil, err
	}
	partner := findFreePartner(h.t, e, rng)
	if partner == nil {
		return starlark.False, nil // the partner is gone — no breed this tick (the goal re-acquires)
	}
	h.t.breed(e, partner) // THE one host breed — variant nextBoolean() then XP nextInt(7), the lockstep
	return starlark.True, nil
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

// setLook(yaw, pitch=0.0) MUTATES the body+head facing — EXACTLY what lookAtPlayerGoal.tick /
// randomLookAroundGoal.tick do today (ai_goals_passive.go: e.headYaw = e.yaw = yaw, set instantly;
// the v1 non-gradual LookControl analogue). These are tick-owned plain fields the entity tracker
// auto-broadcasts (RotateHead / the next TeleportEntity). A faithful set_look therefore writes BOTH
// headYaw AND yaw (and pitch) so it is behavior-identical to the Go goal it replaces (Pitfall 2).
// Requires entities.write (a mutate, gated like setVelocity/move_to). Non-finite yaw/pitch are
// clamped to 0 (T-24-03: never feed NaN/Inf to the wire byte-angle packer).
func (h *entityHandle) setLook(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capEntitiesWrite) {
		return nil, capError("entities.write")
	}
	var yaw float64
	pitch := 0.0
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "yaw", &yaw, "pitch?", &pitch); err != nil {
		return nil, err
	}
	if math.IsNaN(yaw) || math.IsInf(yaw, 0) {
		yaw = 0
	}
	if math.IsNaN(pitch) || math.IsInf(pitch, 0) {
		pitch = 0
	}
	e, err := h.resolve()
	if err != nil {
		return nil, err
	}
	// EXACTLY what the Go look goals wrote: head AND body yaw instantly, plus the pitch.
	e.headYaw = float32(yaw)
	e.yaw = float32(yaw)
	e.pitch = float32(pitch)
	return starlark.None, nil
}

// setLookAt(x, y, z) aims the mob at a WORLD POINT — the faithful LookControl.setLookAt(x,eyeY,z)
// analogue both LookAtPlayerGoal.tick and RandomLookAroundGoal.tick call in the jar bytecode (they
// pass a world point, NOT a precomputed yaw). The host derives the body/head yaw from the horizontal
// delta via the SAME yawTowardDeg the Go oracle goals use (e.headYaw = e.yaw = yawTowardDeg(px-e.x,
// pz-e.z)), so set_look_at is behavior-identical to the Go goal it replaces (Pitfall 2). The y arg is
// accepted (the vanilla setLookAt takes the eye-Y) but the v1 non-gradual analogue sets only the
// horizontal yaw — matching the Go goals, which set yaw from (dx,dz) and leave pitch untouched.
// Requires entities.write (a look mutate, gated like set_look). Non-finite coords are ignored (no-op).
func (h *entityHandle) setLookAt(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capEntitiesWrite) {
		return nil, capError("entities.write")
	}
	var x, y, z float64
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 3, &x, &y, &z); err != nil {
		return nil, err
	}
	e, err := h.resolve()
	if err != nil {
		return nil, err
	}
	dx := x - e.x
	dz := z - e.z
	if math.IsNaN(dx) || math.IsInf(dx, 0) || math.IsNaN(dz) || math.IsInf(dz, 0) {
		return starlark.None, nil // ignore a non-finite target (never feed NaN to the byte-angle packer)
	}
	_ = y // the eye-Y is part of the vanilla signature; the v1 non-gradual yaw set ignores it (Go parity)
	yaw := yawTowardDeg(dx, dz) // the EXACT ported MC-convention yaw the Go look goals use
	e.headYaw = yaw
	e.yaw = yaw
	return starlark.None, nil
}

// randInt(n) draws a pseudo-random int in [0, n) from the mob's PER-ENTITY seeded source (e.ai.rng —
// the Mob.getRandom() analogue, ai_random.go). Deterministic for a fixed mob seed. NO capability gate
// (CONTEXT decision 1 / 24-RESEARCH: a read of the mob's OWN RNG, like has_path). n must be > 0. A
// mob with no AI/rng errors cleanly (the caller should only draw on a goal-bearing mob).
func (h *entityHandle) randInt(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var n int
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &n); err != nil {
		return nil, err
	}
	if n <= 0 {
		return nil, fmt.Errorf("rand_int(n): n must be > 0, got %d", n)
	}
	e, err := h.resolve()
	if err != nil {
		return nil, err
	}
	if e.ai == nil || e.ai.rng == nil {
		return nil, fmt.Errorf("entity %d has no AI random source (cannot rand_int)", h.id)
	}
	return starlark.MakeInt(e.ai.rng.nextInt(n)), nil
}

// randFloat() draws a pseudo-random float in [0, 1) from the mob's per-entity seeded source (the
// RandomSource.nextFloat() analogue). No capability gate (the mob's own RNG). A mob with no AI/rng
// errors cleanly.
func (h *entityHandle) randFloat(_ *starlark.Thread, _ *starlark.Builtin,
	_ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	e, err := h.resolve()
	if err != nil {
		return nil, err
	}
	if e.ai == nil || e.ai.rng == nil {
		return nil, fmt.Errorf("entity %d has no AI random source (cannot rand_float)", h.id)
	}
	return starlark.Float(float64(e.ai.rng.nextFloat())), nil
}

// randDouble() draws a pseudo-random double in [0, 1) from the mob's per-entity seeded source (the
// RandomSource.nextDouble() analogue). DISTINCT from rand_float() (nextFloat) — vanilla goals that
// draw a double (RandomLookAroundGoal.start: d = 2pi * getRandom().nextDouble()) MUST use this seam so
// the draw matches the Go oracle's nextDouble() exactly (a nextFloat substitute would diverge the RNG
// stream and break behavior-identity). No capability gate (the mob's own RNG). A mob with no AI/rng
// errors cleanly.
func (h *entityHandle) randDouble(_ *starlark.Thread, _ *starlark.Builtin,
	_ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	e, err := h.resolve()
	if err != nil {
		return nil, err
	}
	if e.ai == nil || e.ai.rng == nil {
		return nil, fmt.Errorf("entity %d has no AI random source (cannot rand_double)", h.id)
	}
	return starlark.Float(e.ai.rng.nextDouble()), nil
}

// getState(key, default=0.0) READS a per-(mob,goal) scratch value (the frozen-Starlark-boundary fix,
// FIDELITY GAP 2). It is the OWNING goal's mutable state bag — the analogue of the Go goal's struct
// fields (lookAtPlayerGoal.lookTime/lookX/Y/Z, randomLookAroundGoal.relX/relZ/lookTime). NO capability
// gate (the goal's own scratch, like rand_int/rand_float). A missing key returns the supplied default
// (or 0.0). A handle with no scratch (no owning goal) returns the default rather than erroring, so a
// read is always safe. Values are float64 (the only scalar the ported goals stash — counts/coords).
func (h *entityHandle) getState(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	def := 0.0
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "key", &key, "default?", &def); err != nil {
		return nil, err
	}
	if h.scratch == nil {
		return starlark.Float(def), nil
	}
	if v, ok := h.scratch[key]; ok {
		return starlark.Float(v), nil
	}
	return starlark.Float(def), nil
}

// setState(key, value) WRITES a per-(mob,goal) scratch value. NO capability gate (the goal's own
// state). A handle with no scratch (no owning goal) errors cleanly so a misuse is observable rather
// than a silent no-op. Tick-owned: mutated only on the tick goroutine from the goal callback — the
// scratch map is the goal's own, never shared across mobs (allocated per spawned mob in buildAIFromDecl).
func (h *entityHandle) setState(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	var value starlark.Value
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "key", &key, "value", &value); err != nil {
		return nil, err
	}
	// Accept either an int or a float (a Starlark `40 + rand_int(40)` is an int; coords are floats).
	// The scratch stores float64, so coerce both via AsFloat — a non-number value is a clean error.
	f, ok := starlark.AsFloat(value)
	if !ok {
		return nil, fmt.Errorf("set_state %q: value must be a number, got %s", key, value.Type())
	}
	if h.scratch == nil {
		return nil, fmt.Errorf("entity %d has no goal scratch (cannot set_state)", h.id)
	}
	h.scratch[key] = f
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
// the tick loop + the owning plugin's capability set. Phase-27 STEP-3: it also carries the owning
// region (for block reads it is irrelevant — the world is SHARED across regions — but it makes the
// handle graph a callback receives region-consistent + future-proofs a per-region world).
type worldHandle struct {
	t      *TickLoop
	region *region
	caps   capSet
}

// world returns the shared ChunkManager (wired into every region by SetWorld). Region-bound or not,
// it is the same world; the helper keeps the read site uniform.
func (h *worldHandle) world() *world.ChunkManager {
	if h.region != nil {
		return h.region.world
	}
	return h.t.world()
}

// newWorldHandle builds a world handle with the given capability set (region-unbound).
func newWorldHandle(t *TickLoop, caps capSet) *worldHandle {
	return &worldHandle{t: t, caps: caps}
}

// newWorldHandleInRegion builds a region-BOUND world handle (Phase-27 STEP-3): the world twin of
// newEntityHandleInRegion (the block store is shared, but the binding keeps the callback's handles
// region-consistent).
func newWorldHandleInRegion(t *TickLoop, region *region, caps capSet) *worldHandle {
	return &worldHandle{t: t, region: region, caps: caps}
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
	case "nearest_player":
		return h.bound("nearest_player", h.nearestPlayer), nil
	case "is_dark":
		return h.bound("is_dark", h.isDark), nil
	}
	return nil, nil
}

func (h *worldHandle) AttrNames() []string {
	return []string{"block_at", "set_block", "entities_near", "nearest_player", "is_dark"}
}

// isDark() READS the day/night darkness proxy (world.read): true when it is dark enough for hostiles
// (the night portion of the gametime cycle). It is the FORCED proxy the spawn gate uses
// (TickLoop.isDarkEnoughToSpawn, 35-02) standing in for the real sky/block-light read until the
// lighting engine lands. The Spider$SpiderAttackGoal daylight-flee is gated on its INVERSE (bright ==
// daytime == not is_dark): a spider drops its target 1-in-100/tick only when NOT dark. Exposed as a
// host seam so the vanilla_spider .star can express the daylight gate faithfully (the brightness read
// stays Go-side, cite-deferred to getLightLevelDependentMagicValue). NO RNG; tick-owned read; gated on
// capWorldRead. CITE: net.minecraft.world.entity.monster.Monster.isDarkEnoughToSpawn (the proxy) +
// Spider$SpiderAttackGoal.canContinueToUse (getLightLevelDependentMagicValue >= 0.5f -> bright).
func (h *worldHandle) isDark(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capWorldRead) {
		return nil, capError("world.read")
	}
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 0); err != nil {
		return nil, err
	}
	return starlark.Bool(h.t.isDarkEnoughToSpawn()), nil
}

// nearestPlayer(x,y,z,max_dist) READS the nearest PLAYER position in range (world.read). Players are
// NOT in the entityStore (entities_near returns mobs only) — they live in t.players, the same data
// the Go lookAtPlayerGoal.canUse reads via nearestPlayerWithin. This seam REUSES that exact scan
// (nearestPlayerAt — the position-based core factored out of nearestPlayerWithin), faithful to
// vanilla's ServerLevel.getNearestPlayer "nearest player in range". Returns a (px,py,pz) tuple, or
// None when no player is within range. Tick-owned read (TICK-05); gated on capWorldRead.
func (h *worldHandle) nearestPlayer(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capWorldRead) {
		return nil, capError("world.read")
	}
	var x, y, z, maxDist float64
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 4, &x, &y, &z, &maxDist); err != nil {
		return nil, err
	}
	px, py, pz, ok := nearestPlayerAt(h.t, x, y, z, maxDist)
	if !ok {
		return starlark.None, nil
	}
	return starlark.Tuple{starlark.Float(px), starlark.Float(py), starlark.Float(pz)}, nil
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
	if h.world() == nil {
		return starlark.Tuple{starlark.MakeInt(0), starlark.False}, nil
	}
	pos := pk.Position{X: x, Y: y, Z: z}
	state, ok := h.world().GetBlock(pos, dimMinY)
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
	if h.world() == nil {
		return starlark.False, nil
	}
	pos := pk.Position{X: x, Y: y, Z: z}
	changed := h.world().SetBlock(pos, block.StateID(state), dimMinY)
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
	// Phase-27 STEP-3 (N=2): entities_near resolves against the CALLING region's store (h.store()),
	// NOT across regions. A goal callback runs inside the fan-out where reading another region's store
	// would race it (the cross-region query is a BARRIER-only operation — the tracker uses
	// entitiesNearAcrossRegions there). So a goal sees the entities in its OWN region; this matches
	// the Folia single-owner discipline (a region tick must not touch another region's store). The
	// result handles are region-BOUND (h.region) so a later read re-resolves the same owning region.
	store := h.entityStore()
	if store == nil {
		return starlark.NewList(nil), nil
	}
	near := store.near(x, z, radiusChunks)
	out := make([]starlark.Value, 0, len(near))
	for _, e := range near {
		out = append(out, newEntityHandleInRegion(h.t, h.region, e.id, h.caps))
	}
	return starlark.NewList(out), nil
}

// entityStore returns the entity store the world handle queries: the bound region's store (a goal
// callback's region), else t.only() (the goroutine-local fallback).
func (h *worldHandle) entityStore() *entityStore {
	if h.region != nil {
		return h.region.entities
	}
	return h.t.cur().entities
}

// ----------------------------------------------------------------------------------------------
// navHandle (Wave 2: the wander gate's one mutate seam)
// ----------------------------------------------------------------------------------------------

// navHandle is the thin Starlark handle over a mob's NAVIGATION — the seam a declared MOVE goal's
// tick callback uses to (re)target the mob. Like the other handles it carries an id (re-resolved
// each access on the tick goroutine) + *TickLoop + the owning plugin's capSet, NEVER a live *Entity.
// path_to routes through the EXISTING setWantTarget -> groundNavigation.requestPath (the async A*
// already wired via pathPool); has_path reads the nav's hasTarget flag. The goal SETS a target; the
// Go nav/physics MOVES the mob (Pitfall 5: never a raw position write).
type navHandle struct {
	t      *TickLoop
	id     int32
	region *region // Phase-27 STEP-3 owning region (approach (a)); nil → store() falls back to t.only()
	caps   capSet
}

// store returns the nav handle's owning-region store (region-bound) or the t.only() fallback. It
// falls back to the entity's ACTUAL owning region (owningRegion scan) when the position-bound store
// does not contain the id: a mob that walked into a different region's column is bound (in handles())
// to the position-derived region, but its store entry only transfers on applyCrossRegionTransfers
// (1+ ticks later, or never in a direct-drive test harness). Without this fallback a nav callback for
// a region-crossing mob errors "entity no longer exists" and the goal silently stops — which desyncs
// the bit-fragile pig oracle the moment the faithful stroll first walks the mob across a region seam.
// Resolving against the real store keeps the plugin path identical to the Go-native path (which reads
// e.ai directly, never through a store), so both stay in lockstep across region boundaries.
func (h *navHandle) store() *entityStore {
	if h.region != nil {
		if _, ok := h.region.entities.get(h.id); ok {
			return h.region.entities
		}
		if r := h.t.owningRegion(h.id); r != nil {
			return r.entities
		}
		return h.region.entities // not found anywhere — return the bound store so get() errors cleanly
	}
	return h.t.cur().entities
}

// newNavHandle builds a nav handle for an id with the given capability set (region-unbound).
func newNavHandle(t *TickLoop, id int32, caps capSet) *navHandle {
	return &navHandle{t: t, id: id, caps: caps}
}

// newNavHandleInRegion builds a region-BOUND nav handle (Phase-27 STEP-3): re-resolves against the
// owning region's store, the nav twin of newEntityHandleInRegion.
func newNavHandleInRegion(t *TickLoop, region *region, id int32, caps capSet) *navHandle {
	return &navHandle{t: t, region: region, id: id, caps: caps}
}

var (
	_ starlark.Value    = (*navHandle)(nil)
	_ starlark.HasAttrs = (*navHandle)(nil)
)

func (h *navHandle) String() string        { return fmt.Sprintf("<nav %d>", h.id) }
func (h *navHandle) Type() string          { return "nav" }
func (h *navHandle) Freeze()               {} // no mutable Starlark state -> no-op
func (h *navHandle) Truth() starlark.Bool  { return starlark.True }
func (h *navHandle) Hash() (uint32, error) { return uint32(h.id), nil }

func (h *navHandle) bound(name string,
	fn func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error),
) starlark.Value {
	return starlark.NewBuiltin(name, fn).BindReceiver(h)
}

// Attr dispatches the nav handle surface. Both path_to and has_path are bound METHODS (callables) —
// the plugin invokes them as nav.path_to(...) / nav.has_path(). path_to enforces capNav when called;
// has_path is a read (no capability gate — observing whether a path is wanted is harmless) that
// re-resolves the entity on the tick goroutine.
func (h *navHandle) Attr(name string) (starlark.Value, error) {
	switch name {
	case "path_to":
		return h.bound("path_to", h.pathTo), nil
	case "has_path":
		return h.bound("has_path", h.hasPath), nil
	case "stop":
		return h.bound("stop", h.stop), nil
	case "jump":
		return h.bound("jump", h.jump), nil
	}
	return nil, nil
}

func (h *navHandle) AttrNames() []string { return []string{"path_to", "has_path", "stop", "jump"} }

// stop() clears the mob's pending nav target — the navigation.stop() analogue (RandomStrollGoal.stop
// = navigation.stop()). The Go oracle's randomStrollGoal.stop() calls clearWantTarget(); this is that
// seam. No capability gate beyond nav (clearing a target the same caller could set). Re-resolves on
// the owner; a removed / non-AI entity errors cleanly.
func (h *navHandle) stop(_ *starlark.Thread, _ *starlark.Builtin,
	_ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capNav) {
		return nil, capError("nav")
	}
	e, ok := h.store().get(h.id)
	if !ok {
		return nil, fmt.Errorf("entity %d no longer exists", h.id)
	}
	if e.ai == nil {
		return nil, fmt.Errorf("entity %d has no AI (cannot nav.stop)", h.id)
	}
	e.ai.clearWantTarget()
	return starlark.None, nil
}

// jump() arms the mob's JumpControl — the seam a declared FloatGoal's tick callback uses to claim the
// JUMP flag's impulse (the Starlark twin of the Go FloatGoal calling e.ai.jumpControl.doJump()). It
// routes to jumpControl.doJump(), which only SETS a bool consumed once per tick by jumpControl.tick
// in the serverAiStep JUMP slot — so repeated calls within a tick are idempotent (no impulse
// stacking; the T-30-06 DoS bound). Gated on capNav (a jump is a navigation-adjacent mutate, like
// path_to / stop). Re-resolves the mob on the OWNER (h.store(), never t.cur() — the T-30-05 Pitfall-7
// discipline); a removed / non-AI entity errors cleanly (no panic).
func (h *navHandle) jump(_ *starlark.Thread, _ *starlark.Builtin,
	_ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capNav) {
		return nil, capError("nav")
	}
	e, ok := h.store().get(h.id)
	if !ok {
		return nil, fmt.Errorf("entity %d no longer exists", h.id)
	}
	if e.ai == nil {
		return nil, fmt.Errorf("entity %d has no AI (cannot nav.jump)", h.id)
	}
	e.ai.jumpControl.doJump()
	return starlark.None, nil
}

// hasPath() READS whether the mob's nav currently wants a target (e.ai.hasTarget). Re-resolves the
// mob on the owner; a removed / non-AI entity errors cleanly. No capability gate — a read-only
// observation of the nav state.
func (h *navHandle) hasPath(_ *starlark.Thread, _ *starlark.Builtin,
	_ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	e, ok := h.store().get(h.id)
	if !ok {
		return nil, fmt.Errorf("entity %d no longer exists", h.id)
	}
	if e.ai == nil {
		return nil, fmt.Errorf("entity %d has no AI (cannot read has_path)", h.id)
	}
	// has_path() reads mobAI.hasTarget — the canContinueToUse = !navigation.isDone() analogue. Both the
	// vanilla_pig stroll's canContinueToUse and the wander mob's `if not nav.has_path()` re-request
	// guard depend on this equalling hasTarget. hasTarget is set by path_to and CLEARED ON ARRIVAL by
	// serverAiStep when the path completes (the 07-02 "flip hasTarget false when the path completes"
	// step), so it correctly goes false when the mob reaches its target — ending the Go stroll's
	// canContinueToUse AND letting the wander mob re-request. Go-native + plugin pig clear it in the
	// SAME serverAiStep step, so the equality oracle stays in lockstep.
	return starlark.Bool(e.ai.hasTarget), nil
}

// pathTo(x,y,z) MUTATES through the nav seam: setWantTarget -> requestPath (the async A*). It sets a
// TARGET only — a POSITION change happens later via serverAiStep's navigation.tick -> moveEntity
// (Pitfall 5). Requires capNav (issuing a navigation request). Re-resolves the mob on the owner; a
// removed / non-AI entity errors cleanly.
func (h *navHandle) pathTo(_ *starlark.Thread, b *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capNav) {
		return nil, capError("nav")
	}
	e, ok := h.store().get(h.id)
	if !ok {
		return nil, fmt.Errorf("entity %d no longer exists", h.id)
	}
	if e.ai == nil {
		return nil, fmt.Errorf("entity %d has no AI (cannot path_to)", h.id)
	}
	// path_to is OVERLOADED on arity (a HANDOFF overload — the want payload — NOT a new method or a new
	// world-solidity handle; the .star still reads no world solidity). 3 floats = a single legacy target
	// (setWantTarget, unchanged). 31 floats = the 10 RAW stroll candidates as x0,y0,z0,...,x9,y9,z9
	// (candidate-major, args[0..29]) PLUS the wantLandMode flag (args[30]: != 0.0 => true) → setWantCandidates
	// → the Go runtime snap (Phase 30.1). The per-goal scratch is float-only (set_state coerces via AsFloat),
	// so a candidate LIST cannot cross via set_state — the flat floats are AsFloat-compatible, and the mode
	// is carried as a 0.0/1.0 float so the .star and Go arms store the IDENTICAL wantLandMode for the same
	// probability nextFloat() roll (WR-05). kwargs are not accepted (positional only).
	switch len(args) {
	case 3:
		var x, y, z float64
		if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 3, &x, &y, &z); err != nil {
			return nil, err
		}
		e.ai.setWantTarget(x, y, z) // legacy single target — UNCHANGED
	case 31:
		if len(kwargs) != 0 {
			return nil, fmt.Errorf("path_to: the 31-float candidate form takes no keyword args")
		}
		var cands [10][3]float64
		for i := 0; i < 30; i++ {
			f, fok := starlark.AsFloat(args[i])
			if !fok {
				return nil, fmt.Errorf("path_to: arg %d must be a number, got %s", i, args[i].Type())
			}
			cands[i/3][i%3] = f
		}
		// args[30] is the wantLandMode flag carried across the handoff (the .star computes it from the
		// SAME probability nextFloat() draw: nextFloat() >= probability => Land/true, the ~99.9% common
		// up-snap path; < probability => Default/false, the ~0.1% rare no-up-snap path — see CR-01 and the
		// jar in ai_goals_passive.go getPosition). Carrying the real mode (not a hard-coded false) keeps
		// mobAI.wantLandMode BYTE-IDENTICAL across the Go-native and plugin arms, so the oracle cannot
		// desync if the field is ever observed. The runtime snap is still consumed-as-Land today (it up-snaps
		// every target — see stroll_snap.go), so the mode does not change the committed target yet.
		landFlag, lok := starlark.AsFloat(args[30])
		if !lok {
			return nil, fmt.Errorf("path_to: arg 30 (landMode) must be a number, got %s", args[30].Type())
		}
		e.ai.setWantCandidates(cands, landFlag != 0.0)
	default:
		return nil, fmt.Errorf("path_to: expected 3 floats (target) or 31 floats (10 candidates + landMode), got %d", len(args))
	}
	return starlark.None, nil
}
