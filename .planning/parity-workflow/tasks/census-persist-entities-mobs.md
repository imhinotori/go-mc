# Task: census-persist-entities-mobs

Read `CLAUDE.md` completely before acting.

## Objective

Produce a field-level, read-only round-trip census for generic entities, living entities, mobs and representative type-specific mobs against vanilla 26.2.

## Allowed edit

Only create/update:

`.planning/parity-workflow/outputs/census-persist-entities-mobs.md`

Do not edit Go code, tests, the ledger, scripts or any other file.

## Required method

1. Trace production entity snapshot/save/load and reconstruction call chains in `server/` and `save/`.
2. Compare `Entity`, `LivingEntity`, `Mob` and at least five representative type-specific entities with `javap -p -c` on `temp/cache/26.2-inner.jar`.
3. Verify UUID, transforms, timers, health, attributes, equipment, effects, leash, brain/AI reconstruction, age and type-specific state.
4. Classify every audited field as `round-trip`, `write-only`, `read-only`, `dropped`, `defaulted`, `absent`, or `unknown` with exact file:line evidence.
5. Separate critical behavioral loss from cosmetic/optional state and record commands executed.

## Output

Include totals per status, a complete audited-field table, representative type coverage and proposed ledger rows. No implementation and no commit.

