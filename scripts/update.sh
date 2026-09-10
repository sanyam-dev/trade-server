#!/usr/bin/env bash
# Incremental DB refresh since last stored timestamps (then trim to 3 months).
set -euo pipefail
cd "$(dirname "$0")/.."
exec go run . update
