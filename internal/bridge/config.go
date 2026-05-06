package bridge

import (
	"os"
	"strings"
	"time"
)

type Config struct {
	ListenAddr     string
	MiniMaxBaseURL string
	APIKey         string
	APIKeys        []string
	GroupID        string
	Model          string
	DefaultType    string
	Timeout        time.Duration
}

func LoadConfig() Config {
	return Config{
		ListenAddr:     getenv("BRIDGE_ADDR", "127.0.0.1:8500"),
		MiniMaxBaseURL: getenv("MINIMAX_BASE_URL", "https://api.minimax.chat/v1"),
		APIKey:         firstAPIKey(),
		APIKeys:        loadAPIKeys(),
		GroupID:        os.Getenv("MINIMAX_GROUP_ID"),
		Model:          getenv("MINIMAX_EMBEDDING_MODEL", "embo-01"),
		DefaultType:    getenv("MINIMAX_EMBEDDING_TYPE", "db"),
		Timeout:        getenvDuration("MINIMAX_TIMEOUT", 30*time.Second),
	}
}

func loadAPIKeys() []string {
	var keys []string
	for _, key := range strings.Split(os.Getenv("MINIMAX_API_KEYS"), ",") {
		keys = appendKey(keys, key)
	}
	keys = appendKey(keys, os.Getenv("MINIMAX_API_KEY_1"))
	keys = appendKey(keys, os.Getenv("MINIMAX_API_KEY_2"))
	keys = appendKey(keys, os.Getenv("MINIMAX_API_KEY"))
	return keys
}

func firstAPIKey() string {
	keys := loadAPIKeys()
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

func appendKey(keys []string, key string) []string {
	key = strings.TrimSpace(key)
	if key == "" || strings.HasPrefix(key, "TODO_") {
		return keys
	}
	for _, existing := range keys {
		if existing == key {
			return keys
		}
	}
	return append(keys, key)
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
