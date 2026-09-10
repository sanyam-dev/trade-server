package main

import (
	"fmt"
	"net/http"
	"os"

	"myServer/trade-server/db"
	_ "myServer/trade-server/utils" // load .env via init
	"myServer/trade-server/web"
)

func main() {
	if db.RunDBCommand(os.Args[1:]) {
		return
	}

	webDir := web.ResolveWebDir()
	dbPath := db.DefaultDBPath

	web.RegisterWebSocket()
	web.RegisterStaticAndAPI(webDir, dbPath)

	fmt.Println("trade-server listening on :8080")
	fmt.Println("  UI:  http://localhost:8080/")
	fmt.Println("  WS:  ws://localhost:8080/ws")
	fmt.Println("  API: http://localhost:8080/api/bars?symbol=SPY&interval=daily")
	fmt.Println("  News: http://localhost:8080/api/news?date=YYYY-MM-DD")
	fmt.Println("static:", webDir)
	fmt.Println("init local OHLCV db (no API): go run . init-db")
	fmt.Println("pull Yahoo max-history into SQLite: go run . ingest-db")
	fmt.Println("daily refresh (bounded Yahoo + news): go run . ingest-daily")
	fmt.Println("pull Finnhub market news into SQLite: go run . ingest-news")
	fmt.Println("pull GDELT market news into SQLite:  go run . ingest-gdelt --from=YYYY-MM-DD --to=YYYY-MM-DD")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
