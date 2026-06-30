# Phase 32: Held-Item + Item Tags (S4) - Context

**Gathered:** 2026-06-30
**Status:** Ready for planning
**Mode:** Auto-generated (user AFK; jar-verified — the goal map + tags + read surfaces all confirmed in-tree)

<domain>
## Phase Boundary

Build the S4 subsystem: a goal can read the NEAREST player's held item (main-hand + off-hand) and test
item-tag / item-id membership. Wire **TemptGoal@4 ×2** onto the pig (Go-native `newPigAI` + BOTH
`vanilla_pig/main.star` copies, lockstep): the pig follows a player holding a carrot-on-a-stick OR any
`pig_food` item. The S4 read also powers love-on-feed in Phase 33 (breeding) — build the read here.

OUT OF SCOPE: BreedGoal / in_love / aging (Phase 33); FollowParentGoal (Phase 33); any other mob.
</domain>

<decisions>
## Implementation Decisions (jar-verified — javap/CFR over temp/cache/26.2-inner.jar)

### Pig.registerGoals (confirmed): two TemptGoals at priority 4
```java
this.goalSelector.addGoal(4, new TemptGoal(this, 1.2, i -> i.is(Items.CARROT_ON_A_STICK), false)); // canScare=false
this.goalSelector.addGoal(4, new TemptGoal(this, 1.2, i -> i.is(ItemTags.PIG_FOOD), false));        // canScare=false
```
Two SEPARATE goals at the same priority 4, speed 1.2, canScare=false. One matches the literal item
`carrot_on_a_stick` (id 887, data/item/item.go:5340), the other matches the `pig_food` item TAG
(data/tag/tags.go:305 = {carrot 1257, potato 1258, beetroot 1317}). NOTE the goal-selector at the same
priority: vanilla runs BOTH at prio 4 (the selector allows multiple goals of the same priority if their
flags don't conflict — but both claim {MOVE,LOOK}, so only ONE runs at a time; the selector picks by
insertion order / availability). The v1 goal runner must handle two goals at the same priority claiming
the same flags — verify how the existing goal selector (ai_mob.go goals) handles same-priority same-flag
goals; the faithful behavior is "first one whose canUse is true claims the flags".

### TemptGoal (net.minecraft.world.entity.ai.goal.TemptGoal), flags {MOVE, LOOK}
```java
private static final TargetingConditions TEMPT_TARGETING = TargetingConditions.forNonCombat().ignoreLineOfSight();
private static final double DEFAULT_STOP_DISTANCE = 2.5;   // pig uses the default ctor → stopDistance 2.5

public boolean canUse() {
    if (calmDown > 0) { --calmDown; return false; }                         // post-stop cooldown
    player = getServerLevel(mob).getNearestPlayer(
        TEMPT_TARGETING.range(mob.getAttributeValue(Attributes.TEMPT_RANGE)), mob);  // TEMPT_RANGE attr
    return player != null;
}
private boolean shouldFollow(LivingEntity p) {                              // the TARGETING selector
    return items.test(p.getMainHandItem()) || items.test(p.getOffhandItem());
}
public boolean canContinueToUse() {
    if (canScare()) { ... player-moved-too-much abort ... }                 // PIG: canScare=false → this whole block is SKIPPED
    return canUse();
}
public void start() { px=player.getX(); py=...; pz=...; isRunning=true; }
public void stop() { player=null; stopNavigation(); calmDown = reducedTickDelay(100); isRunning=false; } // 100 → 50 @ 20TPS
public void tick() {
    mob.getLookControl().setLookAt(player, maxHeadYRot+20, maxHeadXRot);
    if (mob.distanceToSqr(player) < stopDistance*stopDistance) stopNavigation();   // 2.5^2 = 6.25
    else navigateTowards(player);                                                   // moveTo(player, 1.2)
}
```

KEY (canScare=false for the pig): `canContinueToUse` skips the entire canScare block → just `return canUse()`.
So a pig's TemptGoal continues as long as a player in TEMPT_RANGE holds a tempt item; no flee-on-player-move.

### TEMPT_RANGE attribute
`Attributes.TEMPT_RANGE` — the nearest-player scan radius. Default value: VERIFY via javap
`Attributes.<clinit>` / DefaultAttributes for the pig (typical default is 10.0). Port it as the attribute
read; if the attribute system has no TEMPT_RANGE entry yet, add it with the jar default (cite it) — never
bake the literal away.

### NO RNG → oracle-trivially-safe
TemptGoal.canUse draws ZERO RNG (calmDown int math + a player scan + held-item predicate test). The pig
ORACLE world has NO players near the pig (TestPluginPigEqualsGoNativePig drives a pig with a player far
away / not holding tempt items) → canUse returns false (player==null or shouldFollow false) → zero new
draws → oracle byte-identical. STILL add both TemptGoals to BOTH the Go newPigAI AND both .star copies in
lockstep (same flags, same priority 4). VERIFY the oracle's player (if any) is NOT holding a carrot/pig_food
within TEMPT_RANGE, else the goal would fire and change behavior on BOTH halves identically (still
byte-identical, but document it). Gate on the oracle.

### The S4 read (the new subsystem)
1. **Nearest-player-with-predicate scan.** Extend the existing `nearestPlayerAt`/`nearestPlayerWithin`
   (ai_goals_passive.go:343-351, currently returns only x,y,z) to ALSO expose the player so the goal can
   read its held items — OR add `nearestPlayerHolding(t, e, range, predicate) (player, ok)`. The TEMPT_TARGETING
   is forNonCombat + ignoreLineOfSight + the shouldFollow selector — i.e. the nearest player WHO PASSES
   shouldFollow (holds a tempt item). Port the selector INTO the scan (vanilla's getNearestPlayer applies the
   TargetingConditions selector during the scan).
