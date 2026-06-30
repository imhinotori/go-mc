# Phase 36: Wolf (Neutral/Tameable) - Pattern Map

**Mapped:** 2026-06-30
**Files analyzed:** 11 (new/modified)
**Analogs found:** 11 / 11 (every new file has a strong in-repo analog — the wolf is a heavy REUSE phase)

## Orientation (read first)

The wolf is built almost entirely by EXTENDING proven seams, not inventing new ones. Three rules
the planner should carry into every plan:

1. **Combat goals are ZERO re-port.** The wolf's LeapAtTargetGoal/MeleeAttackGoal/HurtByTargetGoal/
   NearestAttackableTargetGoal are the Phase-35 Go-native goals, consumed via `kind=` in the .star
   (the `vanilla_zombie` template). DO NOT re-implement them.
2. **The pig oracle stays byte-identical.** Every new field/metadata/interact/anger path is wolf-
   gated (`e.typ == entity.Wolf.ID` / a new base_type "wolf") so it draws ZERO new RNG on the pig —
   the exact discipline cow/sheep/chicken/zombie additions already follow (the gates are visible in
   every analog).
3. **NEW machinery follows an EXISTING shape.** Tame/owner/sit/anger STATE mirrors the baby/wool/
   eggTime field pattern (entity.go); the DATA_FLAGS/owner metadata mirrors babyDataEntry/woolDataEntry
   (entity_encode.go); the taming interact mirrors tryMilkCow/trySheepShear (attack_dispatch.go); the
   new goals mirror the Phase-35 goal ports + the `buildNativeGoal` kind seam. There is a precise
   analog for each — copy its structure, cite the wolf bytecode from 36-JARNOTES.md.

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `plugins/vanilla_wolf/main.star` | plugin/declaration | event-driven (goal callbacks) | `plugins/vanilla_zombie/main.star` (combat-kind) + `plugins/vanilla_cow/main.star` (passive) | exact |
| `server/assets/vanilla_wolf/main.star` (+ plugin.toml) | embed/config | byte-identical copy | `server/assets/vanilla_zombie/main.star` | exact |
| `server/entity.go` (wolf state fields) | model/state | field bookkeeping | `entity.go` baby/inLove/sheared/eggTime fields (270-337) | exact |
| `server/entity_encode.go` (DATA_FLAGS + owner data) | model/encode | request-response (metadata) | `babyDataEntry`/`woolDataEntry` (384-456) | exact |
| `server/attack_dispatch.go` (`tryTameWolf`) | controller/interact | request-response (RNG) | `tryMilkCow` (872-932) / `trySheepShear` | exact |
| `server/combat_mob.go` (anger-on-hit set) | service | event-driven (RNG timer) | the `flag2` lastHurtByMob store-point (110-126) | role+flow match |
| `server/ai_goals_sit.go` (NEW SitWhenOrderedToGoal) | goal port | event-driven | `ai_goals_attack.go` leapAtTargetGoal / `ai_goals_target.go` hurtByTargetGoal | role match |
| `server/ai_goals_owner.go` (FollowOwner/OwnerHurt*) | goal port | event-driven | `ai_goals_target.go` hurtByTargetGoal (lastHurtBy read) | role+flow match |
| `server/plugin_mob_ai.go` + `plugin_mob_decl.go` (new kinds) | seam/wiring | dispatch | `buildNativeGoal` switch (271-290) + `nativeKind` validation | exact |
| `level/attribute/defaults.go` (`wolfSupplier`) | config/data | data build | `cowSupplier`/`catSupplier`/`zombieSupplier` (107-170) | exact |
| `server/mob_category.go` (`categoryOf` wolf) + `commands_dbg.go` + embed list | config/wiring | dispatch | the cow arm of `categoryOf` (87-91) + `/dbg cow` + `vanillaMobNames` | exact |

## Pattern Assignments

### `plugins/vanilla_wolf/main.star` (plugin, event-driven)

