package core

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"myServer/trade-server/utils"
)

const yahooChartBaseURL = "https://query1.finance.yahoo.com/v8/finance/chart/"

// YahooClient fetches max-history OHLCV from Yahoo Finance chart API (no key).
// Sole OHLCV ingest path for RL harness training data.
type YahooClient struct {
	HTTPClient *http.Client
}

// NewYahooClient returns a Yahoo chart API client with a 90s timeout.
func NewYahooClient() *YahooClient {
	return &YahooClient{
		HTTPClient: &http.Client{Timeout: 90 * time.Second},
	}
}

// FetchMaxHistory pulls the longest available series for interval (1d / 1wk / 1mo).
func (c *YahooClient) FetchMaxHistory(symbol string, interval Interval) ([]OHLCVBar, error) {
	yInterval, err := yahooInterval(interval)
	if err != nil {
		return nil, err
	}

	// SPY listing starts ~1993; request well before that for "max".
	period1 := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	period2 := time.Now().UTC().Unix()

	u, err := url.Parse(yahooChartBaseURL + url.PathEscape(symbol))
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("interval", yInterval)
	q.Set("period1", strconv.FormatInt(period1, 10))
	q.Set("period2", strconv.FormatInt(period2, 10))
	q.Set("includePrePost", "false")
	q.Set("events", "div,splits")
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "trade-server/1.0 (rl-harness)")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("yahoo GET %s: %w", symbol, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("yahoo read %s: %w", symbol, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yahoo HTTP %d for %s: %s", resp.StatusCode, symbol, utils.Truncate(string(body), 200))
	}

	var payload struct {
		Chart struct {
			Result []struct {
				Timestamp  []int64 `json:"timestamp"`
				Indicators struct {
					Quote []struct {
						Open   []*float64 `json:"open"`
						High   []*float64 `json:"high"`
						Low    []*float64 `json:"low"`
						Close  []*float64 `json:"close"`
						Volume []*int64   `json:"volume"`
					} `json:"quote"`
				} `json:"indicators"`
			} `json:"result"`
			Error *struct {
				Code        string `json:"code"`
				Description string `json:"description"`
			} `json:"error"`
		} `json:"chart"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("yahoo json %s: %w", symbol, err)
	}
	if payload.Chart.Error != nil {
		return nil, fmt.Errorf("yahoo error %s: %s (%s)", symbol, payload.Chart.Error.Code, payload.Chart.Error.Description)
	}
	if len(payload.Chart.Result) == 0 {
		return nil, fmt.Errorf("yahoo empty result for %s", symbol)
	}
	res := payload.Chart.Result[0]
	if len(res.Indicators.Quote) == 0 {
		return nil, fmt.Errorf("yahoo missing quote for %s", symbol)
	}
	quote := res.Indicators.Quote[0]
	n := len(res.Timestamp)
	if len(quote.Open) != n || len(quote.High) != n || len(quote.Low) != n || len(quote.Close) != n {
		return nil, fmt.Errorf("yahoo length mismatch for %s", symbol)
	}

	now := time.Now().UTC()
	out := make([]OHLCVBar, 0, n)
	for i := 0; i < n; i++ {
		if quote.Open[i] == nil || quote.High[i] == nil || quote.Low[i] == nil || quote.Close[i] == nil {
			continue
		}
		var vol int64
		if i < len(quote.Volume) && quote.Volume[i] != nil {
			vol = *quote.Volume[i]
		}
		day := time.Unix(res.Timestamp[i], 0).UTC()
		day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
		out = append(out, OHLCVBar{
			Symbol:    symbol,
			Date:      day,
			Open:      *quote.Open[i],
			High:      *quote.High[i],
			Low:       *quote.Low[i],
			Close:     *quote.Close[i],
			Volume:    vol,
			Source:    "yahoo",
			FetchedAt: now,
		})
	}
	return out, nil
}

func yahooInterval(interval Interval) (string, error) {
	switch interval {
	case IntervalDaily:
		return "1d", nil
	case IntervalWeekly:
		return "1wk", nil
	case IntervalMonthly:
		return "1mo", nil
	default:
		return "", fmt.Errorf("unsupported yahoo interval %q", interval)
	}
}
