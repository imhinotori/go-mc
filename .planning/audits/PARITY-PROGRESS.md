# Progress de paridad 1:1 — Minecraft Server 26.2

**Actualizado:** 2026-07-12
**Commit:** `0771ecf7`
**Referencia:** `temp/cache/26.2-inner.jar`
**Tipo de medición:** estimación ponderada por dominios observables
**Margen de incertidumbre:** ±4 puntos porcentuales; gamerules, serverbound y las cuatro capas principales de persistencia ya tienen censos de campo/ruta

## Barra global

```text
Paridad observable estimada
[████████████░░░░░░░░] 60%  (rango razonable: 56–64%)
```

Este porcentaje NO significa que el 57% de las clases del JAR esté portado. Mide cuánto de la experiencia observable está implementado con suficiente profundidad para acercarse a vanilla. Penaliza subsistemas amplios que existen pero todavía usan stubs, subsets, constantes o aproximaciones.

## Score por dominio

| Dominio | Peso | Score actual | Contribución | Motivo principal |
|---|---:|---:|---:|---|
| Protocolo, login, sesión y streaming | 10% | 58% | 5,8 | 69 paquetes reales + sentinel: 4 exactos, 33 parciales, 3 no-op explícitos y 30 default-noop |
| Chunks, lighting y ciclo de mundo | 12% | 72% | 8,64 | Relight funnel, status chain y scheduled ticks reales; quedan lifecycle/distances long-tail |
| Worldgen y estructuras | 15% | 60% | 9,0 | Noise/surface/features amplios; varias estructuras/providers conservan reducciones |
| Bloques, fluidos, física y redstone | 12% | 50% | 6,0 | Friction/speed-factor mejorados; neighbor updates, shapes, piston/redstone siguen parciales |
| Entidades, AI, Brain y spawning | 16% | 60% | 9,6 | Todas las categorías tienen cadence/pool; brains y roster efectivo siguen parciales |
| Combate, efectos, proyectiles y enchants | 10% | 72% | 7,2 | Keystone sólido y fixes recientes; todavía quedan effects/enchants/guards incompletos |
| Inventario, ítems, crafting y loot | 10% | 55% | 5,5 | Menús/recetas funcionales; componentes, loot functions y acciones mantienen gaps |
| Persistencia integral | 7% | 45% | 3,15 | Autosave de entidades, empty-cell y shutdown durable cerrados; el field-level sigue en player 22/68, chunk 18/26, entities 19/99 y components 8/111 |
| Commands, advancements y gamerules | 5% | 56% | 2,8 | Registry 59/59; 15 reglas tienen consumidor live-store, 6 siguen constantes y 38 sin consumidor |
| Región/concurrencia con semántica vanilla | 3% | 65% | 1,95 | Arquitectura/race discipline fuerte; feeding/breeding/knockback cross-region difieren |
| **Total ponderado** | **100%** | — | **59,65% ≈ 60%** | Mejora por durabilidad P0 y dos consumidores gamerule vivos; field parity aún limita el avance |

## Contadores objetivos del snapshot

```text
Gamerules registradas:            59 / 59   = 100%
Atributos modelados:              40 / 40   = 100%
Serverbound cases explícitos:     40 / 70   = 57% (incluye sentinel en el denominador generado)
Serverbound verificados exactos:   4 / 69   =  6% de paquetes reales
Componentes con codec de disco:    8 / 111  =  7%
Gamerules con consumidor live:     15 / 59   = 25%
Block entities auditadas con codec: 9; live-drive sin seam: 10; ausentes: 4
Player persistence field groups: 22 / 68 = 32% round-trip
Chunk persistence field groups:  18 / 26 = 69% round-trip
Entity/mob audited units:         19 / 99 = 19% round-trip
Natural spawn categories activas:  8 / 8    = 100% en cadence/pool wiring
Marcadores DEFERRED/CITED STUB:    1.048 ocurrencias
```

Los marcadores incluyen duplicados entre plugins/assets y comentarios históricos; sirven como señal de deuda, no como denominador exacto.

## Cambios integrados desde la auditoría inicial

- Breeze Slide locomotion.
- Multishot, Piercing y las dos curses en el effect engine.
- Raid captains, ominous banner, hero-of-the-village y ravager riders.
- Ender Dragon STRAFE DragonFireball y SITTING_FLAMING dragon breath.
- Corrección del light-mask de `ClientboundLightUpdate`.
- Ajuste del test de skylight superflat al comportamiento vanilla.
- Las 59 gamerules registradas y consumidores desconectados corregidos.
- Dificultad viva leída por los runtime sites.
- Reconstrucción del runtime AI-bearing de mobs al recargar chunks.
- Los 40 atributos vanilla y consumidores de dig/enchant conectados.
- Todas las categorías MobCategory cableadas al natural-spawn cadence.
- Scheduled ticks/header fields de chunks y BE tickers eager.