**Analog:** `plugins/vanilla_zombie/main.star` (the combat-`kind=` template) with the PASSIVE goal
callbacks copied verbatim from `plugins/vanilla_cow/main.star`.

The wolf is a HYBRID: it declares its combat goals via `kind=` (the Go-native Phase-35 ports, like
the zombie) AND its passive goals as `.star` callbacks (Float/Stroll/Look/Around/Breed, copied
verbatim from the cow — they are mob-agnostic). The NEW goals (sit/follow-owner/owner-hurt) are
added as either new `kind=` values (preferred — they carry no per-mob RNG) or `.star` callbacks.

**The `kind=` combat-goal declaration pattern** (`vanilla_zombie/main.star:166-209`):
```python
goals = [
    goal(priority = 3, flags = ["MOVE"], kind = "melee_attack"),          # @5 MeleeAttackGoal(1.0)
    goal(priority = 4, flags = ["JUMP", "MOVE"], kind = "leap_at_target"),# @4 LeapAtTargetGoal(0.4)
    # ... passive .star goals (stroll/look/around/float/breed copied from vanilla_cow) ...
    goal(priority = 1, flags = ["TARGET"], kind = "hurt_by_target"),      # targetSelector @3
    goal(priority = 2, flags = ["TARGET"], kind = "angry_player_target"), # @4 the anger-gated player target (B1 fix: a distinct kind, NOT bare nearest_attackable_target)
]
```

**The passive goal copy source** (cow @0 Float, @5 Stroll, @6 Look, @7 Around — `vanilla_cow/main.star:78-385`):
copied byte-for-byte (the callbacks read only `entity.*`/`world.*`/`nav.*` handles, mob-agnostic).
The wolf's BreedGoal reuses the cow's `breed_*` callbacks (Phase 33). Draw ORDER in each callback
MUST match the wolf's `registerGoals` bytecode (36-JARNOTES.md:13-37).

**The declaration block** (`vanilla_zombie/main.star:157-211` shape):
```python
declare_mob(
    name = "vanilla_wolf",
    base_type = "wolf",     # NEW base_type — must be added to baseTypeByName (see below)
    attributes = {
        "movement_speed": 0.3,   # Wolf.createAttributes: MOVEMENT_SPEED 0.3
        "max_health": 8.0,       # MAX_HEALTH 8.0 (untamed; tamed bumps to 40 via setTame)
        "attack_damage": 4.0,    # ATTACK_DAMAGE 4.0
    },
    goals = [ ... ],
)
```

---

### `server/assets/vanilla_wolf/main.star` + `plugin.toml` (embed, byte-identical)

**Analog:** `server/assets/vanilla_zombie/main.star` + `server/assets/vanilla_cow/plugin.toml`.

The embedded copy MUST be byte-identical to the repo-root `plugins/vanilla_wolf/` copy (the
`vanilla_pig_embed.go:28-30` invariant: "Keep each repo-root/embed pair byte-identical. Embedding the
dirs covers plugin.toml + main.star"). The `plugin.toml` caps mirror the cow's (the taming interact
is a HOST-side path in attack_dispatch.go, NOT a plugin op — `assets/vanilla_cow/plugin.toml:7`
"tryMilkCow... not a plugin op, so no widened cap").

---

### `server/entity.go` — wolf state fields (model/state)

**Analog:** the `breedAge`/`inLove`/`sheared`/`eggTime` field cluster (`entity.go:270-337`).

Add the tame/owner/sit/anger fields following the EXACT documented-field pattern: each carries a
jar-cite, a zero-value-is-the-pig-default note, and a "tick-owned, snapshot-friendly" note. The
`sheared bool` field (315-325) is the closest single template (a DATA-mirrored bool, wolf-gated, zero
for the pig):
```go
// sheared is the host-side mirror of net.minecraft.world.entity.animal.sheep.Sheep's DATA_WOOL
// sheared bit ... Tick-owned plain bool (TICK-05), FALSE (not sheared) for the zero value, never read
// for a non-sheep entity ... so it is a harmless no-op for the pig ...
//	[VERIFIED javap Sheep: isSheared() == (DATA_WOOL & 0x10) != 0; ...]
sheared bool
```

