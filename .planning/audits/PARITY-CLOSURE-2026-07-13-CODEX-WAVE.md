# Cierre de gaps de paridad — Codex/MiniMax — 2026-07-13

**Baseline:** `d54cf8f2`  
**HEAD verificado:** `d2308921`
**Oracle:** `temp/cache/26.2-inner.jar`  
**Aislamiento:** cuatro worktrees MiniMax separados bajo `D:\ender-parity-worktrees\mm-*`; los worktrees de Claude no se modificaron.

## Resultado

```text
Vanilla 26.2 parity: [████████████▍░░░░░░░] 62% ±4
```

El ponderado pasa de 61,60% a 62,19%, pero la barra no sube de entero. Esta ola cerró cuatro caminos observables adicionales (ticket cap, edad de candidato de vibración, durabilidad plain y resistencia acuática del anchor); subsistemas amplios aún parciales impiden justificar 63%.

## Integrado

### `1c5d1879` — relight CR-01 / WR-01

- Sustituye el mapa global concurrente por fan-in protegido con swap atómico.
- No sostiene el mutex durante `RelightColumns` ni envíos.
- Presupuesta la unión completa del vecindario 3×3 antes de aceptar un edit.
- Ordena dimensiones/columnas de forma estable.
- Conserva antigüedad FIFO al reinsertar carry, incluso si la columna se reedita durante el relight.

### `65e5e773` — neighbor backpressure

- El scheduler toma propiedad durable de los ocho vecinos en `pendingRequests`.
- Sólo marca `requested` cuando existe esa propiedad durable.
- Vacía la FIFO mediante un caso dinámico del `select`; `ctx.Done` y `carved` siguen activos.
- Pausa `wantedCh` mientras existe carry, acotando la FIFO a un anillo sin goroutines auxiliares.

### `c7145798` — Tool defaults y creative

- Restaura defaults codec `damage_per_block=1` y `can_destroy_blocks_in_creative=true` para 29 herramientas ordinarias.
- Conserva `2/false` para espadas, mace y trident.
- Centraliza resolución efectiva patch → removed → default.
- `stackToolDamagePerBlock` usa el componente efectivo.
- Una espada creative ya no destruye stone; una pickaxe creative sí.

### `4d8c98c5` — edad del candidato de vibración

- Un candidato registrado en el mismo `gameTime` no se promueve prematuramente.
- Warden, sensor y shrieker comparten la condición `candidate.gameTime < now`.
- La promoción al tick siguiente conserva el decremento 3→2 en ese mismo tick.

### `4d86ace0` — límite de player ticket

- Replica el cap vanilla de 32 chunks para player tickets.
- Distancia 32 queda retenida/ticketeada; distancia 33 cae al nivel default 34.
- El grafo y el valor almacenado usan constantes coherentes en sus bordes.

### `fca43d8f` — durabilidad efectiva de stacks plain

- Genera 84 defaults `minecraft:max_damage` desde los reports datagen 26.2.
- Resuelve patch agregado → removal → default registrado; `damage` ausente/removido vale cero.
- Un pickaxe/sword sin componentes explícitos ya materializa `DAMAGE` al usarse.
- El predicado compartido de yunque, equipamiento, grindstone y desgaste reconoce esos stacks vanilla.

### `d2308921` — respawn anchor bajo agua

- Threading opt-in del calculador de resistencia mantiene las explosiones genéricas sin cambios.
- Agua horizontal o sobre el anchor activa resistencia 100 sólo en el BlockPos central ya removido.
- Tests deterministas comparan el conjunto de bloques afectado y prueban el callback por posición.

## Gates

```text
go test ./server -run 'Test.*Relight' -count=1                         PASS
go test ./world -run 'TestRelight' -count=1                           PASS
go test ./world -run 'TestWorkerPool|TestWorkerNeighbor' -count=1     PASS
go test ./level/component -count=1                                    PASS
go test ./server -run 'Test(Dig|StackHurt|StackTool|Tool|Creative)'   PASS
go test ./... -count=1  # worktree Tool                                PASS
(cwd tools) go test ./... -count=1                                    PASS
go vet ./server ./level/component ./data/item                          PASS
go test ./chunkticket -count=1                                        PASS
go test ./server -run 'Test(...durability/vibration/anchor...)'        PASS
git diff --check                                                       PASS
```

`go test -race` no está disponible porque el entorno tiene `CGO_ENABLED=0`.
El gate global del worktree anchor tuvo un único flake no relacionado en `TestAllAsyncSubsystemsRaceClean` (el spawner no agregó mobs); su rerun aislado y toda la suite enfocada pasaron.

## Abierto / no sobrevendido

- `Tool`: la durabilidad observable y el generador de `max_damage` están cerrados; sigue abierto reemplazar la tabla runtime de defaults `Tool` por provenance/codegen directo del artifact.
- Respawn anchor: calculador acuático cerrado; el guard de glowstone en off-hand continúa abierto.
- Chunk tickets: el cap de player ticket está cerrado, pero el sistema completo continúa parcialmente observe-only.
- Vibration/sculk: cerrada la edad del candidato; Brain/decay y superficie sculk amplia siguen parciales.
- Fluid: el budget wall-clock continúa haciendo el timeline dependiente de carga/CPU.
- NoteBlock (frente Claude): falta block-event queue real, sonido seeded/RNG, instrumento por head, hand y stats.
- Bat (frente Claude): wake interno no emite metadata index 16 a trackers.
- Cache `idSetToStateSet` (frente Claude): key NUL-joined admite colisión y no deduplica IDs.

## Ejecutor MiniMax

MiniMax-M3 produjo el parche base de relight y, en esta segunda ola, cuatro instancias de OpenCode trabajaron simultáneamente en worktrees independientes. Para evitar el lock transitorio de la base de datos, los arranques se escalonaron aproximadamente tres segundos. Ticket y vibration se integraron tras revisión directa; Tool y anchor requirieron una segunda ejecución MiniMax y revisión central. La revisión central detectó y corrigió además el predicado duplicado de durabilidad del yunque/equipamiento antes de integrar.
