# Censo de paridad 26.2: persistencia de player y chunk

Fecha: 2026-07-12  
Base auditada: árbol de trabajo en `D:\ender` contra `temp/cache/26.2-inner.jar`  
Alcance: rutas de producción de snapshot/save/load de jugador y serialización/deserialización de chunk. Los payloads internos de block entities se clasifican solamente como presentes o descartados, según el contrato.

## Veredicto

La persistencia no tiene todavía paridad 1:1. El núcleo jugable más visible sí sobrevive —posición, rotación, salud, hambre, inventarios base, XP, modo, dimensión, respawn, efectos y atributos modelados; secciones, paletas, luz, heightmaps, ticks, block entities y starts de estructuras—, pero hay pérdidas deterministas y diferencias de esquema importantes.

- Player: **22/68 grupos de campo round-trip**, 46 grupos no fieles.
- Chunk: **18/26 grupos de campo round-trip**, 8 grupos no fieles.
- Total combinado: **40/94 grupos round-trip (42.6%)**. Esta cifra mide cobertura de campos de persistencia, no paridad global del servidor.
- Riesgo operativo: el único snapshot de player se ofrece en un canal no bloqueante y puede descartarse; al cancelar, el consumidor no drena snapshots pendientes. Tampoco existe un snapshot final de todos los players/chunks cargados.
- Incompatibilidad concreta: vanilla 26.2 usa `fall_distance` como `double`; Ender declara/escribe `FallDistance` como `float`, por lo que el campo vanilla está ausente y un archivo Ender contiene una clave extra que vanilla no consume.

## Rutas reales de producción

### Player: escritura

1. La salida de un cliente llega a `TickLoop.removePlayer` (`server/tick.go:2111-2150`).
2. Antes de retirar el player se construye `snapshotPlayer(p)` y se intenta enviar el valor a `leaveSnapshots` (`server/tick.go:2128-2139`). El `default` de la operación no bloqueante descarta el snapshot si el buffer está lleno.
3. `snapshotPlayer` copia el subconjunto base, inventario/ender chest y llama `snapshotPlayerExtras` (`server/persistence.go:55-97`; `server/player_persist_ext.go:44-96`).
4. `RunSaveLoop` consume el snapshot y llama `savePlayer` (`server/persistence.go:269-308`). En cancelación retorna inmediatamente (`server/persistence.go:281-289`): no drena `leaveSnapshots`.
5. `savePlayer` codifica `save.PlayerData` como NBT gzip en `world/playerdata/<uuid>.dat` mediante escritura directa (`server/persistence.go:218-240`). No se observó rotación atómica `.dat`/`.dat_old`.

No hay snapshot periódico de jugadores. El segundo productor encontrado (`server/saveddata.go:159-178`) usa la misma oferta no bloqueante. Por tanto, un proceso terminado con jugadores aún registrados, un buffer lleno o cancelación que gane la carrera al consumidor puede perder todo el progreso desde el último leave persistido.

### Player: lectura

1. En el accept/join, `loadPlayer` abre y decodifica el `.dat`; cualquier fallo cae a defaults y `ok=false` (`server/persistence.go:243-266`, defaults en `server/persistence.go:311-323`).
2. La posición se usa para el único teleport de bootstrap (`server/gameplay_tick.go:242-260`).
3. Salud, comida, rotación, inventario, slot seleccionado y ender chest se aplican antes del registro (`server/gameplay_tick.go:394-441`).
4. XP, modo, dimensión de ingreso, respawn, efectos y atributos se aplican en `applyLoadedPlayerExtras` (`server/player_persist_ext.go:239-277`).
5. Los demás miembros que el decoder llenó no se aplican a `tickPlayer`; su presencia en `save.PlayerData` por sí sola no constituye un load path.

### Chunk: escritura

1. Cada 100 ticks se drena el dirty set y se llama `flushColumn`; unload usa `flushColumnNow` (`server/chunk_persist.go:29-80`).
2. Se materializan varios block entities vivos, entidades paralelas y ticks pendientes antes de serializar (`server/chunk_persist.go:86-146`).
3. `SerializeChunkData` llama `level.ChunkToSave`, incorpora estructuras/ticks y crea el NBT mínimo (`world/chunk_save.go:24-112`; conversión en `level/chunk.go:276-376`).
4. El snapshot de bytes se encola (`server/chunk_persist.go:146-154`) y `ChunkSaver` lo escribe en `region/r.x.z.mca` (`world/chunk_save_loop.go:86-100,102-125,141-190`).
5. En cancelación se drena lo **ya encolado** (`world/chunk_save_loop.go:108-138`), pero no se toman snapshots nuevos de chunks dirty/cargados. Además, un error de serialización se considera manejado y no re-marca dirty (`server/chunk_persist.go:146-151`).