**New wolf fields to add (each cited from 36-JARNOTES.md:109-156):**
- `tame bool` / `orderedToSit bool` / `inSittingPose bool` — TamableAnimal DATA_FLAGS bits 0x4 / (orderedToSit field) / 0x1 (JARNOTES:112-115).
- `ownerUUID` (the DATA_OWNERUUID_ID owner ref — store the owner entity id/UUID; JARNOTES:73,113).
- `angerTime int` (remainingPersistentAngerTime) + `angerTarget` (persistentAngerTarget UUID) — NeutralMob (JARNOTES:77-81).
- The owner-side `lastHurtMob` bookkeeping the OwnerHurtTargetGoal needs (mirror `lastHurtByMob`; JARNOTES:149-156).

**Existing `lastHurtByMob`/`lastHurtByMobTimestamp` fields** (set in combat_mob.go:124-125) are
REUSED by HurtByTargetGoal AND drive anger-on-hit — no new field for the inbound side.

---

### `server/entity_encode.go` — DATA_FLAGS + owner metadata (model/encode)

**Analog:** `babyDataEntry` (384-417) and `woolDataEntry` (428-456).

The DATA_FLAGS byte is the EXACT shape of the DATA_WOOL byte: a `const dataFlagsIndex uint8` derived
by the same defineId-down-the-hierarchy count, a `byteSerializerID` (== 0) entry, and a
`wolfFlagsDataEntry(flagsByte byte)` builder. The owner UUID is an Optional<EntityReference> accessor —
follow the same `entityDataEntry` framing.

**The index-derivation pattern** (`entity_encode.go:428-436`):
```go
// dataWoolIndex is the SynchedEntityData accessor index for Sheep.DATA_WOOL_ID. Continuing the
// dataBabyIndex=16 derivation (defineId assigns indices sequentially down the hierarchy): Entity 0..7,
// LivingEntity 8..14, Mob 15, AgeableMob 16 (DATA_BABY_ID) + 17 (AGE_LOCKED), Animal adds NO accessor,
// Sheep adds DATA_WOOL_ID = index 18, BYTE serializer.
const dataWoolIndex uint8 = 18
```
For the wolf: TamableAnimal extends Animal→AgeableMob; DATA_FLAGS_ID + DATA_OWNERUUID_ID are
TamableAnimal's OWN accessors AFTER index 17 (AGE_LOCKED) — so the wolf's DATA_FLAGS index is **18**,
owner UUID **19** (JARNOTES:118 flags the exec-time chain-count requirement — VERIFY at exec).

**The builder pattern** (`woolDataEntry`, 450-456):
```go
func woolDataEntry(woolByte byte) entityDataEntry {
    return entityDataEntry{
        index:        dataWoolIndex,
        serializerID: byteSerializerID,
        value:        pk.Byte(int8(woolByte)),
    }
}
```

**The spawn-time metadata splice** (the carry pattern, `plugin_mob_decl.go:474-479` + the broadcast
pattern `combat_mob.go:786-796` `broadcastBabyFlag`): a tamed/sitting wolf splices its DATA_FLAGS
entry onto `e.metadata` at spawn and broadcasts the flip on tame/sit-toggle via
`encodeSetEntityDataByID` (entity_encode.go:506).

---

### `server/attack_dispatch.go` — `tryTameWolf` (controller/interact, RNG)

**Analog:** `tryMilkCow` (872-932) — the cleanest sibling (a held-item-gated, mob-gated interact that
returns true to consume the interact). `trySheepShear` is the second reference.

