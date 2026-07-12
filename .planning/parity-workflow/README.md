# Flujo multiagente para cerrar paridad 1:1

Este directorio prepara un flujo híbrido para trabajar contra el JAR vanilla 26.2:

- un coordinador/auditor central define scope, integra y verifica;
- agentes nativos trabajan los puertos que requieren razonamiento profundo;
- workers externos OpenCode con `minimax/MiniMax-M3` toman tareas simples, mecánicas y verificables;
- cada worker opera en un git worktree independiente;
- ningún worker hace commit, merge, push, reset, clean ni cleanup automático.

## Estado

**ACTIVO.** Baseline `go test ./...` verde; ola 00 ejecutada e integrada. MiniMax queda reservado a censos mecánicos con `expected_output`; persistencia profunda escaló a agentes nativos y ya produjo los informes de player/chunk y entities/mobs.

## Precondiciones de arranque

1. Claude confirma que terminó.
2. No quedan worktrees o ramas activas con cambios sin integrar.
3. No existen cambios rastreados pendientes. Los archivos no rastreados se preservan y no entran a los worktrees.
4. El flujo de este directorio está committed para que exista dentro de los worktrees nuevos.
5. `opencode --version` funciona.
6. `opencode models minimax` contiene `minimax/MiniMax-M3`.
7. La autenticación MiniMax de OpenCode está configurada.
8. Se ejecuta un baseline completo y se guarda su resultado.

## Arquitectura de una ola

```text
                     COORDINADOR / AUDITOR
        scope + prompts + JAR evidence + integration gates
                              │
          ┌───────────────────┼───────────────────┐
          │                   │                   │
     Worktree A          Worktree B          Worktree C
     MiniMax M3          MiniMax M3          agente nativo
     tarea simple        tarea simple        lógica compleja
          │                   │                   │
          └───────────────────┼───────────────────┘
                              │
               revisión de diffs + tests focalizados
                              │
        integración serial + full suite + race + oráculos
                              │
              ledger + progress bar + siguiente ola
```

Máximo recomendado: **tres workers simultáneos**. La integración siempre es serial.

## Política de delegación

### Permitido para MiniMax M3

- censos de clases/métodos/packets/registries;
- actualización de reportes y filas del parity ledger;
- búsqueda y clasificación de stubs;
- tablas y codecs mecánicos con especificación exacta;
- tests unitarios mecánicos a partir de un comportamiento ya especificado;
- cambios pequeños, locales y sin decisiones arquitectónicas;
- documentación y extracción de evidencia `javap`.

### No delegar a MiniMax M3

- lógica con orden de RNG;
- orden de fases/tick o lifecycle complejo;
- concurrencia, barreras o ownership regional;
- persistencia polimórfica de entidades;
- AI/Brain/goal arbitration no trivial;
- worldgen con secuencias aleatorias;
- resolución de conflictos de merge;
- cambios que atraviesen más de un dominio;
- decisiones sobre si una desviación es aceptable;
- commit, merge, push, cleanup o modificación del worktree principal.

Esas tareas pertenecen a agentes nativos y requieren auditoría central contra el JAR.

## Ciclo de trabajo

### 0. Congelar baseline

- registrar commit base;
- `go test ./...`;
- suite race acordada;
- oráculos existentes;
- snapshot de `PARITY-LEDGER.csv` y barra de progreso.

### 1. Definir una ola

Cada tarea usa `TASK-TEMPLATE.md` y debe declarar:

- archivos permitidos;
- clases/métodos JAR;
- comportamiento observable esperado;
- RNG/order contract;
- tests obligatorios;
- paths prohibidos;
- output exacto.

No se lanza una ola si dos tareas pueden editar el mismo archivo.

### 2. Plan/dry-run

```powershell
.\.planning\parity-workflow\scripts\Invoke-ParityWave.ps1 `
  -Manifest .planning/parity-workflow/wave-00-census.json
```

Esto imprime branches, worktrees, prompts y modelo sin crear nada.

### 3. Ejecutar la ola

```powershell
.\.planning\parity-workflow\scripts\Invoke-ParityWave.ps1 `
  -Manifest .planning/parity-workflow/wave-00-census.json `
  -Execute
```

El launcher:

1. crea los worktrees secuencialmente para evitar locks de Git;
2. inicia OpenCode en paralelo y oculto;
3. usa `minimax/MiniMax-M3` y JSONL;
4. aplica la configuración restrictiva de `opencode/opencode.json`;
5. guarda stdout/stderr/metadatos en `runs/`;
6. deja los cambios sin stage y sin commit.
7. exige el `expected_output` declarado: debe existir, no estar vacío y ser el único path modificado; `exit 0` sin artefacto se rechaza.

### 4. Revisión central

Aplicar `REVIEW-CHECKLIST.md` a cada worktree. Un output de OpenCode nunca se integra por confianza.

### 5. Integración serial

- revisar diff;
- verificar cada cita contra `javap`/CFR;
- ejecutar tests focalizados;
- commit en la branch sólo después de aprobación;
- integrar una branch por vez;
- repetir tests después de cada integración.

### 6. Gate de ola

- `gofmt`/`go vet`/tests focalizados;
- `go test ./...`;
- race suite;
- pig/oráculos/differential tests relevantes;
- cero stubs o aproximaciones nuevas;
- ledger actualizado;
- barra de progreso recalculada.

## Orden de olas recomendado

| Ola | Objetivo | Workers MiniMax | Agentes nativos |
|---|---|---|---|
| 00 | Censo/ledger inicial | gamerules, packets, persistence schema | auditor central |
| 01 | Fuentes autoritativas | tablas/tests mecánicos | dificultad + 59 gamerules |
| 02 | Persistencia | codecs simples/tests | factory y estado completo de mobs |
| 03 | Región transparente | matrices/tests de frontera | intents/barriers/ownership |
| 04 | Natural spawning | extracción de tablas | categorías + special spawners |
| 05+ | Contenido por dominio | censos/tests/codecs | Brain/AI, blocks, redstone, structures |
| final | Convergencia | ledger/coverage | auditoría diferencial y cierre |

## Archivos

- `PARITY-LEDGER.csv` — registro auditable JAR→Go.
- `TASK-TEMPLATE.md` — contrato obligatorio de tarea.
- `REVIEW-CHECKLIST.md` — gate de revisión.
- `wave-00-census.json` — primera ola segura de ejemplo.
- `tasks/` — prompts versionados.
- `scripts/Invoke-ParityWorker.ps1` — un worker.
- `scripts/Invoke-ParityWave.ps1` — prepara secuencialmente y ejecuta hasta tres workers.
- `opencode/opencode.json` — modelo y permisos restrictivos.
- `runs/` — logs locales ignorados por Git.
- `outputs/` — informes producidos por las tareas de censo.

## Reglas de seguridad

- No usar `--dangerously-skip-permissions`.
- `--auto` se permite porque el config niega todo por defecto y sólo habilita comandos concretos.
- No hay cleanup automático; remover worktrees requiere una acción humana separada.
- Nunca ejecutar workers sobre el checkout principal.
- Nunca compartir una branch entre workers.
- Nunca integrar si el worker tocó archivos fuera de su allowlist.
- Nunca convertir una cita/constante/stub en “completo” sin test observable.

## Criterio de éxito

El flujo termina cuando el ledger no contiene filas `partial`, `stub`, `absent` ni `unverified` para paths observables, y los gates completos permanecen verdes.
