# Auditoría de paridad 1:1 contra Minecraft Server 26.2

**Proyecto:** Ender / Sulfur
**Fecha:** 2026-07-11
**Commit auditado originalmente:** `d41ee98398c462cd5b6b1ab986487c7a4ea2b89e`
**Último snapshot de progreso:** `42b58a21`
**Referencia vanilla:** `temp/cache/26.2-inner.jar`
**Estado:** `gaps_found` — la paridad integral 1:1 todavía no está completa

**Progress actualizado:** [████████████░░░░░░░░] **60% ±4**
Metodología y desglose: [`PARITY-PROGRESS.md`](PARITY-PROGRESS.md).

Auditoría de la ola posterior de Claude: [`PARITY-AUDIT-2026-07-12-CLAUDE-WAVE.md`](PARITY-AUDIT-2026-07-12-CLAUDE-WAVE.md).

> Recalibración 2026-07-12: los censos multiworker verificaron registro 59/59 de gamerules y ahora 15 consumidores live-store; serverbound 4 exactos / 33 parciales / 33 no-op (incluido el sentinel generado); components de disco 8/111; player 22/68, chunk 18/26 y entities/mobs 19/99 unidades de persistencia round-trip; y 10 block entities live-drive sin seam. La ola posterior cerró autosave/empty-cell/shutdown durable y dos gamerules vivas. Los informes fuente están en `.planning/parity-workflow/outputs/`.

## 1. Objetivo y alcance

Esta auditoría evalúa si la implementación Go reproduce de forma observable el servidor vanilla 26.2, de acuerdo con la regla del proyecto: portar la lógica método por método, conservando call chain, operaciones numéricas, casts, guards y orden de RNG.

Se revisaron:

- código de producción en `server/`, `world/`, `level/`, `save/` y `net/`;
- documentos y verificaciones de `.planning/`;
- inventarios generados de entidades, paquetes y componentes;
- clases del JAR mediante `javap -p`;
- comentarios `DEFERRED`, `CITED STUB`, reducciones y aproximaciones activas;
- wiring entre configuración, tick loop, persistencia, regiones y gameplay;
- estado de worktrees usados actualmente por Claude.

No se modificó código de producción durante la auditoría.

## 2. Resumen ejecutivo

El servidor ya tiene una base funcional extensa y numerosos puertos fieles, pero aún no cumple paridad 1:1 integral. Las diferencias no se limitan a partículas o presentación: existen desviaciones observables en persistencia de mobs, dificultad, gamerules, interacción entre regiones, natural spawning, AI/Brain, estructuras, protocolo y persistencia de componentes.

Los bloqueadores más importantes son:

1. Los mobs cargados desde disco pierden AI y casi todo su estado específico.
2. `/difficulty` modifica un campo que gran parte del gameplay no consulta.
3. Sólo 25 de las 59 gamerules del JAR están registradas y varios consumidores siguen usando constantes.
4. Las fronteras internas de regiones cambian interacciones, breeding, feeding y knockback.
5. El natural spawner sólo tiene pools efectivos para `CREATURE` y `MONSTER`, con cinco tipos por categoría.

## 3. Métricas de cobertura

| Superficie | JAR / registro 26.2 | Implementación observada | Resultado |
|---|---:|---:|---|
| Gamerules | 59 | 25 registradas | 34 ausentes; varios consumidores sin conectar |
| Atributos | 40 | 20 modelados | 20 ausentes |
| Paquetes serverbound de juego | 70 | 41 casos explícitos | 29 caen al `default` no-op |
| Data components de ítems | 111 | 8 con transcoder wire↔disk | 103 sin persistencia completa |
| Natural spawn pools | 8 categorías declaradas | 2 categorías efectivas | 5 criaturas + 5 monstruos |
| Marcadores explícitos de deuda | — | 1.051 ocurrencias | Incluye duplicados y comentarios históricos |

Las cifras de gamerules y atributos fueron contrastadas directamente con:

- `net.minecraft.world.level.gamerules.GameRules` mediante `javap -p`;
- `net.minecraft.world.entity.ai.attributes.Attributes` mediante `javap -p`.

## 4. Hallazgos críticos