The taming path slots into `handleInteract` at the SAME spot the milk/shear gates use
(`attack_dispatch.go:773-786`), wolf-gated:
```go
if mob.typ == entity.Sheep.ID && t.trySheepShear(p, mob) { return }
if mob.typ == entity.Cow.ID && t.tryMilkCow(p, mob) { return }
// ADD: if mob.typ == entity.Wolf.ID && t.tryWolfInteract(p, mob) { return }
t.tryFeedAnimal(p, mob)
```

**The interact-handler shape** (`tryMilkCow:897-932` — held read, gate, action, return bool):
```go
func (t *TickLoop) tryMilkCow(p *tickPlayer, mob *Entity) bool {
    inv := ensureInventory(p)
    held := inv.get(heldWindowSlot(inv.heldSlot))       // server-side held read (NEVER from packet)
    if slotIsEmpty(held) { return false }
    if int32(held.ItemID) != int32(item.Bucket.ID) { return false }  // is(BUCKET) gate
    if mob.isBaby() { return false }                    // !isBaby gate
    // ... action (sound + item swap) ...
    return true
}
```

**The wolf taming logic** (`Wolf.mobInteract` + `tryToTame`, JARNOTES:120-129) — the RNG path:
- Untamed + held `is(WOLF_FOOD)`... actually held `is(Items.BONE)` + `!isAngry()` → `stack.consume(1)` → `tryToTame`.
- `tryToTame`: `if (random.nextInt(3) == 0) { tame(player); setOrderedToSit(true); broadcastEntityEvent(7 /*hearts*/) } else broadcastEntityEvent(6 /*smoke*/)`.
- **ONE `nextInt(3)` draw per BONE feed** — drawn on the wolf's `mobRandom(e)` stream (the same
  `mobRandom`/`nextInt` seam combat goals use, ai_goals_target.go:91). The hearts(7)/smoke(6) reuse
  the EntityEvent broadcast (`broadcastHearts`/`broadcastMobDamageEvent` pattern, combat_mob.go).
- `tame()` = setTame(true) [flip DATA_FLAGS 0x4 + applyTamingSideEffects MAX_HEALTH 8→40] + setOwner.