### Chunk: lectura

1. `Worker.tryRegion` prefiere `.linear` si existe y, si no, lee `.mca` (`world/worker.go:530-586`).
2. `decodeChunk` ejecuta `save.Chunk.Load` y `level.ChunkFromSave` (`world/worker.go:652-666`; decoder NBT en `save/chunk.go:55-76`).
3. `ChunkFromSave` rehidrata secciones, paletas, luz, block entities, PostProcessing, heightmaps, status, inhabited/light flags y ticks (`level/chunk.go:106-214`).
4. `decodeAndSeed` entrega `structures` a `ReadChunkStructures` (`world/worker.go:589-609`). Éste carga solamente `Starts`; decodifica pero no instala `References` (`world/structure/persistence.go:107-152`).

## Contrato vanilla comprobado por bytecode

Las fuentes autoritativas fueron:

- `Entity.saveWithoutId` / `Entity.load`: base `Pos`, `Motion`, `Rotation`, `fall_distance` (`putDouble`), `Fire`, `Air`, `OnGround`, `Invulnerable`, `PortalCooldown`, `UUID`, nombre/flags/tags/custom data y pasajeros.
- `LivingEntity.addAdditionalSaveData` / `readAdditionalSaveData`: salud, tiempos, fall flying, absorción, efectos, atributos, equipment, Brain, sleeping/combate/impulso y locator icon.
- `Player.addAdditionalSaveData` / `readAdditionalSaveData`: Inventory, SelectedItemSlot, SleepTimer, XP, Score, abilities, EnderItems y LastDeathLocation; invoca también `FoodData`.
- `FoodData.addAdditionalSaveData` / `readAdditionalSaveData`: los cuatro campos `food*`.
- `ServerPlayer.addAdditionalSaveData` / `readAdditionalSaveData`, `storeGameTypes`, `saveParentVehicle` y `saveEnderPearls`: campos server-only, dimensión, respawn, recipe book, shoulder entities, vehicle y perlas.
- `SerializableChunkData.parse`, `copyOf`, `write`, `saveTicks`, `packStructureData` y `read`: el record de 19 componentes y sus claves NBT.

## Tabla de player por campo/grupo

Estados usados: `round-trip`, `write-only`, `read-only`, `dropped`, `defaulted`, `absent`, `unknown`.