2. **Held-item read.** Main-hand = `inv.get(heldWindowSlot(inv.heldSlot))` (block_interact.go:187,326).
   Off-hand = window slot 45 (verify the off-hand slot index in inventory.go). Read BOTH (shouldFollow tests
   main || off).
3. **Item predicate.** `items.test(stack)` = either `stack.is(carrot_on_a_stick)` (item id 887) OR
   `stack.is(pig_food)` (ItemTags membership via data/tag ItemTags["pig_food"], tags.go:305). Two goals, two
   predicates. Add a Go item-tag membership read (the analog of the damage-type is(tag) the keystone uses)
   + the plugin handle to test an item id against an item tag by NAME.

### Plugin side (lockstep)
The .star tempt_can_use needs to read the nearest player's held item + test the predicate. Add the
host-side handles: `entity.nearest_tempt_player(range, item_pred)` is too complex for Starlark; PREFER the
RUNTIME-COMPUTES pattern (like FloatGoal/PanicGoal): expose frozen scalar handles the goal reads —
e.g. `entity.nearest_player_holding_pig_food(range)` / `..._carrot_on_a_stick(range)` returning the player
position + a found bool, host-computed (the host does the scan + predicate). The .star just reads the bool +
position and calls path_to / look. This keeps the item-tag id set Go-side (mirrors Phase-31 damage_in_tag).
Design the exact handle surface in the plan so Go + plugin agree (no RNG, so no lockstep draw risk — but
the BEHAVIOR must match: same range, same predicate, same stop-distance).
</decisions>

<code_context>
## Existing surfaces (reuse/extend)
- `server/ai_goals_passive.go:343-351` — `nearestPlayerWithin`/`nearestPlayerAt` (position-only; extend to expose the player + a held-item predicate).
- `server/block_interact.go:184-187,326-330` — the main-hand read (`inv.get(heldWindowSlot(heldSlot))`); off-hand slot index in inventory.go.
- `data/item/item.go:5340` — CarrotOnAStick (id 887). `data/tag/tags.go:305` — ItemTags["pig_food"].
- The item-tag is(tag) read: mirror the damage-type `damageSource.is(tag)` (damage_source.go:78) for items — add an `itemInTag(id, tagName)` over ItemTags.
- `server/ai_mob.go` — `newPigAI` (add two TemptGoal@4); the goal selector's same-priority/same-flag handling (verify).
- `server/ai_goals_passive.go` — the goal pattern (canUse/canContinueToUse/start/tick/stop, baseGoal {MOVE,LOOK}). lookControl.setLookAt analog (lookAtPlayerGoal already aims at a player — reuse the look seam).
- `server/plugin_entity.go` — the frozen-scalar handle pattern (was_hurt/in_water/damage_in_tag); add the nearest-tempt-player handles.
- `plugins/vanilla_pig/main.star` + `server/assets/vanilla_pig/main.star` — add tempt_can_use/tempt_tick/tempt_stop ×2 (or one parameterized), lockstep byte-identical.
- The attribute system (server/attribute) — TEMPT_RANGE (add with jar default if absent).

## Verification
- `CGO_ENABLED=0 go build ./...` + `go vet ./server/` (gopls STALE — trust the compiler).
- `CGO_ENABLED=0 go test ./server/ -run 'TestTempt|TestPluginPigEqualsGoNativePig|TestPluginPigBootLoads|TestPanicGoal|TestPigStrolls|TestFloatGoal' -v`.
- NEW tests: a pig FOLLOWS a player holding pig_food/carrot_on_a_stick within range (acquires a path toward the player, looks at it); a pig does NOT follow a player holding a non-tempt item or out of range. Boot-load now 7 goals {0,1,4,4,6,7,8} (TWO @4).
- LIVE bot: spawn a pig, select a carrot/pig_food slot in the bot's hotbar, hold it near the pig, assert the pig navigates toward the bot. (The bot has SULFUR_TEST_KIT items — check if a pig_food item is in the kit, else use bot_select_slot + a kit item that is in pig_food, or extend the kit.)
- Docker -race; oracle GREEN.
- javap: Pig.registerGoals (done), TemptGoal (done), Attributes.TEMPT_RANGE default.

## Boot-load assertion
The pig goes from 5 goals {0,1,6,7,8} to 7 goals {0,1,4,4,6,7,8} (TWO TemptGoals @4). Update all three goal-count assertions (TestPluginPigBootLoads, TestVanillaPigDeclaresGoalSet, TestPigGoalSetRegistered).
</code_context>

<specifics>
## Specific Ideas
- Two TemptGoal@4 (carrot_on_a_stick literal + pig_food tag), speed 1.2, canScare=false, stopDistance 2.5.
- canContinueToUse for a canScare=false mob = just canUse() (no player-move-abort branch).
- Host-computes the nearest-tempt-player scan + predicate; the .star reads frozen scalars (no item-id set in Starlark).
- New regression: follows-on-tempt + ignores-non-tempt + boot-load 7 goals.
</specifics>

<deferred>
## Deferred Ideas
- in_love / breeding / aging (Phase 33 — but the held-item read built here is the dependency).
- The canScare=true flee-on-player-move branch (no pig goal uses it; port the structure as a cited skip for canScare=false).
- canContinueToUse stuck-timeout hardening (still deferred).
</deferred>
