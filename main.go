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
	fmt.Println("  UI:     http://localhost:8080/")
	fmt.Println("  bars:   http://localhost:8080/api/bars?symbol=SPY&interval=daily")
	fmt.Println("  news:   http://localhost:8080/api/news?date=YYYY-MM-DD")
	fmt.Println("  CLI:    go run . {init-db|update|trim-db|status}")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
