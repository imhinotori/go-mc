# Auditoría de paridad — ola Claude posterior a `eef1ee6b`

**Fecha:** 2026-07-12  
**Snapshot:** `42b58a21cf9f54ca08f20758ff6319ea6921f73e`  
**Rango:** `eef1ee6b..42b58a21`  
**Oracle:** `temp/cache/26.2-inner.jar` mediante `javap -p -c`  
**Método:** tres auditores paralelos (core/tickets/commands, gameplay y worldgen/worker), revisión central y tests dirigidos.  
**Veredicto:** progreso real pero varias unidades etiquetadas “1:1” siguen parciales; se encontró una regresión operativa de carga y múltiples divergencias P1.

## Barra global

```text
Vanilla 26.2 parity: [████████████░░░░░░░░] 60% ±4
```

La estimación no sube de forma material. Hay mejoras aisladas exactas (loot, ramas de providers, timeline/spider y macroorden del tick), pero `execute`, tickets, vibration/sculk, spawning, spawn persistence y el pool de terrain conservan o introducen diferencias observables.

## Hallazgos priorizados

### P0 — El pool de terrain puede perder vecinos para siempre

**Evidencia:** `world/worker.go:211-215`, `:229-238`, `:416-418`.

`requestNeighbors` marca un vecino como `requested=true` antes del envío no bloqueante a `requestInternal`. Con el nuevo pool, `Run` puede bloquearse en `pool.Submit`; la cola se llena, el envío del vecino se descarta, pero el bit de deduplicación queda activo. El centro wanted nunca completa su 3×3 y puede permanecer en “Loading terrain” indefinidamente.

`TestWorkerPoolBoundsConcurrency` usa buffer 1024 para 81 centros y no fuerza el drop.

**Cierre:** marcar requested sólo tras aceptar la solicitud, o usar una cola/backpressure que no descarte vecinos; añadir saturación determinista y comprobar entrega del centro.

### P1 — `/execute` pierde el `CommandSourceStack` transformado

**Evidencia:** `server/commands_execute.go:502-525`.

Al entrar a `run`, la posición, rotación, dimensión y anchor del `execSource` no se transmiten al comando final. Para una entidad no-player se conserva directamente el contexto original. En vanilla, los redirects ejecutan el root dispatcher con cada source modificado.

También:

- el tail sólo devuelve `error`, por lo que `store result` termina almacenando 1 en lugar del resultado entero del comando;
- `store` suma forks y escribe una sola vez, mientras vanilla agrega callback por source;
- `@s` no se resuelve al guardar score;
- `@r` no consume RNG y elige actor/primer player; `@p` no calcula nearest;
- entidades genéricas seleccionadas se tratan como overworld;
- `if blocks` ignora límite 32768, loaded guards y NBT/componentes de block entities.

**Clasificación:** `a6ef3511` es `partial severo`, no 1:1.

### P1 — ChunkLevel usa constantes y tabla incorrectas

**Evidencia:** `chunkticket/chunklevel.go:42-56`, `:118-127`.

Oracle ejecutado desde el JAR 26.2:

```text
RADIUS_AROUND_FULL_CHUNK = 11
MAX_LEVEL = 44
distance 0 = full
distance 1 = initialize_light
distance 2 = carvers
distance 3 = biomes
distance 4..11 = structure_starts
```

Go fija 8/41 y una tabla distinta. Los tests autopinan los valores incorrectos. Impacto live actual nulo porque el subsistema es observe-only; impacto latente alto si pasa a controlar carga.

### P1 — Tick de block entities todavía altera orden y gating

**Evidencia:** `server/tick_phases.go:171-217`.

El nuevo macroorden raid → chunk source → block events → entities → block entities mejora la fidelidad. Sin embargo, Go agrupa BE por tipo. `Level.tickBlockEntities` recorre una lista única en orden de registro, elimina tickers retirados y exige `runsNormally && shouldTickBlocksAt(pos)`. El orden entre tipos y el simulation-distance gate siguen divergentes.

### P1 — Selector de vibraciones incorrecto y entrega un tick tarde

**Evidencia:** `server/vibration_block.go:152`, `:209`, `:274-297`.

Go reemplaza candidatos incondicionalmente (`last-event-wins`). `VibrationSelector.shouldReplaceVibration` sólo reemplaza, dentro del mismo tick, por menor distancia o por mayor frecuencia si empatan.

Al promover candidato Go retorna. Vanilla continúa en el mismo tick y decrementa travel time. Todas las vibraciones llegan un tick tarde; `server/vibration_block_test.go:56-70` codifica explícitamente el off-by-one incorrecto.

### P1 — Sweet berry harvest usa otro RNG y rompe continuidad

**Evidencia:** `server/sweet_berry_bush.go:76-103`.

Vanilla ejecuta loot, jitter de `popResource` y pitch sobre el mismo `ServerLevel.getRandom()` y en ese orden. Go crea `LegacyRandomSource` desde `math/rand.Int64` y después usa el RNG global para jitter/pitch. Cambian fuente, algoritmo y stream. Los tests verifican cantidad/estado, no secuencia.

### P1 — Fall damage no emite `HIT_GROUND`

**Evidencia:** `server/fall_damage.go:260-274`, `server/game_event.go`.