### P0-01 — La persistencia de mobs no reconstruye el runtime vanilla

**Evidencia:** `server/entity_persist.go:70-130`, `server/entity_persist.go:202`.

`entityToDisk` guarda para un mob principalmente tipo, posición, movimiento, rotación, UUID y salud. Sin embargo, `diskToEntity` crea la entidad mediante:

```go
e := NewEntity(id, typeRec, rec.Pos[0], rec.Pos[1], rec.Pos[2])
```

La ruta no:

- restaura el UUID persistido;
- recompone la declaración `vanilla_<mob>`;
- adjunta `mobAI`, `goalSelector` ni `targetSelector`;
- restaura equipo, atributos/modificadores, efectos o metadata;
- restaura edad, `inLove`, owner, tame/sit, anger, variantes o memorias Brain;
- restaura estados específicos de raid, horse, villager, piglin, etc.

El mob reaparece como una entidad genérica con tipo y salud, pero sin su comportamiento. La prueba `TestEntityPersistRoundTrip` (`server/persistence_ext_test.go:95`) sólo verifica tipo, salud y yaw, por lo que no detecta la pérdida de AI/UUID/estado.

**Impacto observable:** después de descargar y recargar un chunk o reiniciar, mobs previamente activos pueden reaparecer inertes y con identidad/estado diferente.

**Cierre requerido:** codec por jerarquía/tipo más una factory de carga que reconstruya el mismo runtime que el spawn correspondiente sin ejecutar `finalizeSpawn` ni introducir draws nuevos.

### P0-02 — La dificultad configurada no gobierna el gameplay

**Evidencia:** `server/tick.go:190-198`, `server/food.go:96-107`.

`TickLoop.levelDifficulty` es el estado que `/difficulty` y los paquetes correspondientes leen/escriben. El propio comentario de `tick.go` reconoce que los hot paths continúan leyendo `serverDifficulty`, una constante `NORMAL`.

Consumidores todavía conectados a la constante incluyen:

- hambre y regeneración;
- variantes zombie y reinforcements;
- daño/inaccuracy de proyectiles;
- equipamiento de mobs;
- ranged goals y crossbows;
- lightning y conversiones;
- Wither, Shulker, raids y efectos;
- effective difficulty usada por spawns y ataques.

**Impacto observable:** seleccionar PEACEFUL, EASY o HARD cambia la UI/estado comunicado, pero no cambia de forma coherente el comportamiento del servidor.

**Cierre requerido:** eliminar `serverDifficulty` como fuente de runtime y pasar/leer `t.levelDifficulty` en todos los call sites, preservando el punto exacto en que vanilla consulta `ServerLevel.getDifficulty()`.

### P0-03 — Gamerules incompletas y consumidores desconectados

**Evidencia:** `server/gamerules.go:23`, `server/gamerules.go:72-100`.

El comentario afirma que el defaults table contiene el set completo, pero el JAR expone 59 `GameRule` y la tabla actual contiene 18 booleanas + 7 enteras.

Entre las reglas ausentes o no integradas se encuentran familias como:

- raids, wardens, phantoms, patrols y wandering traders;
- command blocks y command feedback;
- portal delays y entrada al Nether;
- water/lava source conversion;
- explosion drop decay;
- movement checks;
- limited crafting y locator bar;
- vine spread y otros toggles.

Además, reglas presentes en el store pueden no afectar al consumidor. Ejemplo:

- `random_tick_speed` está registrado, pero `tickRandomBlocks` usa `const randomTickSpeed = 3` (`server/random_tick.go:64,98`).

Persisten stubs semejantes para `spawn_wardens`, `spawn_mobs`, `mob_griefing`, regeneración natural y gates de fuego.

**Impacto observable:** `/gamerule` puede aceptar o mostrar valores que no alteran el gameplay correspondiente.

### P0-04 — Las fronteras de regiones son observables

**Evidencia:**

- `server/attack_dispatch.go:1058-1065`;
- `server/ai_goals_breed.go:144-160`;
- `server/attack_dispatch.go:399`;
- `.planning/REQUIREMENTS.md:28,61`.

Desviaciones activas:

- feeding cross-region se descarta;
- breeding y follow-parent sólo escanean la región propietaria;
- parte del knockback cross-region está diferida;
- varios target scans y behaviors utilizan el mismo corte local.

**Impacto observable:** dos entidades físicamente próximas pueden comportarse distinto dependiendo de qué lado de una frontera interna estén.

**Cierre requerido:** consultas snapshot/barrier y colas de intención para todas las interacciones cross-region, con resolución de IDs en el owner y aplicación en el mismo punto lógico del tick vanilla.

### P0-05 — Natural spawning es un subconjunto filtrado

**Evidencia:** `server/biome_spawners.go:35-46`, `server/biome_spawners.go:277-288`, `server/async.go:390-459`.

La tabla biome se carga, pero se filtra para conservar únicamente tipos que estén en la registry interna y en los pools explícitos.

Pools actuales:

- `CREATURE`: pig, cow, sheep, chicken, rabbit;
- `MONSTER`: zombie, skeleton, spider, creeper, enderman.

Las categorías `AMBIENT`, `AXOLOTLS`, `UNDERGROUND_WATER_CREATURE`, `WATER_CREATURE` y `WATER_AMBIENT` retornan pool vacío. Otros mobs implementados pero biome/special-spawned quedan fuera de la selección natural general.

**Impacto observable:** población, distribución, pack composition y ecosistema difieren fuertemente de vanilla.

## 5. Hallazgos mayores

### P1-01 — Cobertura de atributos: 20/40

**Evidencia:** `level/attribute/attributes.go:25-38`.

Faltan atributos con consumidores observables, entre ellos:

- `BLOCK_BREAK_SPEED` y `BLOCK_INTERACTION_RANGE`;
- `SCALE` y `FALL_DAMAGE_MULTIPLIER`;
- `FRICTION_MODIFIER` y `BOUNCINESS`;
- `LUCK` y `OXYGEN_BONUS`;
- `MOVEMENT_EFFICIENCY`, `WATER_MOVEMENT_EFFICIENCY`;
- `SUBMERGED_MINING_SPEED`, `MINING_EFFICIENCY`;
- `BURNING_TIME`, `AIR_DRAG_MODIFIER`;
- name/waypoint range attributes.

Esto obliga a varios sistemas a usar constantes o a ignorar modifiers presentes en ítems/efectos.

### P1-02 — Persistencia de componentes: 8/111

**Evidencia:** `save/item_components.go:37-68`, `save/item_components.go:336-351`.

El transcoder de disco soporta:

- damage;
- max_damage;
- repair_cost;
- custom_name;
- lore;
- potion_contents;
- enchantments;
- stored_enchantments.

El resto se contabiliza y descarta. Esto incluye componentes que afectan comportamiento, identidad, equipamiento, containers, food/consumable, bundles, fireworks, trims, books, entity data, tool rules y muchos otros.

**Impacto observable:** ítems con componentes no soportados cambian o pierden propiedades después de guardar/cargar.

### P1-03 — AI y Brain siguen reducidos

Brechas activas encontradas:

- Piglin: activity graph de admire/avoid/hunt/celebrate/ride y crossbow incompleto (`server/piglin.go`).
- Villager: sensores, WORK/MEET/PANIC/REST y múltiples behaviors reducidos (`server/brain_villager.go`).
- Goat: Brain, ram y long jump parcialmente sustituidos (`server/goat.go`).
- Sniffer: Brain, búsqueda de posición de excavación y memorias incompletas (`server/sniffer.go`).
- Allay: pickup/fetch, note-block y duplication diferidos (`server/allay.go`).
- Bee: hive/pollination/anger y navegación asociada incompletos (`server/bee.go`).
- Warden: vibration system y Brain reducidos a feeds directos (`server/warden.go`).
- Horse family: ride/jump, inventory GUI, armor/saddle y goals incompletos (`server/horse.go`).
- Water mobs: navegación/goals especializados y varios behaviors diferidos (`server/water_mobs.go`).
- Phantom: natural PhantomSpawner diferido (`server/phantom.go`).

### P1-04 — Bloques y redstone no tienen cobertura general

**Evidencia:** `server/block_survival.go:107-108`, `server/piston.go`, `server/redstone.go`.

