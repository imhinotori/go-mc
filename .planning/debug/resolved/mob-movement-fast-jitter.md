---
status: resolved
trigger: "Los mobs se mueven muy rápido y pegan saltitos entre el movimiento."
created: 2026-07-13
updated: 2026-07-13
---

# Mob movement fast/jitter

## Symptoms

- expected: Los mobs deben desplazarse a velocidad vanilla y verse interpolados de forma continua.
- actual: Los mobs recorren distancia demasiado rápido y su movimiento visual avanza mediante pequeños saltos.
- errors: No se reportan errores.
- timeline: Detectado en el servidor actual; commit exacto de introducción todavía desconocido.
- reproduction: Observar mobs caminando o persiguiendo objetivos en un cliente conectado.

## Current Focus

- hypothesis: Confirmada: Sulfur omitía el `Mob.setZza(f)` implícito de `Mob.setSpeed(f)` y usaba input forward 1, acelerando mobs terrestres.
- test: `TestStrollingMobWalkSpeedMatchesVanilla` mide desplazamiento end-to-end contra la fórmula derivada del bytecode.
- expecting: Cerdo vanilla con MOVEMENT_SPEED 0,25 converge a ~0,13767 bloques/tick, no ~0,55062.
- next_action: Ninguna; arreglo integrado y suite completa verde.
- reasoning_checkpoint: Tracker interval 3 y delta/rebase son fieles; los saltos visibles eran deltas excesivos, no una segunda emisión incorrecta.
- tdd_checkpoint:

## Evidence

- timestamp: 2026-07-13T14:00:00-04:00
  observation: `javap` de `net.minecraft.world.entity.Mob.setSpeed(float)` llama `LivingEntity.setSpeed(f)` y luego `setZza(f)`.
- timestamp: 2026-07-13T14:02:00-04:00
  observation: Sulfur llamaba `travelInAir(..., inZ=1, speed=n.speed)`, omitiendo el segundo factor `f` de `zza`.
- timestamp: 2026-07-13T14:04:00-04:00
  observation: El test previo aceptaba 0,55062 bloques/tick; la fórmula vanilla corregida y el runtime dan 0,13767.
- timestamp: 2026-07-13T14:06:00-04:00
  observation: Tracker verificado con intervalo pig=3, deltas relativos, baseline/rebase y teleport fallback; tests verdes, sin segunda divergencia.
- timestamp: 2026-07-13T14:18:00-04:00
  observation: Gate enfocado, `go vet ./server`, `git diff --check` y `go test ./server -count=1` completos pasan.

## Eliminated

- hypothesis: Movimiento aplicado dos veces por tick.
  reason: `traveledThisTick` y las regresiones de navegación/física confirman una sola integración.
- hypothesis: Tracker enviando movimientos duplicados o con cadencia incorrecta.
  reason: `ServerEntity.sendChanges` interval/rebase y tests packet-level coinciden con el JAR.
- hypothesis: Budget wall-clock de fluidos como causa principal actual.
  reason: Explica stalls históricos, pero no el exceso determinista 4× reproducido en terreno seco.

## Resolution

- root_cause: `groundNavigation.tick` usaba vector forward unitario mientras vanilla `Mob.setSpeed(f)` también fija `zza=f`; faltaba un factor `MOVEMENT_SPEED` en el input de `moveRelative`.
- fix: Pasar `inZ=float32(n.speed)` y conservar `speed=float32(n.speed)` en `travelInAir`; recalibrar la regresión absoluta a la doble escala `zza × frictionInfluencedSpeed`.
- verification: Commit `c5b4b6ec`; 0,55062→0,13767 bloques/tick; focused tests, tracker tests, vet y suite completa `server` verdes.
- files_changed: `server/navigation.go`, `server/mob_walk_speed_test.go`.
