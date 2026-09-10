# trade-server

Local index market data + chart UI (Go). Retains the **last 3 months** of OHLCV/news for buy/sell/hold work.

## Layout

| Path | Role |
|------|------|
| `main.go` | CLI dispatch + HTTP server |
| `core/` | Yahoo / Finnhub / GDELT clients |
| `db/` | SQLite CRUD + incremental `update` |
| `web/` | Chart UI + `/api/bars` + `/api/news` |
| `scripts/update.sh` | Cron wrapper → `go run . update` |

## Keys (`.env`)

- `FINNHUB_API_KEY` — news (Yahoo/GDELT need no key)

## CLI

```bash
cd trade-server
go run . init-db                 # Create schema + seed SPY/DIA
go run . update                  # Fetch since last timestamps, then trim
go run . trim-db --months=3      # Delete older than 3 months
go run . status                  # Counts + last bar/news dates
```

| Op | How |
|----|-----|
| **Create** | `init-db` → schema + SPY/DIA |
| **Read** | `GetBars` / `GetNewsByDate` / `status` |
| **Update** | `update` (`sync` alias) — remote → DB since last, then trim |
| **Delete** | `trim-db` / auto-trim inside `update` |

### Cron

```cron
30 21 * * 1-5 cd /path/to/trade-server && ./scripts/update.sh >> data/update.log 2>&1
```

## Run UI

```bash
go run .
# http://localhost:8080/
```

| Endpoint | Purpose |
|----------|---------|
| `/` | Chart + news dashboard |
| `/api/bars?symbol=SPY&interval=daily` | OHLCV JSON |
| `/api/news?date=YYYY-MM-DD` | Headlines for that day |
| `/ws` | Stub for later live ticks |
