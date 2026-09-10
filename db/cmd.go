package db

import (
	"fmt"
	"os"
	"strings"

	"myServer/trade-server/core"
)

// InitMarketDB creates schema + seeds symbols (Create). No external API calls.
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
	for _, s := range core.SeedSymbols {
		fmt.Printf("  %s -> %s\n", s.Symbol, s.IndexName)
	}
	return nil
}

// TrimMarketDB deletes rows older than the training window (Delete).
func TrimMarketDB(path string, months int) error {
	if months <= 0 {
		months = TrainingWindowMonths
	}
	mdb, err := OpenMarketDB(path)
	if err != nil {
		return err
	}
	defer mdb.Close()

	before, _ := mdb.Stats()
	fmt.Println("before:", before)
	cutoff, deleted, err := mdb.TrimToMonths(months)
	if err != nil {
		return err
	}
	fmt.Printf("kept date >= %s (last %d months)\n", cutoff, months)
	for k, v := range deleted {
		fmt.Printf("  deleted %s: %d\n", k, v)
	}
	after, _ := mdb.Stats()
	fmt.Println("after:", after)
	return nil
}

func printHelp() {
	fmt.Print(`Commands:

  go run . init-db                 # Create schema + seed SPY/DIA
  go run . update                  # Fetch since last DB timestamps, then trim to 3 months
  go run . sync                    # Alias for update
  go run . trim-db [--months=3]    # Delete older than training window
  go run . status                  # Print counts / last dates

Training window: last 3 months (buy/sell/hold).
`)
}

func argDBPath(args []string) string {
	if len(args) > 1 && !strings.HasPrefix(args[1], "--") {
		return args[1]
	}
	return DefaultDBPath
}

func exitErr(cmd string, err error) {
	fmt.Fprintf(os.Stderr, "%s failed: %v\n", cmd, err)
	os.Exit(1)
}

// RunDBCommand handles: init-db | update | sync | trim-db | status
func RunDBCommand(args []string) bool {
	if len(args) < 1 {
		return false
	}
	switch args[0] {
	case "help", "-h", "--help":
		printHelp()
		return true

	case "init-db":
		if err := InitMarketDB(argDBPath(args)); err != nil {
			exitErr("init-db", err)
		}
		return true

	case "update", "sync":
		if err := UpdateSinceLast(argDBPath(args)); err != nil {
			exitErr("update", err)
		}
		return true

	case "trim-db":
		path := DefaultDBPath
		months := TrainingWindowMonths
		for i := 1; i < len(args); i++ {
			a := args[i]
			switch {
			case strings.HasPrefix(a, "--months="):
				fmt.Sscanf(strings.TrimPrefix(a, "--months="), "%d", &months)
			case !strings.HasPrefix(a, "--") && path == DefaultDBPath:
				path = a
			}
		}
		if err := TrimMarketDB(path, months); err != nil {
			exitErr("trim-db", err)
		}
		return true

	case "status":
		mdb, err := OpenMarketDB(argDBPath(args))
		if err != nil {
			exitErr("status", err)
		}
		defer mdb.Close()
		stats, err := mdb.Stats()
		if err != nil {
			exitErr("status", err)
		}
		fmt.Println(stats)
		for _, s := range core.SeedSymbols {
			for _, iv := range []core.Interval{core.IntervalDaily, core.IntervalWeekly, core.IntervalMonthly} {
				last, _ := mdb.LastBarDate(s.Symbol, iv)
				if last.IsZero() {
					fmt.Printf("  %s %s: (empty)\n", s.Symbol, iv)
				} else {
					fmt.Printf("  %s %s last: %s\n", s.Symbol, iv, last.Format("2006-01-02"))
				}
			}
		}
		return true

	default:
		return false
	}
}
