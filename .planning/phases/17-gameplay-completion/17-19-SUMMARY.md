# Phase 17 Plan 19: FoodData Hunger / Exhaustion / Regen / Starvation Summary

**One-liner:** 1:1 port of `net.minecraft.world.food.FoodData` — exhaustion accumulates from movement/attacks/damage, drains saturation then food, health regenerates from saturation while fed, and the player starves at food 0 — all observable on a real client (hunger bar depletes, hearts regen, starvation damage at empty bar).

**Commit:** `ab4b4490` — `fix(17-19): port FoodData hunger/exhaustion/regen/starvation 1:1`

## Vanilla methods ported (all javap-verified against `temp/cache/26.2-inner.jar` this session)

| Vanilla method | Sulfur site | Notes |
|---|---|---|
| `FoodData.addExhaustion(float)` | `tickPlayer.addExhaustion` (food.go) | `exhaustionLevel = Math.min(exhaustionLevel + v, 40.0f)` |
| `FoodData.add(int,float)` | `tickPlayer.foodAdd` | `food = Mth.clamp(food+foodLevel, 0, 20)`; `sat = Mth.clamp(sat+saturationLevel, 0.0f, (float)foodLevel)` |
| `FoodData.eat(int,float)` | `tickPlayer.foodEat` | `add(food, saturationByModifier(food, mod))` — ported for next plan; NO consume handler wired |
| `FoodConstants.saturationByModifier(int,float)` | `saturationByModifier` | `nutrition * modifier * 2.0f` |
| `FoodData.needsFood()` | `tickPlayer.foodNeedsFood` | `foodLevel < 20` (provided for consume path; unused this plan) |
| `FoodData.tick(ServerPlayer)` | `TickLoop.foodDataTick` | EXACT branch order: exhaustion-drain block FIRST, then fast-regen(>=10t) / slow-regen(>=80t) / starvation(>=80t) / else if/else-if-else chain |
| `ServerPlayer.checkMovementStatistics(double,double,double)` | `TickLoop.checkMovementStatistics` | movement exhaustion ladder; `didNotMove` = exact-zero `dx==0&&dy==0&&dz==0` (javap-verified) |
| `ServerPlayer.didNotMove(double,double,double)` | inlined in `checkMovementStatistics` | `dcmpl` against `dconst_0` for each component |
| `LivingEntity.heal(float)` | `TickLoop.heal` | `if (getHealth() > 0.0F) setHealth(getHealth()+amount)`, clamped `[0, getMaxHealth()]` |
| `LivingEntity.setHealth(float)` clamp | folded into `heal` | `Mth.clamp(value, 0.0F, getMaxHealth())` |
| `Player.isHurt()` | `tickPlayer.isHurt` | `getHealth() > 0.0F && getHealth() < getMaxHealth()` (verified — defined on Player, not LivingEntity) |
| `Player.causeFoodExhaustion(float)` | `TickLoop.causeFoodExhaustion` (attack_dispatch.go) | de-stubbed: invulnerable guard + `addExhaustion(v)` (server never client-side) |
| `Player.actuallyHurt` exhaustion site | `TickLoop.actuallyHurt` tail (combat.go) | `causeFoodExhaustion(getFoodExhaustion())` after `amount==0` guard, before `setHealth` subtraction |
| `Math.round(float)` | `mathRoundF` | `floor(value + 0.5)` for the cm count |

## Movement-exhaustion ladder (jar-verified constants, food.go)

`if didNotMove return;` then: `isSwimming` -> `0.01f*cm*0.01f` (3D cm); `isEyeInFluid(WATER)` -> `0.01f*cm*0.01f` (3D); `isInWater` -> `0.01f*cm*0.01f` (HORIZONTAL x,z cm); `onClimbable` -> climb stat only, NO exhaustion; `onGround` -> sprint `0.1f*cm*0.01f`, crouch `0.0f*cm*0.01f`, walk `0.0f*cm*0.01f` (HORIZONTAL cm). NET: only sprinting on the ground and ANY in-water movement cost food; plain walk/crouch cost 0.0 (the `fconst_0` multiplier — verified). `isFallFlying`/vehicle branches omitted (elytra/vehicles out of v1 scope, cited).

## Starvation gate (jar-verified, food.go)

Damage applies (food 0, every 80t) iff `getHealth() > 10.0f` OR `diff == HARD` OR (`getHealth() > 1.0f` AND `diff == NORMAL`); else timer resets with no damage. Routes through `applyDamage` (the hurtServer port) so the starve hit obeys i-frames.

