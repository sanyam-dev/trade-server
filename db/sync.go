package db

import (
	"fmt"
	"os"
	"time"

	"myServer/trade-server/core"
	"myServer/trade-server/utils"
)

// TrainingWindowMonths is the retained history for the buy/sell/hold agent.
const TrainingWindowMonths = 3

// UpdateSinceLast pulls only new OHLCV + news since the latest dates already in SQLite.
// Empty tables seed from (today − TrainingWindowMonths). Safe to re-run (upserts).
func UpdateSinceLast(path string) error {
	started := time.Now()
	fmt.Println("=== update (since last DB timestamps) ===")

	mdb, err := OpenMarketDB(path)
	if err != nil {
		return err
	}
	defer mdb.Close()

	today := time.Now().UTC()
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	windowStart := today.AddDate(0, -TrainingWindowMonths, 0)

	if err := updateBarsSinceLast(mdb, windowStart, today); err != nil {
		return err
	}
	if err := updateNewsSinceLast(mdb, windowStart, today); err != nil {
		return err
	}

	// Keep training window tight after each update.
	cutoff, deleted, err := mdb.TrimToMonths(TrainingWindowMonths)
	if err != nil {
		return err
	}
	fmt.Printf("trim: kept date >= %s (deleted daily=%d weekly=%d monthly=%d news=%d)\n",
		cutoff, deleted["ohlcv_daily"], deleted["ohlcv_weekly"], deleted["ohlcv_monthly"], deleted["market_news"])

	stats, err := mdb.Stats()
	if err != nil {
		return err
	}
	fmt.Println("=== update complete ===")
	fmt.Println(stats)
	fmt.Printf("elapsed: %s\n", time.Since(started).Round(time.Millisecond))
	return nil
}

func updateBarsSinceLast(mdb *MarketDB, windowStart, today time.Time) error {
	client := core.NewYahooClient()
	intervals := []core.Interval{core.IntervalDaily, core.IntervalWeekly, core.IntervalMonthly}
	fmt.Println("--- OHLCV (Yahoo, since last bar) ---")

	for i, s := range core.SeedSymbols {
		for j, interval := range intervals {
			last, err := mdb.LastBarDate(s.Symbol, interval)
			if err != nil {
				return err
			}
			from := windowStart
			if !last.IsZero() {
				// Re-fetch from last stored day to pick up revisions / same-day updates.
				from = last
			}
			fmt.Printf("upsert %s %s from %s → %s\n", s.Symbol, interval, from.Format("2006-01-02"), today.Format("2006-01-02"))
			bars, err := client.FetchRange(s.Symbol, interval, from, today)
			if err != nil {
				return fmt.Errorf("yahoo %s %s: %w", s.Symbol, interval, err)
			}
			if err := mdb.UpsertBars(interval, bars); err != nil {
				return err
			}
			fmt.Printf("  %d bars\n", len(bars))
			if !(i == len(core.SeedSymbols)-1 && j == len(intervals)-1) {
				time.Sleep(400 * time.Millisecond)
			}
		}
	}
	return nil
}

func updateNewsSinceLast(mdb *MarketDB, windowStart, today time.Time) error {
	fmt.Println("--- News (Finnhub + GDELT, since last as_of_date) ---")

	lastNews, err := mdb.LastNewsDate()
	if err != nil {
		return err
	}
	from := windowStart
	if !lastNews.IsZero() {
		from = lastNews
	}

	apiKey := utils.GetEnv("FINNHUB_API_KEY")
	fh, err := core.NewFinnhubClient(apiKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: finnhub skipped: %v\n", err)
	} else {
		// Recent general feed (incremental via minId when possible).
		maxID, _ := mdb.MaxNewsID()
		articles, err := fh.FetchMarketNews("general", maxID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: finnhub general: %v\n", err)
		} else if len(articles) == 0 && maxID > 0 {
			articles, err = fh.FetchMarketNews("general", 0)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: finnhub general refresh: %v\n", err)
			}
		}
		if err == nil && len(articles) > 0 {
			if err := mdb.UpsertNews(articles); err != nil {
				return err
			}
			fmt.Printf("finnhub general: %d articles\n", len(articles))
		}

		// Dated company/ETF news from last stored day → today (30d chunks).
		for _, s := range core.SeedSymbols {
			for chunkStart := from; !chunkStart.After(today); {
				chunkEnd := chunkStart.AddDate(0, 0, 30)
				if chunkEnd.After(today) {
					chunkEnd = today
				}
				articles, err := fh.FetchCompanyNews(s.Symbol, chunkStart, chunkEnd)
				if err != nil {
					fmt.Fprintf(os.Stderr, "warning: finnhub %s %s→%s: %v\n",
						s.Symbol, chunkStart.Format("2006-01-02"), chunkEnd.Format("2006-01-02"), err)
				} else {
					if err := mdb.UpsertNews(articles); err != nil {
						return err
					}
					fmt.Printf("finnhub %s %s→%s: %d articles\n",
						s.Symbol, chunkStart.Format("2006-01-02"), chunkEnd.Format("2006-01-02"), len(articles))
				}
				time.Sleep(1200 * time.Millisecond)
				if chunkEnd.Equal(today) {
					break
				}
				chunkStart = chunkEnd.AddDate(0, 0, 1)
			}
		}
	}

	// GDELT: fill calendar days from last news (or window start) through today.
	gdeltFrom := from
	if !lastNews.IsZero() {
		gdeltFrom = lastNews.AddDate(0, 0, 1)
	}
	if gdeltFrom.Before(windowStart) {
		gdeltFrom = windowStart
	}
	gdelt := core.NewGDELTClient()
	gdeltN := 0
	for d := gdeltFrom; !d.After(today); d = d.AddDate(0, 0, 1) {
		day := d.Format("2006-01-02")
		articles, err := gdelt.FetchDay(d)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: gdelt %s: %v\n", day, err)
			continue
		}
		if err := mdb.UpsertNews(articles); err != nil {
			return err
		}
		gdeltN += len(articles)
		fmt.Printf("gdelt %s: %d articles\n", day, len(articles))
	}
	fmt.Printf("gdelt total upserted this run: %d\n", gdeltN)
	return nil
}
