# Phase 33: Aging + Breeding (S3) — Pig Parity / DOGFOOD GATE - Pattern Map

**Mapped:** 2026-06-30
**Files analyzed:** 14 surfaces (struct, baseTick, metadata wire, interact, AI driver, selector, goals, spawn, orb/particle, plugin handles, 2 .star copies, 4 goal-count tests, NBT)
**Analogs found:** 13 with strong analogs / 14 (1 gap = particle wire-out has NO encoder)

---

## File Classification

| Surface (file) | Role | Data Flow | Closest Analog | Match Quality |
|----------------|------|-----------|----------------|---------------|
| `server/entity.go` (struct fields) | model | per-entity state | the existing `hurtTime/invulnerableTime/lastHurt` i-frame ints + the `age int` (item/orb) | exact (sibling fields, same block) |
| `server/combat_mob.go` `tickMobIFrames` | service | per-tick decrement | `tickMobIFrames` itself (the i-frame decrement = the aging/inLove decrement twin) | exact |
| `server/entity_encode.go` (DATA_BABY_ID) | encoder | wire metadata | `airDataEntry` (INT) / `livingEntityFlagsEntry` (BYTE) / `itemDataEntry` (ITEM) | exact (need a BOOL twin) |
| `server/attack_dispatch.go` `handleInteract` | controller | request-response | (empty no-op now); the held-item read lives in `playerHoldsTempt` + `shrinkHeldItem` | role-match (analog is the tempt held-read + block_interact consume) |
| `server/ai_mob.go` `newPigAI` | config | goal registration | `newPigAI` itself (the @0/@1/@4×2/@6/@7/@8 addGoal calls) | exact |
| `server/ai_goal.go` (selector) | service | event-driven | the selector itself — CONFIRM empty-flag goal | exact (no change needed; verified below) |
| `server/ai_goals_breed.go` (NEW) | service | event-driven scan+nav | `temptGoal` (entity/player scan + moveTo) | role-match (tempt scans players; breed scans same-class mobs) |
| `server/ai_goals_follow.go` (NEW) | service | event-driven scan+nav | `temptGoal` / `lookAtPlayerGoal` (scan + moveTo, NO RNG) | role-match |
| `server/plugin_mob_decl.go` `spawnDeclaredMob` | service | spawn | `spawnDeclaredMob` itself + `awardExperienceOrbs` (NewEntity+store.add) | exact |
| `server/xp_orb.go` `awardExperienceOrbs` | service | spawn | `awardExperienceOrbs` (the breed XP-orb drop) | exact |
| Heart-particle wire-out | encoder | broadcast | `encodeSoundEntity` (the framing twin) — but NO particle encoder exists | **GAP** |
| `plugins/vanilla_pig/main.star` + `server/assets/vanilla_pig/main.star` | config | declaration | the @4 TemptGoal block (`tempt_*_can_use/tick/stop/continue` + `nearest_player_holding_*`) | exact |
| `server/plugin_entity.go` (handles) | service | host scan | `nearestPlayerHoldingPigFood` + `was_hurt`/`has_last_damage` read accessors | exact |
| `server/persistence.go` (Age/InLove NBT) | service | file-I/O | `saveEntities`/`loadEntities` (region IO exists) | **partial gap** (no live Entity→save.Entities field wiring) |
| 4 goal-count tests | test | assertion | the existing `!= 7` assertions | exact |

---

## Pattern Assignments

### 1. `server/entity.go` — the breedAge + inLove fields (model)

**Analog:** the i-frame block at `entity.go:185-238` (`health/lastHurt/invulnerableTime/hurtTime/hurtDuration/lastDamageSource/dead/deathTime`) — plain value ints, "set for a goal-bearing mob, zero for others, tick-owned (TICK-05), snapshot-friendly."

**COLLISION TO AVOID — there is ALREADY an `age int`** (`entity.go:119`):
```go
// age is ItemEntity.age: ticks since spawn. ... The XP-orb tick reuses this field with the
// SAME semantics (ExperienceOrb.age, discard at 6000) ...
age int
```
The new AgeableMob breed-age MUST be named distinctly. Recommend `breedAge int` (isBaby = breedAge<0). Also add `inLove int`. Place them in the existing per-entity int block (right after the i-frame fields, ~`entity.go:238`), with the same TICK-05 + snapshot-friendly doc comment style.

