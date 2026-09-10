package db

import (
	"fmt"
	"os"
	"strings"
	"time"

	"myServer/trade-server/core"
	"myServer/trade-server/utils"
)

// InitMarketDB creates the local SQLite schema and seeds SPY/DIA symbols.
// No external API calls.
func InitMarketDB(path string) error {
	mdb, err := OpenMarketDB(path)
	if err != nil {
		return err
	}
	defer mdb.Close()

	stats, err := mdb.Stats()
	if err != nil {
		return err
	}
	fmt.Println("market db ready:", stats)
	fmt.Println("symbols:")
	for _, s := range core.SeedSymbols {
		fmt.Printf("  %s -> %s (%s)\n", s.Symbol, s.IndexName, s.InstrumentType)
	}
	return nil
}

// IngestMarketDB pulls max-history OHLCV from Yahoo into SQLite for RL harness training.
func IngestMarketDB(path string) error {
	client := core.NewYahooClient()

	mdb, err := OpenMarketDB(path)
	if err != nil {
		return err
	}
	defer mdb.Close()

	intervals := []core.Interval{core.IntervalDaily, core.IntervalWeekly, core.IntervalMonthly}
	for i, s := range core.SeedSymbols {
		for j, interval := range intervals {
			fmt.Printf("fetching %s %s (yahoo max)…\n", s.Symbol, interval)
			bars, err := client.FetchMaxHistory(s.Symbol, interval)
			if err != nil {
				return err
			}
			if err := mdb.UpsertBars(interval, bars); err != nil {
				return err
			}
			var from, to string
			if len(bars) > 0 {
				from = bars[0].Date.Format("2006-01-02")
				to = bars[len(bars)-1].Date.Format("2006-01-02")
				// Yahoo order is chronological, but be safe.
				minD, maxD := bars[0].Date, bars[0].Date
				for _, b := range bars[1:] {
					if b.Date.Before(minD) {
						minD = b.Date
					}
					if b.Date.After(maxD) {
						maxD = b.Date
					}
				}
				from, to = minD.Format("2006-01-02"), maxD.Format("2006-01-02")
			}
			fmt.Printf("  upserted %d %s bars for %s (%s → %s)\n", len(bars), interval, s.Symbol, from, to)

			// Light polite pause between Yahoo chart calls.
			if !(i == len(core.SeedSymbols)-1 && j == len(intervals)-1) {
				time.Sleep(500 * time.Millisecond)
			}
		}
	}

	stats, err := mdb.Stats()
	if err != nil {
		return err
	}
	fmt.Println("ingest complete:", stats)
	return nil
}

// IngestMarketNews pulls Finnhub general market news into SQLite (shared SPY/DIA feed).
// One request by default; uses minId from DB max to fetch only newer articles when possible.
func IngestMarketNews(path string) error {
	mdb, err := OpenMarketDB(path)
	if err != nil {
		return err
	}
	defer mdb.Close()

	if err := ingestFinnhubInto(mdb); err != nil {
		return err
	}

	stats, err := mdb.Stats()
	if err != nil {
		return err
	}
	fmt.Println("news ingest complete:", stats)
	return nil
}

// lookbackDaysForInterval returns how far back a daily Yahoo refresh should reach.
// Bounded windows avoid re-downloading full max history every day.
func lookbackDaysForInterval(interval core.Interval) int {
	switch interval {
	case core.IntervalDaily:
		return 14 // covers weekends/holidays + late revisions
	case core.IntervalWeekly:
		return 90
	case core.IntervalMonthly:
		return 120
	default:
		return 14
	}
}

func barDateRange(bars []core.OHLCVBar) (from, to string) {
	if len(bars) == 0 {
		return "", ""
	}
	minD, maxD := bars[0].Date, bars[0].Date
	for _, b := range bars[1:] {
		if b.Date.Before(minD) {
			minD = b.Date
		}
		if b.Date.After(maxD) {
			maxD = b.Date
		}
	}
	return minD.Format("2006-01-02"), maxD.Format("2006-01-02")
}

