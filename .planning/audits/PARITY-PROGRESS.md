# Progress de paridad 1:1 — Minecraft Server 26.2

**Actualizado:** 2026-07-13
**Commit auditado:** `d2308921`
**Referencia:** `temp/cache/26.2-inner.jar`
**Tipo de medición:** estimación ponderada por dominios observables
**Margen de incertidumbre:** ±4 puntos porcentuales; gamerules, serverbound y las cuatro capas principales de persistencia ya tienen censos de campo/ruta

## Barra global

```text
Paridad observable estimada
[████████████▍░░░░░░░] 62%  (rango razonable: 58–66%)
```

Este porcentaje NO significa que el 57% de las clases del JAR esté portado. Mide cuánto de la experiencia observable está implementado con suficiente profundidad para acercarse a vanilla. Penaliza subsistemas amplios que existen pero todavía usan stubs, subsets, constantes o aproximaciones.

## Score por dominio

| Dominio | Peso | Score actual | Contribución | Motivo principal |
|---|---:|---:|---:|---|
| Protocolo, login, sesión y streaming | 10% | 58% | 5,8 | 69 paquetes reales + sentinel: 4 exactos, 33 parciales, 3 no-op explícitos y 30 default-noop |
| Chunks, lighting y ciclo de mundo | 12% | 76% | 9,12 | Relight/status/scheduled ticks reales y player-ticket cap 32; el sistema de tickets amplio sigue parcial/observe-only |
| Worldgen y estructuras | 15% | 64% | 9,6 | Neighbor backpressure y precisión float de providers cerrados; aún falta oracle integral de bytes/order |
| Bloques, fluidos, física y redstone | 12% | 51% | 6,12 | Anchor acuático y física básica mejorados; neighbor updates, shapes, piston/redstone siguen parciales |
| Entidades, AI, Brain y spawning | 16% | 64% | 10,24 | Edad/selector de vibration y snapshot por categoría corregidos; AI/Brain, roster y superficie sculk siguen parciales |
| Combate, efectos, proyectiles y enchants | 10% | 73% | 7,3 | Fall-per-packet y evento `HIT_GROUND` corregidos; effects/enchants/guards siguen incompletos |
| Inventario, ítems, crafting y loot | 10% | 58% | 5,8 | Durabilidad plain y defaults max_damage reproducibles; match_tool/components y acciones siguen parciales |
| Persistencia integral | 7% | 48% | 3,36 | Spawn negativo/cero y espiral inicial 11×11 corregidos; field-level y otros formatos siguen incompletos |
| Commands, advancements y gamerules | 5% | 58% | 2,9 | `/execute run` preserva source por fork; result/store/selectores/if blocks siguen parciales |
| Región/concurrencia con semántica vanilla | 3% | 65% | 1,95 | Pool acotado pero sin equivalencia de order/backpressure; cross-region gameplay sigue divergente |
| **Total ponderado** | **100%** | — | **62,19% ≈ 62%** | Cuatro caminos observables más cerrados; field parity y subsistemas amplios parciales aún limitan el avance |

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

## Ola MiniMax M3 de remediación — 2026-07-12

- `acc6e4a9`: el worker terrain sólo marca un vecino como solicitado si el enqueue acotado fue aceptado y reintenta vecinos faltantes en cada notificación `wantedCh`; cierra el P0 de `Loading terrain` permanente bajo backpressure.
- `e0fe1bfb`: persistencia de spawn usa `math.Floor` en X/Y/Z, preservando centros negativos al round-trip de `level.dat`; el sentinel `[0,0,0]` y la búsqueda reducida siguen abiertos.
- `408f2f0a`: selector compartido de vibraciones conserva el candidato vanilla por tick (distancia y frecuencia) y promoción/decremento ocurren en el mismo tick para warden, sensor y shrieker.
- Regresiones nuevas: cuatro reglas del selector y timeline corregido del sensor. Gates centrales verdes en paralelo: `go test ./world -run 'TestWorkerPool|TestWorker'` y suite dirigida `./server` de vibration + persistencia.
- Esa ola mantuvo la barra en **60% ±4**; la ola agresiva siguiente cerró los gaps listados salvo la persistencia de búsqueda de largo alcance y el resto de `/execute`.

## Ola agresiva MiniMax M3 — 2026-07-12