**Spawn init:** fields default to the zero value (adult/age-0, inLove-0) on `NewEntity` (`entity.go:285-302`) — exactly the oracle's "lone un-fed adult" start. NO change to NewEntity needed for the adult oracle path; the breed child sets `breedAge = -24000` AFTER spawn (see §9). The init helper precedent is `initSpawnHealth` (`entity.go:342`) — called explicitly per spawn path; breed() calls a `setBreedAge(child, -24000)` after `spawnDeclaredMob` similarly.

**Edit point:** insert two field declarations after `deathTime int32` (`entity.go:238`); no constructor change for the adult default.

---

### 2. `server/combat_mob.go` — the aiStep tail (aging + inLove decrement) (service)

**Analog:** `tickMobIFrames` (`combat_mob.go:647-654`) — the EXACT twin (a pure-int per-tick decrement, "PURE INTEGER MATH (no RNG draw), so it cannot perturb the per-mob RNG stream the pig oracle pins"):
```go
func (t *TickLoop) tickMobIFrames(e *Entity) {
	if e.hurtTime > 0 { e.hurtTime-- }
	if e.invulnerableTime > 0 { e.invulnerableTime-- }
}
```

**What to add (jar — Animal.aiStep tail + AgeableMob ageUp, 33-JARNOTES.md:26-41):**
- aging: `if breedAge < 0 { breedAge++ } else if breedAge > 0 { breedAge-- }` (pure int; baby grows toward 0, cooldown decays).
- inLove (adult-only): `if breedAge != 0 { inLove = 0 }; if inLove > 0 { inLove--; if inLove%10==0 { emit hearts } }`.

**Insertion point — CRITICAL ordering:** these run in `tickAI` "structurally OUTSIDE serverAiStep's goal callbacks" (the comment at `combat_mob.go:644-646` explains WHY: keeping them out of the RNG-stream path preserves the oracle). Mirror `tickMobIFrames`: add a sibling `func (t *TickLoop) tickMobAging(e *Entity)` (or fold into a new `tickAnimalStep`) and call it from the SAME per-mob loop in `tickAI` that calls `tickMobIFrames`. Find that call site by grepping `tickMobIFrames(` (it is the dedicated per-mob loop in tickAI). Do NOT put it inside `serverAiStep` (`ai_mob.go:155`).

**Oracle note:** on the lone un-fed adult (breedAge==0, inLove==0) both branches are no-ops — zero RNG, stable age-0. Byte-identical preserved.

---

### 3. `server/entity_encode.go` — DATA_BABY_ID bool metadata (encoder)

**Analog:** three existing DataValue builders, pick the BYTE/INT one as the shape and ADD a BOOL serializer:
- `itemDataEntry` (`entity_encode.go:250`, serializer 7)
- `airDataEntry` (`entity_encode.go:290`, INT serializer 1)
- `livingEntityFlagsEntry` (`entity_encode.go:331`, BYTE serializer 0)

The framing type `entityDataEntry` (`entity_encode.go:186-212`) is `Byte(index) + VarInt(serializerID) + value`.

