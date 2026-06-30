# Phase 32: Held-Item + Item Tags (S4) - Pattern Map

**Mapped:** 2026-06-30
**Files analyzed:** 10 (3 new logic surfaces + 7 edit-in-place files)
**Analogs found:** 10 / 10 (every surface has a concrete in-tree analog)

All surfaces are EXISTING files edited in place. There are no new files. Every "analog" below is
either the seam being extended or its nearest sibling. NO RNG anywhere in this phase (TemptGoal.canUse
draws zero) → oracle-trivially-safe.

## File Classification

| File (edited in place) | Role | Data Flow | Closest Analog | Match Quality |
|------------------------|------|-----------|----------------|---------------|
| `server/ai_goals_passive.go` | AI goal (`temptGoal`) + player scan | event-driven (canUse probe) | `lookAtPlayerGoal` (same file) | exact |
| `server/ai_mob.go` | mob goal registration | config (registerGoals) | `newPigAI` @1/@6/@7 adds (same file) | exact |
| `server/ai_goal.go` | goal selector (read-only; verify) | arbitration | n/a — confirm two-@4 handling | n/a (no edit) |
| `server/damage_source.go` → new `itemInTag` helper | tag membership read | transform | `damageSource.is(tag)` (line 78) | exact |
| `server/inventory.go` | held-item read (offhand const) | CRUD read | `get` + `heldWindowSlot` | exact |
| `server/plugin_entity.go` | host-computed handle | request-response | `damageInTag` (278) + `worldHandle.nearestPlayer` (619) | exact |
| `plugins/vanilla_pig/main.star` | plugin goal decl | event-driven | `panic_can_use` + `panic_*` goal() | exact |
| `server/assets/vanilla_pig/main.star` | plugin goal decl (mirror) | event-driven | byte-identical copy | exact |
| `level/attribute/attributes.go` | attribute (ALREADY EXISTS) | config | `TemptRange` (line 87) | already present |
| `server/ai_mob_test.go` + `server/plugin_pig_test.go` | tests | assertion | the 5→7 goal-count asserts | exact |

## Pattern Assignments

### `server/ai_goals_passive.go` — `temptGoal` (new goal) + player-holding scan

**Analog:** `lookAtPlayerGoal` (lines 263-338, same file) — the existing "find nearest player, look at
it" goal. TemptGoal is structurally `lookAtPlayerGoal` + `path_to`: same `nearestPlayerWithin` seam,
same `set_look_at`/yaw aim, plus a navigation move.

**Goal struct pattern** (lines 263-284, copy this shape):
```go
type lookAtPlayerGoal struct {
	baseGoal
	lookDistance float32
	hasLook            bool
	lookX, lookY, lookZ float64
	alwaysLook bool // test seam
}
func newLookAtPlayerGoal(lookDistance float32) *lookAtPlayerGoal {
	return &lookAtPlayerGoal{baseGoal: newBaseGoal(flagLook), lookDistance: lookDistance, ...}
}
```
→ `temptGoal` carries `baseGoal: newBaseGoal(flagMove | flagLook)` (TemptGoal flags {MOVE, LOOK}),
`speed 1.2`, `stopDistance 2.5`, `canScare bool` (false for pig), a held-item predicate, and a
`calmDown int` (the post-stop cooldown, `reducedTickDelay(100)` = 50 at 20 TPS). Capture the player
position in `start()` (px,py,pz) exactly like `lookAtPlayerGoal.canUse` captures lookX/Y/Z.

**canUse pattern — the calmDown gate + scan** (model on lines 293-305, but with the calmDown decrement
FIRST per CONTEXT bytecode):
```go
// CONTEXT.md canUse: if (calmDown > 0) { --calmDown; return false; }  then nearest player who holds a tempt item.
```
The scan must be the nearest player WHO PASSES the held-item predicate (vanilla
`getNearestPlayer(TEMPT_TARGETING, mob)` where TEMPT_TARGETING includes `shouldFollow`). Range =
`e.getAttributeValue(attribute.TemptRange)` (default 10.0, already registered — see attributes section).

**The nearestPlayer scan — DECISION (add a sibling, do NOT mutate the existing two):**

