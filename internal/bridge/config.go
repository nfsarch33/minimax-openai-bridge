package bridge

import (
	"os"
	"time"
)

type Config struct {
	ListenAddr     string
	MiniMaxBaseURL string
	APIKey         string
	GroupID        string
	Model          string
	DefaultType    string
	Timeout        time.Duration
}

func LoadConfig() Config {
	return Config{
		ListenAddr:     getenv("BRIDGE_ADDR", "127.0.0.1:8500"),
		MiniMaxBaseURL: getenv("MINIMAX_BASE_URL", "https://api.minimax.chat/v1"),
		APIKey:         os.Getenv("MINIMAX_API_KEY"),
		GroupID:        os.Getenv("MINIMAX_GROUP_ID"),
		Model:          getenv("MINIMAX_EMBEDDING_MODEL", "embo-01"),
		DefaultType:    getenv("MINIMAX_EMBEDDING_TYPE", "db"),
		Timeout:        getenvDuration("MINIMAX_TIMEOUT", 30*time.Second),
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getenvDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
