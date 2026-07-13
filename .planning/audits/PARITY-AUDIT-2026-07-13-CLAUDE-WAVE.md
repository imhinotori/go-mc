---
status: findings
snapshot: 6d342091c9441232e5f2770ea8a0890c513f5a6b
range: ce8c9cd2..6d342091
files_reviewed: 17
findings:
  critical: 1
  warning: 6
  info: 2
  total: 9
---

# Auditoría de paridad — ola Claude del 13-07-2026

**Oracle:** `temp/cache/26.2-inner.jar` mediante `javap -p -c` y los reportes datagen 26.2.  
**Método:** diff completo de siete commits, revisión de call chains, contraste de bytecode/codecs y gates dirigidos.  
**Veredicto:** hay progreso real en mining speed, respawn anchor y caches transparentes, pero la ola no justifica mover la barra. El batching de relight introduce un P0 de concurrencia y las dos features de gameplay continúan parciales en caminos observables.

## Barra

```text
Vanilla 26.2 parity: [████████████▍░░░░░░░] 62% ±4
Ponderado conservado: 61,60%
```

## Hallazgos

### CR-01 — P0: `dirtyRelight` puede recibir escrituras concurrentes desde regiones

`server/light.go:59-70` crea y muta mapas Go ordinarios compartidos por todo el `TickLoop`. El fan-out ejecuta cada `region.tick` en paralelo (`server/region_coordinator.go:199-202`) y el AI sí puede escribir bloques durante ese fan-out; por ejemplo, `snowGolemAiStep` llama `SetBlock` en `server/snow_golem.go:203`. El hook global de `ChunkManager.SetBlock` entra entonces a `relightChanged` desde más de una región.

Dos regiones con cambios de propiedades lumínicas pueden producir data race, `concurrent map writes`, pérdida de dirty columns o corrupción del backlog. El comentario de ownership que afirma una única tick goroutine no coincide con el modelo N=2. `CGO_ENABLED=0` impidió ejecutar `go test -race`, pero el cruce es estáticamente directo.

**Cierre requerido:** dirty sets por región drenados en el barrier, o una cola/mutex con test N=2 que haga writes lumínicos simultáneos desde dos regiones bajo `-race`.

### WR-01 — P1: el presupuesto de relight no limita ocho recomputes y hace el timeline no determinista

`server/light.go:140-160` comprueba `len(affected) >= 8` antes de agregar el vecindario 3×3. El primer dirty column agrega nueve columnas; desde ese punto todos los demás dirty columns se difieren aunque sean adyacentes y no agreguen ningún recompute nuevo. En la práctica se procesa un dirty column por dimensión y tick, no ocho columnas recomputadas.

Además, `byCol` es un mapa: cuál dirty column gana cada tick depende del orden aleatorio de iteración. Con actividad continua, una columna puede demorarse o hambrear de forma no reproducible; clientes observan luz y paquetes varios ticks después. No existe test de `flushRelight`, presupuesto, carry, orden ni starvation.

**Cierre requerido:** cola ordenada/estable, accounting sobre nuevas columnas del union antes de aceptar un dirty entry y regresiones de cluster, columnas lejanas y carry multi-tick.

### WR-02 — P1: los defaults omitidos del componente `Tool` se materializaron con valores falsos

`level/component/tool_defaults_gen.go` asigna `DamagePerBlock: 0` y `CanDestroyBlocksInCreative: false` a las 29 herramientas cuyos JSON omiten ambos campos. El codec 26.2 de `Tool` usa `optionalFieldOf("damage_per_block", 1)` y `optionalFieldOf("can_destroy_blocks_in_creative", true)`; el bytecode muestra explícitamente `iconst_1` para ambos defaults.

El efecto ya es observable: `server/durability.go:129-160` sólo consulta un `Tool` presente en el patch del stack. Una diamond pickaxe vanilla sin patch nunca pierde el punto de durabilidad por bloque. Los tests nuevos cubren velocidad y correct-for-drops, pero no desgaste de una herramienta default.

**Cierre requerido:** generar los defaults aplicando el codec (`1/true`) y resolver `damage_per_block` a través del mismo effective component usado por mining speed.

### WR-03 — P1: creative ignora `can_destroy_blocks_in_creative`

`server/block_break.go:409-411` rompe inmediatamente cualquier bloque si el jugador está en creative, sin consultar el `Tool` efectivo. Vanilla bloquea esa acción cuando el componente tiene `canDestroyBlocksInCreative=false`; espadas, tridente y mace 26.2 llevan ese valor explícito. Sulfur permite que una espada creative destruya bloques.

El campo fue agregado a `ToolData`, pero no tiene consumidor. Tampoco hay test de la rama creative con espada versus pickaxe.

