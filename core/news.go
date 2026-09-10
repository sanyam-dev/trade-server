package core

import "time"

// MarketNewsArticle is one Finnhub general market news item, shared across SPY/DIA.
type MarketNewsArticle struct {
	ID        int64
	Category  string
	Datetime  time.Time // publication instant (UTC)
	AsOfDate  time.Time // US/Eastern calendar day for sim alignment (UTC midnight of that date)
	Headline  string
	Summary   string
	Source    string
	URL       string
	Image     string
	Related   string
	FetchedAt time.Time
}
