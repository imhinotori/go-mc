# Task: census-serverbound

Read `CLAUDE.md` completely before acting.

## Objective

Produce a read-only census of all 70 game-state serverbound packet IDs, their vanilla handler methods, and their current Sulfur dispatch/handler status.

## Allowed edit

Only create/update:

`.planning/parity-workflow/outputs/census-serverbound.md`

Do not edit Go code, tests, the ledger, scripts or any other file.

## Required method

1. Read the generated game Serverbound enum in `data/packetid/packetid.go`.
2. Inspect `TickLoop.dispatch`, `applyInput` and any direct state-specific handlers.
3. For each packet classify `exact`, `partial`, `explicit-noop`, `default-noop`, or `unverified`.
4. Map the corresponding vanilla handler in `ServerGamePacketListenerImpl` where applicable.
5. Record exact file:line evidence and user-visible impact.
6. Propose ledger rows, but do not edit `PARITY-LEDGER.csv`.

## Output

The report must contain all packet names, totals by status, a priority list and commands executed.

No implementation. No commit. Do not classify a packet as exact merely because a switch case exists.
