# Task: fix-vibration-selector-timing

Read `CLAUDE.md` and `.planning/audits/PARITY-AUDIT-2026-07-12-CLAUDE-WAVE.md` completely.

## Objective

Port the vanilla 26.2 VibrationSelector replacement rule and same-tick travel decrement.

## Allowed edit

Only modify `server/vibration_block.go`. Do not edit tests or any other file.

## Required method

1. Verify `VibrationSelector.addCandidate/shouldReplaceVibration` and `VibrationSystem$Ticker.tick` with `javap -p -c` on `temp/cache/26.2-inner.jar`.
2. Replace candidates only for the same game tick when closer, or at equal distance when the candidate event has higher frequency.
3. After promoting a candidate, continue the tick and decrement travel time exactly as vanilla; do not return early.
4. Preserve listener filters and event ordering.
5. Run `gofmt -w server/vibration_block.go`, `go test ./server -run 'TestBlockVibration|TestVibration' -count=1 -timeout=120s`, and `git diff --check`. Existing tests may encode the audited off-by-one; report that explicitly rather than editing them.

No commit and no unrelated sculk changes.

