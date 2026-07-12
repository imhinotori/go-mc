# Task: fix-gamerule-silverfish

Read `CLAUDE.md` completely before acting.

## Objective

Replace both silverfish AI uses of the constant `MOB_GRIEFING` default with the existing live gamerule store without changing RNG order or block behavior.

## Allowed edit

Only modify `server/ai_goals_silverfish.go`. Do not edit tests or any other file.

## Required method

1. Verify `SilverfishMergeWithStoneGoal.canUse`, `SilverfishWakeUpFriendsGoal.tick` and `GameRules.MOB_GRIEFING` using `javap -p -c` on `temp/cache/26.2-inner.jar`.
2. Inspect `server/gamerules.go` and use `t.gameRule(ruleMobGriefing)` at both existing gates.
3. Preserve short-circuiting: when false, the merge path must not draw the merge `nextInt`; the wake branch must de-infest rather than destroy/summon.
4. Do not touch the separate constant declaration in `silverfish_infest.go`; central review will remove it after this isolated worker is integrated.
5. Run `gofmt -w server/ai_goals_silverfish.go`, `go test ./server -run Silverfish`, and `git diff --check`.

No commit and no broader silverfish changes.