| # | Campo vanilla 26.2 | Estado | Evidencia Ender (save → load) | Evidencia JAR / observación |
|---:|---|---|---|---|
| 1 | `DataVersion` | round-trip | stamp 4903 `server/persistence.go:94-103`; miembro `save/playerdata.go:9-11`; decode `server/persistence.go:262-266` | Player storage aplica `NbtUtils.addCurrentDataVersion`; valor 26.2 = 4903. |
| 2 | `Pos` | round-trip | snapshot `server/persistence.go:64-66`; join `server/gameplay_tick.go:246-250` | `Entity.saveWithoutId` almacena `Vec3.CODEC`; `Entity.load` lee y clampa. Ender no replica los clamps, pero valores producidos normalmente vuelven. |
| 3 | `Motion` | defaulted | existe en `save/playerdata.go:12-15`, nunca se toma ni aplica | `Entity.saveWithoutId`/`load` round-trip; load vanilla limita cada componente a magnitud 10. Ender siempre vuelve con movimiento de ctor. |
| 4 | `Rotation` | round-trip | snapshot `server/persistence.go:65-66`; apply `server/gameplay_tick.go:416-417` | `Entity.saveWithoutId`/`load`, `Vec2.CODEC`. |
| 5 | `fall_distance` (double) | absent | Ender declara `FallDistance float32` sin tag (`save/playerdata.go:16`), generando la clave equivocada `FallDistance`; tampoco toma/aplica estado | `Entity.saveWithoutId` usa literalmente `fall_distance` + `putDouble`; `Entity.load` usa `getDoubleOr`. Incompatibilidad de nombre **y** tipo. |
| 6 | `Fire` | defaulted | miembro `save/playerdata.go:24-27`, snapshot cero, load ignorado | `Entity.saveWithoutId`/`load` short. |
| 7 | `Air` | defaulted | miembro `save/playerdata.go:24`, snapshot cero y load ignorado; runtime fresco usa 300 (`server/gameplay_tick.go:357-362`) | `Entity.saveWithoutId`/`load` short. Un jugador que sale ahogándose recupera aire. |
| 8 | `OnGround` | write-only | snapshot `server/persistence.go:77-79`; no apply | `Entity.saveWithoutId`/`load` boolean. |
| 9 | `Invulnerable` | defaulted | miembro `save/playerdata.go:32`; no snapshot/aplicación | `Entity.saveWithoutId`/`load`. No equivale a `abilities.invulnerable`. |
| 10 | `PortalCooldown` | defaulted | miembro `save/playerdata.go:30`; no snapshot/aplicación | `Entity.saveWithoutId`/`load` int. Permite reusar portal antes que vanilla tras reconnect. |
| 11 | `UUID` | defaulted | miembro `save/playerdata.go:20`; siempre cero/no aplicado | `Entity.saveWithoutId`/`load` usa `UUIDUtil.CODEC`. El nombre del archivo conserva identidad, pero el payload no es 1:1. |
| 12 | `CustomName` | absent | sin miembro/ruta | `Entity.saveWithoutId`/`load`, Component codec. |
| 13 | `CustomNameVisible` | absent | sin miembro/ruta | `Entity.saveWithoutId`/`load`. |
| 14 | `Silent` | absent | sin miembro/ruta | `Entity.saveWithoutId`/`load`. |
| 15 | `NoGravity` | absent | sin miembro/ruta | `Entity.saveWithoutId`/`load`. |
| 16 | `Glowing` | absent | sin miembro/ruta | `Entity.saveWithoutId`/`load`. |
| 17 | `TicksFrozen` | absent | sin miembro/ruta | `Entity.saveWithoutId`/`load`. |
| 18 | `HasVisualFire` | absent | sin miembro/ruta | `Entity.saveWithoutId`/`load`. |
| 19 | `Tags` | absent | sin miembro/ruta | `Entity.saveWithoutId`/`load`, lista limitada por vanilla. |
| 20 | `data` (CustomData) | absent | sin miembro/ruta | `Entity.saveWithoutId`/`load`. |
| 21 | `Passengers` / `RootVehicle` | absent | leave desmonta al player antes de snapshot (`server/tick.go:2120-2126`) | `Entity.saveWithoutId` persiste passengers; `ServerPlayer.saveParentVehicle` usa `RootVehicle.{Attach,Entity}` y existe load complementario. |
| 22 | `Health` | round-trip | snapshot `server/persistence.go:67`; apply `server/gameplay_tick.go:402,415` | `LivingEntity.addAdditionalSaveData`/read, float. |
| 23 | `HurtTime` | defaulted | miembro `save/playerdata.go:27`, no snapshot/aplicación | `LivingEntity` escribe/lee. |
| 24 | `DeathTime` | defaulted | miembro `save/playerdata.go:25`, no snapshot/aplicación | `LivingEntity` escribe/lee. |
| 25 | `FallFlying` | defaulted | miembro `save/playerdata.go:17`, no snapshot/aplicación | `LivingEntity` escribe/lee. |
| 26 | `AbsorptionAmount` | defaulted | miembro `save/playerdata.go:36`, no snapshot/aplicación | `LivingEntity` escribe/lee float. |
| 27 | `active_effects` | round-trip | codec disk `save/playerdata.go:55-61,98-120`; snapshot/apply `server/player_persist_ext.go:70-76,197-236,266-274` | `LivingEntity` usa `MobEffectInstance.CODEC.listOf`. Unknown IDs se conservan en mapa Go pero pueden no tener gameplay asociado. |
| 28 | `attributes` (40 IDs modelados) | round-trip | disk `save/playerdata.go:71-78,138-163`; pack/apply `server/player_persist_ext.go:88-130,172-195,276` | `LivingEntity` usa `AttributeInstance.Packed.LIST_CODEC`. IDs no conocidos se descartan en `server/player_persist_ext.go:181-185`. |
| 29 | `equipment` | absent | no campo player; armor/offhand sí viajan dentro de `Inventory` | `LivingEntity` almacena `EntityEquipment.CODEC` además de los datos de `Player`. Diferencia de forma de archivo aunque el inventario cubra los slots usuales. |
| 30 | `Brain` | absent | sin miembro/ruta | `LivingEntity` escribe/lee `Brain`. |
| 31 | `sleeping_pos` | absent | sin miembro/ruta de load | `LivingEntity.readAdditionalSaveData`; vanilla lo consume al cargar. |
| 32 | `last_hurt_by_*`, timestamps | absent | Ender escribe la clave legacy/extránea `HurtByTimestamp` (`save/playerdata.go:29`) pero no las claves 26.2 ni aplica ninguna | `LivingEntity` bytecode usa `last_hurt_by_player`, `last_hurt_by_mob`, `last_hurt_by_player_memory_time`, `ticks_since_last_hurt_by_mob`. |
| 33 | `locator_bar_icon` | absent | sin miembro/ruta | `LivingEntity` round-trip. |
| 34 | `current_impulse_context_reset_grace_time`, `current_explosion_impact_pos` | absent | sin miembro/ruta | `LivingEntity` round-trip. |
| 35 | `Inventory` id/count/slot | round-trip | save `server/persistence.go:80-83,105-135`; load `server/persistence.go:194-215`, apply `server/gameplay_tick.go:424-431` | `Player` usa lista `ItemStackWithSlot`. |
| 36 | `Inventory.components` — 8 tipos soportados | round-trip | transcode `save/item_components.go:191-233,298-350` | DataComponentPatch 26.2; soportados: damage, max_damage, repair_cost, custom_name, lore, potion_contents, enchantments, stored_enchantments. |
| 37 | `Inventory.components` — long tail restante | dropped | `wireToDiskComponents` cuenta y omite unsupported (`save/item_components.go:75-138`); caller sólo loguea (`server/persistence.go:130-133`) | Vanilla persiste el DataComponentPatch completo. Pérdida de propiedades de items. |
| 38 | `SelectedItemSlot` | round-trip | snapshot `server/persistence.go:80-82`; apply con clamp 0..8 `server/gameplay_tick.go:428-430` | `Player` int. |
| 39 | `SleepTimer` | absent | sin miembro/ruta | `Player` escribe/lee int. |
| 40 | `EnderItems` id/count/slot | round-trip | snapshot `server/persistence.go:84-90,137-156`; apply `server/gameplay_tick.go:432-437` | `PlayerEnderChestContainer` / `Player`. |
| 41 | `EnderItems.components` — 8 soportados | round-trip | mismo transcoder que Inventory | Vanilla usa el mismo ItemStack codec. |
| 42 | `EnderItems.components` — long tail | dropped | `enderItemsToDisk` ignora el contador devuelto por `SaveAllItems` (`server/persistence.go:155`) | Pérdida silenciosa, incluso sin el log del inventario principal. |
| 43 | `XpP` | round-trip | `server/player_persist_ext.go:51,244`; disk `save/playerdata.go:45-48` | `Player` float. |
| 44 | `XpLevel` | round-trip | `server/player_persist_ext.go:52,245` | `Player` int. |
| 45 | `XpTotal` | round-trip | `server/player_persist_ext.go:53,246` | `Player` int. |
| 46 | `XpSeed` | round-trip | `server/player_persist_ext.go:54,247` | `Player` int. |
| 47 | `Score` | defaulted | miembro `save/playerdata.go:35`, no snapshot/aplicación | `Player` escribe/lee. |
| 48 | `abilities` flags | defaulted | se derivan del modo, no del runtime (`server/player_persist_ext.go:22-41,61-68`); compound decodificado nunca se aplica | `Player` almacena `Abilities.Packed.CODEC`. Estado `flying` y overrides no sobreviven. |
| 49 | `abilities.flySpeed/walkSpeed` | defaulted | siempre defaults `server/player_persist_ext.go:67-68`; load ignorado | Vanilla round-trip del packed. |
| 50 | `LastDeathLocation` | absent | sin miembro/ruta | `Player` usa `GlobalPos.CODEC`; confirmado en add/read y lambda del bytecode. |
| 51 | `foodExhaustionLevel` | round-trip | snapshot `server/persistence.go:73`; apply `server/gameplay_tick.go:409` | `FoodData.addAdditionalSaveData`/read. |
| 52 | `foodLevel` | round-trip | snapshot `server/persistence.go:68`; apply `server/gameplay_tick.go:403` | `FoodData` int. |
| 53 | `foodSaturationLevel` | round-trip | snapshot `server/persistence.go:69`; apply `server/gameplay_tick.go:404` | `FoodData` float. |
| 54 | `foodTickTimer` | round-trip | snapshot `server/persistence.go:74`; apply `server/gameplay_tick.go:410` | `FoodData` int. |
| 55 | `warden_spawn_tracker` | absent | sin miembro/ruta | `ServerPlayer` codec nullable. |
| 56 | `entered_nether_pos` | absent | sin miembro/ruta | `ServerPlayer` Vec3 codec. |
| 57 | `last_explosion_impact_pos` | absent | sin miembro/ruta | `ServerPlayer` Vec3 codec. |
| 58 | `seenCredits` | defaulted | miembro `save/playerdata.go:33`, nunca snapshot/aplicado | `ServerPlayer` boolean. |
| 59 | `recipeBook.settings` | dropped | `save/playerdata.go:90-95` sólo tiene cuatro flags legacy; snapshot no los llena y load no los aplica | `ServerRecipeBook.Packed` contiene `RecipeBookSettings` de crafting/furnace/blast_furnace/smoker (open+filtering). |
| 60 | `recipeBook.known/highlight` | absent | sin modelo | `ServerRecipeBook$Packed` record: `settings`, `known`, `highlight`. |
| 61 | `respawn` | round-trip | disk `save/playerdata.go:63-69,122-136`; snapshot/apply `server/player_persist_ext.go:78-86,257-264` | `ServerPlayer$RespawnConfig.CODEC`. |
| 62 | `spawn_extra_particles_on_fall` | absent | sin miembro/ruta | `ServerPlayer` boolean. |
| 63 | `raid_omen_position` | absent | sin miembro/ruta | `ServerPlayer` GlobalPos nullable. |
| 64 | `playerGameType` | round-trip | snapshot `server/player_persist_ext.go:58`; bootstrap/apply `server/gameplay_tick.go:253-261`, `server/player_persist_ext.go:249` | `ServerPlayer.storeGameTypes`, legacy ID codec. |
| 65 | `previousPlayerGameType` | defaulted | se fuerza igual al actual `server/player_persist_ext.go:56-59`; load ignorado | Vanilla guarda y carga ambos modos. |
| 66 | `ShoulderEntityLeft/Right` | absent | sin miembro/ruta | `ServerPlayer` child compounds. |
| 67 | `Dimension` | round-trip | snapshot `server/player_persist_ext.go:49`; placement `server/player_persist_ext.go:250-255` | `ServerPlayer.addAdditionalSaveData`; el nivel se selecciona durante place-new-player. |
| 68 | `ender_pearls` + `ender_pearl_dimension` | absent | sin miembro/ruta | `ServerPlayer.saveEnderPearls` y load complementario. |

