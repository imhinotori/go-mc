# Task: census-persist-player-chunk

Read `CLAUDE.md` completely before acting.

## Objective

Produce a field-level, read-only round-trip census for player data and chunk data against the vanilla 26.2 JAR.

## Allowed edit

Only create/update:

`.planning/parity-workflow/outputs/census-persist-player-chunk.md`

Do not edit Go code, tests, the ledger, scripts or any other file.
Do not open or summarize any other file under `.planning/parity-workflow/outputs/`.

## Required method

1. Trace every production player snapshot/save/load path in `server/` and `save/`.
2. Trace chunk serialization/deserialization, including sections, heightmaps, ticks, structures, block entities and header fields; classify block-entity payloads here only as present/dropped, not their internal schemas.
3. Compare with `Player`, `ServerPlayer`, `ChunkSerializer` and related storage methods using `javap -p -c` on `temp/cache/26.2-inner.jar`.
4. Build complete field tables with `round-trip`, `write-only`, `read-only`, `dropped`, `defaulted`, `absent`, or `unknown`.
5. Cite exact Go file:line and JAR class/method evidence; never infer parity from a struct field alone.

## Output

Include totals per status, critical behavioral loss, commands executed, and proposed ledger rows. No implementation and no commit.
Before finishing, verify that the exact allowed output path exists and is non-empty.
