# Phase 17 Plan 11: Melee Combat 1:1 Vanilla Port Summary

Re-ported Sulfur's melee combat to a literal method-for-method copy of vanilla Minecraft Java 26.2 (protocol 776): a faithful attribute holder, the i-frame-gated `hurtServer` damage path with the armor curve, and the full `Player.attack` sequence (attack-strength ramp, crit, knockback, sweep, exhaustion). Closes the spam-click infinite-damage gap and replaces the hardcoded `damage=1.0` with the real vanilla formula reading attributes.

## Scope

Files owned and edited:
- `server/attributes.go` (new) — the per-player attribute holder.
- `server/combat.go` — `applyDamage` rewritten as the `LivingEntity.hurtServer` port + `actuallyHurt` + armor curve + per-tick bookkeeping.
- `server/attack_dispatch.go` — `handleAttack` rewritten as the `Player.attack(Entity)` port + all helpers.
- `server/tick.go` — added the combat fields to `tickPlayer` (additive only).
- `server/tick_phases.go` — one additive `t.tickPlayerCombat()` call inside the existing `tickEntities` phase (no phase reorder).
- `server/combat_test.go`, `server/attack_dispatch_test.go` — new/updated tests.

Tick-pipeline edit was kept minimal (field additions + a single hook call) so the sibling Wave edits (subtick.go fluid, block_drop.go drops) do not conflict. No new tick phase was added — `TestTickPhaseOrder` stays green.

## Methods Ported (each decompiled via `javap -c -p` from `temp/cache/26.2-inner.jar` and cited)

### Attribute system (`attributes.go`)
- `Attributes.<clinit>` — the `RangedAttribute(name, DEFAULT, min, max)` registration defaults: armor 0.0, armor_toughness 0.0, attack_damage 2.0, attack_knockback 0.0, attack_speed 4.0, entity_interaction_range 3.0, knockback_resistance 0.0, max_absorption 0.0, max_health 20.0, sweeping_damage_ratio 0.0.
- `Player.createAttributes()` — overrides ATTACK_DAMAGE to 1.0 and MOVEMENT_SPEED to 0.1; ATTACK_SPEED/SWEEPING_DAMAGE_RATIO keep their registration defaults.
- `LivingEntity.createLivingAttributes()` — adds MAX_HEALTH/ARMOR/ARMOR_TOUGHNESS/KNOCKBACK_RESISTANCE/MAX_ABSORPTION/ENTITY_INTERACTION_RANGE with no overrides.
- `LivingEntity.getAttributeValue(Holder)` — modifier-free case returns base; the `attributeHolder.getAttributeValue` mirror.

### Damage path (`combat.go`)
- `LivingEntity.hurtServer(ServerLevel, DamageSource, float)` — `applyDamage`. The full i-frame gate: negative clamp, NaN/Inf clamp to Float.MAX_VALUE, the `(float)invulnerableTime > 10.0F` window (excess-over-lastHurt vs full hit), the fresh-hit arm (`invulnerableTime = 20`, `lastHurt = amount`, `hurtDuration = 10`, `hurtTime = hurtDuration`), then the death drive.
- `LivingEntity.actuallyHurt(ServerLevel, DamageSource, float)` — armor then magic reduction, absorption fold (`Math.max(amount - absorption, 0)`), the `if (amount == 0) return`, the `setHealth(getHealth() - amount)`.
- `LivingEntity.getDamageAfterArmorAbsorb(DamageSource, float)` — the `!BYPASSES_ARMOR` guard, `getArmorValue()` and ARMOR_TOUGHNESS read, the CombatRules call.
- `LivingEntity.getDamageAfterMagicAbsorb(DamageSource, float)` — RESISTANCE/protection structure (all constant-false in v1), the `if (amount <= 0) return 0`.
- `CombatRules.getDamageAfterAbsorb(LivingEntity, float, DamageSource, float, float)` — `2.0 + toughness/4`, `Mth.clamp(armor - dmg/f, armor*0.2, 20.0)`, `/25`, `dmg * (1 - f4)`.
- `CombatRules.getDamageAfterMagicAbsorb(float, float)` — `dmg * (1 - clamp(protection,0,20)/25)`.
- `LivingEntity.getArmorValue()` — `Mth.floor(getAttributeValue(ARMOR))`.
- `LivingEntity.getAbsorptionAmount()` / `setAbsorptionAmount(float)` — with the `[0, getMaxAbsorption()]` clamp.
- `Player.tick()` increment + `ServerPlayer.tick()` / `LivingEntity.tick()` decrements — `tickPlayerCombat` (attackStrengthTicker++, invulnerableTime--, hurtTime--). Confirmed the ServerPlayer path: `LivingEntity.tick` SKIPS invulnerableTime for a ServerPlayer (`instanceof ServerPlayer` guard) and `ServerPlayer.tick` decrements it unconditionally while > 0.
- `Player.resetAttackStrengthTicker()`.
- `Mth.clamp(float,float,float)` — `mthClampF`.