### Totales player

| Estado | Cantidad |
|---|---:|
| round-trip | 22 |
| write-only | 1 |
| read-only | 0 |
| dropped | 3 |
| defaulted | 15 |
| absent | 27 |
| unknown | 0 |
| **Total** | **68** |

## Tabla de chunk por campo/grupo

| # | Campo vanilla 26.2 | Estado | Evidencia Ender (save → load) | Evidencia JAR / observación |
|---:|---|---|---|---|
| 1 | `DataVersion` | round-trip | stamp 4903 `world/chunk_save.go:74-80,150-153`; decode disponible `save/chunk.go:14-33` | `SerializableChunkData.write` comienza con data version actual; parse se ejecuta tras datafix. |
| 2 | `xPos` | round-trip | set `world/chunk_save.go:40-43`; usado para BE coords `level/chunk.go:136-153` | `SerializableChunkData.parse`/write int. |
| 3 | `yPos` | round-trip | set desde `minY>>4` `world/chunk_save.go:30-44`; usado como base de sección `level/chunk.go:110-114` | Vanilla write incluye `yPos`; parse 26.2 usa `LevelHeightAccessor.getMinSectionY`, no la clave para construir el record. Ender es más dependiente del tag, pero su propio round-trip funciona. |
| 4 | `zPos` | round-trip | `world/chunk_save.go:40-43`; BE coords `level/chunk.go:148-153` | `SerializableChunkData.parse`/write int. |
| 5 | `LastUpdate` | absent | `save.Chunk` puede decodificarlo (`save/chunk.go:24`), pero `level.Chunk` no lo porta y `chunkSaveShape` no lo escribe | `SerializableChunkData` record `lastUpdateTime`; parse/write `LastUpdate` long. |
| 6 | `InhabitedTime` | round-trip | live `level/chunk.go:58-64`; load `level/chunk.go:207-213`; save `level/chunk.go:311-319`, `world/chunk_save.go:78-80` | `SerializableChunkData` long. Nota: el comentario declara pendiente la acumulación runtime (`level/chunk.go:61-62`), por lo que persistencia fiel no implica gameplay fiel. |
| 7 | `Status` | round-trip | save namespaced `level/chunk.go:306-309`; load `level/chunk.go:203-206` | `SerializableChunkData.getChunkStatusFromTag` / write. |
| 8 | `blending_data` | absent | sin campo/ruta productiva | `SerializableChunkData` record + parse/write codec. Afecta blending de bordes de chunks actualizados. |
| 9 | `below_zero_retrogen` | absent | sin campo/ruta | `SerializableChunkData` record + parse/write codec. |
| 10 | `UpgradeData` | absent | `save.Chunk` tiene `CarvingMasks` pero no UpgradeData real; no ruta live | `SerializableChunkData` record + parse/write `UpgradeData`. |
| 11 | `carving_mask` | absent | `save.Chunk.CarvingMasks` (`save/chunk.go:17`) no llega a `level.Chunk` ni `chunkSaveShape` | `SerializableChunkData` `long[] carvingMask`, parse/write. |
| 12 | `Heightmaps` (los 6 tipos 26.2) | round-trip | save `level/chunk.go:297-305`; load `level/chunk.go:190-201`; shape `world/chunk_save.go:118-121` | `Heightmap$Types` tiene exactamente WORLD_SURFACE_WG, WORLD_SURFACE, OCEAN_FLOOR_WG, OCEAN_FLOOR, MOTION_BLOCKING y MOTION_BLOCKING_NO_LEAVES; Serializable parse/write itera mapa. |
| 13 | `block_ticks` | round-trip | pack producción `server/chunk_persist.go:139-146`; codec `save/block_ticks.go:28-85`; load `level/chunk.go:177-188,212` | `SerializableChunkData.saveTicks` / BLOCK_TICKS_CODEC; `SavedTick` {i,x,y,z,t,p}. |
| 14 | `fluid_ticks` | round-trip | mismas rutas, `server/chunk_persist.go:143-146`; load `level/chunk.go:185-213` | FLUID_TICKS_CODEC. |
| 15 | listas de ticks vacías | absent | Ender omite la clave (`save/block_ticks.go:40-48`; `world/chunk_save.go:82-96`) | `SerializableChunkData.write` llama siempre `saveTicks`, que almacena ambos codecs; diferencia de forma NBT, aunque load default a lista vacía mantiene conducta. |
| 16 | `PostProcessing` | round-trip | encode `level/chunk.go:321-349`; shape `world/chunk_save.go:100-105`; decode `level/chunk.go:155-175` | `SerializableChunkData.packOffsets` / parse. Decode Ender silencia payload malformado y lo defaulta vacío. |
| 17 | `isLightOn` | round-trip | live/load `level/chunk.go:66-70,207-213`; save `level/chunk.go:311-319`, `world/chunk_save.go:78-80` | `SerializableChunkData.lightCorrect`. |
| 18 | `sections[].Y` | round-trip | save `level/chunk.go:278-295`; load bounds-check `level/chunk.go:108-134` | `SerializableChunkData$SectionData`; parse/write byte `Y`. |
| 19 | `sections[].block_states` palette/data | round-trip | save `level/chunk.go:280-296`; load `level/chunk.go:115-120`; structs `save/chunk.go:35-50` | `PalettedContainer.codecRW`; single-valued palette puede omitir `data` en vanilla, mientras Ender normalmente emite slice vacía: forma no necesariamente byte-idéntica, contenido rehidrata. |
| 20 | `sections[].biomes` palette/data | round-trip | save/load `level/chunk.go:283-294,128-130`; structs `save/chunk.go:35-53` | `PalettedContainer.codecRO`. Misma salvedad de representación de palette data. |
| 21 | `sections[].SkyLight` | round-trip | save/load `level/chunk.go:132,293`; disk `save/chunk.go:39` | `SerializableChunkData` parse/write byte array opcional. |
| 22 | `sections[].BlockLight` | round-trip | save/load `level/chunk.go:133,294`; disk `save/chunk.go:40` | `SerializableChunkData` parse/write byte array opcional. |
| 23 | `block_entities` (payload opaco) | round-trip | full metadata save `level/chunk.go:351-375`; decode conserva RawMessage `level/chunk.go:136-153`; shape `world/chunk_save.go:125-127` | `SerializableChunkData` lista. Según el contrato no se auditó aquí cada schema interno. IDs desconocidos no se rechazan explícitamente al mapear `block.EntityTypes[tmp.ID]`. |
| 24 | `entities` dentro del chunk | absent | `chunkSaveShape` no tiene el campo; Ender guarda entidades de full chunks en `entities/r.*.mca` (`server/chunk_persist.go:123-137`) | `SerializableChunkData` aún porta `entities` para post-load/proto paths. Para full chunks, EntityStorage paralelo reduce el impacto, pero el campo del serializer no es 1:1. |
| 25 | `structures.starts` | round-trip | write `world/structure/persistence.go:57-105`; read/seed `world/structure/persistence.go:107-152` | `SerializableChunkData.packStructureData` / `unpackStructureStart`. Ender omite starts INVALID en vez de escribir `id=INVALID` (`world/structure/persistence.go:57-61,71-74`). |
| 26 | `structures.References` | write-only | se construyen/escriben `world/structure/persistence.go:86-104`; `ReadChunkStructures` sólo itera `sd.Starts` y nunca instala `sd.References` (`world/structure/persistence.go:119-152`) | `SerializableChunkData.unpackStructureReferences` rehidrata el mapa de referencias, filtra distancia inválida y lo instala en el chunk. En Ender se pierden al recargar y dependen de recomputación posterior. |

