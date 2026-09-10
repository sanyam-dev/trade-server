package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"myServer/trade-server/core"
	"myServer/trade-server/db"
)

// barJSON is the wire format shared by /api/bars and sample JSON files.
type barJSON struct {
	Time   string  `json:"time"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume int64   `json:"volume"`
}

type barsResponse struct {
	Symbol   string    `json:"symbol"`
	Interval string    `json:"interval"`
	Source   string    `json:"source"`
	Bars     []barJSON `json:"bars"`
}

// ResolveWebDir finds the static UI directory (web/ next to this package or trade-server/web).
func ResolveWebDir() string {
	candidates := []string{"web", "trade-server/web"}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			abs, err := filepath.Abs(c)
			if err == nil {
				return abs
			}
			return c
		}
	}
	return "web"
}

type newsItemJSON struct {
	ID       int64  `json:"id"`
	Category string `json:"category"`
	Datetime int64  `json:"datetime"`
	AsOfDate string `json:"as_of_date"`
	Headline string `json:"headline"`
	Summary  string `json:"summary"`
	Source   string `json:"source"`
	URL      string `json:"url"`
	Related  string `json:"related"`
}

type newsResponse struct {
	Date  string         `json:"date"`
	Count int            `json:"count"`
	News  []newsItemJSON `json:"news"`
}

// RegisterStaticAndAPI mounts /api/bars, /api/news, and the static file server.
func RegisterStaticAndAPI(webDir string, dbPath string) {
	mux := http.DefaultServeMux

	mux.HandleFunc("/api/bars", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		symbol := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("symbol")))
		if symbol == "" {
			symbol = "SPY"
		}
		intervalStr := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("interval")))
		if intervalStr == "" {
			intervalStr = "daily"
		}
		interval := core.Interval(intervalStr)

		resp, err := loadBarsResponse(webDir, dbPath, symbol, interval)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/api/news", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		date := strings.TrimSpace(r.URL.Query().Get("date"))
		if date == "" {
			http.Error(w, "missing date (YYYY-MM-DD)", http.StatusBadRequest)
			return
		}
		if len(date) != 10 {
			http.Error(w, "date must be YYYY-MM-DD", http.StatusBadRequest)
			return
		}

		resp, err := loadNewsResponse(dbPath, date)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(resp)
	})

	fs := http.FileServer(http.Dir(webDir))
	mux.Handle("/", fs)
}

func loadNewsResponse(dbPath, date string) (newsResponse, error) {
	mdb, err := db.OpenMarketDB(dbPath)
	if err != nil {
		return newsResponse{}, err
	}
	defer mdb.Close()

	rows, err := mdb.GetNewsByDate(date)
	if err != nil {
		return newsResponse{}, err
	}
	out := make([]newsItemJSON, 0, len(rows))
	for _, a := range rows {
		out = append(out, newsItemJSON{
			ID:       a.ID,
			Category: a.Category,
			Datetime: a.Datetime.Unix(),
			AsOfDate: a.AsOfDate.UTC().Format("2006-01-02"),
			Headline: a.Headline,
			Summary:  a.Summary,
			Source:   a.Source,
			URL:      a.URL,
			Related:  a.Related,
		})
	}
	return newsResponse{Date: date, Count: len(out), News: out}, nil
}

func loadBarsResponse(webDir, dbPath, symbol string, interval core.Interval) (barsResponse, error) {
	if bars, err := tryLoadFromDB(dbPath, symbol, interval); err == nil && len(bars) > 0 {
		return barsResponse{
			Symbol:   symbol,
			Interval: string(interval),
			Source:   "sqlite",
			Bars:     bars,
		}, nil
	}

	sample, err := loadSampleBars(webDir, symbol, interval)
	if err != nil {
		return barsResponse{}, fmt.Errorf("no sqlite bars and sample fallback failed: %w", err)
	}
	return sample, nil
}

func tryLoadFromDB(dbPath, symbol string, interval core.Interval) ([]barJSON, error) {
	mdb, err := db.OpenMarketDB(dbPath)
	if err != nil {
		return nil, err
	}
	defer mdb.Close()

	rows, err := mdb.GetBars(symbol, interval)
	if err != nil {
		return nil, err
	}
	out := make([]barJSON, 0, len(rows))
	for _, b := range rows {
		out = append(out, barJSON{
			Time:   b.Date.UTC().Format("2006-01-02"),
			Open:   b.Open,
			High:   b.High,
			Low:    b.Low,
			Close:  b.Close,
			Volume: b.Volume,
		})
	}
	return out, nil
}

func loadSampleBars(webDir, symbol string, interval core.Interval) (barsResponse, error) {
	name := fmt.Sprintf("%s_%s.json", strings.ToLower(symbol), string(interval))
	path := filepath.Join(webDir, "data", name)
	f, err := os.Open(path)
	if err != nil {
		return barsResponse{}, fmt.Errorf("open sample %s: %w", path, err)
	}
	defer f.Close()

	var raw struct {
		Symbol   string    `json:"symbol"`
		Interval string    `json:"interval"`
		Source   string    `json:"source"`
		Bars     []barJSON `json:"bars"`
	}
	if err := json.NewDecoder(f).Decode(&raw); err != nil {
		return barsResponse{}, err
	}
	src := raw.Source
	if src == "" {
		src = "sample"
	}
	sym := raw.Symbol
	if sym == "" {
		sym = symbol
	}
	iv := raw.Interval
	if iv == "" {
		iv = string(interval)
	}
	return barsResponse{
		Symbol:   strings.ToUpper(sym),
		Interval: iv,
		Source:   src,
		Bars:     raw.Bars,
	}, nil
}
