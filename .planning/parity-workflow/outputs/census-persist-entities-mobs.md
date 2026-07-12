# Census de persistencia: entidades y mobs (vanilla 26.2)

Fecha: 2026-07-12  
Snapshot auditado: `fe9073ba` (el censo es read-only; este archivo es la unica salida)  
Oracle: `temp/cache/26.2-inner.jar`, inspeccionado con `javap -p -c`.

## Resultado ejecutivo

La persistencia de entidades **no tiene paridad 1:1**. El camino basico reconstruye tipo, identidad propia, transform, salud y una parte de `LivingEntity`/`Mob`, pero pierde estado conductual importante y el disparador de guardado no equivale al autosave vanilla.

Hallazgos de mayor riesgo:

1. `flushColumn` guarda entidades solamente cuando el chunk entra al conjunto *dirty* por cambios de mundo/BEs. No hay `MarkDirty` por movimiento, dano, efectos, edad, equipo o AI de entidades: un mob puede cambiar durante horas sin que su snapshot se actualice (`server/chunk_persist.go:44-61`, `server/chunk_persist.go:123-137`; los `MarkDirty` productivos enumerados estan ligados a bloques/BEs).
2. `flushColumnNow` no tiene callers productivos, y el shutdown solo llama `flushSavedData`, no `flushColumn`/`saveEntities` (`server/chunk_persist.go:64-72`, `server/tick.go:1962-1969`). Por tanto no existe el equivalente efectivo de `MinecraftServer.saveEverything -> saveAllChunks` para entidades.
3. Una columna que queda sin entidades nunca sobrescribe la celda anterior porque `saveEntities` solo se llama con `len(ents)>0`; una entidad removida puede reaparecer tras reinicio/reload desde el snapshot viejo (`server/chunk_persist.go:130-136`).
4. Variantes modernas se escriben con clave/tipo incorrectos: Fox espera `Type`, Rabbit `RabbitType`, y Cat usa el `variant` de `VariantUtils` codificado por registro; Go guarda un `TAG_Int variant`. Sheep `Color` se guarda como byte, mientras 26.2 usa `DyeColor.CODEC`. Son incompatibilidades con vanilla aunque el codec Go pueda releer sus propios datos (`save/chunk.go:209-217`, `server/entity_persist.go:197-208`, `server/entity_persist.go:451-462`).
5. `Brain`, leash, pasajeros, owner UUID completo, target de ira, `NoAI`, `PersistenceRequired`, modifiers permanentes y casi todo estado especifico se pierde. El load crea selectores/Brain nuevos, no restaura el estado serializado vanilla (`server/entity_persist.go:359-389`).

## Cadena productiva trazada

| Etapa | Implementacion | Resultado |
|---|---|---|
| Trigger periodico | `tickChunkSave` drena solo `world.DrainDirty()` cada 100 ticks (`server/chunk_persist.go:29-61`) | No hay dirty tracking de entidades; snapshot potencialmente obsoleto. |
| Snapshot owner-side | `snapshotColumnEntities` itera el bucket y llama `entityToDisk` (`server/entity_persist.go:313-331`) | Omite players y `dead`; no representa jerarquia de pasajeros. |
| Mapeo a NBT | `entityToDisk` + `writeMobDisk` (`server/entity_persist.go:72-209`) | Base parcial, living/mob parcial, cuatro variantes intentadas. |
| Storage | `saveEntities` escribe `DataVersion`, `Entities`, `Position` a `entities/r.x.z.mca` (`server/persistence.go:326-381`, `server/persistence.go:426-447`) | Forma de celda compatible con `EntityStorage`, gzip fijo. |
| Load de celda | `loadEntities` valida `Position` y decodifica (`server/persistence.go:384-423`, `server/persistence.go:450-470`) | Una celda mal ubicada se trata como miss. |
| Activacion | `drainSavedEntities` corre al quedar Ready y esta one-shot guardado (`server/tick.go:146-150`, `server/entity_persist.go:495-535`) | Unknown type se descarta; no carga pasajeros de forma recursiva. |
| Reconstruccion | `diskToEntity` crea `NewEntity`, repone base y llama `reconstructMobRuntime` (`server/entity_persist.go:260-310`) | Nuevo runtime/ID; UUID propio se conserva. |
| AI/Brain | Busca declaration por tipo, recrea goal selectors y solo adjunta Brain para HappyGhast/Villager (`server/entity_persist.go:368-389`) | Configuracion se reconstruye; progreso/memorias/TTL/schedule persistidos se pierden. |