### Totales chunk

| Estado | Cantidad |
|---|---:|
| round-trip | 18 |
| write-only | 1 |
| read-only | 0 |
| dropped | 0 |
| defaulted | 0 |
| absent | 7 |
| unknown | 0 |
| **Total** | **26** |

## Pérdidas conductuales críticas, priorizadas

1. **P0 — pérdida completa y silenciosa de player save.** `removePlayer` descarta el único snapshot cuando `leaveSnapshots` está lleno (`server/tick.go:2132-2139`). `RunSaveLoop` retorna en cancelación sin drenar (`server/persistence.go:281-289`). No hay save periódico ni snapshot final de players activos. Resultado: rollback arbitrario de sesión.
2. **P0 — shutdown no toma snapshots finales de chunks dirty.** El loop de chunk drena sólo bytes ya encolados (`world/chunk_save_loop.go:108-138`); no fuerza `DrainDirty`/snapshot de todos los chunks antes de cancelar. Un shutdown antes del siguiente intervalo de 100 ticks pierde ediciones recientes.
3. **P0 — componentes de items no soportados se destruyen.** Inventario principal sólo loguea; ender chest ignora incluso el contador (`server/persistence.go:130-155`). Esto puede borrar propiedades funcionales de cualquier componente fuera de los 8 transcoded.
4. **P1 — estado físico/combat de player se resetea.** Motion, aire, fuego, portal cooldown, fall flying/distance, absorción y hurt/death timers no vuelven. `fall_distance` además usa nombre/tipo incompatibles.
5. **P1 — referencias de estructuras no cargan.** `References` es write-only; vanilla las usa para localizar estructuras que alcanzan el chunk. La recomputación puede ocultar el fallo en seeds deterministas, pero no es el mismo load contract y puede afectar búsquedas/locate tras reload.
6. **P1 — recipe book se pierde.** El schema Go no representa known/highlight ni todos los settings 26.2 y no aplica lo decodificado.
7. **P1 — vehículos, shoulder entities y ender pearls no persisten.** Salir desmonta explícitamente al player, y los demás objetos ligados al ServerPlayer desaparecen en reconnect.
8. **P1 — chunk migration metadata ausente.** Blending, retrogen, UpgradeData y carving mask se pierden; afecta importación/continuación de chunks proto o actualizados aunque un mundo generado enteramente por Ender no los produzca hoy.
9. **P2 — write no atómico de `.dat`.** `os.WriteFile` reemplaza directamente (`server/persistence.go:239-240`), sin temp+rename ni `.dat_old`; un crash durante escritura puede convertir un player válido en defaults al próximo join.
10. **P2 — serialize error limpia dirty.** Tras `DrainDirty`, un error en `SerializeChunkData` retorna éxito lógico sin re-marcar (`server/chunk_persist.go:146-151`); el chunk no se reintenta hasta otra mutación.