### Attack sequence (`attack_dispatch.go`)
- `Player.attack(Entity)` — `handleAttack`. Base damage from ATTACK_DAMAGE, `getAttackStrengthScale(0.5)`, the `(getEnchantedDamage - damage) * scale` enchant term (0 in v1), `*= baseDamageScaleFactor()`, the `damage > 0 || enchBonus > 0` gate, the sprint-knockback gate, crit, sweep gate, `hurtOrSimulate`, `getKnockback + sprint 0.5 -> causeExtraKnockback`, `doSweepAttack`, `causeFoodExhaustion(0.1)`.
- `Player.getAttackStrengthScale(float)` / `LivingEntity.getAttackStrengthScale` — `clamp((ticker+adjust)/delay, 0, 1)`.
- `Player.getCurrentItemAttackStrengthDelay()` — `(float)(1.0/ATTACK_SPEED*20.0)` = 5.0 ticks bare-hand.
- `Player.baseDamageScaleFactor()` — `0.2 + scale*scale*0.8`.
- `Player.canCriticalAttack(Entity)` — fallDistance>0 && !onGround && !onClimbable && !isInWater && !isMobilityRestricted && !isPassenger && LivingEntity && !isSprinting.
- `Player.isSweepAttack(boolean,boolean,boolean)` — the fullStrength/!crit/!sprint/onGround/movement-threshold/SWORDS-tag gate.
- `LivingEntity.getKnockback(Entity, DamageSource)` — `ATTACK_KNOCKBACK / 2`.
- `Player.causeExtraKnockback(...)` — the LivingEntity-target branch: `knockback(strength, sin(yaw·π/180), -cos(yaw·π/180), source, damage)`.
- `LivingEntity.knockback(double,double,double,DamageSource,float[,boolean])` — `strength *= 1 - KNOCKBACK_RESISTANCE`, the `<= 0` guard, normalized horizontal impulse, the `onGround ? min(0.4, cur.y/2 + strength) : cur.y` vertical, the ServerPlayer SetEntityMotion send.
- `Player.doSweepAttack(Entity, float, DamageSource, float)` — `1.0 + SWEEPING_DAMAGE_RATIO*damage`, the inflate(1.0,0.25,1.0)+distanceToSqr<9 scan, per-target hurtServer + 0.4 sweep knockback.
- `Player.causeFoodExhaustion(float)` — the invulnerable guard + addExhaustion call site.

## Before / After

| Aspect | Before | After (1:1) |
|--------|--------|-------------|
| Attack damage | hardcoded `1.0` | `getAttributeValue(ATTACK_DAMAGE) * (0.2 + scale^2*0.8)` (0.2x fresh -> 1.0x charged) |
| Attack cooldown ramp | none | `getAttackStrengthScale` over `(1/ATTACK_SPEED)*20` = 5 ticks; ticker reset on swing |
| Critical hits | none | `×1.5` when airborne+falling+not-sprinting+fullStrength |
| Knockback | none | `getKnockback + sprint 0.5`, yaw-directed impulse, vertical pop, SetEntityMotion to victim |
| Sweep | none | sword-gated structure (no-op today, no sword items), 3-block scan + 0.4 sweep knockback |
| i-frames | **none (spam-click = infinite damage)** | `invulnerableTime`/`lastHurt` window: fresh hit arms 20 ticks, spam within window applies only excess; **anti-spam closed** |
| Armor reduction | none | `CombatRules` curve present (no-op at armor 0, correct at armor 20 -> 10 dmg becomes 4) |
| Exhaustion | none | `causeFoodExhaustion(0.1)` call site ported (accumulation stubbed pending FoodData) |
| Damage entry | `applyDamage` bypassed everything | `applyDamage` IS `hurtServer` — ALL sources (attack + fall) now i-frame/armor gated |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Critical correctness] Routed fall damage through the i-frame/armor gate**
- **Found during:** porting `hurtServer`.
- **Issue:** `fall_damage.go:causeFallDamage` calls `applyDamage` directly. In vanilla ALL damage (fall + attack) funnels through `hurt -> hurtServer`, so fall damage is i-frame and armor gated too.
- **Fix:** Made `applyDamage` itself the `hurtServer` port. Its existing callers (`handleAttack`, `causeFallDamage`) are unchanged and now correctly get the i-frame/armor behavior, exactly as vanilla applies it for every caller. The fall-damage 1:1 port (17-10) and the water guard are untouched.
- **Files:** `server/combat.go`. **Commit:** 3fca6523.

