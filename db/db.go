package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"myServer/trade-server/core"

	_ "modernc.org/sqlite"
)

// DefaultDBPath is the SQLite file relative to cwd when run from trade-server/.
const DefaultDBPath = "data/market.db"

const schemaSQL = `
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS symbols (
	symbol          TEXT PRIMARY KEY,
	index_name      TEXT NOT NULL,
	instrument_type TEXT NOT NULL DEFAULT 'etf_proxy',
	notes           TEXT NOT NULL DEFAULT ''
);

-- Daily OHLCV (one row per symbol per trading day)
CREATE TABLE IF NOT EXISTS ohlcv_daily (
	symbol     TEXT NOT NULL,
	date       TEXT NOT NULL, -- YYYY-MM-DD
	open       REAL NOT NULL,
	high       REAL NOT NULL,
	low        REAL NOT NULL,
	close      REAL NOT NULL,
	volume     INTEGER NOT NULL,
	source     TEXT NOT NULL DEFAULT 'yahoo',
	fetched_at TEXT NOT NULL, -- RFC3339
	PRIMARY KEY (symbol, date),
	FOREIGN KEY (symbol) REFERENCES symbols(symbol)
);

-- Weekly OHLCV
CREATE TABLE IF NOT EXISTS ohlcv_weekly (
	symbol     TEXT NOT NULL,
	date       TEXT NOT NULL, -- YYYY-MM-DD (week bar date)
	open       REAL NOT NULL,
	high       REAL NOT NULL,
	low        REAL NOT NULL,
	close      REAL NOT NULL,
	volume     INTEGER NOT NULL,
	source     TEXT NOT NULL DEFAULT 'yahoo',
	fetched_at TEXT NOT NULL,
	PRIMARY KEY (symbol, date),
	FOREIGN KEY (symbol) REFERENCES symbols(symbol)
);

-- Monthly OHLCV (one row per symbol per month bar date)
CREATE TABLE IF NOT EXISTS ohlcv_monthly (
	symbol     TEXT NOT NULL,
	date       TEXT NOT NULL, -- YYYY-MM-DD
	open       REAL NOT NULL,
	high       REAL NOT NULL,
	low        REAL NOT NULL,
	close      REAL NOT NULL,
	volume     INTEGER NOT NULL,
	source     TEXT NOT NULL DEFAULT 'yahoo',
	fetched_at TEXT NOT NULL,
	PRIMARY KEY (symbol, date),
	FOREIGN KEY (symbol) REFERENCES symbols(symbol)
);

CREATE INDEX IF NOT EXISTS idx_ohlcv_daily_date ON ohlcv_daily(date);
CREATE INDEX IF NOT EXISTS idx_ohlcv_weekly_date ON ohlcv_weekly(date);
CREATE INDEX IF NOT EXISTS idx_ohlcv_monthly_date ON ohlcv_monthly(date);

-- Shared US market news (Finnhub general) — one feed for SPY/DIA study days
CREATE TABLE IF NOT EXISTS market_news (
	id         INTEGER PRIMARY KEY, -- Finnhub article id
	category   TEXT NOT NULL DEFAULT '',
	datetime   INTEGER NOT NULL,    -- unix seconds (UTC)
	as_of_date TEXT NOT NULL,       -- YYYY-MM-DD America/New_York calendar day
	headline   TEXT NOT NULL,
	summary    TEXT NOT NULL DEFAULT '',
	source     TEXT NOT NULL DEFAULT '',
	url        TEXT NOT NULL DEFAULT '',
	image      TEXT NOT NULL DEFAULT '',
	related    TEXT NOT NULL DEFAULT '',
	fetched_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_market_news_as_of ON market_news(as_of_date);
CREATE INDEX IF NOT EXISTS idx_market_news_datetime ON market_news(datetime);
`

// MarketDB wraps the local SQLite market database.
type MarketDB struct {
	db   *sql.DB
	path string
}

