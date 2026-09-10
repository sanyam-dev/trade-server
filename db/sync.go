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

// UpdateSinceLast pulls OHLCV since last bar timestamps, backfills news gaps in the
// training window, then trims to TrainingWindowMonths. Safe to re-run (upserts).
func UpdateSinceLast(path string) error {
	started := time.Now()
	fmt.Println("=== update ===")

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
	if err := updateNewsGaps(mdb, windowStart, today); err != nil {
		return err
	}

	cutoff, deleted, err := mdb.TrimToMonths(TrainingWindowMonths)
	if err != nil {
		return err
	}
	fmt.Printf("trim: kept >= %s (deleted daily=%d weekly=%d monthly=%d news=%d)\n",
		cutoff, deleted["ohlcv_daily"], deleted["ohlcv_weekly"], deleted["ohlcv_monthly"], deleted["market_news"])

	stats, err := mdb.Stats()
	if err != nil {
		return err
	}
	fmt.Println("=== update complete ===")
	fmt.Println(stats)
	fmt.Printf("elapsed: %s\n", time.Since(started).Round(time.Second))
	return nil
}

func updateBarsSinceLast(mdb *MarketDB, windowStart, today time.Time) error {
	client := core.NewYahooClient()
	intervals := []core.Interval{core.IntervalDaily, core.IntervalWeekly, core.IntervalMonthly}
	fmt.Println("--- OHLCV (Yahoo) ---")

	for i, s := range core.SeedSymbols {
		for j, interval := range intervals {
			last, err := mdb.LastBarDate(s.Symbol, interval)
			if err != nil {
				return err
			}
			from := windowStart
			if !last.IsZero() {
				from = last
			}
			fmt.Printf("%s %s %s → %s\n", s.Symbol, interval, from.Format("2006-01-02"), today.Format("2006-01-02"))
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

// updateNewsGaps fills trading days in the window that have zero headlines.
// Finnhub general feed is recent-only (~1–2 days); company-news + GDELT cover gaps.
func updateNewsGaps(mdb *MarketDB, windowStart, today time.Time) error {
	fmt.Println("--- News (backfill gaps in training window) ---")

	missing, err := mdb.MissingNewsDates(windowStart, today)
	if err != nil {
		return err
	}
	fmt.Printf("trading days missing news: %d\n", len(missing))

	apiKey := utils.GetEnv("FINNHUB_API_KEY")
	fh, err := core.NewFinnhubClient(apiKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: finnhub skipped: %v\n", err)
	} else {
		// Always refresh recent general batch (API only returns latest headlines).
		articles, err := fh.FetchMarketNews("general", 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: finnhub general: %v\n", err)
		} else if err := mdb.UpsertNews(articles); err != nil {
			return err
		} else {
			fmt.Printf("finnhub general: %d articles\n", len(articles))
		}

		// Company/ETF dated news for gap days only. Finnhub caps ~250 results/request, so
		// long windows truncate older days — use short chunks that overlap missing dates.
		if len(missing) > 0 {
			const chunkDays = 7
			missingSet := make(map[string]struct{}, len(missing))
			for _, d := range missing {
				missingSet[d.Format("2006-01-02")] = struct{}{}
			}
			from := missing[0]
			for _, s := range core.SeedSymbols {
				for chunkStart := from; !chunkStart.After(today); {
					chunkEnd := chunkStart.AddDate(0, 0, chunkDays-1)
					if chunkEnd.After(today) {
						chunkEnd = today
					}
					if !chunkOverlapsMissing(chunkStart, chunkEnd, missingSet) {
						chunkStart = chunkEnd.AddDate(0, 0, 1)
						continue
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
	}

	// Recompute gaps after Finnhub, then GDELT only those days (rate-limited).
	missing, err = mdb.MissingNewsDates(windowStart, today)
	if err != nil {
		return err
	}
	fmt.Printf("days still missing after Finnhub: %d — filling with GDELT\n", len(missing))

	gdelt := core.NewGDELTClient()
	gdeltN := 0
	for _, d := range missing {
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
	fmt.Printf("gdelt upserted: %d articles across %d days\n", gdeltN, len(missing))
	return nil
}

func chunkOverlapsMissing(start, end time.Time, missing map[string]struct{}) bool {
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if _, ok := missing[d.Format("2006-01-02")]; ok {
			return true
		}
	}
	return false
}