// IngestDailyRefresh ensures schema, upserts a bounded Yahoo OHLCV window for SPY/DIA,
// refreshes Finnhub recent market news, and pulls GDELT for today + yesterday (US/Eastern).
// Safe to re-run (upserts). Does not download full Yahoo max history.
func IngestDailyRefresh(path string) error {
	started := time.Now()
	fmt.Println("=== ingest-daily start ===")
	fmt.Println("db:", path)

	mdb, err := OpenMarketDB(path)
	if err != nil {
		return fmt.Errorf("init/open db: %w", err)
	}
	defer mdb.Close()
	fmt.Println("schema ready (init if needed)")

	now := time.Now().UTC()
	client := core.NewYahooClient()
	intervals := []core.Interval{core.IntervalDaily, core.IntervalWeekly, core.IntervalMonthly}
	ohlcvTotal := 0

	fmt.Println("--- Yahoo OHLCV (bounded range) ---")
	for i, s := range core.SeedSymbols {
		for j, interval := range intervals {
			lookback := lookbackDaysForInterval(interval)
			from := now.AddDate(0, 0, -lookback)
			to := now
			fmt.Printf("fetching %s %s (yahoo last ~%d days)…\n", s.Symbol, interval, lookback)
			bars, err := client.FetchRange(s.Symbol, interval, from, to)
			if err != nil {
				return fmt.Errorf("yahoo %s %s: %w", s.Symbol, interval, err)
			}
			if err := mdb.UpsertBars(interval, bars); err != nil {
				return fmt.Errorf("upsert %s %s: %w", s.Symbol, interval, err)
			}
			ohlcvTotal += len(bars)
			bf, bt := barDateRange(bars)
			fmt.Printf("  upserted %d %s bars for %s (%s → %s)\n", len(bars), interval, s.Symbol, bf, bt)

			if !(i == len(core.SeedSymbols)-1 && j == len(intervals)-1) {
				time.Sleep(500 * time.Millisecond)
			}
		}
	}
	fmt.Printf("yahoo done: %d bars upserted\n", ohlcvTotal)

	fmt.Println("--- Finnhub market news ---")
	if err := ingestFinnhubInto(mdb); err != nil {
		return err
	}

	ny, locErr := time.LoadLocation("America/New_York")
	if locErr != nil {
		ny = time.FixedZone("EST", -5*3600)
	}
	localNow := time.Now().In(ny)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.UTC)
	yesterday := today.AddDate(0, 0, -1)

	fmt.Println("--- GDELT market news (yesterday + today, US/Eastern calendar) ---")
	fmt.Printf("days: %s → %s\n", yesterday.Format("2006-01-02"), today.Format("2006-01-02"))
	gdeltClient := core.NewGDELTClient()
	gdeltTotal := 0
	for d := yesterday; !d.After(today); d = d.AddDate(0, 0, 1) {
		day := d.Format("2006-01-02")
		fmt.Printf("fetching GDELT market news for %s…\n", day)
		articles, err := gdeltClient.FetchDay(d)
		if err != nil {
			// Best-effort: Finnhub already covered recent headlines; GDELT outages/429s should not fail the daily job.
			fmt.Fprintf(os.Stderr, "  warning: gdelt %s skipped: %v\n", day, err)
			continue
		}
		if err := mdb.UpsertNews(articles); err != nil {
			return fmt.Errorf("gdelt upsert %s: %w", day, err)
		}
		gdeltTotal += len(articles)
		fmt.Printf("  upserted %d articles\n", len(articles))
	}
	fmt.Printf("gdelt done: %d articles\n", gdeltTotal)

	stats, err := mdb.Stats()
	if err != nil {
		return err
	}
	fmt.Println("=== ingest-daily complete ===")
	fmt.Println(stats)
	fmt.Printf("elapsed: %s\n", time.Since(started).Round(time.Millisecond))
	return nil
}

// ingestFinnhubInto refreshes Finnhub general news into an already-open MarketDB.
func ingestFinnhubInto(mdb *MarketDB) error {
	apiKey := utils.GetEnv("FINNHUB_API_KEY")
	client, err := core.NewFinnhubClient(apiKey)
	if err != nil {
		return err
	}

	maxID, err := mdb.MaxNewsID()
	if err != nil {
		return err
	}

	minID := maxID
	fmt.Printf("fetching Finnhub market news category=general (minId=%d)…\n", minID)
	articles, err := client.FetchMarketNews("general", minID)
	if err != nil {
		return err
	}
	if len(articles) == 0 && minID > 0 {
		fmt.Println("  no newer ids; refreshing latest batch (minId=0)…")
		articles, err = client.FetchMarketNews("general", 0)
		if err != nil {
			return err
		}
	}
	if err := mdb.UpsertNews(articles); err != nil {
		return err
	}

	var minDate, maxDate string
	for i, a := range articles {
		d := a.AsOfDate.Format("2006-01-02")
		if i == 0 || d < minDate {
			minDate = d
		}
		if i == 0 || d > maxDate {
			maxDate = d
		}
	}
	fmt.Printf("  upserted %d articles (as_of_date %s → %s)\n", len(articles), minDate, maxDate)
	return nil
}

