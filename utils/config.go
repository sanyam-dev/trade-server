package utils

import (
	"os"

	"github.com/joho/godotenv"
)

func init() {
	// Load first existing file; ignore if missing (tests / CI).
	_ = godotenv.Load(".env")
	_ = godotenv.Load("trade-server/.env")
}

// GetEnv returns the value of the environment variable named by key.
func GetEnv(key string) string {
	return os.Getenv(key)
}