## Filas de ledger propuestas

No se modificó el ledger. Filas sugeridas para incorporación por el coordinador:

| ID propuesto | Área | Estado | Prioridad | Evidencia / criterio de cierre |
|---|---|---|---|---|
| PERSIST-PLAYER-RT | Player persistence field contract | partial | P0 | Cerrar cuando los 68 grupos sean round-trip o exista justificación vanilla de ausencia; incluir golden NBT contra JAR. |
| PERSIST-PLAYER-DURABLE | Save scheduling/durability | missing | P0 | Ningún snapshot puede descartarse; save periódico + final; cancel drena; escritura temp+fsync/rename y `.dat_old` compatible. |
| PERSIST-ITEM-COMP | Player/ender item components | partial | P0 | 111/111 DataComponentPatch tipos round-trip; unsupported no puede destruir datos. |
| PERSIST-PLAYER-PHYS | Entity/Living player state | missing | P1 | Motion, fall_distance double, Air, Fire, OnGround, portal, fall flying, absorption y timers aplicados con defaults/clamps vanilla. |
| PERSIST-PLAYER-SERVER | ServerPlayer extras | missing | P1 | RecipeBook Packed completo, last death, shoulders, root vehicle, ender pearls, credits, omen/warden/nether/explosion state. |
| PERSIST-CHUNK-RT | Chunk serializer field contract | partial | P1 | 26/26 grupos fieles; LastUpdate + metadata migration; fixtures proto/full. |
| PERSIST-STRUCT-REF | Structure references load | write-only | P1 | `References` rehidratadas y validadas como `unpackStructureReferences`; test fresh cache tras reload. |
| PERSIST-SHUTDOWN | Chunk final snapshot | missing | P0 | Antes de cancelar IO se serializan todos los chunks dirty/cargados; test shutdown antes del tick 100. |
| PERSIST-RETRY | Chunk serialization failures | partial | P2 | Error conserva/re-marca dirty y tiene retry observable; nunca se acepta como save exitoso. |
| PERSIST-NBT-CANON | Canonical 26.2 NBT shape | partial | P2 | Claves, tipos, optional/empty lists y palette representation comparadas contra `SerializableChunkData.write`. |