- Neighbor survival sólo cubre vegetación; faltan torches, rails, doors y otras familias.
- Pistones aproximan varios collision shapes como full cube.
- Push reactions de entidades/bloques y moving-piston presentation están incompletos.
- Faltan sources/sinks y behaviors de redstone, incluyendo ramas de arrow buttons y hosts especiales.
- Interacciones de tools mantienen follow-ups como wax-off y shear-specific block behavior.

### P1-05 — Acciones del jugador incompletas

**Evidencia:** `server/block_interact.go:84-110`, `server/tick.go:2259-2285`.

Variantes de `ServerboundPlayerAction` como drop item y swap hand siguen cayendo a no-op. `ServerboundPlayerCommand` sólo implementa sleep, sprint y start-fall-flying; las demás actions quedan sin efecto.

### P1-06 — Estructuras/worldgen todavía aproximados

Brechas confirmadas:

- Nether Fortress omite `RoomCrossing`, `StairsRoom`, `CastleEntrance` y el conjunto de castle pieces (`world/structure/nether_fortress.go:29`).
- Ocean Monument conserva shell/layout pero difiere en interiores por room subclass (`world/structure/ocean_monument.go:695`).
- `list_pool_element` de jigsaw se reduce al primer sub-elemento (`world/structure/jigsaw_pool.go:139`).
- Jungle Temple mantiene aproximaciones y ramas de loot/redstone/dispenser incompletas.
- Beardifier no modela todos los junctions/ground-level del jigsaw.
- End features mantienen End Gateway BE y algunas entity spawns diferidas.
- Persisten providers/predicates y nested feature refs no soportados.

### P1-07 — Protocolo serverbound incompleto

**Evidencia:** `server/tick.go:2457-2459`, `data/packetid/packetid.go:229-300`.

29 de 70 paquetes de juego no tienen handler explícito:

- `ServerboundEditBook`;
- `ServerboundPaddleBoat`;
- `ServerboundPlaceRecipe`;
- recipe-book settings/seen;
- pick-item from block/entity;
- spectator action y teleport-to-entity;
- bundle item selected;
- container slot-state changed;
- custom payload/resource pack/cookie paths;
- command block, command minecart, jigsaw, structure y test-block actions;
- entity/block NBT queries y debug subscriptions.

No todos son necesarios para supervivencia básica, pero todos forman parte del comportamiento observable del servidor vanilla 26.2.

### P1-08 — Command surface parcial

La command graph cubre una selección útil (`gamemode`, `gamerule`, `give`, `tp`, `time`, `effect`, `setblock`, `summon`, `clear`, `xp`, `weather`, `difficulty`, `enchant`, `fill`, scoreboard/team/title y comandos básicos), pero no el dispatcher vanilla completo.

Faltan familias como advancement, ban/pardon, bossbar, clone, data, datapack, execute, function, item, locate, loot, particle, playsound/stopsound, random, recipe, ride, schedule, spreadplayers, tag, trigger, worldborder y otras.

### P1-09 — Advancements parciales

**Evidencia:** `level/advancement/embed.go`, `level/advancement/model.go`, `server/advancements.go`.

- Se embeben 126 avances visibles, pero no los 1.562 recipe advancements.
- Sólo se interpreta el subconjunto de criterion conditions requerido por triggers ya conectados.
- Los predicates por tags y otras condiciones permanecen diferidos.

## 6. Trabajo de Claude actualmente en curso

Estos frentes no se contabilizaron como faltantes definitivos porque existen cambios sin commit en worktrees activos:

| Worktree | Trabajo observado |
|---|---|
| `agent-a56afc0c6cabee232` | bow, projectiles, throwable, enchant effects, inventory/death interactions |
| `agent-acf49c4c9a9a42b31` | raid captain, raid tick, mob death y loot context/functions |
| `agent-adb44a53a23138576` | Breeze |
| `agent-aef67842dbf039bee` | Ender Dragon offense/flight y hurting projectile |

Hasta que esos cambios sean integrados y verificados contra el JAR, su estado es **en curso**, no **cerrado**.

## 7. Auditoría formal de v5

