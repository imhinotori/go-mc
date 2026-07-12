# Task: census-persistence

Read `CLAUDE.md` completely before acting.

## Objective

Produce a read-only field-level persistence census for player data, chunk data, block entities, generic entities, mobs and item components against the vanilla 26.2 JAR.

## Allowed edit

Only create/update:

`.planning/parity-workflow/outputs/census-persistence.md`

Do not edit Go code, tests, the ledger, scripts or any other file.

## Required method

1. Inspect `save/` and all production snapshot/load paths in `server/`.
2. Use `javap`/CFR for `Entity`, `LivingEntity`, `Mob`, representative type-specific entities, `Player`, `ServerPlayer`, `EntityStorage`, `ChunkSerializer` and `DataComponentPatch` as needed.
3. Build tables of fields with status `round-trip`, `write-only`, `read-only`, `dropped`, `defaulted`, or `absent`.
4. Verify whether loaded mobs reconstruct AI, attributes, UUID, equipment, effects and type-specific state.
5. Verify item component loss and report exact supported/unsupported counts.
6. Propose ledger rows, but do not edit `PARITY-LEDGER.csv`.

## Output

The report must separate critical behavioral loss from cosmetic/optional fields and list commands executed.

No implementation. No commit. Never infer parity from a struct field existing; trace save and load call sites.
