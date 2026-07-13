# Cierre de gaps de paridad — Codex/MiniMax — 2026-07-13

**Baseline:** `d54cf8f2`  
**HEAD verificado:** `c7145798`  
**Oracle:** `temp/cache/26.2-inner.jar`  
**Aislamiento:** tres worktrees `D:\ender-parity-worktrees\fix-*`; los worktrees de Claude no se modificaron.

## Resultado

```text
Vanilla 26.2 parity: [████████████▍░░░░░░░] 62% ±4
```

La barra no sube de entero: se cerraron fallos severos de infraestructura y dos caminos Tool, pero `Tool` sigue parcial por defaults de durabilidad, y la revisión de NoteBlock/Bat encontró gaps observables nuevos dentro de los frentes activos de Claude.

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

## Gates

```text
go test ./server -run 'Test.*Relight' -count=1                         PASS
go test ./world -run 'TestRelight' -count=1                           PASS
go test ./world -run 'TestWorkerPool|TestWorkerNeighbor' -count=1     PASS
go test ./level/component -count=1                                    PASS
go test ./server -run 'Test(Dig|StackHurt|StackTool|Tool|Creative)'   PASS
git diff --check                                                       PASS
```

`go test -race` no está disponible porque el entorno tiene `CGO_ENABLED=0`.

## Abierto / no sobrevendido

- `Tool`: falta restaurar el generador declarado por los artifacts y resolver defaults efectivos de `max_damage`/`damage`; un stack completamente plain todavía no puede demostrar desgaste físico real.
- Respawn anchor: calculador acuático y guard off-hand continúan abiertos.
- Fluid: el budget wall-clock continúa haciendo el timeline dependiente de carga/CPU.
- NoteBlock (frente Claude): falta block-event queue real, sonido seeded/RNG, instrumento por head, hand y stats.
- Bat (frente Claude): wake interno no emite metadata index 16 a trackers.
- Cache `idSetToStateSet` (frente Claude): key NUL-joined admite colisión y no deduplica IDs.

## Ejecutor MiniMax

MiniMax-M3 produjo el parche base de relight. Las rondas correctivas y las ejecuciones posteriores de worker/Tool quedaron sin primera escritura durante 3–5 minutos, incluso con `opencode run --pure`; se detuvieron para evitar consumo improductivo. La revisión central corrigió fairness/pruebas de relight e implementó los dos parches restantes desde especificaciones multiagente verificadas contra el JAR.