The held-tag read reuses `itemInTag(itemID, "...")` (attack_dispatch.go:838 — confirm `wolf_food`/BONE
tag presence in data/tag at phase open; the BONE branch is `is(Items.BONE)`, an exact item id like
tryMilkCow's `is(BUCKET)`).

---

### `server/combat_mob.go` — anger-on-hit (service, RNG timer)

**Analog:** the `flag2` store-point (110-126) where `lastHurtByMob`/`hasLastDamage`/`lastDamageSource`
are recorded on a fresh hit.

The NeutralMob anger trigger fires at this SAME store-point, wolf-gated: when the victim is a wolf hit
by a player, call `startPersistentAngerTimer` which sets `angerTime = UniformInt(400,780).sample(rng)`.
The existing `lastHurtByMob = src.attacker` write (124) already feeds HurtByTargetGoal; the anger
timer is the additive sibling.

**The store-point** (combat_mob.go:110-126):
```go
if flag2 {
    e.lastDamageSource = src
    e.hasLastDamage = true
    e.lastHurtByMob = src.attacker       // Phase 35: HurtByTargetGoal reads this
    e.lastHurtByMobTimestamp = int32(t.gametime)
    // ADD (wolf-gated): if e.typ == entity.Wolf.ID && src.attacker is a player -> startPersistentAngerTimer(e)
}
```

**The anger-timer RNG draw** (JARNOTES:139-143): `PERSISTENT_ANGER_TIME = UniformInt(400, 780)`;
`sample(random) = 400 + nextInt(780 - 400 + 1) = 400 + nextInt(381)` — ONE `mobRandom(e).nextInt(381)`
draw per anger trigger. Port the EXACT UniformInt.sample draw (`minInclusive + nextInt(max-min+1)`).
The RNG seam is `mobRandom(e)` (combat_mob is on the tick goroutine; this is the wolf's per-entity
stream — pig draws ZERO since the gate is `typ == Wolf.ID`).

`isAngryAt` (JARNOTES:144-145) gates the kind-routed `angry_player_target` goal (B1 fix: a parameterized
target goal with targetClass=PLAYER + the isAngryAt angerGate; the bare `nearest_attackable_target` the
hostiles use stays un-gated) — see the new-kind seam below; the anger read is a new predicate the
targetSelector goal consults.

---

### `server/ai_goals_sit.go` (NEW) — SitWhenOrderedToGoal (goal port)

**Analog:** `ai_goals_target.go` hurtByTargetGoal (a NO-RNG goal that reads entity state) and
`ai_goals_attack.go` leapAtTargetGoal (the goal struct + ctor + flags shape).

SitWhenOrderedToGoal has NO RNG (JARNOTES:131-137). Port the goal struct + `newSitWhenOrderedToGoal()`
ctor with flags `{JUMP, MOVE}`, mirroring `newLeapAtTargetGoal` (ai_goals_attack.go:404):
```go
func newLeapAtTargetGoal(yd float64) *leapAtTargetGoal {
    return &leapAtTargetGoal{baseGoal: newBaseGoal(flagJump | flagMove), yd: yd}
}
```

**canUse** (JARNOTES:133-135): `if (!orderedToSit && !isTame()) return false; if (isInWater()) return false;
if (!onGround()) return false; owner = getOwner(); if (owner==null||owner.level!=mob.level) return true;
if (distanceToSqr(owner) < 144.0 && owner.getLastHurtByMob() != null) return false; return orderedToSit`.
Reads the new entity fields (orderedToSit/tame/owner) + the owner's lastHurtByMob (the owner-side
bookkeeping added to entity.go). `start()` = navigation.stop() + setInSittingPose(true) (broadcast
DATA_FLAGS 0x1); `stop()` = setInSittingPose(false). Use the `e.ai.setWantTarget`/nav-stop seam
(ai_goals_attack.go) for the park.

---

### `server/ai_goals_owner.go` (NEW) — FollowOwner/OwnerHurtBy/OwnerHurt (goal ports)

**Analog:** `ai_goals_target.go` hurtByTargetGoal (49-290) — the EXACT shape: a TARGET goal that reads
the entity's lastHurtBy bookkeeping + stamps a timestamp so it fires once per fresh hit.

**OwnerHurtByTargetGoal.canUse** (JARNOTES:149-154) is the owner-side mirror of hurtByTargetGoal:
```go
// hurtByTargetGoal.canUse (ai_goals_target.go:218): reads e.lastHurtByMobTimestamp / e.lastHurtByMob
// OwnerHurtByTargetGoal: reads OWNER.lastHurtByMob (+ ts != this.timestamp guard) -> setTarget(ownerLastHurtBy)
```
Port using the owner resolved from `ownerUUID` and the owner's `lastHurtByMob`/`lastHurtByMobTimestamp`
(or `lastHurtMob` for OwnerHurtTargetGoal). Gate: `if (!isTame() || isOrderedToSit()) return false`.
NO RNG. The `setTarget`/`getTarget` seam is `e.ai.setTarget()` (ai_goals_target.go:161-177).