Current scan (lines 343-365):
```go
func nearestPlayerWithin(t *TickLoop, e *Entity, maxDist float64) (x, y, z float64, ok bool) {
	return nearestPlayerAt(t, e.x, e.y, e.z, maxDist)
}
func nearestPlayerAt(t *TickLoop, cx, cy, cz, maxDist float64) (x, y, z float64, ok bool) {
	best := maxDist * maxDist
	for _, p := range t.players {           // p is *tickPlayer — has p.inventory
		if p == nil { continue }
		dx, dy, dz := p.x-cx, p.y-cy, p.z-cz
		d2 := dx*dx + dy*dy + dz*dz
		if d2 <= best { best = d2; x, y, z, ok = p.x, p.y, p.z, true }
	}
	return x, y, z, ok
}
```
**Recommendation:** ADD a sibling `nearestPlayerHolding(t, e, maxDist, pred func(component.SlotData) bool)
(x, y, z float64, ok bool)` rather than extend `nearestPlayerAt`. Rationale: `nearestPlayerAt` is the
shared seam reused by `worldHandle.nearestPlayer` (plugin_entity.go:628) and `lookAtPlayerGoal`;
changing its signature ripples to both. The sibling copies the identical `for _, p := range t.players`
loop body and adds `if !pred(mainHand(p)) && !pred(offHand(p)) { continue }` BEFORE the distance test
(matches vanilla's `getNearestPlayer` applying the TargetingConditions selector during the scan). The
predicate tests main || off — see the held-item read below. `p` is already `*tickPlayer` with
`p.inventory`, so the held read is reachable with zero new plumbing.

**look + navigate (tick) pattern** — TemptGoal.tick = `setLookAt(player)` then move/stop by distance.
The look seam is `lookAtPlayerGoal.tick` (lines 327-335): `yaw := yawTowardDeg(...); e.headYaw = yaw;
e.yaw = yaw`. The move seam is `randomStrollGoal.start` → `e.ai.setWantTarget(x,y,z)` (used as the
`navigateTowards(player)` analogue); stop-distance² (2.5²=6.25) → `e.ai.clearWantTarget()` (the
`stopNavigation()` analogue).

**canContinueToUse — canScare=false short-circuit** (CONTEXT line 49-62): for the pig
`canContinueToUse` = just `return g.canUse(t, e)` (the entire canScare flee-abort block is skipped).
Port the structure with a cited `if g.canScare { ... }` that is dead for the pig (faithful skip).

---

### `server/ai_mob.go` — register TWO `temptGoal@4` in `newPigAI`

**Analog:** `newPigAI` lines 250-257 — the exact `m.goals.addGoal(prio, ...)` registration site:
```go
m.goals.addGoal(0, newFloatGoal())
m.goals.addGoal(1, newPanicGoal(panicSpeedModifier))
m.goals.addGoal(6, newWaterAvoidingRandomStrollGoal(1.0))
m.goals.addGoal(7, newLookAtPlayerGoal(6.0))
m.goals.addGoal(8, newRandomLookAroundGoal())
```
**Edit point — insert between @1 and @6 (lines 254-255), in the jar order (carrot FIRST):**
```go
m.goals.addGoal(4, newTemptGoal(1.2, /*carrot_on_a_stick literal pred*/, false)) // canScare=false
m.goals.addGoal(4, newTemptGoal(1.2, /*pig_food TAG pred*/, false))              // canScare=false
```
The carrot goal MUST be added first (CONTEXT decision: `Items.CARROT_ON_A_STICK` then `ItemTags.PIG_FOOD`).
`addGoal` (ai_goal.go:149-160) insertion-sorts with `priority <= priority` — so the SECOND @4 lands
AFTER the first @4 in the slice (the `<=` keeps insertion order among equals). This preserves the
vanilla "first-added wins the flags" semantics. Also update the deferral comment in the `newPigAI`
doc (lines 231-232) that currently says "TemptGoal@4 ... DEFERRED".

---

### `server/ai_goal.go` — the two-@4 arbitration (READ-ONLY; confirm, do not edit)

**CONFIRMED — the selector already handles two same-priority goals both claiming {MOVE,LOOK}:**

Pass 2 of `goalSelector.tick` (lines 246-269) walks `gs.goals` in sorted order. The first @4 goal whose
`canUse` is true claims MOVE+LOOK via `lock(fl, wg)`. When the loop reaches the SECOND @4 goal,
`goalCanBeReplacedForAllFlags` (lines 182-191) calls `holderOf(MOVE).canBeReplacedBy(secondGoal)`. The
holder is now the first @4 goal; `canBeReplacedBy` (lines 120-122) is:
```go
return w.g.isInterruptable() && other.priority < w.priority   // 4 < 4 is FALSE
```
→ the second @4 cannot replace the first @4 (equal priority). So **exactly one @4 TemptGoal runs at a
time, the first-added (carrot) wins when both canUse**, which is the faithful vanilla behavior. No
selector change needed. The insertion-sort `<=` in `addGoal` is what guarantees carrot precedes
pig_food among the equals.

---

### `server/damage_source.go` → new `itemInTag` helper

**Analog (EXACT):** `damageSource.is(tag)` (lines 74-80):
```go
func (s damageSource) is(tagName string) bool {
	return tag.DamageTypeTags[tagName][int32(s.typeTag)]
}
```
**New helper** (place near the item read, e.g. in inventory.go or a small item_tag.go — mirror the
shape): `func itemInTag(itemID int32, tagName string) bool { return tag.ItemTags[tagName][itemID] }`.
`tag.ItemTags` is `map[string]map[int32]bool` (tags.go:163); `"pig_food"` = `{1257,1258,1317}`
(tags.go:305). A nil/absent tag yields false (zero-value map read), exactly like the damage analog.
The carrot_on_a_stick predicate is a direct id compare: `itemID == 887` (data/item/item.go:5341,
`CarrotOnAStick.ID = 887`).

---

### `server/inventory.go` — main-hand + off-hand read

**Main-hand analog (EXACT):** block_interact.go:186-187:
```go
inv := ensureInventory(p)
held := inv.get(heldWindowSlot(inv.heldSlot))
```
`heldWindowSlot` (block_interact.go:326-331) maps hotbar 0..8 → window 36+slot.

**Off-hand — DOES NOT EXIST YET, add it.** The inventory is 46 slots (inventory.go:23,
`playerInventorySize = 46`); the comment confirms "4 craft + 4 armor + 36 main + 1 offhand". The
offhand is the LAST slot, **window index 45**. Add a const + helper:
```go
const offhandWindowSlot = 45 // the single offhand slot (Inventory.OFFHAND_SLOT); 46-slot window, last index
```
The predicate must test BOTH (CONTEXT: `shouldFollow = items.test(mainHand) || items.test(offhand)`):
`mainHand := inv.get(heldWindowSlot(inv.heldSlot)); off := inv.get(offhandWindowSlot)`. Slot id is
`int32(stack.ItemID)` (component.SlotData.ItemID, types.go:261). Empty-guard with the existing
`slotIsEmpty(held)` pattern (block_interact.go:197) before reading ItemID.

---

### `server/plugin_entity.go` — host-computed nearest-tempt-player handles (DECISION: RUNTIME-COMPUTES)

**Analog A — the host-computed predicate read:** `entityHandle.damageInTag` (lines 278-292):
```go
func (h *entityHandle) damageInTag(_ *starlark.Thread, b *starlark.Builtin, args, kwargs) (starlark.Value, error) {
	if !h.caps.has(capEntitiesRead) { return nil, capError("entities.read") }
	var tagName string
	UnpackPositionalArgs(b.Name(), args, kwargs, 1, &tagName)
	e, err := h.resolve()
	return starlark.Bool(e.lastDamageSource.is(tagName)), nil   // host owns the tag-id set
}
```
**Analog B — the host-computed nearest-player scan returning a position tuple:**
`worldHandle.nearestPlayer` (lines 619-633):
```go
px, py, pz, ok := nearestPlayerAt(h.t, x, y, z, maxDist)
if !ok { return starlark.None, nil }
return starlark.Tuple{starlark.Float(px), starlark.Float(py), starlark.Float(pz)}, nil
```

**Recommendation (per CONTEXT line 96-103): the host does the scan + predicate; the .star reads frozen
scalars — keep the item-id set Go-side.** Add TWO entityHandle methods (mirroring `damageInTag` + the
nearestPlayer tuple return), so the .star never sees an item id:
- `nearest_player_holding_carrot_on_a_stick(range)` → `Tuple{px,py,pz}` or `None`
- `nearest_player_holding_pig_food(range)` → `Tuple{px,py,pz}` or `None`

Each calls the new `nearestPlayerHolding(h.t, e, range, pred)` (ai_goals_passive.go) with the Go-side
predicate (`itemID == 887` for carrot; `itemInTag(itemID,"pig_food")` for the tag). This mirrors
`damageInTag` (host owns the tag set) AND `nearestPlayer` (tuple-or-None return). Register them in
`Attr` (line 133-158, mutate/method block — they take an arg so they are bound methods) and in
`AttrNames` (line 232-239). Gate on `capEntitiesRead` (or `capWorldRead`, matching `nearestPlayer`).

**The look + navigate seams the .star already has:**
- `entity.set_look_at(x,y,z)` (setLookAt, lines 412-435) — the `getLookControl().setLookAt(player)` analogue.
- `nav.path_to(...)` (line 791) — used by panic/stroll for candidate lists, but for Tempt the target
  is a single known player position, so the simpler `entity.move_to(x,y,z)` (moveTo, lines 297-317 →
  `e.ai.setWantTarget`) is the `navigateTowards(player)` seam (move_to needs capNav+capEntitiesWrite).
- `nav.stop()` (line 809) — the `stopNavigation()` analogue when within stopDistance.

---

### `plugins/vanilla_pig/main.star` + `server/assets/vanilla_pig/main.star` (BYTE-IDENTICAL — confirmed)

**Confirmed identical:** `diff` of the two copies == IDENTICAL. Every edit must be applied to BOTH.

**Analog — `panic_*` goal (the most recent host-computed-predicate goal):** lines 89-112 +
declaration 272-278:
```python
def panic_can_use(entity, world, nav):
    if not (entity.has_last_damage and entity.damage_in_tag("panic_causes")):  # host predicate, zero draws if false
        return False
    ... nav.path_to(*flat) ...
    return True
def panic_stop(entity, world, nav):  nav.stop()
def panic_continue(entity, world, nav):  return nav.has_path()
```
declaration:
```python
goal(priority = 1, flags = ["MOVE"], can_use = panic_can_use, stop = panic_stop, can_continue = panic_continue),
```

**TemptGoal x2 — add two goal blocks at priority 4, flags ["MOVE","LOOK"], carrot FIRST.** Pattern per
goal (parameterize by which host handle to call, OR write two near-identical callback sets):
```python
def tempt_carrot_can_use(entity, world, nav):
    p = entity.nearest_player_holding_carrot_on_a_stick(TEMPT_RANGE)   # host scan+predicate, item id stays Go-side
    if p == None: return False
    entity.set_state("tempt_px", p[0]); entity.set_state("tempt_py", p[1]); entity.set_state("tempt_pz", p[2])
    return True
def tempt_carrot_tick(entity, world, nav):
    px = entity.get_state("tempt_px"); py = entity.get_state("tempt_py"); pz = entity.get_state("tempt_pz")
    entity.set_look_at(px, py, pz)                                     # TemptGoal.tick setLookAt(player)
    dx=px-entity.x; dy=py-entity.y; dz=pz-entity.z
    if dx*dx+dy*dy+dz*dz < STOP_DISTANCE*STOP_DISTANCE: nav.stop()    # 2.5^2 = 6.25
    else: entity.move_to(px, py, pz)                                  # navigateTowards(player) @ 1.2
def tempt_carrot_stop(entity, world, nav):
    nav.stop()   # + calmDown handling lives Go-side OR via get_state/set_state (mirror look_time scratch)
```
Constants to add near line 23-49: `TEMPT_RANGE = 10.0` (attribute default), `TEMPT_SPEED = 1.2`,
`STOP_DISTANCE = 2.5`, `TEMPT_CALM_DOWN = 50` (reducedTickDelay(100)). The pig_food goal is the SAME
callbacks calling `entity.nearest_player_holding_pig_food(...)`. canContinueToUse for canScare=false =
just re-run can_use (the .star can call the can_use function or use a `tempt_continue` that re-scans).

declaration — insert BOTH between the @1 and @6 goal() blocks (lines 278-279), carrot first:
```python
goal(priority = 4, flags = ["MOVE","LOOK"], can_use = tempt_carrot_can_use, tick = tempt_carrot_tick, stop = tempt_carrot_stop, can_continue = tempt_carrot_continue),
goal(priority = 4, flags = ["MOVE","LOOK"], can_use = tempt_pigfood_can_use, tick = tempt_pigfood_tick, stop = tempt_pigfood_stop, can_continue = tempt_pigfood_continue),
```
Also update the header deferral note (lines 9-16) that lists @4 as DEFERRED.

---

### `level/attribute/attributes.go` — TEMPT_RANGE (ALREADY EXISTS — NO EDIT)

`TemptRange` is already registered (lines 85-87):
```go
// TemptRange is Attributes.TEMPT_RANGE (RangedAttribute "tempt_range", 10.0, 0.0, 2048.0).
TemptRange = NewRangedAttribute("tempt_range", 10.0, 0.0, 2048.0)
```
Default 10.0 matches the jar. The Go TemptGoal reads it via the entity accessor
`e.getAttributeValue(attribute.TemptRange)` (the same `e.getAttributeValue(attribute.MaxHealth)` /
`attribute.Armor` pattern in combat_mob.go:568-569). **NO attribute work needed** — just consume it.
Confirm a spawned pig's attribute map carries TemptRange (Animal.createAnimalAttributes adds it); if a
pig's holder lacks it, getAttributeValue returns 0 — verify the pig holder is seeded with it, else the
scan radius collapses to 0. Cite the 10.0 default in the goal ctor as the fallback constant.

---

## Shared Patterns

### Host-computes / .star reads frozen scalars (the keystone discipline)
**Source:** `entityHandle.damageInTag` (plugin_entity.go:278-292) + `was_hurt`/`in_water` frozen-scalar
reads (lines 189-225). **Apply to:** both new nearest-tempt-player handles. The host owns the item-id
set (`887` / `ItemTags["pig_food"]`); the .star only sees a position tuple or None. Re-resolve the
entity through `h.resolve()` / `h.store()` (Pitfall 7 — never `t.cur()`).

### Goal lifecycle + flag claim
**Source:** `ai_goal.go` Goal interface (lines 53-76) + `baseGoal` (88-101) + the selector
arbitration (233-272). **Apply to:** the new `temptGoal`. Embed `baseGoal`, override only
canUse/canContinueToUse/start/tick/stop; flags = MOVE|LOOK via `newBaseGoal(flagMove|flagLook)`.

### Held-item read
**Source:** block_interact.go:186-187 (`inv.get(heldWindowSlot(inv.heldSlot))`) + the empty-guard
`slotIsEmpty(held)` (line 197) + `int32(held.ItemID)`. **Apply to:** the main-hand read; add the
off-hand sibling at window slot 45.

### Lockstep .star edits
**Source:** the two byte-identical main.star copies. **Apply to:** EVERY .star change — write to both
`plugins/vanilla_pig/main.star` AND `server/assets/vanilla_pig/main.star`, then `diff` to confirm.

## Test Edits (server/ai_mob_test.go + server/plugin_pig_test.go)

5 goals {0,1,6,7,8} → 7 goals {0,1,4,4,6,7,8}. Exact assertions to update:

**`server/ai_mob_test.go` — `TestPigGoalSetRegistered`:**
- Line 137-138: `!= 5` → `!= 7`; message "5 goals" → "7 goals (… + two TemptGoal@4)".
- Lines 146-149: `byPriority := map[int]Goal{}` COLLAPSES the two @4 goals to one key — **change the
  assertion** to count `*temptGoal` instances directly (e.g. iterate goals, assert exactly two
  `*temptGoal` at priority 4 with flags `flagMove|flagLook`), since a map-by-priority cannot represent
  two @4. Add a `*temptGoal` type assertion + flag check mirroring the floatGoal/panicGoal checks
  (lines 150-159).

**`server/plugin_pig_test.go` — `TestPluginPigBootLoads`:**
- Line 55-56: `!= 5` → `!= 7`; message → "7 (FloatGoal@0 + PanicGoal@1 + TemptGoal@4 x2 + @6/@7/@8)".
- Line 58-65: `priorities := map[int]bool{}` loses the duplicate @4 — fine for "priority 4 present"
  but add an explicit "exactly two goals at priority 4" count. Update `[]int{0,1,6,7,8}` →
  `[]int{0,1,4,6,7,8}` plus the dup-count assertion.

**`server/plugin_pig_test.go` — `TestVanillaPigDeclaresGoalSet`:**
- Line 94-95: `!= 5` → `!= 7`; message updated.
- Lines 97-100: `byPriority := map[int]goalDecl{}` collapses @4 — add a count of decls with
  `priority == 4` (expect 2) instead of relying on the map key.

**`TestPluginPigEqualsGoNativePig` (line 225):** the oracle drives a pig with NO player holding a
tempt item within range, so both TemptGoals' canUse returns false (player==None) → ZERO new draws →
the byte-identical stream is preserved. VERIFY the oracle's player (if present) is far / not holding
carrot/pig_food; if it is, document that both halves fire identically (still byte-identical).

## No Analog Found

None. Every surface has an exact in-tree analog or is an already-existing facility.

## Metadata

**Analog search scope:** server/ (ai_goals_passive.go, ai_goal.go, ai_mob.go, block_interact.go,
inventory.go, plugin_entity.go, attributes.go, *_test.go), level/attribute/, level/component/,
data/tag/, data/item/, plugins/vanilla_pig/, server/assets/vanilla_pig/.
**Files scanned:** ~16.
**Pattern extraction date:** 2026-06-30.
