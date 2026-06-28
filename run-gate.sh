#!/usr/bin/env bash
# run-gate.sh — the PLUGIN-07 VISUAL GATE launch (Plan 28-02). It starts Sulfur in the exact
# configuration the in-process gate bot (cmd/testbot -mode gate) needs to connect and drive the
# 4-item plugin-system checklist.
#
# WHY OFFLINE (the key compatibility fact): the gate bot logs in OFFLINE (it derives its UUID via
# offline.NameToUUID, sends no Mojang session). run-debug.sh sets SULFUR_ONLINE_MODE=1 +
# --online-mode, which makes the login path REQUIRE a real Mojang hasJoined auth — it would REJECT
# the bot's offline login. So this gate launch DELIBERATELY omits online-mode (default = offline)
# so the bot can connect. run-debug.sh stays online-mode for the operator's real-client testing;
# this is the developer/gate launch, not the production run (threat T-28-07 accept).
#
# SULFUR_TEST_KIT=1 seeds the gate kit (incl. the slot-35 custom-mob spawn egg, item id 1161,
# Plan 28-01) so the bot can right-click to spawn the wander mob (checklist item #1). A fixed
# -seed (777) + SULFUR_PERSIST_CHUNKS=1 keep the world reproducible run-to-run. The bundled
# gate_events plugin (plugins/gate_events) subscribes on_block_break + on_player_join and reacts
# via chat() so the bot observes a "gate_events:" SystemChat (checklist item #4).
#
# Build is CGO_ENABLED=0 — the DEFAULT pure-Go static binary (the Starlark plugin core). The
# Python off-tick lane is NOT exercised here; it has its own Phase-26 `-tags python` Docker gate.
#
# Usage:  ./run-gate.sh            # build + run (background), wait for "listening", print address
set -euo pipefail
cd "$(dirname "$0")"

echo "building gate server (CGO_ENABLED=0, default Starlark-core static binary)..."
CGO_ENABLED=0 go build -o sulfur-gate.exe ./cmd/sulfur

# Kill any prior gate instance so the port frees.
PID=$(tasklist 2>/dev/null | grep -i sulfur-gate.exe | awk '{print $2}' || true)
if [ -n "${PID:-}" ]; then
  echo "stopping prior gate instance (pid $PID)..."
  taskkill //F //PID "$PID" >/dev/null 2>&1 || true
fi

# OFFLINE (no SULFUR_ONLINE_MODE, no --online-mode) + test-kit + chunk persistence + fixed seed.
ENVS=(SULFUR_TEST_KIT=1 SULFUR_PERSIST_CHUNKS=1)
CMD="env ${ENVS[*]} ./sulfur-gate.exe -seed 777"

echo "starting OFFLINE gate server in background (logs -> sulfur-gate.log)..."
$CMD > sulfur-gate.log 2>&1 &
echo "started pid $!"
until grep -qiE "listening" sulfur-gate.log 2>/dev/null; do sleep 1; done
tail -1 sulfur-gate.log
echo "gate server ready (OFFLINE so the in-process bot can log in); connect: localhost:25565"