// OpenMarketDB opens (or creates) the SQLite file and applies schema + symbol seed.
// Does not call any external APIs.
func OpenMarketDB(path string) (*MarketDB, error) {
	if path == "" {
		path = DefaultDBPath
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)

	m := &MarketDB{db: sqlDB, path: path}
	if err := m.migrate(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	if err := m.seedSymbols(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return m, nil
}

// Close closes the underlying SQLite connection.
func (m *MarketDB) Close() error {
	if m == nil || m.db == nil {
		return nil
	}
	return m.db.Close()
}

// Path returns the SQLite file path.
func (m *MarketDB) Path() string { return m.path }

func (m *MarketDB) migrate() error {
	if _, err := m.db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	return nil
}

func (m *MarketDB) seedSymbols() error {
	const q = `
INSERT INTO symbols (symbol, index_name, instrument_type, notes)
VALUES (?, ?, ?, ?)
ON CONFLICT(symbol) DO UPDATE SET
	index_name = excluded.index_name,
	instrument_type = excluded.instrument_type,
	notes = excluded.notes
`
	for _, s := range core.SeedSymbols {
		if _, err := m.db.Exec(q, s.Symbol, s.IndexName, s.InstrumentType, s.Notes); err != nil {
			return fmt.Errorf("seed symbol %s: %w", s.Symbol, err)
		}
	}
	return nil
}

// UpsertBars inserts or replaces bars into the interval table.
func (m *MarketDB) UpsertBars(interval core.Interval, bars []core.OHLCVBar) error {
	table, err := tableFor(interval)
	if err != nil {
		return err
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	q := fmt.Sprintf(`
INSERT INTO %s (symbol, date, open, high, low, close, volume, source, fetched_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(symbol, date) DO UPDATE SET
	open = excluded.open,
	high = excluded.high,
	low = excluded.low,
	close = excluded.close,
	volume = excluded.volume,
	source = excluded.source,
	fetched_at = excluded.fetched_at
`, table)

	stmt, err := tx.Prepare(q)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, b := range bars {
		src := b.Source
		if src == "" {
			src = "yahoo"
		}
		fetched := b.FetchedAt
		if fetched.IsZero() {
			fetched = time.Now().UTC()
		}
		_, err := stmt.Exec(
			b.Symbol,
			b.Date.UTC().Format("2006-01-02"),
			b.Open, b.High, b.Low, b.Close, b.Volume,
			src,
			fetched.UTC().Format(time.RFC3339),
		)
		if err != nil {
			return fmt.Errorf("upsert %s %s: %w", b.Symbol, b.Date.Format("2006-01-02"), err)
		}
	}
	return tx.Commit()
}

// GetBars returns OHLCV rows for a symbol/interval ordered by date ascending.
func (m *MarketDB) GetBars(symbol string, interval core.Interval) ([]core.OHLCVBar, error) {
	table, err := tableFor(interval)
	if err != nil {
		return nil, err
	}
	q := fmt.Sprintf(`
SELECT symbol, date, open, high, low, close, volume, source, fetched_at
FROM %s
WHERE symbol = ?
ORDER BY date ASC
`, table)
	rows, err := m.db.Query(q, symbol)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []core.OHLCVBar
	for rows.Next() {
		var b core.OHLCVBar
		var dateStr, fetchedStr string
		if err := rows.Scan(
			&b.Symbol, &dateStr,
			&b.Open, &b.High, &b.Low, &b.Close, &b.Volume,
			&b.Source, &fetchedStr,
		); err != nil {
			return nil, err
		}
		b.Date, err = time.ParseInLocation("2006-01-02", dateStr, time.UTC)
		if err != nil {
			return nil, fmt.Errorf("parse date %q: %w", dateStr, err)
		}
		if fetchedStr != "" {
			if t, err := time.Parse(time.RFC3339, fetchedStr); err == nil {
				b.FetchedAt = t
			}
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// CountBars returns row counts for status checks.
func (m *MarketDB) CountBars(interval core.Interval) (int, error) {
	table, err := tableFor(interval)
	if err != nil {
		return 0, err
	}
	var n int
	err = m.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n)
	return n, err
}

// UpsertNews inserts or replaces Finnhub market news articles.
func (m *MarketDB) UpsertNews(articles []core.MarketNewsArticle) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	const q = `
INSERT INTO market_news (
	id, category, datetime, as_of_date, headline, summary, source, url, image, related, fetched_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	category = excluded.category,
	datetime = excluded.datetime,
	as_of_date = excluded.as_of_date,
	headline = excluded.headline,
	summary = excluded.summary,
	source = excluded.source,
	url = excluded.url,
	image = excluded.image,
	related = excluded.related,
	fetched_at = excluded.fetched_at
`
	stmt, err := tx.Prepare(q)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, a := range articles {
		fetched := a.FetchedAt
		if fetched.IsZero() {
			fetched = time.Now().UTC()
		}
		_, err := stmt.Exec(
			a.ID,
			a.Category,
			a.Datetime.Unix(),
			a.AsOfDate.UTC().Format("2006-01-02"),
			a.Headline,
			a.Summary,
			a.Source,
			a.URL,
			a.Image,
			a.Related,
			fetched.UTC().Format(time.RFC3339),
		)
		if err != nil {
			return fmt.Errorf("upsert news id=%d: %w", a.ID, err)
		}
	}
	return tx.Commit()
}

// CountNews returns the number of rows in market_news.
func (m *MarketDB) CountNews() (int, error) {
	var n int
	err := m.db.QueryRow(`SELECT COUNT(*) FROM market_news`).Scan(&n)
	return n, err
}

// TrimToMonths deletes OHLCV + news rows older than (today UTC − months).
// Keeps the training window tight for buy/sell/hold agents. Returns cutoff date string.
func (m *MarketDB) TrimToMonths(months int) (cutoff string, deleted map[string]int64, err error) {
	if months <= 0 {
		months = 3
	}
	now := time.Now().UTC()
	cut := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, -months, 0)
	cutoff = cut.Format("2006-01-02")
	deleted = map[string]int64{}

	tx, err := m.db.Begin()
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = tx.Rollback() }()

	tables := []string{"ohlcv_daily", "ohlcv_weekly", "ohlcv_monthly"}
	for _, table := range tables {
		res, err := tx.Exec(`DELETE FROM `+table+` WHERE date < ?`, cutoff)
		if err != nil {
			return "", nil, fmt.Errorf("trim %s: %w", table, err)
		}
		n, _ := res.RowsAffected()
		deleted[table] = n
	}
	res, err := tx.Exec(`DELETE FROM market_news WHERE as_of_date < ?`, cutoff)
	if err != nil {
		return "", nil, fmt.Errorf("trim market_news: %w", err)
	}
	n, _ := res.RowsAffected()
	deleted["market_news"] = n

	if err := tx.Commit(); err != nil {
		return "", nil, err
	}
	// Reclaim disk after large deletes (best-effort).
	_, _ = m.db.Exec(`VACUUM`)
	return cutoff, deleted, nil
}

// MaxNewsID returns the highest Finnhub article id stored (0 if empty).
func (m *MarketDB) MaxNewsID() (int64, error) {
	var id sql.NullInt64
	err := m.db.QueryRow(`SELECT MAX(id) FROM market_news`).Scan(&id)
	if err != nil {
		return 0, err
	}
	if !id.Valid {
		return 0, nil
	}
	return id.Int64, nil
}

// GetNewsByDate returns market news for a US session calendar day (YYYY-MM-DD as_of_date).
func (m *MarketDB) GetNewsByDate(asOfDate string) ([]core.MarketNewsArticle, error) {
	rows, err := m.db.Query(`
SELECT id, category, datetime, as_of_date, headline, summary, source, url, image, related, fetched_at
FROM market_news
WHERE as_of_date = ?
ORDER BY datetime DESC
`, asOfDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []core.MarketNewsArticle
	for rows.Next() {
		var a core.MarketNewsArticle
		var unix int64
		var asOfStr, fetchedStr string
		if err := rows.Scan(
			&a.ID, &a.Category, &unix, &asOfStr,
			&a.Headline, &a.Summary, &a.Source, &a.URL, &a.Image, &a.Related, &fetchedStr,
		); err != nil {
			return nil, err
		}
		a.Datetime = time.Unix(unix, 0).UTC()
		a.AsOfDate, err = time.ParseInLocation("2006-01-02", asOfStr, time.UTC)
		if err != nil {
			return nil, fmt.Errorf("parse as_of_date %q: %w", asOfStr, err)
		}
		if fetchedStr != "" {
			if t, e := time.Parse(time.RFC3339, fetchedStr); e == nil {
				a.FetchedAt = t
			}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// LastBarDate returns the newest bar date for symbol/interval, or zero time if empty.
func (m *MarketDB) LastBarDate(symbol string, interval core.Interval) (time.Time, error) {
	table, err := tableFor(interval)
	if err != nil {
		return time.Time{}, err
	}
	var s sql.NullString
	err = m.db.QueryRow(`SELECT MAX(date) FROM `+table+` WHERE symbol = ?`, symbol).Scan(&s)
	if err != nil {
		return time.Time{}, err
	}
	if !s.Valid || s.String == "" {
		return time.Time{}, nil
	}
	return time.ParseInLocation("2006-01-02", s.String, time.UTC)
}

// LastNewsDate returns the newest market_news as_of_date, or zero time if empty.
func (m *MarketDB) LastNewsDate() (time.Time, error) {
	var s sql.NullString
	err := m.db.QueryRow(`SELECT MAX(as_of_date) FROM market_news`).Scan(&s)
	if err != nil {
		return time.Time{}, err
	}
	if !s.Valid || s.String == "" {
		return time.Time{}, nil
	}
	return time.ParseInLocation("2006-01-02", s.String, time.UTC)
}

// Stats returns a human-readable summary of the empty/filled DB.
func (m *MarketDB) Stats() (string, error) {
	var symbols int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM symbols`).Scan(&symbols); err != nil {
		return "", err
	}
	daily, err := m.CountBars(core.IntervalDaily)
	if err != nil {
		return "", err
	}
	weekly, err := m.CountBars(core.IntervalWeekly)
	if err != nil {
		return "", err
	}
	monthly, err := m.CountBars(core.IntervalMonthly)
	if err != nil {
		return "", err
	}
	news, err := m.CountNews()
	if err != nil {
		return "", err
	}
	lastNews, _ := m.LastNewsDate()
	lastNewsStr := "none"
	if !lastNews.IsZero() {
		lastNewsStr = lastNews.Format("2006-01-02")
	}
	return fmt.Sprintf(
		"db=%s symbols=%d ohlcv_daily=%d ohlcv_weekly=%d ohlcv_monthly=%d market_news=%d last_news=%s",
		m.path, symbols, daily, weekly, monthly, news, lastNewsStr,
	), nil
}

func tableFor(interval core.Interval) (string, error) {
	switch interval {
	case core.IntervalDaily:
		return "ohlcv_daily", nil
	case core.IntervalWeekly:
		return "ohlcv_weekly", nil
	case core.IntervalMonthly:
		return "ohlcv_monthly", nil
	default:
		return "", fmt.Errorf("unknown interval %q", interval)
	}
}
