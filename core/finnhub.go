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

const finnhubBaseURL = "https://finnhub.io/api/v1"

// FinnhubClient fetches market news from Finnhub (token from FINNHUB_API_KEY).
type FinnhubClient struct {
	APIKey     string
	HTTPClient *http.Client
}

// NewFinnhubClient builds a client. apiKey must be non-empty.
func NewFinnhubClient(apiKey string) (*FinnhubClient, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("FINNHUB_API_KEY is empty")
	}
	return &FinnhubClient{
		APIKey:     apiKey,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// FetchMarketNews pulls general (or other) category market news.
// minID 0 returns the latest batch; use last seen id to page newer items.
func (c *FinnhubClient) FetchMarketNews(category string, minID int64) ([]MarketNewsArticle, error) {
	if category == "" {
		category = "general"
	}

	u, err := url.Parse(finnhubBaseURL + "/news")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("category", category)
	if minID > 0 {
		q.Set("minId", strconv.FormatInt(minID, 10))
	}
	q.Set("token", c.APIKey)
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Finnhub-Token", c.APIKey)
	req.Header.Set("User-Agent", "trade-server/1.0 (rl-harness)")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("finnhub GET /news: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("finnhub read: %w", err)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("finnhub rate limited (429)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("finnhub HTTP %d: %s", resp.StatusCode, utils.Truncate(string(body), 200))
	}

	var raw []struct {
		Category string `json:"category"`
		Datetime int64  `json:"datetime"`
		Headline string `json:"headline"`
		ID       int64  `json:"id"`
		Image    string `json:"image"`
		Related  string `json:"related"`
		Source   string `json:"source"`
		Summary  string `json:"summary"`
		URL      string `json:"url"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("finnhub json: %w; body=%s", err, utils.Truncate(string(body), 200))
	}

	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		ny = time.FixedZone("EST", -5*3600)
	}

	now := time.Now().UTC()
	out := make([]MarketNewsArticle, 0, len(raw))
	for _, a := range raw {
		if a.ID == 0 || a.Headline == "" {
			continue
		}
		pub := time.Unix(a.Datetime, 0).UTC()
		local := pub.In(ny)
		asOf := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
		out = append(out, MarketNewsArticle{
			ID:        a.ID,
			Category:  a.Category,
			Datetime:  pub,
			AsOfDate:  asOf,
			Headline:  a.Headline,
			Summary:   a.Summary,
			Source:    a.Source,
			URL:       a.URL,
			Image:     a.Image,
			Related:   a.Related,
			FetchedAt: now,
		})
	}
	return out, nil
}
