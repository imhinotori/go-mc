#!/usr/bin/env bash
# run-debug.sh — start Sulfur in the standard DEBUG configuration the operator uses for
# day-to-day testing: online-mode + test-kit + chunk persistence + the ULTRA_DEBUG firehose,
# seed 777, foreground (logs to sulfur.log). Debug mode stays ON by default per operator request.
#
# Usage:  ./run-debug.sh            # build + run (background, logs to sulfur.log)
#         ./run-debug.sh -fg        # run in the foreground (Ctrl-C to stop)
set -euo pipefail
cd "$(dirname "$0")"

echo "building (CGO_ENABLED=0)..."
CGO_ENABLED=0 go build -o sulfur.exe ./cmd/sulfur

# Kill any prior instance so the port frees.
PID=$(tasklist 2>/dev/null | grep -i sulfur.exe | awk '{print $2}' || true)
if [ -n "${PID:-}" ]; then
  echo "stopping prior instance (pid $PID)..."
  taskkill //F //PID "$PID" >/dev/null 2>&1 || true
fi

ENVS=(SULFUR_ONLINE_MODE=1 SULFUR_TEST_KIT=1 SULFUR_PERSIST_CHUNKS=1 SULFUR_ULTRA_DEBUG=1)
CMD="env ${ENVS[*]} ./sulfur.exe --online-mode -seed 777"

if [ "${1:-}" = "-fg" ]; then
  echo "running in foreground: $CMD"
  exec $CMD
fi

echo "starting in background (logs -> sulfur.log)..."
$CMD > sulfur.log 2>&1 &
echo "started pid $!"
until grep -qiE "listening" sulfur.log 2>/dev/null; do sleep 1; done
tail -1 sulfur.log
echo "firehose categories: packet move water tick fluid edit combat bandwidth skin"