**GAP — no BOOLEAN serializer constant exists.** The registration order (documented at `entity_encode.go:237-242`) is `0=BYTE, 1=INT, 2=LONG, 3=FLOAT, 4=STRING, 5=COMPONENT, 6=OPTIONAL_COMPONENT, 7=ITEM_STACK, 8=BOOLEAN` (verify with `javap -c -p net.minecraft.network.syncher.EntityDataSerializers` static{} — BOOLEAN's registerSerializer index). Add `const boolSerializerID int32 = <verified>`. The BOOLEAN value codec is `ByteBufCodecs.BOOL` → a single byte 0/1, so the value encoder is `pk.Boolean(b)` (or `pk.UnsignedByte(0|1)` if no Boolean codec exists — check `net/packet`).

**DATA_BABY_ID accessor index — VERIFY via javap.** The defineId order is documented for Entity (0..7) and LivingEntity (8 = LIVING_ENTITY_FLAGS) and the rest of LivingEntity (8..14, per `entity_encode.go:341`: "LivingEntity 8..14 (7)"). AgeableMob extends `net.minecraft.world.entity.PathfinderMob` → `Mob` → `LivingEntity`. So the index is `Entity(8) + LivingEntity(7) + Mob's defineId count + AgeableMob's first defineId`. Run `javap -c -p net.minecraft.world.entity.Mob` (count its defineId calls — DATA_MOB_FLAGS is one) and `javap -c -p net.minecraft.world.entity.AgeableMob` (DATA_BABY_ID is its first/only). Likely `15 + Mob's count` — DO NOT guess; the comment block at `entity_encode.go:339-347` shows the exact "walk the hierarchy" method (Avatar=16 because Entity 8 + LivingEntity 7 + Avatar.MAIN_HAND 15 + MODE_CUSTOMISATION 16). Apply the SAME walk for AgeableMob.

**Builder to add:**
```go
func babyDataEntry(isBaby bool) entityDataEntry {
	return entityDataEntry{index: dataBabyIndex, serializerID: boolSerializerID, value: pk.Boolean(isBaby)}
}
```
Plus a metadata-carry helper following `playerSkinMetadata` (`entity_encode.go:366-373`) if the baby flag rides `Entity.metadata` at spawn, OR push it via `encodeSetEntityDataByID` (`entity_encode.go:423`) when breedAge crosses 0 (baby→adult) like the living-flags push.

**Edit point:** add the two consts + builder after the skin-customisation block (`entity_encode.go:360`).

---

### 4. `server/attack_dispatch.go` `handleInteract` — the FEED path (controller)

**Current body (`attack_dispatch.go:725-731`) — a pure no-op:**
```go
func (t *TickLoop) handleInteract(p *tickPlayer, pkt pk.Packet) {
	_ = p
	_ = pkt
}
```

**Pattern pieces to assemble (jar Animal.mobInteract, 33-JARNOTES.md:92-115):**
1. **Decode** the ServerboundInteract (entityId + InteractionHand + Vec3 + usingSecondaryAction — `attack_dispatch.go:717-720` documents the layout). Resolve the target *Entity by id via the store (`handleAttack` in the same file is the precedent for resolving + cross-region; reuse `t.owningRegion(id)` / the store get pattern).
2. **Read held item** — REUSE `playerHoldsTempt`'s held-item read (`ai_goals_passive.go:405-416`): `inv.get(heldWindowSlot(inv.heldSlot))` (`block_interact.go:326`), guard `slotIsEmpty`, `int32(main.ItemID)`.
3. **isFood** — `itemInTag(id, "pig_food")` (Phase 32; already used at `ai_mob.go:264`).
4. **Adult branch** (`breedAge==0 && inLove<=0`): `setInLove(600)` + `shrinkHeldItem(p, inv)` (`block_interact.go:339`) + playEatingSound.
5. **Baby branch** (`canAgeUp` = breedAge<0): `ageUp(getSpeedUpSecondsWhenFeeding(-breedAge))` + `shrinkHeldItem` + playEatingSound.

**playEatingSound seam — EXISTS:** `encodeSoundEntity(soundID, source, entityID, volume, pitch, seed)` (`entity_encode.go:749`); `combat_mob.go` already broadcasts pig hurt/death sounds through it to trackers. Use the same broadcast-to-trackers fan-out combat_mob uses. Find the eat sound id (`entity.generic.eat` or `entity.pig.ambient`? — vanilla `playEatingSound` returns getEatingSound; for a pig it is `SoundEvents.GENERIC_EAT`. Verify the registry id).

**Edit point:** replace the no-op body. Keep the defensive-decode discipline (malformed payload → ignore). NOTE: handleInteract runs on the player's region; the mob may be cross-region — mirror `handleAttack`'s owner-resolve/queue pattern (do NOT mutate a foreign region's mob inline; queue an intent if needed — see `queueDamageIntent`, `region_transfer.go:186`).

---

### 5. `server/ai_mob.go` `newPigAI` — add BreedGoal@3 + FollowParentGoal@5 (config)

**Current registration (`ai_mob.go:235-269`)** — exactly 7 goals:
```go
m.goals.addGoal(0, newFloatGoal())
m.goals.addGoal(1, newPanicGoal(panicSpeedModifier))
m.goals.addGoal(4, newTemptGoal(1.2, func(id int32) bool { return id == 887 }, false))
m.goals.addGoal(4, newTemptGoal(1.2, func(id int32) bool { return itemInTag(id, "pig_food") }, false))
m.goals.addGoal(6, newWaterAvoidingRandomStrollGoal(1.0))
m.goals.addGoal(7, newLookAtPlayerGoal(6.0))
m.goals.addGoal(8, newRandomLookAroundGoal())
```

**Add (jar Pig.registerGoals, 33-JARNOTES.md:71):**
```go
m.goals.addGoal(3, newBreedGoal(1.0))          // BreedGoal(mob, 1.0) {MOVE,LOOK}
m.goals.addGoal(5, newFollowParentGoal(1.1))   // FollowParentGoal(mob, 1.1) EMPTY flags
```
addGoal (`ai_goal.go:149`) insertion-sorts by priority, so placement in the source is cosmetic — but match the jar order for readability. Result: 9 goals `{0,1,3,4,4,5,6,7,8}`.

**Update the doc comment** at `ai_mob.go:212-234` (it currently says BreedGoal/FollowParentGoal are DEFERRED — flip to PORTED). The `serverAiStep` driver (`ai_mob.go:155`) needs NO change — the goals reuse `setWantTarget`/`clearWantTarget` (the nav seam at `ai_mob.go:179-198`), exactly as tempt does.

---

### 6. `server/ai_goal.go` — empty-flag goal handling (service) — CONFIRMED, NO CHANGE

**FollowParentGoal has EMPTY flags.** Verified the selector handles it correctly:
- `newBaseGoal(0)` → `gflags=0`, `flags()` returns 0.
- `goalCanBeReplacedForAllFlags` (`ai_goal.go:182-191`): `eachFlag(0, ...)` iterates NOTHING (the `f&fl != 0` test in `eachFlag`, `ai_goal.go:164-170`, never fires) → `ok` stays true → the goal can ALWAYS start (it claims no flag, blocks nobody).
- start (`ai_goal.go:261-266`): `eachFlag(0, ...)` locks nothing.
- It still runs canUse/canContinueToUse/tick every tick — exactly FollowParentGoal's "locks nothing, just navigates in tick" (33-JARNOTES.md:45-46).

**Priority arbitration BreedGoal{MOVE,LOOK}@3 vs TemptGoal{MOVE,LOOK}@4:** `canBeReplacedBy` (`ai_goal.go:120-122`) = `interruptable && other.priority < this.priority`. BreedGoal@3 < TemptGoal@4, so a running TemptGoal(interruptable) YIELDS its MOVE+LOOK to BreedGoal when breed's canUse fires (3<4). Lower number wins. (On the oracle, breed.canUse is false — `isInLove()` gate — so it never preempts; dormant.)

**NO edit to ai_goal.go.** This row is a verification, not a change.

---

### 7. `server/ai_goals_breed.go` + `server/ai_goals_follow.go` (NEW) — the two goals (service)

**Closest analog: `temptGoal`** (`ai_goals_passive.go:450-542`) — the scan-then-moveTo goal with NO RNG (oracle-trivially-safe). Copy its skeleton: `baseGoal` embed, `canUse` (scan), `canContinueToUse`, `start`/`tick` (`setWantTarget`/`clearWantTarget`), `stop`. Also study `lookAtPlayerGoal` (`ai_goals_passive.go:266-340`) for the look-at + captured-position pattern.

**The same-class entity scan — the entity-AABB query EXISTS but is UNFILTERED:**
- `entityStore.near(x, z, rangeChunks)` (`entity_store.go:168-183`) returns every entity in the Chebyshev column range — NO class filter.
- The cross-region twin `entitiesNearAcrossRegions` (`region_transfer.go:143`) — but a GOAL runs IN-region, so use the in-region `near` (the goal-callback rule, `plugin_entity.go:744-749`: "a goal sees the entities in its OWN region").
- **Filter by `e.typ`** (the wire type id, `entity.go:49`) to get "same class" — `getFreePartner`/parent-scan filter `other.typ == e.typ` (the pig type). `entityTypeName(e.typ)` exists (`plugin_entity.go:186`) if a name compare is preferred.

So BreedGoal.getFreePartner = `near()` filtered to `typ==pig && canMate && !panicking`, nearest. FollowParentGoal parent-scan = `near()` filtered to `typ==pig && breedAge>=0` (adult), nearest, gated 9 < distSqr < 256.

**canMate** (Animal.canMate, 33-JARNOTES.md:31): `other != this && other.typ==this.typ && this.isInLove() && other.isInLove()`. **isPanicking** — check whether a panicking signal exists; the PanicGoal is `*panicGoal` (`ai_goals_panic.go`) — read its running state via the selector, OR add an `e.ai` panicking flag. FLAG: confirm a panicking read exists (see Gaps).

**inflate(8,4,8) / inflate(8.0):** the scan box is the mob AABB inflated. `near()` is column-granular (chunk-radius), so use `rangeChunks=1` (covers ±8 blocks comfortably) then filter by exact distSqr (`distanceToSqr < 9.0` for breed, `9 < d < 256` for follow). The exact-distance filter is the precedent in `scanOrbPickup`/`nearestPlayerHolding` (squared-distance compares).

**Edit point:** two NEW files mirroring `ai_goals_passive.go`'s structure; add `newBreedGoal(speed)` / `newFollowParentGoal(speed)` constructors. BreedGoal claims `flagMove|flagLook`; FollowParentGoal claims `0` (empty).

**breed() lives in the BreedGoal** (tick, when loveTime>=adjustedTickDelay(60) && distSqr<9) — see §8/§9.

---

### 8. `server/plugin_mob_decl.go` `spawnDeclaredMob` — the child spawn (service)

**Signature + body (`plugin_mob_decl.go:385-414`):**
```go
func (t *TickLoop) spawnDeclaredMob(decl *mobDecl, x, y, z float64) *Entity {
	e := NewEntity(t.idAlloc.AllocID(), decl.baseType, x, y, z)
	seedAttributes(e.attributes, decl.attrs)
	initSpawnHealth(e)
	e.ai = buildAIFromDecl(t, decl)
	reseedMobAI(e.ai, e.id)
	t.regionForEntity(e).entities.add(e)
	return e
}
```

**How a goal reaches the registry/decl to spawn a `vanilla_pig` child:** the goal callback has `*TickLoop` (every goal's `tick(t *TickLoop, e *Entity)`). The decl is reached via the loaded registry — find `spawnVanillaPig` (used throughout tests, e.g. `plugin_pig_test.go:157` `loop.spawnVanillaPig(...)`) which wraps `spawnDeclaredMob` with the vanilla_pig decl. breed() calls `t.spawnVanillaPig(childX, childY, childZ)` (or `t.spawnDeclaredMob(pigDecl, ...)`), then `setBreedAge(child, -24000)` (BABY_START_AGE). For the Go-native oracle pig, the equivalent spawns via `newPigAI` — but the oracle pig NEVER breeds (un-fed), so this path is dormant in the gate.

**Edit point:** breed() (in ai_goals_breed.go) calls the existing spawn helper; AFTER spawn set `child.breedAge = -24000` and both parents `breedAge = 6000` (cooldown) + `inLove = 0` reset. NO change to spawnDeclaredMob's signature.

---

### 9. `server/xp_orb.go` — breed XP orb + heart particles (service / GAP)

**XP orb — analog EXISTS:** `awardExperienceOrbs` (`death_mob.go:347-361`):
```go
orb := NewEntity(t.idAlloc.AllocID(), entity.ExperienceOrb, e.x, e.y, e.z)
orb.isOrb = true
orb.xpValue = chunk
owner.entities.add(orb)
```
breed() drops `1 + random.nextInt(7)` XP (vanilla finalizeSpawnChildFromBreeding) — call `t.awardExperienceOrbs(child_or_parent, value)` directly. NOTE: this draws RNG (the orb-value split) — but ONLY mid-breeding, which the oracle never reaches (dormant). Still add the draw to BOTH pigs in lockstep per the rule.

**Heart particles — GAP, NO ENCODER EXISTS.**
- `packetid.ClientboundLevelParticles` is defined (`data/packetid/packetid.go:130`) but there is NO `encodeLevelParticles` / particle wire-out anywhere in `server/`.
- The framing TWIN to copy is `encodeSoundEntity` (`entity_encode.go:749`) — same "build a `pk.Marshal(packetid.X, fields...)` and broadcast to trackers" shape.
- The planner must WRITE a new `encodeLevelParticles` (javap `net.minecraft.network.protocol.game.ClientboundLevelParticlesPacket` for the wire layout: particle-type id, overrideLimiter bool, x/y/z double, xDist/yDist/zDist float, maxSpeed float, count int, + the particle options) and a `broadcastHearts(e)` that emits `minecraft:heart` at the mob with the `inLove%10==0` cadence (Animal.aiStep). Heart particle = `ParticleTypes.HEART`.
- **Oracle-safe:** hearts only emit when inLove>0 (dormant on the un-fed oracle). The gate stays byte-identical regardless — particles are a broadcast, not an RNG draw.

---

### 10. The plugin side — `plugins/vanilla_pig/main.star` + `server/assets/vanilla_pig/main.star` (config)

**LOCKSTEP — both copies are byte-identical (both 27456 bytes, confirmed).** Every edit goes into BOTH.

**Goal declaration (`main.star:341-420`)** currently lists exactly 7 `goal(...)` entries. Add two:
```python
# @3 BreedGoal(mob, 1.0) [MOVE, LOOK]
goal(priority = 3, flags = ["MOVE", "LOOK"],
     can_use = breed_can_use, tick = breed_tick, stop = breed_stop, can_continue = breed_continue),
# @5 FollowParentGoal(mob, 1.1) EMPTY flags
goal(priority = 5, flags = [],
     can_use = follow_can_use, tick = follow_tick, stop = follow_stop, can_continue = follow_continue),
```
(empty `flags = []` is the FollowParentGoal contract — verify the decl parser accepts an empty flag list; the selector handles it per §6.)

**The callback pattern — copy the Tempt block (`main.star:124-203`):**
```python
def tempt_pigfood_can_use(entity, world, nav):
    p = entity.nearest_player_holding_pig_food(TEMPT_RANGE)   # HOST scan; item id stays Go-side
    if p == None: return False
    entity.set_state("tempt_p_px", p[0]); ...; return True
def tempt_pigfood_tick(entity, world, nav):
    px = entity.get_state("tempt_p_px"); ...
    entity.set_look_at(px, py, pz); entity.move_to(px, py, pz)
```
The breed/follow goals follow this shape: a HOST handle returns the scan result (a partner/parent position tuple or None), the `.star` stores it via set_state and navigates via move_to. The scans are host-computed (the same-class entity scan + canMate logic stays Go-side, exactly as the tempt item-id predicate stays Go-side).

**New HOST handles the `.star` needs (added to `plugin_entity.go` — see §11):**
- `is_in_love` (read accessor — bool)
- `is_baby` (read accessor — bool)
- `breed_age` / `age` (read accessor — int)
- `nearest_breeding_partner(range)` (host scan → position tuple or None)
- `nearest_adult_parent(range)` (host scan → position tuple or None)

**Oracle contract:** breed_can_use gates on `is_in_love` (false on the un-fed oracle → zero effect); follow_can_use gates on `is_baby` (false on the adult oracle → zero effect). Both dormant → byte-identical. Add to BOTH .star copies + verify identical (diff the two files after editing).

---

### 11. `server/plugin_entity.go` — the host handles (service)

**Read-accessor analog (`plugin_entity.go:193-215`):**
```go
case "was_hurt":
	return starlark.Bool(e.hurtTime > 0), nil      // host-COMPUTED scalar, re-resolved via h.store()
case "has_last_damage":
	return starlark.Bool(e.hasLastDamage), nil
```
Add (in the READ block after `in_lava`, `plugin_entity.go:229`, and to `AttrNames` `plugin_entity.go:236-245`):
```go
case "is_in_love":  return starlark.Bool(e.inLove > 0), nil          // Animal.isInLove
case "is_baby":     return starlark.Bool(e.breedAge < 0), nil        // AgeableMob.isBaby
case "breed_age":   return starlark.MakeInt(e.breedAge), nil         // AgeableMob.getAge
```

**Host-scan analog (`plugin_entity.go:326-345`):**
```go
func (h *entityHandle) nearestPlayerHoldingPigFood(...) (starlark.Value, error) {
	if !h.caps.has(capEntitiesRead) { return nil, capError("entities.read") }
	var maxDist float64
	starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &maxDist)
	e, err := h.resolve()
	x, y, z, ok := nearestPlayerHolding(h.t, e, maxDist, func(id int32) bool { return itemInTag(id, "pig_food") })
	if !ok { return starlark.None, nil }
	return starlark.Tuple{starlark.Float(x), starlark.Float(y), starlark.Float(z)}, nil
}
```
Add `nearestBreedingPartner(range)` and `nearestAdultParent(range)` host methods following this exact shape — they call a Go-side `getFreePartner(t, e, range)` / `nearestAdultSameClass(t, e, range)` (the same-class `near()` scan from §7) and return a position tuple or None. Register them in `Attr` (`plugin_entity.go:133-162`, the mutate/method block that returns WITHOUT a read-cap check, OR the cap-checked-inside style of `nearestPlayerHoldingPigFood`) and `AttrNames`.

**Edit points:** ~3 read cases (l.229), ~2 method cases (l.147), ~2 method impls (after l.345), AttrNames (l.236).

---

### 12. The FOUR goal-count tests (test) — 7 → 9 `{0,1,3,4,4,5,6,7,8}`

| Test | File:line | Current assertion | New |
|------|-----------|-------------------|-----|
| `TestPigGoalSetRegistered` | `ai_mob_test.go:137` | `len(m.goals.goals); got != 7` | `!= 9`; add `*breedGoal`@3 + `*followParentGoal`@5 type+flag checks (l.165-191 byPriority block); breed flags `flagMove\|flagLook`, follow flags `0` |
| `TestPluginPigBootLoads` | `plugin_pig_test.go:55` | `len(pig.ai.goals.goals); got != 7` | `!= 9`; the `for _, p := range []int{0, 1, 4, 6, 7, 8}` loop (l.66) → add 3 and 5 |
| `TestVanillaPigDeclaresGoalSet` | `plugin_pig_test.go:103` | `len(decl.goals) != 7` | `!= 9`; add `byPriority[3]`/`byPriority[5]` flag checks after l.146 (breed {MOVE,LOOK}; follow EMPTY = 0) |
| `TestVanillaPigGoalsPorted` | `plugin_pig_test.go:151` | maps goals by priority (`byPriority[8]` etc.) | add @3/@5 to the `byPriority` map walk (l.158-164) — neither requires-update-every-tick (assert false), like @6/@7 |

The `t.Fatalf` message strings also say "want 7 (...)" — update them to 9 and the new goal list. The `TestPluginPigEqualsGoNativePig` oracle (`plugin_pig_test.go:251`) needs NO assertion change (it compares observable sequences) but the pig it drives now has 9 goals — it stays green ONLY if breed/follow are dormant (un-fed lone adult, no nearby partner/parent in its world). Confirm the oracle world (`build` closure, l.258+) spawns ONE pig with no partner — it does.

---

### 13. NBT persist — Age + InLove (service / partial GAP)

**Infrastructure EXISTS** (`persistence.go:266-380`): `entityRegion{Entities []save.Entities}`, `saveEntities`/`loadEntities` via `entities/r.x.z.mca` (the modern entity-region Anvil path, gzip NBT).

**GAP — no LIVE Entity→save.Entities serialization wires mob-specific fields.** The only `save.Entities{...}` construction is in `persistence_test.go:135` (a test fixture with `Pos`/`Motion` only). There is no production code that walks the live entityStore and writes mob fields (breedAge/inLove) into the NBT. `save.Entities` (go-mc fork) carries `Pos`/`Motion` + (likely) a raw tag bag, but nothing populates Age/InLove.

**Recommendation:** For THIS phase, Age/InLove NBT persist is LOW priority for the gate (the oracle is in-memory, the live bot test spawns fresh pigs). If the planner includes it: add a live `Entity → save.Entities` snapshot that emits `Age` (=breedAge) and `InLove` (=inLove) int NBT tags (vanilla AgeableMob.addAdditionalSaveData "Age", Animal.addAdditionalSaveData "InLove"), wired into the chunk-unload/save path. **FLAG this as the weakest-analog surface** — it needs new wiring, not a copy. Suggest deferring full persist to a follow-up unless the live test requires a server restart (it does not).

---

## Shared Patterns

### The RNG-lockstep oracle rule (applies to EVERY new tick/draw)
**Source:** `33-CONTEXT.md:39-67`, the aging/inLove no-op reasoning at `combat_mob.go:644-646`.
**Apply to:** every change in §2, §5, §7, §9, §10. Add each new goal + the aging/inLove tick to BOTH the Go `newPigAI`/baseTick AND both `vanilla_pig/main.star` copies in lockstep. The oracle pig (un-fed lone adult) keeps Breed/Follow dormant + aging a pure-int no-op on stable age-0 → byte-identical. GATE on `TestPluginPigEqualsGoNativePig` after every change.

### The .star lockstep-copy rule
**Source:** both copies are byte-identical (27456 bytes each).
**Apply to:** §10 — every `.star` edit lands in `plugins/vanilla_pig/main.star` AND `server/assets/vanilla_pig/main.star`. Diff the two after editing (must be identical).

### The host-computes-the-scan pattern
**Source:** `nearestPlayerHolding` (`ai_goals_passive.go:381`) + `nearestPlayerHoldingPigFood` (`plugin_entity.go:326`).
**Apply to:** §7, §11 — the entity-AABB same-class scan + canMate/adult-filter logic stays Go-side; the `.star` only ever sees a position tuple or None (never an entity id, never the class set). Mirrors how tempt's item-id predicate stays Go-side.

### The snapshot-friendly / TICK-05 field rule
**Source:** the field doc block at `entity.go:36-41` + every i-frame field.
**Apply to:** §1 — `breedAge`/`inLove` are plain `int`, tick-owned, zero for non-animals, NOT pointers/maps (preserve the async-tracker snapshot contract).

### The tracker-broadcast seam (sound, and the NEW particle)
**Source:** `encodeSoundEntity` (`entity_encode.go:749`) + the combat_mob hurt/death broadcast.
**Apply to:** §4 (playEatingSound — REUSE) and §9 (hearts — NEW encoder following the same `pk.Marshal` + broadcast-to-trackers shape).

---

## No Analog Found / Gaps to Flag

| Surface | Reason | Planner action |
|---------|--------|----------------|
| **Heart-particle wire-out** | `ClientboundLevelParticles` packet id exists but NO `encodeLevelParticles` encoder anywhere | WRITE a new encoder (javap `ClientboundLevelParticlesPacket`); framing twin = `encodeSoundEntity`. Oracle-safe (dormant). |
| **BOOLEAN metadata serializer** | only BYTE(0)/INT(1)/ITEM(7) consts exist; no `boolSerializerID` | Add the const (javap EntityDataSerializers registration order, likely 8) + `pk.Boolean` value codec (verify it exists in `net/packet`). |
| **DATA_BABY_ID accessor index** | not yet computed | javap `Mob` + `AgeableMob` defineId counts; walk the hierarchy like the Avatar=16 derivation at `entity_encode.go:339-347`. DO NOT guess. |
| **isPanicking read** | BreedGoal.getFreePartner needs `!other.isPanicking()`; PanicGoal running-state is in the selector, no direct flag | Confirm/add an `e.ai` panicking signal (or read the @1 panicGoal's `running` via the selector). FLAG for the planner. |
| **Live Entity→NBT mob-field serialization** | infra exists (`saveEntities`) but nothing writes breedAge/inLove; only a test fixture builds `save.Entities` | New wiring, not a copy. Recommend DEFER full persist unless the live test restarts the server (it does not). |
| **`getSpeedUpSecondsWhenFeeding` + `canAgeUp`** | the baby-feed grow-speedup formula `(int)(ageDelta/20*0.1f)` (33-JARNOTES.md:113) | Port verbatim; `canAgeUp` = `breedAge<0 && forcedAgeTimer<=0` (forcedAgeTimer is a v1 const-0 stub). |

---

## Recommended Plan Split (planner decides)

My read — 4 sequential waves (each keeps the oracle green; the goals depend on the subsystem):

- **Plan A — Aging + AgeableMob + DATA_BABY_ID wire** (§1 breedAge field, §2 aging tick in baseTick, §3 the bool metadata + BOOLEAN serializer + the verified DATA_BABY_ID index, §13 Age NBT if included). Self-contained subsystem; oracle stays byte-identical (pure-int no-op on age-0). Tests: aging (baby ticks to adult), baby-renders-small wire.
- **Plan B — Animal in-love + the FEED path** (§1 inLove field, §2 the inLove decrement + hearts seam, §4 handleInteract FEED path with shrinkHeldItem + playEatingSound, isFood reuse, §9 the heart-particle ENCODER (the gap), InLove NBT). Depends on A (needs breedAge for the adult/baby branch). Tests: feed-sets-love (adult), feed-ages-up (baby).
- **Plan C — BreedGoal@3 + FollowParentGoal@5** (§5 newPigAI +2 goals, §6 verified-no-change selector, §7 the two new goal files + the same-class scan + canMate/getFreePartner + breed(), §8 child spawn, §9 breed XP orb, §10 BOTH .star copies +2 goals + callbacks, §11 the 5 new host handles, §12 ALL FOUR goal-count tests 7→9). Depends on A+B (the goals gate on isInLove/isBaby). The biggest plan — the planner may split C into C1 (goals+scan) and C2 (.star lockstep + handles + tests).
- **Plan D — THE GATE** (the full 9-goal `TestPluginPigEqualsGoNativePig` byte-identical + breed/aging/follow scenario tests + `-race` + the LIVE bot breeding test: feed two pigs, watch a baby spawn + follow). Depends on A+B+C all green.

---

## Metadata

**Analog search scope:** `server/*.go` (entity, combat_mob, entity_encode, attack_dispatch, ai_mob, ai_goal, ai_goals_passive, xp_orb, death_mob, plugin_mob_decl, plugin_entity, block_interact, persistence, region_transfer, entity_store) + both `vanilla_pig/main.star` + `plugin_pig_test.go` + `ai_mob_test.go`.
**Files scanned:** ~22.
**Pattern extraction date:** 2026-06-30.
