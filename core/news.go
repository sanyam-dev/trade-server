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
