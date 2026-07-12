# Task: census-gamerules

Read `CLAUDE.md` completely before acting.

## Objective

Produce a read-only parity census of every `GameRule` declared by the vanilla 26.2 JAR versus `server/gamerules.go` and every production consumer in `server/`.

## Allowed edit

Only create/update:

`.planning/parity-workflow/outputs/census-gamerules.md`

Do not edit Go code, tests, the ledger, scripts or any other file.

## Required method

1. Run `javap -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.level.gamerules.GameRules`.
2. Enumerate all JAR rule fields.
3. Enumerate rules registered in `newGameRules`.
4. Search every production consumer and classify it as `live-store`, `constant`, `absent`, or `unknown`.
5. Record exact file:line evidence.
6. Propose ledger rows, but do not edit `PARITY-LEDGER.csv`.

## Output

The report must include totals, a complete table, critical consumer mismatches and commands executed.

No code implementation. No commit. If evidence is ambiguous, mark `unknown`; never guess.