## Cited stub constants introduced (each = the vanilla default, structured for a real read later)

| Stub | Cites | Value | Why faithful |
|---|---|---|---|
| `serverDifficulty` (food.go) | `ServerLevel.getDifficulty()` | `difficultyNormal` (NORMAL) | vanilla default; NORMAL means starvation stops at 1.0 HP and the exhaustion-drain food-loss branch fires |
| `naturalHealthRegeneration` (food.go) | `GameRules.get(NATURAL_HEALTH_REGENERATION)` | `true` | gamerule vanilla default; regen branches fire |
| `isSwimming` (checkMovementStatistics) | `ServerPlayer.isSwimming()` | `false` | no swim-pose decode in v1 |
| `isCrouching` (checkMovementStatistics) | `ServerPlayer.isCrouching()` | `false` | no sneak-pose decode in v1 |
| `onClimbable` (checkMovementStatistics) | `ServerPlayer.onClimbable()` | `false` | no ladder/vine detection in v1 |
| `damageFoodExhaustion` (combat.go) | `DamageSource.getFoodExhaustion()` | `0.1f` | the DamageSource DEFAULT food exhaustion (most sources); per-source table slots in later |
| `invulnerable` (causeFoodExhaustion) | `Player.abilities.invulnerable` | `false` | creative/invuln not wired in v1 |

REAL reads used where Sulfur already has them: `isInWater` -> `t.playerInWater(p)` (fluid_physics.go), `isEyeInFluid(WATER)` -> `t.eyeInWater(p)` (breath.go), `onGround` -> `p.onGround`, `isSprinting` -> `p.sprinting` (combat.go field, false default in v1).

## Files changed

- `server/food.go` (NEW) — all FoodData helpers + `foodDataTick` + `tickFood` + `checkMovementStatistics` + `heal`/`isHurt` + `syncFood` dirty-send.
- `server/food_test.go` (NEW) — deterministic tests: exhaustion drain (sat then food), fast saturated regen every 10t, starvation every 80t, NORMAL 1HP floor, add saturation clamp, addExhaustion cap at 40, persistence + snapshot round-trip.
- `server/tick.go` — added tick-owned `exhaustion`, `foodTickTimer`, `prevX/Y/Z`, `lastFoodSent`/`lastSaturationSent`/`lastHealthSent`.
- `server/tick_phases.go` — additive `t.tickFood()` after `t.tickBreath()` (no phase reorder).
- `server/combat.go` — `damageFoodExhaustion` const + the actuallyHurt `causeFoodExhaustion` call.
- `server/attack_dispatch.go` — `causeFoodExhaustion` de-stubbed (now calls `addExhaustion`).
- `server/persistence.go` — `snapshotPlayer` writes `FoodExhaustionLevel`/`FoodTickTimer`.
- `server/gameplay_tick.go` — seed new fields at registration; round-trip exhaustion/tickTimer on load; re-seed dirty-send trackers to loaded values.

## Deviations from Plan

None beyond the plan's own allowances. The plan flagged a possible health-only dirty-send gap: the regen path (`heal`) changes health WITHOUT routing through `applyDamage`'s SetHealth, so `syncFood` folds health into its dirty check (added `lastHealthSent`) — this is the plan's documented "if `heal` doesn't push, the food packet carries it" direction, implemented as the SetHealth carrier. `save.PlayerData` already had `FoodExhaustionLevel`/`FoodTickTimer` NBT fields (correct vanilla keys `foodExhaustionLevel`/`foodTickTimer`), so no save-struct change was needed — only wiring.

## Verification

- `go build ./...` — exit 0.
- `go vet ./server/` — clean.
- `go test ./server/ -count=1` — PASS (the known `TestTickAIDrivesMobs` flake did not surface; no failures).
- `go test ./...` — PASS (every package ok / no-test-files; zero failures).
- Race detector unavailable in this environment (no gcc/cgo); the food subsystem is single-owner (tick goroutine only, TICK-05), race-clean by the same discipline as breath.go.
- Pre-port javap verification done for `ServerPlayer.didNotMove` (exact-zero compare), `Player.isHurt` (`>0 && <max`), `FoodData.tick` full branch order, `addExhaustion`/`add`/`eat`/`saturationByModifier`, `LivingEntity.heal`/`setHealth` clamp, `Player.actuallyHurt` exhaustion-site placement, and the movement multipliers.

## Self-Check: PASSED

- `server/food.go` — FOUND
- `server/food_test.go` — FOUND
- Commit `ab4b4490` — FOUND (`git log` head)