## Censo campo por campo

Definiciones: `round-trip` conserva valor y semantica; `write-only` produce un tag que vanilla no puede consumir como el campo pretendido; `read-only` se acepta sin equivalente de escritura; `dropped` existe en runtime/oracle pero se pierde; `defaulted` se reemplaza por cero/default/fresh state; `absent` no tiene representacion en este camino; `unknown` requiere una prueba adicional.

| Capa/tipo | Campo/tag vanilla 26.2 | Estado | Evidencia Go y efecto |
|---|---|---|---|
| Entity | `id` | `round-trip` | Se escribe nombre y se resuelve por registry (`server/entity_persist.go:79-80`, `server/entity_persist.go:260-264`). |
| Entity | `Pos` | `round-trip` | Snapshot y ctor load (`server/entity_persist.go:81`, `server/entity_persist.go:272`, `server/entity_persist.go:291`). |
| Entity | `Motion` | `round-trip` | `server/entity_persist.go:82`, `server/entity_persist.go:276`, `server/entity_persist.go:296`. |
| Entity | `Rotation` | `round-trip` | Yaw/pitch conservados (`server/entity_persist.go:83`, `server/entity_persist.go:277`, `server/entity_persist.go:297`). |
| Entity | `UUID` | `round-trip` | Conversion big-endian 4 ints en ambos sentidos (`server/entity_persist.go:84`, `server/entity_persist.go:273`, `server/entity_persist.go:292-295`, `server/entity_persist.go:334-356`). |
| Entity | `fall_distance` | `defaulted` | Campo NBT existe pero `entityToDisk` no copia `e.fallDistance`; load tampoco lo aplica (`save/chunk.go:104-107`, `server/entity.go:1620-1632`, `server/entity_persist.go:79-87`, `server/entity_persist.go:291-304`). |
| Entity | `Fire` | `defaulted` | Runtime `remainingFireTicks` existe, pero se emite cero y no se restaura (`server/entity.go:1526-1533`, `save/chunk.go:107`, `server/entity_persist.go:79-87`). |
| Entity | `Air` | `defaulted` | Se emite cero; load conserva el default nuevo 300 en vez del valor guardado (`server/entity.go:1602-1611`, `server/entity.go:2363-2367`, `server/entity_persist.go:291-304`). |
| Entity | `OnGround` | `round-trip` | `server/entity_persist.go:85`, `server/entity_persist.go:278`, `server/entity_persist.go:299`. |
| Entity | `Invulnerable` | `defaulted` | Declarado en disk, nunca llenado/aplicado (`save/chunk.go:109-112`, `server/entity_persist.go:79-87`, `server/entity_persist.go:291-304`). |
| Entity | `PortalCooldown` | `defaulted` | Se fuerza explicitamente a 0 y no se carga (`server/entity_persist.go:86`, `server/entity_persist.go:291-304`). |
| Entity | `CustomName` | `defaulted` | Existe solo en record, no en snapshot/load (`save/chunk.go:114-121`, `server/entity_persist.go:79-87`, `server/entity_persist.go:291-304`). |
| Entity | `CustomNameVisible` | `defaulted` | Igual (`save/chunk.go:114-121`). |
| Entity | `Silent` | `defaulted` | Igual (`save/chunk.go:114-121`). |
| Entity | `NoGravity` | `defaulted` | Igual (`save/chunk.go:114-121`). |
| Entity | `Glowing` | `defaulted` | Igual (`save/chunk.go:114-121`). |
| Entity | `TicksFrozen` | `defaulted` | Runtime cambia, pero snapshot no copia `e.ticksFrozen` (`server/entity.go:1535-1545`, `server/entity_persist.go:79-87`). |
| Entity | `HasVisualFire` | `defaulted` | Disk field sin productor/consumidor (`save/chunk.go:114-121`). |
| Entity | `Tags` | `defaulted` | Disk field sin productor/consumidor (`save/chunk.go:114-121`). |
| Entity | custom `data` component | `absent` | JAR lo guarda/carga en `Entity.saveWithoutId/load`; `save.Entities` no tiene campo (`save/chunk.go:98-218`). |
| Entity | `Passengers` | `absent` | JAR serializa hijos recursivamente; snapshot aplana bucket y no hay tag (`server/entity_persist.go:313-327`, `save/chunk.go:98-218`). |
| Leashable | leash data/holder | `dropped` | Runtime solo tiene thin `leashHolderID`; no se snapshottea (`server/entity.go:1918-1927`, `server/entity_persist.go:123-208`). |
| LivingEntity | `Health` | `round-trip` | Float para living; load restaura si >0 (`server/entity_persist.go:107-113`, `server/entity_persist.go:300-304`). Nota: salud 0 se reemplaza por max health, aunque `dead` evita normalmente snapshot. |
| LivingEntity | `HurtTime` | `absent` | Runtime existe, record no (`server/entity.go:1518-1524`, `save/chunk.go:149-218`). |
| LivingEntity | `DeathTime` | `absent` | Runtime existe, record no (`server/entity.go:1592-1600`, `save/chunk.go:149-218`). |
| LivingEntity | `AbsorptionAmount` | `round-trip` | `server/entity_persist.go:124`, `server/entity_persist.go:390`. |
| LivingEntity | `current_impulse_context_reset_grace_time` | `absent` | Confirmado por JAR; no campo en `save.Entities` (`save/chunk.go:149-218`). |
| LivingEntity | `current_explosion_impact_pos` | `absent` | Confirmado por JAR; sin representacion (`save/chunk.go:149-218`). |
| LivingEntity | `attributes[].id/base` | `round-trip` | Guarda solo instancias locales con base distinta y aplica `SetBaseValue` (`server/entity_persist.go:147-160`, `server/entity_persist.go:408-417`). |
| LivingEntity | modifiers permanentes de atributos | `dropped` | `AttributeInstance` los modela, pero `AttributeDisk` solo tiene `id/base` (`level/attribute/instance.go:43-45`, `save/chunk.go:220-226`). Se pierde, entre otros, random spawn bonus. |
| LivingEntity | `active_effects` campos basicos | `round-trip` | id/amplifier/duration/ambient/particles/icon se copian en ambos sentidos (`server/entity_persist.go:162-175`, `server/entity_persist.go:419-431`). |
| LivingEntity | estado anidado/extra de `MobEffectInstance` | `dropped` | Disk subset fijo de seis campos (`save/chunk.go:228-237`); cualquier payload que el codec completo preserve queda fuera. |
| LivingEntity | `FallFlying` | `absent` | JAR lo persiste; sin campo (`save/chunk.go:149-218`). |
| LivingEntity | `sleeping_pos` | `absent` | JAR lo persiste opcionalmente; sin campo (`save/chunk.go:149-218`). |
| LivingEntity | `Brain` packed data | `dropped` | No hay tag; load crea Brain fresco para solo dos tipos (`server/entity.go:1089-1094`, `server/entity_persist.go:380-387`). |
| LivingEntity | `last_hurt_by_player` | `absent` | Runtime no equivalente completo y record no lo contiene (`save/chunk.go:149-218`). |
| LivingEntity | `last_hurt_by_player_memory_time` | `absent` | Sin representacion (`save/chunk.go:149-218`). |
| LivingEntity | `last_hurt_by_mob` | `dropped` | Runtime thin ref existe, no se guarda (`server/entity.go:1569-1581`, `server/entity_persist.go:123-208`). |
| LivingEntity | `ticks_since_last_hurt_by_mob` | `dropped` | Runtime usa timestamp, no se guarda (`server/entity.go:1577-1581`, `server/entity_persist.go:123-208`). |
| LivingEntity | `equipment` id/count | `round-trip` | Slots no vacios se copian (`server/entity_persist.go:125-137`, `server/entity_persist.go:391-402`). |
| LivingEntity | `equipment` item components | `dropped` | `ItemStackDisk` soporta components, pero writer/loader de entidad solo llena id/count (`save/item_nbt.go:73-88`, `server/entity_persist.go:134-137`, `server/entity_persist.go:398-401`). |
| LivingEntity | `locator_bar_icon` | `absent` | JAR lo persiste; sin campo (`save/chunk.go:149-218`). |
| LivingEntity | `Team` legacy input | `read-only` | JAR 26.2 todavia lo lee en `readAdditionalSaveData` pero no lo escribe ahi; Go tampoco lo consume. |
| Mob | `CanPickUpLoot` | `round-trip` | `server/entity_persist.go:179`, `server/entity_persist.go:433`. |
| Mob | `PersistenceRequired` | `defaulted` | Disk field existe, writer reconoce que no hay runtime field y nunca lo asigna (`save/chunk.go:180-184`, `server/entity_persist.go:177-180`). |
| Mob | `drop_chances` de slot equipado | `round-trip` | Override no-default de un slot con item se copia/restaura (`server/entity_persist.go:125-145`, `server/entity_persist.go:403-407`). |
| Mob | `drop_chances` de slot vacio | `dropped` | La escritura esta dentro del `continue` por `Count<=0`; un override sin item no llega al record (`server/entity_persist.go:126-145`). |
| Mob | `home_radius` | `absent` | JAR lo guarda cuando hay home restriction; sin campo (`save/chunk.go:149-218`). |
| Mob | `home_pos` | `absent` | Igual (`save/chunk.go:149-218`). |
| Mob | `LeftHanded` | `round-trip` | `server/entity_persist.go:180`, `server/entity_persist.go:434`. |
| Mob | `DeathLootTable` | `absent` | JAR read/write opcional; sin campo (`save/chunk.go:149-218`). |
| Mob | `DeathLootTableSeed` | `absent` | Sin campo (`save/chunk.go:149-218`). |
| Mob | `NoAI` | `absent` | JAR lo persiste; Go siempre intenta reconstruir AI (`server/entity_persist.go:368-389`). |
| AgeableMob | `Age` | `round-trip` | TAG_Int y breeding age se conservan; baby refresca dimensiones (`server/entity_persist.go:181-184`, `server/entity_persist.go:435-442`). |
| AgeableMob | `ForcedAge` | `defaulted` | Disk field existe, pero writer/load no lo copian (`save/chunk.go:186-190`, `server/entity_persist.go:181-186`, `server/entity_persist.go:435-442`). |
| AgeableMob | `AgeLocked` | `defaulted` | Igual (`save/chunk.go:186-190`). |
| Animal | `InLove` | `round-trip` | `server/entity_persist.go:185-186`, `server/entity_persist.go:438-439`. |
| Animal | `LoveCause` | `absent` | Runtime ya documenta la reduccion y no hay disk field (`server/entity.go:1669-1677`, `save/chunk.go:149-218`). |
| TamableAnimal | `Owner` UUID/reference | `dropped` | Convierte thin entity id a ultimo int de UUID falso; tras restart los entity IDs no son identidad persistente (`server/entity_persist.go:187-192`, `server/entity_persist.go:443-447`, `server/entity.go:1790-1797`). |
| TamableAnimal | `Sitting` (`orderedToSit`) | `dropped` | Writer toma `inSittingPose`, no `orderedToSit`; load repone pose, no orden. El goal puede levantarse tras reload (`server/entity.go:1741-1752`, `server/entity_persist.go:193`, `server/entity_persist.go:448`). |
| NeutralMob | `anger_end_time` | `round-trip` | Endpoint gametime copiado (`server/entity_persist.go:194-196`, `server/entity_persist.go:449-450`). |
| NeutralMob | `angry_at` target/reference | `dropped` | Runtime `angerTarget` existe pero no se guarda (`server/entity.go:1811-1817`, `save/chunk.go:201-207`). |
| AI clasica | goal/target selector configuracion | `defaulted` | Se reconstruye desde declaration y se reseedea con el **nuevo** entity id (`server/entity_persist.go:265`, `server/entity_persist.go:368-380`), no continua RNG/progreso anterior. |
| Creeper | `powered` | `absent` | Runtime existe (`server/entity.go:466-478`), no hay disk fields especificos (`save/chunk.go:209-218`). |
| Creeper | `Fuse` (max fuse) | `absent` | Runtime `maxSwell` existe, no se guarda (`server/entity.go:469-477`). El progreso `swell` es transitorio y vanilla tampoco lo serializa. |
| Creeper | `ExplosionRadius` | `absent` | JAR lo guarda; sin campo (`save/chunk.go:209-218`). |
| Creeper | `ignited` | `absent` | Runtime existe, no se guarda (`server/entity.go:471-478`). |
| Sheep | `Sheared` | `round-trip` | `server/entity_persist.go:198-199`, `server/entity_persist.go:452-453`. |
| Sheep | `Color` (`DyeColor.CODEC`) | `write-only` | Go usa `byte`/TAG_Byte (`save/chunk.go:214`), mientras JAR usa `store("Color", DyeColor.CODEC, ...)`; vanilla no obtiene el valor esperado. |
| Cat | registry `variant` | `write-only` | Go usa int (`save/chunk.go:216`, `server/entity_persist.go:201-203`); JAR usa `VariantUtils.writeVariant/readVariant`, un valor de registry. |
| Cat | `CollarColor` | `round-trip` | Solo Cat lo copia en writer/loader (`server/entity_persist.go:201-203`, `server/entity_persist.go:455-457`). |
| Cat | `sound_variant` | `absent` | JAR read/write opcional; sin runtime/disk field. |
| Fox | `Type` | `write-only` | Go escribe `variant` int (`server/entity_persist.go:204-205`); JAR espera `Type` con `Fox.Variant.CODEC`. |
| Fox | `Trusted` | `absent` | JAR guarda lista de referencias; sin field persistente. |
| Fox | `Sleeping` | `absent` | JAR lo guarda; sin disk field. |
| Fox | `Sitting` | `absent` | JAR lo guarda; `save.Entities.Sitting` se llena genericamente desde `inSittingPose`, no desde estado Fox y Fox load no lo aplica como Fox state (`server/entity_persist.go:193`, `server/entity_persist.go:448`). |
| Fox | `Crouching` | `absent` | JAR lo guarda; sin field. |
| Rabbit | `RabbitType` | `write-only` | Go escribe `variant`; JAR espera `RabbitType` con `Rabbit.Variant.LEGACY_CODEC` (`save/chunk.go:216`, `server/entity_persist.go:206-207`). |
| Rabbit | `MoreCarrotTicks` | `absent` | JAR lo guarda; sin field. |
| Wolf | registry `variant` | `absent` | JAR usa `VariantUtils`; sin field de wolf variant. |
| Wolf | `CollarColor` | `absent` | JAR lo guarda, pero Go solo llena `CollarColor` en caso Cat (`server/entity_persist.go:200-208`). |
| Wolf | `sound_variant` | `absent` | JAR lo guarda opcionalmente; sin field. |
| Villager | `VillagerData` | `dropped` | Runtime type/profession/level existe (`server/entity.go:1095-1109`), pero no se snapshottea. |
| Villager | `VillagerDataFinalized` | `absent` | JAR lo persiste; sin field. |
| Villager | `FoodLevel` | `dropped` | Runtime existe (`server/entity.go:1153-1163`), no se guarda. |
| Villager | `Gossips` | `dropped` | Runtime existe (`server/entity.go:1143-1152`), no se guarda; cambia precios/reputacion. |
| Villager | `Xp` | `dropped` | Runtime existe (`server/entity.go:1117-1124`), no se guarda. |
| Villager | `LastRestock` | `dropped` | Runtime existe (`server/entity.go:1133-1142`), no se guarda. |
| Villager | `LastGossipDecay` | `dropped` | Runtime existe (`server/entity.go:1164-1169`), no se guarda. |
| Villager | `RestocksToday` | `dropped` | Runtime existe (`server/entity.go:1133-1142`), no se guarda. |
| Villager | `AssignProfessionWhenSpawned` | `absent` | JAR lo persiste; sin field. |
| Villager | `Brain` (JOB_SITE y memorias) | `dropped` | Job site runtime y Brain existen, pero load adjunta Brain nuevo (`server/entity.go:1100-1109`, `server/entity_persist.go:382-387`). |
| Bee | `hive_pos` | `dropped` | Runtime existe, no se snapshottea (`server/entity.go:803-815`). |
| Bee | `flower_pos` | `dropped` | Igual (`server/entity.go:803-815`). |
| Bee | `HasNectar` | `dropped` | Runtime existe (`server/entity.go:807-815`), no se guarda. |
| Bee | `HasStung` | `dropped` | Runtime existe (`server/entity.go:790-801`), no se guarda. |
| Bee | `TicksSincePollination` | `dropped` | Runtime timer existe con nombre reducido, no se guarda (`server/entity.go:816-827`). |
| Bee | `CannotEnterHiveTicks` | `dropped` | Runtime timer existe, no se guarda (`server/entity.go:816-827`). |
| Bee | `CropsGrownSincePollination` | `absent` | JAR lo persiste; no se encontro equivalente runtime/disk. |

