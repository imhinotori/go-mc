# Contrato de tarea de paridad

## Identidad

- **Task ID:** `<lowercase-kebab-id>`
- **Ola:** `<NN>`
- **Worker recomendado:** `minimax-m3 | native`
- **Base commit:** `<hash>`
- **Branch:** `codex/parity-<task-id>`

## Objetivo acotado

Una oración concreta que describa el resultado terminado.

## Evidencia vanilla obligatoria

- **JAR:** `temp/cache/26.2-inner.jar`
- **Clase(s):**
- **Método(s):**
- **Comando(s) javap/CFR:**
- **Constantes/casts/guards relevantes:**
- **Contrato RNG/order:** `sí/no/n/a`, con detalle.

## Archivos permitidos

- `<path exacto>`

El worker no puede editar ningún archivo fuera de esta lista.

## Archivos prohibidos

- `go.mod`, `go.sum` salvo autorización explícita;
- `.planning/parity-workflow/scripts/`;
- cualquier archivo compartido con otra tarea de la ola;
- cualquier path fuera del worktree.

## Trabajo requerido

1. Paso verificable.
2. Paso verificable.
3. Paso verificable.

## Fuera de scope

- Lista explícita de trabajo cercano que no debe tocarse.

## Tests obligatorios

```text
<comando focalizado>
```

## Output esperado

- archivos creados/modificados;
- informe de evidencia;
- tests ejecutados y resultado;
- desviaciones encontradas;
- filas propuestas para `PARITY-LEDGER.csv`.

## Prohibiciones

- No commit, merge, push, reset, clean ni checkout destructivo.
- No agregar dependencias.
- No introducir `TODO`, `DEFERRED`, proxy, aproximación o constante-default nueva.
- No modificar tests para aceptar una desviación.
- No asumir que un comentario existente es correcto: contrastar con el JAR.
- Si el scope exige una decisión no especificada, detenerse y reportar `BLOCKED`.

## Definition of Done

- [ ] Scope respetado.
- [ ] Evidencia JAR registrada.
- [ ] Implementación o informe completo.
- [ ] Tests focalizados verdes.
- [ ] `git diff --check` verde.
- [ ] Sin archivos fuera del allowlist.
- [ ] Sin nuevos stubs/deviations.
- [ ] Resultado final enumera exactamente qué se verificó y qué no.
