# Task: census-persist-components-blockentities

Read `CLAUDE.md` completely before acting.

## Objective

Produce a read-only persistence census for block-entity internal state and item data components against vanilla 26.2.

## Allowed edit

Only create/update:

`.planning/parity-workflow/outputs/census-persist-components-blockentities.md`

Do not edit Go code, tests, the ledger, scripts or any other file.

## Required method

1. Enumerate every supported block-entity snapshot/save/load codec and trace its production call sites.
2. Audit representative inventory, furnace, sign, spawner and structure-like block entities against their vanilla save/load methods using `javap -p -c` on `temp/cache/26.2-inner.jar`.
3. Enumerate all generated 26.2 data component IDs, then trace the disk item encode/decode switch and calculate exact supported, partial and dropped counts.
4. Verify nested item stacks in inventories and block entities use the same component-preserving path.
5. Cite exact Go file:line and JAR class/method evidence; mark ambiguity `unknown` rather than guessing.

## Output

Include complete component totals, audited block-entity tables, critical behavioral loss, commands executed and proposed ledger rows. No implementation and no commit.

