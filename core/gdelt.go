package core

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"myServer/trade-server/utils"
)

const gdeltDocURL = "https://api.gdeltproject.org/api/v2/doc/doc"

// DefaultGDELTMarketQuery targets English US-relevant equity/macro coverage for SPY/DIA study days.
const DefaultGDELTMarketQuery = `("S&P 500" OR "Dow Jones" OR "Federal Reserve" OR "stock market" OR Nasdaq) sourcelang:english`

// GDELTClient fetches article lists from the GDELT DOC 2.0 API (no API key).
// DOC searchable history is roughly the last ~3 months; be polite (≤1 req / 5s).
type GDELTClient struct {
	HTTPClient *http.Client
	Query      string
	MaxRecords int
	MinInterval time.Duration
	lastRequest time.Time
}

// NewGDELTClient returns a DOC API client with conservative defaults.
func NewGDELTClient() *GDELTClient {
	return &GDELTClient{
		HTTPClient:  &http.Client{Timeout: 60 * time.Second},
		Query:       DefaultGDELTMarketQuery,
		MaxRecords:  75,
		MinInterval: 6 * time.Second,
	}
}

// FetchDay pulls market articles seen on calendar day d (UTC date window).
func (c *GDELTClient) FetchDay(d time.Time) ([]MarketNewsArticle, error) {
	day := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	start := day.Format("20060102150405")
	end := day.Add(24*time.Hour - time.Second).Format("20060102150405")
	return c.fetch(start, end)
}

func (c *GDELTClient) fetch(start, end string) ([]MarketNewsArticle, error) {
	if err := c.waitRateLimit(); err != nil {
		return nil, err
	}

	q := c.Query
	if q == "" {
		q = DefaultGDELTMarketQuery
	}
	max := c.MaxRecords
	if max <= 0 || max > 250 {
		max = 75
	}

	u, err := url.Parse(gdeltDocURL)
	if err != nil {
		return nil, err
	}
	params := u.Query()
	params.Set("query", q)
	params.Set("mode", "ArtList")
	params.Set("format", "json")
	params.Set("sort", "DateDesc")
	params.Set("maxrecords", fmt.Sprintf("%d", max))
	params.Set("startdatetime", start)
	params.Set("enddatetime", end)
	u.RawQuery = params.Encode()

	var body []byte
	var status int
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt+1) * 5 * time.Second)
			if err := c.waitRateLimit(); err != nil {
				return nil, err
			}
		}
		req, err := http.NewRequest(http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "trade-server/1.0 (rl-harness; gdelt-doc)")
		req.Header.Set("Accept", "application/json")

		c.lastRequest = time.Now()
		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("gdelt GET: %w", err)
			continue // TLS/timeouts are common; soft-retry then soft-fail upstream
		}
		body, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("gdelt read: %w", err)
			continue
		}
		status = resp.StatusCode
		lastErr = nil
		if status == http.StatusTooManyRequests || looksLikeGDELTRateLimit(body) {
			lastErr = fmt.Errorf("gdelt rate limited")
			continue
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("gdelt HTTP %d: %s", status, utils.Truncate(string(body), 240))
		}
		break
	}
	if lastErr != nil {
		return nil, lastErr
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("gdelt HTTP %d after retries", status)
	}

	var payload struct {
		Articles []struct {
			URL           string `json:"url"`
			URLMobile     string `json:"url_mobile"`
			Title         string `json:"title"`
			SeenDate      string `json:"seendate"`
			SocialImage   string `json:"socialimage"`
			Domain        string `json:"domain"`
			Language      string `json:"language"`
			SourceCountry string `json:"sourcecountry"`
		} `json:"articles"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("gdelt json: %w; body=%s", err, utils.Truncate(string(body), 240))
	}

	now := time.Now().UTC()
	out := make([]MarketNewsArticle, 0, len(payload.Articles))
	seen := make(map[int64]struct{}, len(payload.Articles))
	for _, a := range payload.Articles {
		link := strings.TrimSpace(a.URL)
		if link == "" {
			link = strings.TrimSpace(a.URLMobile)
		}
		title := strings.TrimSpace(a.Title)
		if link == "" || title == "" {
			continue
		}
		pub, err := parseGDELTSeenDate(a.SeenDate)
		if err != nil {
			continue
		}
		id := gdeltArticleID(link)
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}

		related := strings.TrimSpace(strings.Join([]string{a.Language, a.SourceCountry}, "|"))
		src := strings.TrimSpace(a.Domain)
		if src == "" {
			src = "gdelt"
		}
		out = append(out, MarketNewsArticle{
			ID:        id,
			Category:  "gdelt_market",
			Datetime:  pub,
			AsOfDate:  AsOfDateNY(pub),
			Headline:  title,
			Summary:   "",
			Source:    src,
			URL:       link,
			Image:     a.SocialImage,
			Related:   related,
			FetchedAt: now,
		})
	}
	return out, nil
}

func (c *GDELTClient) waitRateLimit() error {
	if c.MinInterval <= 0 {
		c.MinInterval = 6 * time.Second
	}
	if c.lastRequest.IsZero() {
		return nil
	}
	wait := c.MinInterval - time.Since(c.lastRequest)
	if wait > 0 {
		time.Sleep(wait)
	}
	return nil
}

func looksLikeGDELTRateLimit(body []byte) bool {
	s := strings.ToLower(string(body))
	return strings.Contains(s, "limit requests") || strings.Contains(s, "one every 5 seconds")
}

func parseGDELTSeenDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty seendate")
	}
	// Common DOC form: 20251016T153045Z
	if t, err := time.Parse("20060102T150405Z", s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("20060102150405", s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("unrecognized seendate %q", s)
}

func gdeltArticleID(articleURL string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(articleURL))
	v := h.Sum64() & 0x7fffffffffffffff
	if v == 0 {
		v = 1
	}
	return int64(v)
}