## Totales del censo

Los totales cuentan cada fila de la tabla anterior como una unidad auditada (campos compuestos se separaron cuando su fidelidad difiere):

| Estado | Total |
|---|---:|
| `round-trip` | 19 |
| `write-only` | 4 |
| `read-only` | 1 |
| `dropped` | 25 |
| `defaulted` | 17 |
| `absent` | 33 |
| `unknown` | 0 |
| **Total** | **99** |

### Cobertura representativa

| Tipo | Cobertura | Evaluacion |
|---|---|---|
| Creeper | powered/Fuse/ExplosionRadius/ignited | 0/4 persistidos. Critico para creepers cargados/encendidos y configs no default. |
| Sheep | Sheared/Color | Sheared si; Color incompatible por tipo de tag. |
| Cat | variant/collar/sound + tame chain | Collar si; variant incompatible; sound ausente; owner/sit semanticamente perdidos. |
| Fox | Trusted/Sleeping/Type/Sitting/Crouching | 0/5 compatibles. |
| Rabbit | RabbitType/MoreCarrotTicks | 0/2 compatibles. |
| Wolf | variant/collar/sound + tame/anger | Estado visual ausente; owner, sit y angry target no sobreviven fielmente. |
| Villager | data/food/gossip/xp/restock/Brain | Perdida conductual/economica casi total; solo la capa generica sobrevive. |
| Bee | hive/flower/nectar/sting/timers/crops | 0/7 fieles; rompe retorno al hive y continuidad de polinizacion. |