## Comandos ejecutados

Todos fueron de lectura salvo la creación de este informe.

```powershell
Get-Content -Raw CLAUDE.md
Get-Content -Raw .planning/parity-workflow/tasks/census-persist-player-chunk.md
rg -n --glob '*.go' '<patrones de player/chunk persistence>' server save world
rg --files save server world | rg '(persist|chunk|player|save|load|storage)'
Get-Content <archivos relevantes>  # mostrado con numeración de línea en memoria
jar tf temp/cache/26.2-inner.jar | Select-String '<clases objetivo>'
javap -classpath temp/cache/26.2-inner.jar -p <clase>
javap -classpath temp/cache/26.2-inner.jar -p -c <clase>
```

Clases inspeccionadas con `javap -p -c`:

```text
net.minecraft.world.entity.Entity
net.minecraft.world.entity.LivingEntity
net.minecraft.world.entity.player.Player
net.minecraft.server.level.ServerPlayer
net.minecraft.world.food.FoodData
net.minecraft.world.entity.player.Abilities
net.minecraft.stats.ServerRecipeBook
net.minecraft.stats.ServerRecipeBook$Packed
net.minecraft.stats.RecipeBookSettings
net.minecraft.world.level.chunk.storage.SerializableChunkData
net.minecraft.world.level.levelgen.Heightmap$Types
```

Archivos Go principales inspeccionados:

```text
save/playerdata.go
save/item_nbt.go
save/item_components.go
save/chunk.go
save/block_ticks.go
save/structure.go
server/persistence.go
server/player_persist_ext.go
server/gameplay_tick.go
server/tick.go
server/saveddata.go
server/chunk_persist.go
level/chunk.go
world/chunk_save.go
world/chunk_save_loop.go
world/worker.go
world/structure/persistence.go
```

## Limitaciones del censo

- No se decompilaron internamente los schemas de cada block entity, por instrucción expresa; sólo se verificó que el RawMessage y metadata completa entren y salgan.
- La tabla usa grupos para codecs compuestos/repetitivos (por ejemplo, seis heightmaps o familias de `last_hurt_by_*`) y cuenta cada fila una vez.
- `round-trip` significa que existe un save path y un load path productivos para el grupo modelado; las notas señalan cuando el dominio aceptado es menor que vanilla (IDs unknown, componentes, forma canónica).
- No se implementó ningún fix ni se ejecutó commit.