// IngestGDELTNews backfills market headlines from GDELT DOC 2.0 into market_news.
// from/to are inclusive calendar days (YYYY-MM-DD). Empty → last 7 UTC days.
// DOC API searchable window is ~last 3 months; requests are paced ≥6s apart.
func IngestGDELTNews(path string, fromStr, toStr string) error {
	now := time.Now().UTC()
	var from, to time.Time
	var err error

	if toStr == "" {
		to = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	} else {
		to, err = time.ParseInLocation("2006-01-02", toStr, time.UTC)
		if err != nil {
			return fmt.Errorf("parse --to: %w", err)
		}
	}
	if fromStr == "" {
		from = to.AddDate(0, 0, -6)
	} else {
		from, err = time.ParseInLocation("2006-01-02", fromStr, time.UTC)
		if err != nil {
			return fmt.Errorf("parse --from: %w", err)
		}
	}
	if to.Before(from) {
		return fmt.Errorf("--to %s is before --from %s", to.Format("2006-01-02"), from.Format("2006-01-02"))
	}

	// Soft guard: DOC API is ~3 months; warn but still try.
	if from.Before(now.AddDate(0, -3, -7)) {
		fmt.Println("warning: GDELT DOC API usually only indexes ~last 3 months; older days may return empty/errors")
	}

	mdb, err := OpenMarketDB(path)
	if err != nil {
		return err
	}
	defer mdb.Close()

	client := core.NewGDELTClient()
	total := 0
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		day := d.Format("2006-01-02")
		fmt.Printf("fetching GDELT market news for %s…\n", day)
		articles, err := client.FetchDay(d)
		if err != nil {
			return fmt.Errorf("%s: %w", day, err)
		}
		if err := mdb.UpsertNews(articles); err != nil {
			return fmt.Errorf("upsert %s: %w", day, err)
		}
		total += len(articles)
		fmt.Printf("  upserted %d articles\n", len(articles))
	}

	stats, err := mdb.Stats()
	if err != nil {
		return err
	}
	fmt.Printf("gdelt ingest complete: %d articles across range; %s\n", total, stats)
	return nil
}

func printIngestPlan() {
	fmt.Print(`
Next steps:

  go run . ingest-db                              # Yahoo OHLCV max history → SQLite
  go run . ingest-daily                           # Daily refresh (bounded Yahoo + news)
  go run . ingest-news                            # Finnhub recent general news
  go run . ingest-gdelt                           # GDELT DOC market news (default: last 7 days)
  go run . ingest-gdelt --from=2025-10-16 --to=2025-10-16
  ./scripts/ingest-daily.sh                       # Cron-friendly wrapper

GDELT DOC is free (no key), ~3 months searchable history, max ~1 request / 5s.
`)
}

// RunDBCommand handles CLI subcommands init-db / ingest-db / ingest-daily / ingest-news / ingest-gdelt.
// Returns true if a DB command was recognized and handled.
func RunDBCommand(args []string) bool {
	if len(args) < 1 {
		return false
	}
	switch args[0] {
	case "init-db":
		path := DefaultDBPath
		if len(args) > 1 && !strings.HasPrefix(args[1], "--") {
			path = args[1]
		}
		if err := InitMarketDB(path); err != nil {
			fmt.Fprintf(os.Stderr, "init-db failed: %v\n", err)
			os.Exit(1)
		}
		printIngestPlan()
		return true
	case "ingest-db":
		path := DefaultDBPath
		if len(args) > 1 && !strings.HasPrefix(args[1], "--") {
			path = args[1]
		}
		if err := IngestMarketDB(path); err != nil {
			fmt.Fprintf(os.Stderr, "ingest-db failed: %v\n", err)
			os.Exit(1)
		}
		return true
	case "ingest-daily", "refresh-daily":
		path := DefaultDBPath
		if len(args) > 1 && !strings.HasPrefix(args[1], "--") {
			path = args[1]
		}
		if err := IngestDailyRefresh(path); err != nil {
			fmt.Fprintf(os.Stderr, "ingest-daily failed: %v\n", err)
			os.Exit(1)
		}
		return true
	case "ingest-news":
		path := DefaultDBPath
		if len(args) > 1 && !strings.HasPrefix(args[1], "--") {
			path = args[1]
		}
		if err := IngestMarketNews(path); err != nil {
			fmt.Fprintf(os.Stderr, "ingest-news failed: %v\n", err)
			os.Exit(1)
		}
		return true
	case "ingest-gdelt":
		path := DefaultDBPath
		fromStr, toStr := "", ""
		for i := 1; i < len(args); i++ {
			a := args[i]
			switch {
			case strings.HasPrefix(a, "--from="):
				fromStr = strings.TrimPrefix(a, "--from=")
			case strings.HasPrefix(a, "--to="):
				toStr = strings.TrimPrefix(a, "--to=")
			case !strings.HasPrefix(a, "--") && path == DefaultDBPath && !strings.Contains(a, "="):
				path = a
			}
		}
		if err := IngestGDELTNews(path, fromStr, toStr); err != nil {
			fmt.Fprintf(os.Stderr, "ingest-gdelt failed: %v\n", err)
			os.Exit(1)
		}
		return true
	default:
		return false
	}
}