### WR-04 — P1: respawn anchor en agua usa el calculador de explosión equivocado

`server/respawn_anchor.go:215-242` elimina el anchor y llama el calculador genérico. El comentario declara que el override acuático es inerte, pero `RespawnAnchorBlock$1.getBlockExplosionResistance` devuelve la resistencia de `Blocks.WATER` justo para el BlockPos del anchor cuando `inWater=true`. Ese nodo está en el origen de los rayos de explosión y amortigua su propagación, aunque el anchor ya haya sido removido.

Resultado: la explosión rodeada de agua destruye un conjunto de bloques distinto a vanilla. El test sólo comprueba que el anchor desaparece; no compara exploded positions con/sin agua.

### WR-05 — P1: el guard de glowstone en off-hand no está implementado

`server/respawn_anchor.go:150-155` reconoce el bytecode pero colapsa la rama por ausencia de off-hand. Con un item no-fuel en main hand y glowstone en off-hand sobre un anchor cargable, vanilla retorna PASS para main hand y deja que off-hand cargue. Sulfur cae a `useWithoutItem`: en overworld puede explotar el anchor y en Nether puede cambiar el respawn en vez de cargarlo.

La feature debe permanecer `partial` hasta que el slot off-hand del inventario/packet path sea observable en esta interacción.

### WR-06 — P1: el límite wall-clock de fluidos altera la simulación según la máquina

`server/fluid.go:98-104` reduce el presupuesto de 8 ms a 6 ms y comprueba el reloj cada ocho celdas. Aunque el `SpreadContext` parece un cache transparente y fiel, el budget difiere ticks pendientes según costo de CPU, carga y resolución del reloj. Dos servidores con el mismo seed/inputs pueden propagar agua/lava en ticks distintos; no es el límite vanilla de 65536 scheduled ticks.

Este desvío ya existía conceptualmente, pero esta ola lo hizo más agresivo. Debe figurar como optimización con desviación temporal, no como byte-identical/parity closure.

### IN-01 — Optimizaciones transparentes revisadas sin divergencia encontrada

- `f18c8064`: cache MRU de dos entradas en `DataLayerStorageMap`; invalida en replace/remove y conserva el mismo puntero mutable.
- `14574331`: `[256]uint8` para la permutación de `ImprovedNoise`; los valores son 0..255 y `pp` ensancha antes del mask, por lo que el resultado es idéntico.

Estas optimizaciones no suben la barra: son no-observables cuando funcionan correctamente.

### IN-02 — El override de workers carece de bounds y tests

`6d342091` permite cualquier entero positivo en `SULFUR_GEN_WORKERS`, saltándose el cap normal de 16. Es una perilla operativa, no paridad, pero necesita tests de unset/invalid/valid y un máximo defensivo o documentación explícita del riesgo de valores extremos.

## Clasificación por commit

| Commit | Unidad | Clasificación |
|---|---|---|
| `f18c8064` | cache DataLayerStorageMap | exacto no-observable |
| `c23e7fd4` | SpreadContext + fluid budget | cache exacto aislado; timeline parcial |
| `14574331` | ImprovedNoise `[256]uint8` | exacto no-observable |
| `31f23d19` | Tool/mining | parcial; speed/tags reales, durability/creative incompletos |
| `07512bc0` | RespawnAnchorBlock | parcial; carga/spawn/comparator básicos, agua/off-hand divergentes |
| `d3997966` | batched relight | optimization-unsafe; P0 de concurrencia y scheduling no determinista |
| `6d342091` | worker env override | no-observable; hardening/test pendiente |

## Gates ejecutados

```text
go test ./server -run 'TestRespawnAnchor|Test.*Tool.*Mining|TestTool|Test.*MiningSpeed|TestBlockBreak' -count=1 -timeout=300s  PASS
go test ./level/lighting ./world/levelgen/synth -count=1 -timeout=300s                                  PASS
go test ./world -run 'TestRelightEdit|TestWorkerPoolBoundsConcurrency' -count=1 -timeout=300s             PASS
git diff --check ce8c9cd2..6d342091                                                                       PASS
go test -race                                                                                              NO DISPONIBLE (CGO_ENABLED=0)
```

Los gates verdes no contradicen los hallazgos: la suite no contiene un test del nuevo `flushRelight` ni los cuatro caminos gameplay señalados.

## Orden recomendado

1. Hacer race-safe el dirty-relight fan-in y agregar gate N=2.
2. Corregir defaults `Tool`, desgaste default y creative restriction.
3. Implementar el calculador acuático del respawn anchor.
4. Cablear off-hand en la interacción del anchor.
5. Reemplazar/justificar los budgets wall-clock con scheduling determinista.

