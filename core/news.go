package core

import "time"

// MarketNewsArticle is one market headline row (Finnhub or GDELT), shared across SPY/DIA.
type MarketNewsArticle struct {
	ID        int64
	Category  string // e.g. Finnhub category or "gdelt_market"
	Datetime  time.Time // publication / seen instant (UTC)
	AsOfDate  time.Time // US/Eastern calendar day for sim alignment (UTC midnight of that date)
	Headline  string
	Summary   string
	Source    string
	URL       string
	Image     string
	Related   string
	FetchedAt time.Time
}

// USEastern returns America/New_York, or a fixed EST fallback.
func USEastern() *time.Location {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.FixedZone("EST", -5*3600)
	}
	return ny
}

// AsOfDateNY maps an instant to the US/Eastern calendar day (UTC midnight of that date).
func AsOfDateNY(t time.Time) time.Time {
	local := t.In(USEastern())
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
}