**FollowOwnerGoal** (JARNOTES:162 STILL-OPEN — decompile at exec): the teleport-to-owner + path goal.
Port like a MOVE goal that sets a nav want toward the owner (the `tempt`/`breed` move-to shape from
the cow `.star`, or a Go-native goal mirroring meleeAttackGoal's `setWantTarget` toward a position).

---

### `server/plugin_mob_ai.go` + `plugin_mob_decl.go` — new goal kinds (seam/wiring)

**Analog:** the `buildNativeGoal` switch (plugin_mob_ai.go:271-290) and the `nativeKind` validation
(plugin_mob_decl.go:289-307). This is the EXACT seam the Phase-35 combat kinds use.

Add the new kinds to the switch (the wolf-specific Go-native goals — sit/follow_owner/owner_hurt_by/
owner_hurt) the same way leap_at_target was added:
```go
func buildNativeGoal(kind string, decl *mobDecl) Goal {
    switch kind {
    case "nearest_attackable_target": return newNearestAttackableTargetGoal()
    case "hurt_by_target":            return newHurtByTargetGoal()
    case "melee_attack":              return newMeleeAttackGoal(declaredWalkSpeed(decl))
    case "leap_at_target":            return newLeapAtTargetGoal(spiderLeapYd)
    case "float":                     return newFloatGoal()
    // ADD: case "sit":            return newSitWhenOrderedToGoal()
    // ADD: case "follow_owner":   return newFollowOwnerGoal(declaredWalkSpeed(decl))
    // ADD: case "owner_hurt_by":  return newOwnerHurtByTargetGoal()
    // ADD: case "owner_hurt":     return newOwnerHurtTargetGoal()
    default: return nil
    }
}
```
The `flags()` assertion (plugin_mob_ai.go:215) verifies the declared flags match each Go goal's own
flag set — so the wolf's `goal(priority=2, flags=["JUMP","MOVE"], kind="sit")` must agree with
`newSitWhenOrderedToGoal()`'s `{JUMP, MOVE}`. Update the panic message's valid-kinds list (208).
NOTE: the kind-only-no-callback validation (plugin_mob_decl.go:296-301) already rejects a callback +
kind combo — no change needed there beyond the switch addition.

---

### `level/attribute/defaults.go` — `wolfSupplier` (config/data)

**Analog:** `cowSupplier` (165-170) / `catSupplier` (110-116) — an Animal-based supplier with overrides.

ROADMAP SC#1 names this "the one missing supplier". Add it exactly like the cow/cat, with the wolf's
`createAttributes` values (JARNOTES:39-41, 157-159):
```go
func wolfSupplier() *Supplier {
    return createAnimalAttributes().      // Animal base (Wolf extends TamableAnimal -> Animal)
        AddValue(MovementSpeed, 0.3).     // Wolf.createAttributes: MOVEMENT_SPEED 0.3
        AddValue(MaxHealth, 8.0).         // MAX_HEALTH 8.0 (untamed; tamed -> 40 via applyTamingSideEffects)
        AddValue(AttackDamage, 4.0).      // ATTACK_DAMAGE 4.0
        Build()
}
```
Register it in the `suppliers` map (246-262): `"wolf": wolfSupplier()`. The tamed MAX_HEALTH 40 bump is
applied at runtime by `setTame`'s `applyTamingSideEffects` (entity.go side, not the base supplier) —
decompile `Wolf.applyTamingSideEffects` at exec (JARNOTES:159,163) for the exact 40-HP + full-heal.

---

### `server/mob_category.go` + `commands_dbg.go` + embed list (config/wiring)

**Analog (categoryOf):** the cow arm (mob_category.go:87-91):
```go
case entity.Wolf.ID:
    // Wolf is MobCategory.CREATURE (vanilla EntityType.WOLF; data/entity Wolf.Type == "creature").
    return categoryCreature
```
(data/entity Wolf.Type is confirmed "creature", entity.go:1368 — ID 149.)

**Analog (base_type resolver):** add `"wolf": entity.Wolf` to `baseTypeByName` (plugin_mob_decl.go:120-133)
and update the loud-error allowed list (355).

**Analog (/dbg):** the `/dbg cow` arm (commands_dbg.go:20-24):
```go
case "wolf":
    e := t.spawnVanillaMob(vanillaWolfMobName, p.x, p.y, p.z)
    if e != nil { t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned wolf eid=%d ...", e.id, ...)) }
```
Update the usage string (60).

**Analog (embed + boot-load):** add `assets/vanilla_wolf` to the `//go:embed` directive
(vanilla_pig_embed.go:32), a `vanillaWolfMobName = "vanilla_wolf"` const (38-54), and append it to
`vanillaMobNames` (62-70). The loader is category-agnostic (it loads a CREATURE exactly as a MONSTER).

## Shared Patterns

### Wolf-gating (the pig-oracle-preservation discipline)
**Source:** every Phase-34/35 mob addition (e.g. attack_dispatch.go:773, combat_mob.go, mob_category.go:87).
**Apply to:** every new field, metadata, interact, and combat path.
Every wolf path is gated `e.typ == entity.Wolf.ID` (or a new base_type "wolf"), so it is a zero-cost
no-op for the pig — the pig oracle stream gains ZERO new draws. State a "pig oracle unperturbed" note
in each new code block (the analogs all carry it).

### Per-entity RNG stream (`mobRandom`)
**Source:** `ai_goals_target.go:91` (`mobRandom(e).nextInt(...)`), `plugin_mob_decl.go:454` (`reseedMobAI`).
**Apply to:** the tame `nextInt(3)` and the anger `nextInt(381)` draws.
The wolf's RNG draws use the SAME per-entity seeded stream the kind-routed combat goals draw from
(reseeded per entity id at spawn). Draw ORDER discipline applies if the wolf is ever dogfooded against
an oracle — but per 36-CONTEXT the wolf uses a per-mob BEHAVIOR test + focused RNG tests, not a full
Go-vs-plugin oracle.

### EntityEvent broadcast (hearts/smoke/damage-flash)
**Source:** `broadcastHearts` (attack_dispatch.go:848), `broadcastMobDamageEvent`/`broadcastBabyFlag` (combat_mob.go:786-796).
**Apply to:** tryToTame's hearts(7)/smoke(6) and the DATA_FLAGS flip on tame/sit.
The status-byte EntityEvent + the SetEntityData fan-out are existing seams — reuse `broadcastToTrackers`
+ `encodeSetEntityDataByID` for the metadata flip, and the EntityEvent broadcast for hearts/smoke.

### Per-mob behavior test + focused RNG tests
**Source:** `cow_test.go` (loadVanillaCowRegistry / cowLoop / spawnCow / TestCowBootLoads),
`zombie_test.go`, `hostiles_embed_test.go` (the real //go:embed boot-load), `skeleton_test.go`.
**Apply to:** the wolf test suite.
Mirror the cow/zombie harness: a `loadVanillaWolfRegistry` (temp-dir from `../plugins/vanilla_wolf`),
a `wolfLoop`, a `spawnWolf`, a `TestWolfBootLoads`, a `TestWolfBehavior` (tame/sit/follow/anger), plus
focused RNG tests for the tame `nextInt(3)` and the anger `UniformInt(400,780)` sample. Add wolf to the
`hostiles_embed_test.go`/`vanilla_mob_test.go` boot-load assertions. The pig oracle test stays
byte-identical (untouched).

## No Analog Found

None. Every new file has a strong in-repo analog — the wolf is the highest-reuse phase in v5 (it sits
on top of Phases 29-35's keystones). The ONLY genuinely new GOAL CLASSES (SitWhenOrderedToGoal,
FollowOwnerGoal, the OwnerHurt goals) still follow the Phase-35 goal-port SHAPE precisely (the
`ai_goals_*.go` struct + ctor + flags + bytecode-cited canUse/start/stop pattern); they are new
PORTS, not new PATTERNS.

## Metadata

**Analog search scope:** `server/` (plugin_mob_ai, plugin_mob_decl, ai_goals_target, ai_goals_attack,
mob_category, attack_dispatch, combat_mob, entity, entity_encode, commands_dbg, vanilla_pig_embed,
cow_test/zombie_test), `plugins/vanilla_zombie`, `plugins/vanilla_cow`, `level/attribute/defaults.go`,
`data/entity/entity.go`.
**Files scanned:** 16 source files + 2 .star templates.
**Pattern extraction date:** 2026-06-30
