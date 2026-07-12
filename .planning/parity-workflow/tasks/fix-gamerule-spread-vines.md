# Task: fix-gamerule-spread-vines

Read `CLAUDE.md` completely before acting.

## Objective

Replace the constant `SPREAD_VINES` default with the live `GameRules.SPREAD_VINES` store read, preserving the exact vanilla random-tick/RNG order.

## Allowed edit

Only modify `server/vine.go`. Do not edit tests or any other file.

## Required method

1. Verify `GameRules.SPREAD_VINES` and `VineBlock.randomTick` with `javap -p -c` against `temp/cache/26.2-inner.jar`.
2. Inspect `server/gamerules.go` and use the existing live-store helper and rule ID.
3. Remove the obsolete constant/comments and gate `vineRandomTick` from the live rule before any RNG draw, matching vanilla order.
4. Run `gofmt -w server/vine.go`, `go test ./server -run Vine`, and `git diff --check`.

No commit. Do not broaden scope or change vine mechanics.