`Entity.checkFallDamage` hace `Block.fallOn`, emite `GameEvent.HIT_GROUND` con supporting state y recién resetea fall distance. El evento ni siquiera está declarado en el bus Go; aterrizajes no despiertan sensor/shrieker/Warden.

### P1 — Natural spawner usa el conteo CREATURE para seis categorías

**Evidencia:** `server/spawner.go:547-553`, `server/region_coordinator.go:173-174`, `server/async.go:351-354`.

El fan-out sólo snapshottea CREATURE y MONSTER; `spawnLiveCount` devuelve CREATURE para AMBIENT, AXOLOTLS y todas las categorías acuáticas. Puede emitir scans estando la categoría real al cap o no emitirlos según un conteo ajeno. La revalidación posterior evita sobre-cap pero no recupera el único slot consumido, pudiendo hambrear categorías.

### P1 — Persistencia del spawn desplaza coordenadas negativas

**Evidencia:** `server/saveddata.go:131-134`, `cmd/sulfur/main.go:233-238`.

El spawn usa centros `.5`, pero se persiste con `int32(x/z)`. Go trunca negativos hacia cero: `-3.5 → -3`; al recargar y sumar `.5` queda `-2.5`, un bloque desplazado. Además, `[0,0,0]` se trata como sentinel inválido aunque sea un BlockPos legal, por lo que se recalcula cada boot.

El cálculo inicial también sigue reducido: radio 3/orden propio frente a la espiral vanilla de radio 5 (121 chunks).

### P1 — NoiseThresholdProvider no replica el cast float

**Evidencia:** `world/levelgen/feature/provider.go:366-380`, `:456-458`.

El JAR almacena `scale:F` y hace `f2d`; Go decodifica/conserva `float64`. Para valores como 0.005 y coordenadas/bordes suficientes, cambia el sample, la rama y potencialmente los draws posteriores. Control flow y orden RNG están bien; sampling numérico es parcial.

### P2 — El pool no tiene prueba de equivalencia ni cancelación saturada

La llegada de jobs decide el orden de `Decorate`; features vecinas escriben sobre chunks compartidos y no está probado que sean conmutativas. La prueba de reorder usa Superflat con Decorate no-op. `ants.Pool.Submit` bloqueado tampoco escucha cancelación; los tests cancelan un pool ocioso.

## Clasificación de los commits auditados

| Commit | Unidad | Clasificación |
|---|---|---|
| `09e8fa6e` | compute/persist spawn | parcial; regressions zero/negative y búsqueda reducida |
| `9eed654a` | pool terrain ants | optimization-unsafe; P0 request drop |
| `1552b22e` | noise/rotated providers | rotated exact aislado; noise partial por float |
| `e5feb1c6` | lake predicates | exact para envelopes efectivos; parser general parcial |
| `8953a284` | tick phase order | mejora parcial; macroorden correcto, BE interno divergente |
| `f3a7e9ba` | Wolf RNG draw | mejora puntual; breeding global parcial |
| `a6ef3511` | `/execute` | parcial severo |
| `9707c7ba` | sky timeline/spider | sampler/delta aislados exactos; integración general parcial |
| `f9576f19` | vibration/sculk | parcial; selector/off-by-one y Warden/decay diferidos |
| `ec1e1555` | chunk tickets | observe-only + partial; constantes falsas |
| `c2db4a98` | match_tool | parcial; subset de ItemPredicate |
| `aa46a6c8` | sweet berry | parcial por RNG |
| `5067e14f` + `42b58a21` | fall damage | mejora real; chain parcial sin HIT_GROUND |
| `42b58a21` | ExplosionCondition/ApplyExplosionDecay | exacto aislado |

## Pruebas y calidad de oráculos

Pasaron suites dirigidas de loot, providers, worker, sweet berry, vibration, fall, spider, breed, spawner y chunkticket. Sin embargo:

- tests ChunkLevel autopinan 8/41 y la tabla falsa;
- execute tests cuentan dispatches pero no source stack/result real;
- vibration test espera explícitamente el tick tardío;
- provider threshold afirma tres ramas pero sólo samplea una;
- rotated usa la misma tabla productiva como oracle;
- smoke omite bodies no registrados y no compara outputs/RNG;
- pool no satura requestInternal ni decora features reales.

`go test ./...` con timeout estándar terminó por timeout en `server` y `world`, sin assertion previa: la suite ya supera 10 minutos bajo carga concurrente. El rerun limpio `go test ./server ./world -timeout 20m` terminó **verde (exit 0)**. Los demás paquetes del full run habían terminado verdes o cached. El gate CI debe aumentar su timeout o dividir paquetes pesados.

## Efecto sobre la barra

Se mantiene **60% ±4**. Los cambios agregan superficie y cierran ramas reales, pero no justifican subir el score mientras las rutas principales nuevas sigan parciales y exista un P0 de carga.

## Orden de corrección recomendado

1. Worker: entrega garantizada de neighbor requests + saturación/cancel tests.
2. `/execute`: preservar source stack/result por fork; selectores y store vanilla.
3. VibrationSelector + decremento en el tick de promoción; corregir el oracle.
4. Persistencia spawn negativa/cero y espiral vanilla.
5. Conteos por las ocho MobCategory en fan-out.
6. `HIT_GROUND` y RNG único de sweet berry.
7. ChunkLevel 11/44 antes de conectar el subsistema live.
8. Orden/lista/gating real de block entities.