## Perdida critica vs cosmetica/opcional

Critica/conductual:

- Trigger/flush/limpieza de celdas: snapshots obsoletos y resurreccion de entidades.
- Fire, Air, fall distance, freeze, HurtTime/DeathTime cuando apliquen a una entidad aun guardable.
- Modifiers permanentes (incluido random spawn bonus), Brain packed data, leash/pasajeros, owner UUID, orden de sentarse y angry target.
- `NoAI`/`PersistenceRequired`, creeper flags/config, villager economia/Brain, bee hive/pollination, fox trusted/state.
- Componentes de items equipados: encantamientos/durabilidad/nombre y otros componentes se pierden.

Principalmente cosmetica/opcional (aunque observable y por tanto necesaria para 1:1):

- CustomName/visible, Glowing, Silent, HasVisualFire, Tags, locator icon.
- Cat/Wolf sound variant, algunas variantes visuales y collar; las variantes tambien pueden afectar drops/comportamiento, por lo que no todas son puramente cosmeticas.

## Filas propuestas para `PARITY-LEDGER.csv`

No se modifico el ledger; propuestas:

| ID propuesto | Area | Severidad | Estado | Resumen | Gate sugerido |
|---|---|---:|---|---|---|
| PERSIST-ENT-TRIGGER-01 | entity persistence | P0 | missing | Entity mutations do not dirty/save entity sections; shutdown/unload path is unwired | Mutate-only mob, autosave/restart byte+behavior round-trip test |
| PERSIST-ENT-STALE-02 | entity persistence | P0 | bug | Empty entity columns do not overwrite prior sector; removed mobs resurrect | Save one mob, remove it, save empty, restart => zero mobs |
| PERSIST-ENT-BASE-03 | Entity | P0 | partial | Fire/Air/fall/frozen and generic flags/names/tags default or disappear | Field matrix oracle NBT -> Go -> NBT |
| PERSIST-LIVING-04 | LivingEntity | P0 | partial | Hurt/death, permanent attributes, full effects/equipment components, refs and Brain are lost | LivingEntity round-trip fixture with modifiers/effects/equipment/Brain |
| PERSIST-MOB-05 | Mob/Ageable/Animal | P1 | partial | NoAI/Persistence/home/death-loot/ForcedAge/AgeLocked/LoveCause missing | Hierarchy fixture against javap tags/defaults |
| PERSIST-REFS-06 | references | P0 | missing | Passengers, leash, owner and anger target lack stable UUID/reference reconstruction | Multi-entity graph save/load test preserving identity links |
| PERSIST-VARIANT-07 | type-specific codecs | P0 | bug | Sheep/Cat/Fox/Rabbit keys or codec types are incompatible with 26.2 | Load output with vanilla codecs; exact tag/type assertions |
| PERSIST-VILLAGER-08 | Villager | P0 | missing | VillagerData/economy/gossip/restock/Brain not persisted | Profession+offers+gossip+job-site restart scenario |
| PERSIST-BEE-09 | Bee | P1 | missing | Hive/pollination/sting state not persisted | Bee with hive/flower/nectar restart scenario |
| PERSIST-CREEPER-10 | Creeper | P1 | missing | powered/Fuse/ExplosionRadius/ignited not persisted | Charged/ignited/custom-fuse restart scenario |
| PERSIST-TYPES-11 | type-specific | P0 | missing | Current schema handles only a handful of 157 entity types | Generated census of every concrete add/readAdditionalSaveData pair |

