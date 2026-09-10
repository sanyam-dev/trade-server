# trade-server

Local index market simulator backend (Go). Module root is the parent `myServer` repo; run from this directory.

## Layout

| Path | Role |
|------|------|
| `main.go` | Thin entry: CLI dispatch + HTTP listen |
| `core/` | OHLCV types, Yahoo Finance client |
| `db/` | SQLite schema, OpenMarketDB, UpsertBars / GetBars / Stats, `init-db` / `ingest-db` |
| `web/` | Static UI (`index.html`, `app.js`, `styles.css`, `data/`) + HTTP/API/WS registration |
| `utils/` | `.env` loading, Truncate |
| `data/` | SQLite path `data/market.db` (relative to cwd) |

## Keys (`trade-server/.env`)

- `FINNHUB_API_KEY` — required for `ingest-news` (general market news)

OHLCV ingest uses Yahoo Finance (no API key). Alpha Vantage was considered earlier but free daily history is capped (~100 bars), so it is not used.

## Local OHLCV database

SQLite file: `trade-server/data/market.db`

| Table | Purpose |
|-------|---------|
| `symbols` | SPY → S&P 500, DIA → Dow Jones (ETF index proxies for training) |
| `ohlcv_daily` | Daily OHLCV |
| `ohlcv_weekly` | Weekly OHLCV |
| `ohlcv_monthly` | Monthly OHLCV |
| `market_news` | Finnhub general market news (shared SPY/DIA feed) |

```bash
cd trade-server
go run . init-db
go run . ingest-db
go run . ingest-news
```

`init-db` creates schema + seeds symbols (no API). `ingest-db` pulls **max listing history** from Yahoo Finance into SQLite (daily / weekly / monthly for SPY + DIA). `ingest-news` pulls Finnhub `category=general` into `market_news` (aligned by `as_of_date` in US/Eastern).

### Why Yahoo for training data

Yahoo chart API returns the full series (SPY daily ≈ 8k+ bars since 1993) — enough for later simple RL. (Alpha Vantage free daily is capped at ~100 bars.)

| Interval | Source | Typical size (SPY) |
|----------|--------|--------------------|
| daily | Yahoo max | ~8k bars |
| weekly | Yahoo max | ~1.7k bars |
| monthly | Yahoo max | ~400 bars |

Until `ingest-db` runs, `/api/bars` may fall back to stale sample JSON under `web/data/` (2024).

## Run server + chart UI

```bash
cd trade-server
go run .
```

From module root: `go build ./trade-server/...`

Then open **http://localhost:8080/** — dark TradingView-style candlestick playback (Lightweight Charts).

| Endpoint | Purpose |
|----------|---------|
| `http://localhost:8080/` | Chart UI (`web/`) |
| `http://localhost:8080/api/bars?symbol=SPY&interval=daily` | OHLCV JSON (SQLite if rows exist, else `web/data/*_daily.json`) |
| `http://localhost:8080/api/news?date=2026-09-10` | Market headlines for that `as_of_date` from `market_news` |
| `ws://localhost:8080/ws` | WebSocket upgrade (stub for live ticks later) |

Dashboard: chart + news panel. **Click a candle** (or scrub/play to the tip) to load Finnhub headlines for that session day. Playback: Play / Pause / Back / Step / Reset, speed scrubber, session clock. Space = play/pause, ←/→ = back/step, R = reset.

Optional live mode (WS frames when server emits them): `http://localhost:8080/?live=1`