- `60f59900`: `ChunkLevel` replica radio 11, `MAX_LEVEL=44` y la tabla de estados 26.2 en todos sus bordes.
- `0604e08e`: fall damage emite `HIT_GROUND` después de `fallOn` y antes de resetear distancia, con source y landing state observables por sculk.
- `ae9ec1b1`: el fan-out de natural spawning toma un único snapshot quiescente con conteos independientes para las siete categorías naturales.
- `2995407c`: los scales de noise providers conservan semántica Java `float` y widening exacto, incluido el producto por componente de dual noise.
- `8b0ff692`: `Initialized` pasa a ser autoritativo y `[0,0,0]` es un spawn persistido válido.
- `9227ff64`: cada fork de `/execute run` instala el source transformado completo y aísla siblings; para entidades genéricas enmascara el player executor heredado. Resultados enteros, `store`, selectores y `if blocks` siguen abiertos.
- `646aac05`: sweet berry usa un único RNG de nivel para loot, jitter y pitch en el orden vanilla; el seed del paquete de sonido permanece independiente.
- `c50f519c`: la búsqueda inicial de spawn recorre la espiral cuadrada 11×11 exacta (121 posiciones, radio 5, orden bytecode) y corta en el primer candidato válido.
- Gates focalizados verdes en `chunkticket`, providers, loot, `cmd/sulfur` y `server`. El gate `-race` de spawning no estuvo disponible porque el entorno no tiene `CGO_ENABLED=1`.
- La nueva barra es **62% ±4**: 61,60% ponderado, redondeado al entero más cercano.

## Auditoría multiagente de la ola Claude `eef1ee6b..42b58a21`

Informe completo: [`PARITY-AUDIT-2026-07-12-CLAUDE-WAVE.md`](PARITY-AUDIT-2026-07-12-CLAUDE-WAVE.md).

- Remediados: pérdida de neighbor requests, selector/timing de vibration, tabla ChunkLevel, spawn negativo/cero, RNG de berries, `HIT_GROUND`, snapshot por categoría y precisión float de providers.
- Parcialmente remediado: `/execute` ya conserva el source stack por fork; integer result, `store`, selectores y `if blocks` permanecen abiertos.
- Aún abiertos: orden/gating interno de block entities, resto de `/execute` y equivalencia integral del pool/worldgen.
- La barra actual es 62% ±4; este bloque conserva la auditoría histórica y enlaza sus remediaciones posteriores.
- Baseline: el timeout estándar de 10 minutos agotó `server/world`; el rerun limpio `go test ./server ./world -timeout 20m` terminó verde.

## Auditoría Claude `ce8c9cd2..6d342091` — 2026-07-13

Informe completo: [`PARITY-AUDIT-2026-07-13-CLAUDE-WAVE.md`](PARITY-AUDIT-2026-07-13-CLAUDE-WAVE.md).

- Progreso real parcial: mining speed/correct-for-drops, carga/spawn/comparator básico del respawn anchor y dos caches transparentes.
- P0 nuevo: `dirtyRelight` es un mapa global sin sincronización alcanzable desde el fan-out N=2.
- P1: relight budget procesa un dirty column por tick en orden de mapa; defaults/durability/creative del componente Tool divergen.
- P1: respawn anchor ignora el calculador acuático y el guard de glowstone en off-hand.
- El cache `SpreadContext` parece fiel, pero el budget wall-clock de fluidos sigue haciendo el timeline dependiente de la máquina.
- La barra se conserva en **62% ±4 (61,60% ponderado)** hasta cerrar el P0 y los caminos observables faltantes.

## Ola de cierre Codex/MiniMax — 2026-07-13

Informe completo: [`PARITY-CLOSURE-2026-07-13-CODEX-WAVE.md`](PARITY-CLOSURE-2026-07-13-CODEX-WAVE.md).

- `1c5d1879`: fan-in de relight race-safe; presupuesto por unión 3×3, orden estable y carry FIFO sin starvation. CR-01 y WR-01 cerrados.
- `65e5e773`: los ocho vecinos aceptados quedan en una FIFO durable del scheduler aunque `requests` esté saturado; el test reproduce la cola llena antes de `Run`.
- `c7145798`: defaults efectivos de `Tool`, removal/replacement de patch, `damage_per_block` efectivo y política creative espada/pickaxe corregidos.
- `4d8c98c5`: edad mínima del candidato de vibration aplicada a warden/sensor/shrieker, sin promoción en el mismo `gameTime`.
- `4d86ace0`: player-ticket cap 32 y bordes 32/33 corregidos.
- `fca43d8f`: 84 defaults `max_damage` generados, resolución efectiva de patch y desgaste real de stacks plain; WR-02/WR-03 quedan cerrados en comportamiento observable.
- `d2308921`: calculador acuático del respawn anchor aplica resistencia 100 en el centro; WR-04 cerrado. Off-hand sigue abierto.
- La barra sigue en **62% ±4**, ahora **62,19% ponderado**: hay avance fraccional verificable, pero no alcanza 63% ni completa un dominio amplio.

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
Vanilla 26.2 parity: [████████████▍░░░░░░░] 62% ±4
```
