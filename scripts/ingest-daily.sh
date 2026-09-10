#!/usr/bin/env bash
# Cron-friendly daily market-data refresh for trade-server.
# Ensures schema, bounded Yahoo OHLCV upsert, Finnhub + GDELT news.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "[ingest-daily] $(date -u +%Y-%m-%dT%H:%M:%SZ) starting in $ROOT"
go run . ingest-daily
echo "[ingest-daily] $(date -u +%Y-%m-%dT%H:%M:%SZ) ok"