Estos avances mejoran dominios concretos, pero los censos mostraron que registro, dispatch o structs presentes no equivalen a paridad observable. Después de cerrar los P0 de durabilidad y dos consumidores gamerule, la estimación vuelve a 60% con menor incertidumbre que el 62% preliminar.

## Ola de fixes integrada

- MiniMax M3 conectó `SPREAD_VINES` al store vivo y, en reintento serial, las dos rutas silverfish de `MOB_GRIEFING`; revisión central y tests focalizados verdes.
- Agente nativo: autosave de entidades desacoplado del dirty de chunks y escritura explícita de columnas vacías para impedir resurrecciones.
- Agente nativo: snapshots de player no descartables/coalescidos, snapshot final de players/chunks/entities, drains ordenados y shutdown graceful TUI/headless con SIGINT/SIGTERM.
- Revisión multiagente encontró y cerró dos bloqueantes antes de integrar: entities con chunk saver deshabilitado y headless usando `log.Fatal`.
- Gates verdes: seis tests P0, `-race` focalizado, dos tests de `ChunkSaver`, compilación de `cmd/sulfur` y `git diff --check`.
- `go test ./...` llegó verde fuera de `server`; dos intentos de `server` tropezaron con flakes async preexistentes distintos (`TestAllAsyncSubsystemsRaceClean`, `TestBehaviorRegressionPathArrives`, `TestServerAiStepWalksToGoalTarget`). Repeticiones múltiples confirman intermitencia; no se relajaron tests.

## Resultados multiworker incorporados

- [`census-gamerules.md`](../parity-workflow/outputs/census-gamerules.md): 59/59 registradas; 14 live-store, 7 constantes y 38 sin consumidor de producción.
- [`census-serverbound.md`](../parity-workflow/outputs/census-serverbound.md): 4 exactos, 33 parciales, 3 no-op explícitos y 30 default-noop sobre las 70 entradas generadas.
- [`census-persist-components-blockentities.md`](../parity-workflow/outputs/census-persist-components-blockentities.md): 8/111 components round-trip; 9 codecs BE auditados, 10 clases live-drive sin persistencia y 4 ausentes.
- [`census-persist-player-chunk.md`](../parity-workflow/outputs/census-persist-player-chunk.md): player 22/68 y chunk 18/26 grupos round-trip; snapshots descartables y ausencia de flush final.
- [`census-persist-entities-mobs.md`](../parity-workflow/outputs/census-persist-entities-mobs.md): 19/99 unidades round-trip; dirty tracking/empty-cell P0 y estado jerárquico/type-specific incompleto.
- El censo monolítico de persistencia se invalidó por timeout remoto; sus dos mitades se completaron con agentes nativos después de que MiniMax fallara el gate de artefacto.

## Qué falta para que la barra sea objetiva

Debe existir un ledger machine-readable con una fila por unidad vanilla auditable:

| Campo | Ejemplo |
|---|---|
| Clase JAR | `net.minecraft.world.entity.animal.Pig` |
| Método | `registerGoals()` |
| Estado | `exact / partial / stub / absent / non-observable` |
| Implementación Go | `server/assets/vanilla_pig/main.star` |
| Evidencia JAR | comando `javap` o dump CFR usado |
| Test diferencial | nombre de test/oracle |
| RNG/order verificado | sí/no/n/a |
| Último commit verificado | hash |

Sin este ledger, los checkboxes de milestone pueden marcar scope reducido como completo aunque el requisito global 1:1 siga abierto.

## Reglas para mover el porcentaje

- Un archivo nuevo o un nuevo mob no aumenta automáticamente el score.
- `partial`, `proxy`, `same-region cut`, `const default` y `reduced` no cuentan como paridad completa.
- Un dominio sube sólo cuando el path observable está conectado y probado.
- Los tests deben comparar comportamiento, estado, wire, RNG/order o persistencia; no basta comprobar que una función fue llamada.
- Un fix puede bajar temporalmente el score si descubre una superficie antes ignorada. Eso hace la medición más honesta.

## Próximos hitos sugeridos

```text
60%  dificultad + 59 gamerules conectadas y tests de comportamiento
65%  persistencia completa de mobs + componentes de ítems sin pérdida común
70%  cross-region transparente + todas las categorías de natural spawn
80%  Brain/AI principales + bloques/redstone/estructuras sin reducciones mayores
90%  protocolo/commands/advancements/long-tail de mobs y componentes
100% cero desviaciones observables conocidas + oráculos diferenciales completos
```

## Barra resumida para README/reportes

```text
Vanilla 26.2 parity: [████████████░░░░░░░░] 60% ±4
```
