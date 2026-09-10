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
		HTTPClient: &http.Client{Timeout: 45 * time.Second},
	}, nil
}

// FetchMarketNews pulls general (or other) category market news (recent feed).
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
	return c.getNewsArticles(u.String(), "")
}

// FetchCompanyNews pulls dated company/ETF news for symbol between from and to (inclusive YYYY-MM-DD).
// Free plans typically allow about one year of company-news history.
func (c *FinnhubClient) FetchCompanyNews(symbol string, from, to time.Time) ([]MarketNewsArticle, error) {
	if symbol == "" {
		return nil, fmt.Errorf("symbol required")
	}
	u, err := url.Parse(finnhubBaseURL + "/company-news")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("symbol", symbol)
	q.Set("from", from.UTC().Format("2006-01-02"))
	q.Set("to", to.UTC().Format("2006-01-02"))
	q.Set("token", c.APIKey)
	u.RawQuery = q.Encode()
	return c.getNewsArticles(u.String(), symbol)
}

func (c *FinnhubClient) getNewsArticles(rawURL, defaultRelated string) ([]MarketNewsArticle, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Finnhub-Token", c.APIKey)
	req.Header.Set("User-Agent", "trade-server/1.0 (rl-harness)")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("finnhub GET: %w", err)
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

	now := time.Now().UTC()
	out := make([]MarketNewsArticle, 0, len(raw))
	for _, a := range raw {
		if a.ID == 0 || a.Headline == "" {
			continue
		}
		pub := time.Unix(a.Datetime, 0).UTC()
		related := a.Related
		if related == "" {
			related = defaultRelated
		}
		cat := a.Category
		if cat == "" && defaultRelated != "" {
			cat = "company"
		}
		out = append(out, MarketNewsArticle{
			ID:        a.ID,
			Category:  cat,
			Datetime:  pub,
			AsOfDate:  AsOfDateNY(pub),
			Headline:  a.Headline,
			Summary:   a.Summary,
			Source:    a.Source,
			URL:       a.URL,
			Image:     a.Image,
			Related:   related,
			FetchedAt: now,
		})
	}
	return out, nil
}
