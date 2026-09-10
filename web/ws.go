package web

import (
	"fmt"
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// RegisterWebSocket mounts the /ws stub upgrade handler on DefaultServeMux.
func RegisterWebSocket() {
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			fmt.Println("Failed to upgrade to WebSocket:", err)
			return
		}
		defer conn.Close()
		// Stub: accept the upgrade and hold the connection.
		// Later: stream OHLCV ticks / sim events to the chart UI.
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
}