El milestone v5 contiene 21 requisitos marcados como completos y las 9 fases poseen `VERIFICATION.md`. Sin embargo:

- Phase 29: `human_needed`;
- Phase 30: `human_needed`;
- ocho de nueve fases no poseen `VALIDATION.md`;
- la única validación existente, Phase 29, está `draft`, `nyquist_compliant: false`, `wave_0_complete: false`;
- los propios requisitos aceptan deviations incompatibles con paridad total: same-region breeding, proxy de oscuridad y Skeleton melee-only.

Conclusión: v5 puede considerarse completo respecto de su scope reducido, pero no prueba ni implica paridad integral 1:1.

## 8. Estado de pruebas

Se inició `go test ./...` sobre el HEAD auditado.

Resultado observado antes de detener únicamente la ejecución iniciada por esta auditoría:

- paquetes completados hasta `save/region`: verdes;
- `server` y `world`: sin resultado concluyente debido a múltiples suites concurrentes ejecutadas por los worktrees de Claude;
- no se observó un `FAIL` en la salida parcial.

Esto no debe interpretarse como full suite green. Además, una suite verde sólo demuestra los contratos codificados; varias pruebas actuales aceptan explícitamente los cortes descritos arriba.

## 9. Orden recomendado para cerrar la paridad

### Ola 1 — Fuentes autoritativas

1. Reemplazar `serverDifficulty` por la dificultad viva en todos los consumidores.
2. Completar las 59 gamerules y conectar cada consumidor.
3. Añadir tests que cambien dificultad/gamerules y prueben efectos reales, no sólo almacenamiento.

### Ola 2 — Persistencia

1. Implementar codecs completos de mobs por jerarquía/tipo.
2. Reconstruir AI/Brain/declaración al cargar sin repetir `finalizeSpawn`.
3. Restaurar UUID, attributes, equipment, effects, ownership, age, variants y memories.
4. Ampliar los data components de disco de 8 a 111 o garantizar round-trip opaco sin pérdida.

### Ola 3 — Región transparente

1. Generalizar queries cross-region mediante snapshots/barrier.
2. Encolar feeding, breeding, target acquisition, knockback y cualquier mutación externa.
3. Añadir tests con entidades a ambos lados de cada frontera y comparar con el mismo escenario dentro de una región.

### Ola 4 — Ecosistema y mobs

1. Implementar todas las categorías de natural spawn y sus special spawners.
2. Completar Brain/sensors/activities antes de declarar cada mob 1:1.
3. Reemplazar classic-goal stand-ins y scans reducidos.

### Ola 5 — Mundo y bloques

1. Completar neighbor updates/survival por familia.
2. Cerrar redstone/piston/collision-shape gaps.
3. Completar structures y worldgen providers sin first-element/reduced fallbacks.

### Ola 6 — Superficie completa del servidor

1. Implementar los 29 serverbound handlers restantes.
2. Completar command dispatcher, advancements y recipe book.
3. Cerrar metadata, sounds, particles y packet emissions que sigan siendo observables.

## 10. Criterio de salida recomendado

No declarar paridad 1:1 hasta cumplir simultáneamente:

- cero `CITED STUB`/`DEFERRED` en paths observables;
- cero constantes usadas en lugar de dificultad/gamerules/atributos vivos;
- persistencia round-trip completa por entidad e ítem;
- fronteras de región no observables;
- todas las categorías y special spawners activos;
- toda feature/config del datapack cargado posee implementación o falla de manera explícita antes de iniciar;
- todos los serverbound packets vanilla relevantes tienen handler fiel;
- pruebas diferenciales contra el JAR/servidor vanilla para RNG, worldgen, AI, combate, inventario, redstone, persistencia y protocolo;
- full suite, race suite y gates de cliente real verdes.

## 11. Conclusión

El proyecto está avanzando rápidamente y Claude está cerrando brechas reales, pero el estado actual sigue siendo una implementación vanilla-faithful parcial, no un emulador indistinguible 1:1. Las prioridades inmediatas deben ser persistencia, dificultad/gamerules y transparencia cross-region; continuar agregando mobs o features sobre esas bases incompletas amplía el área que luego será necesario repasar.
