package db

import (
	"fmt"
	"os"
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
	apiKey := utils.GetEnv("FINNHUB_API_KEY")
	client, err := core.NewFinnhubClient(apiKey)
	if err != nil {
		return err
	}

	mdb, err := OpenMarketDB(path)
	if err != nil {
		return err
	}
	defer mdb.Close()

	maxID, err := mdb.MaxNewsID()
	if err != nil {
		return err
	}

	// First fill: minId=0 (latest batch). Later runs: minId=maxID to prefer newer ids.
	minID := maxID
	fmt.Printf("fetching Finnhub market news category=general (minId=%d)…\n", minID)
	articles, err := client.FetchMarketNews("general", minID)
	if err != nil {
		return err
	}
	if len(articles) == 0 && minID > 0 {
		// No newer than max; refresh latest batch so summaries/headlines stay current.
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

	stats, err := mdb.Stats()
	if err != nil {
		return err
	}
	fmt.Println("news ingest complete:", stats)
	return nil
}

func printIngestPlan() {
	fmt.Print(`
Next steps:

  go run . ingest-db     # Yahoo OHLCV max history → SQLite
  go run . ingest-news   # Finnhub general market news → SQLite (shared for SPY/DIA)

News is one market feed aligned by as_of_date (US/Eastern), not per-ETF duplicates.
`)
}

// RunDBCommand handles CLI subcommands init-db / ingest-db / ingest-news.
// Returns true if a DB command was recognized and handled.
func RunDBCommand(args []string) bool {
	if len(args) < 1 {
		return false
	}
	switch args[0] {
	case "init-db":
		path := DefaultDBPath
		if len(args) > 1 {
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
		if len(args) > 1 {
			path = args[1]
		}
		if err := IngestMarketDB(path); err != nil {
			fmt.Fprintf(os.Stderr, "ingest-db failed: %v\n", err)
			os.Exit(1)
		}
		return true
	case "ingest-news":
		path := DefaultDBPath
		if len(args) > 1 {
			path = args[1]
		}
		if err := IngestMarketNews(path); err != nil {
			fmt.Fprintf(os.Stderr, "ingest-news failed: %v\n", err)
			os.Exit(1)
		}
		return true
	default:
		return false
	}
}
