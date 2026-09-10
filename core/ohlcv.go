package core

import "time"

// Interval identifies which OHLCV table a bar belongs to.
type Interval string

const (
	IntervalDaily   Interval = "daily"
	IntervalWeekly  Interval = "weekly"
	IntervalMonthly Interval = "monthly"
)

// SymbolMeta maps a tradeable ETF symbol to the index it proxies for training data.
type SymbolMeta struct {
	Symbol         string // e.g. SPY, DIA
	IndexName      string // e.g. S&P 500, Dow Jones Industrial Average
	InstrumentType string // etf_proxy
	Notes          string
}

// OHLCVBar is one open/high/low/close/volume row for a symbol + date.
type OHLCVBar struct {
	Symbol    string
	Date      time.Time // calendar date (UTC midnight of YYYY-MM-DD)
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    int64
	Source    string
	FetchedAt time.Time
}

// SeedSymbols are ETF index proxies used for Yahoo OHLCV training data.
var SeedSymbols = []SymbolMeta{
	{
		Symbol:         "SPY",
		IndexName:      "S&P 500",
		InstrumentType: "etf_proxy",
		Notes:          "SPDR S&P 500 ETF — proxy for S&P 500 index training data",
	},
	{
		Symbol:         "DIA",
		IndexName:      "Dow Jones Industrial Average",
		InstrumentType: "etf_proxy",
		Notes:          "SPDR Dow Jones Industrial Average ETF — proxy for DJIA training data",
	},
}