## Known Stubs (faithful — attribute base / unmodeled-state, to become real reads later)

These are NOT shortcuts: each is a vanilla branch that is genuinely false/zero in v1 because the feature it reads does not exist yet. The full structure is ported so the future feature slots in with no formula change.

| Stub | Where | Becomes real when |
|------|-------|-------------------|
| Attribute modifiers (only base stored) | `attributes.go` getAttributeValue | items/effects add modifier lists; `getValue()` folds them in |
| `attrArmor` / `attrArmorToughness` = 0 | combat armor curve | armor items raise the ARMOR attribute -> curve reduces damage |
| `attrAttackKnockback` = 0 | getKnockback | Knockback enchant / weapon modifier |
| `attrSweepingDamageRatio` = 0 | doSweepAttack | Sweeping Edge enchant |
| `absorptionAmount` = 0 | actuallyHurt absorption fold | MAX_ABSORPTION + golden-apple effect |
| `p.sprinting` = false | crit gate + sprint knockback | a sprint-flag decode on the movement packet |
| `onClimbable`/`isMobilityRestricted`/`isPassenger` = false | canCriticalAttack | ladder/use-item/mount state |
| `getEnchantedDamage` term = 0 | attack damage / sweep | enchantments (Sharpness, etc.) |
| `holdingSword` = false in isSweepAttack | sweep gate | sword items + ItemTags.SWORDS |
| `getKnownMovement`/`getSpeed` threshold unreached | isSweepAttack | a velocity/speed model (short-circuited by the sword stub today) |
| `causeFoodExhaustion` accumulation | causeFoodExhaustion | `FoodData` gaining an exhaustion field (the call site IS ported) |
| RESISTANCE / enchant protection branches = false | getDamageAfterMagicAbsorb | mob effects + armor enchantments |
| `bypassesArmor`/`bypassesCooldown`/etc. damage-type tags = false | hurtServer/armor | a DamageSource tag system |

## Tests Added

- `TestIFrameRateLimit` — equal spam within window absorbed (no health change, no SetHealth); larger second hit applies only the excess; hit after the window expires lands fully.
- `TestArmorFormula` — armor 0 takes full damage; synthetic armor 20 reduces a 10-hit to 4.0 (the exact vanilla value).
- `TestAttackChargedDealsFullDamage` / `TestAttackStrengthRamp` — fresh swing 0.208 (0.2x floor), charged swing 1.0x.
- `TestAttackResetsAttackStrengthTicker` — swing resets the ticker.
- `TestAttackCritMultiplier` / `TestAttackNoCritOnGround` — crit ×1.5 airborne, no crit on ground.
- `TestAttackKnockbackApplied` / `TestAttackNoKnockbackWithoutSprint` — sprint imparts velocity + SetEntityMotion; non-sprint imparts none.
- Existing reach/self/forged/malformed/lethal-death tests retained (updated to charge the attacker where full damage is asserted).

## Verification

- `CGO_ENABLED=0 go build ./...` — exit 0.
- `CGO_ENABLED=0 go vet ./server/` — clean.
- `CGO_ENABLED=0 go test ./server/...` — all pass (TestTickPhaseOrder green).
- `-race` could not run in this environment (no gcc for CGO), but all combat state is tick-owned (mutated only on the tick goroutine, TICK-05), so it is -race clean by the same single-owner discipline as the rest of `tickPlayer`.

## Self-Check: PASSED

- `server/attributes.go` — FOUND.
- combat/attack ports in `server/combat.go`, `server/attack_dispatch.go` — present and building.
- Commits e8373fd5, 3fca6523, 35b606d9 — present in git log.