## Comandos ejecutados

```powershell
Get-Content -Raw CLAUDE.md
Get-Content -Raw .planning/parity-workflow/tasks/census-persist-entities-mobs.md
rg -n --glob '*.go' 'EntitySnapshot|Snapshot|SaveEntity|LoadEntity|UUID|Health|Attributes|Equipment|Effects|Leash|Brain|Age|...' server save
rg -l --glob '*.go' 'EntityRecord|entityRecord|EntityDisk|snapshotEntity|loadEntities|reconstruct' server save
Get-Content (con numeracion) save/chunk.go server/entity.go server/entity_persist.go server/chunk_persist.go server/persistence.go server/tick.go server/saveddata.go
rg -n 'flushColumnNow|flushColumn|snapshotColumnEntities|drainSavedEntities|MarkDirty' server world
jar tf temp/cache/26.2-inner.jar | Select-String 'Wolf.class|Cat.class|Fox.class|Rabbit.class|Villager.class'
javap -classpath temp/cache/26.2-inner.jar -p -c net.minecraft.world.entity.Entity
javap -classpath temp/cache/26.2-inner.jar -p -c net.minecraft.world.entity.LivingEntity
javap -classpath temp/cache/26.2-inner.jar -p -c net.minecraft.world.entity.Mob
javap -classpath temp/cache/26.2-inner.jar -p -c net.minecraft.world.entity.AgeableMob
javap -classpath temp/cache/26.2-inner.jar -p -c net.minecraft.world.entity.animal.Animal
javap -classpath temp/cache/26.2-inner.jar -p -c net.minecraft.world.entity.TamableAnimal
javap -classpath temp/cache/26.2-inner.jar -p -c net.minecraft.world.entity.monster.Creeper
javap -classpath temp/cache/26.2-inner.jar -p -c net.minecraft.world.entity.animal.sheep.Sheep
javap -classpath temp/cache/26.2-inner.jar -p -c net.minecraft.world.entity.animal.wolf.Wolf
javap -classpath temp/cache/26.2-inner.jar -p -c net.minecraft.world.entity.animal.feline.Cat
javap -classpath temp/cache/26.2-inner.jar -p -c net.minecraft.world.entity.animal.fox.Fox
javap -classpath temp/cache/26.2-inner.jar -p -c net.minecraft.world.entity.animal.rabbit.Rabbit
javap -classpath temp/cache/26.2-inner.jar -p -c net.minecraft.world.entity.npc.villager.Villager
javap -classpath temp/cache/26.2-inner.jar -p -c net.minecraft.world.entity.animal.bee.Bee
```

## Conclusion

El fix reciente mejora de forma real la reconstruccion de mobs: ya no vuelven como cascarones `type+health`, y los campos basicos listados como `round-trip` funcionan dentro del formato propio. Sin embargo, la paridad de persistencia de entidades sigue siendo **parcial y de alto riesgo**: 19/99 unidades auditadas hacen round-trip fiel, cuatro producen tags incompatibles, y 75 se pierden/defaultan/estan ausentes (mas un input legacy `read-only`). Antes de ampliar mas tipos conviene cerrar primero trigger/flush/empty-cell y un codec jerarquico capaz de conservar referencias, componentes, attributes packed y Brain.
