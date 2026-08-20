package bridge // runx-public-repo-gate: allow-file secret_cred_ref — minimax-api-N is a runtime alias format required by the minimaxauth package

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nfsarch33/minimax-openai-bridge/internal/minimaxauth"
)

type Config struct {
	ListenAddr      string
	MiniMaxBaseURL  string
	APIKey          string
	APIKeys         []string
	KeyBindings     []apiKeyBinding
	GroupID         string
	Model           string
	DefaultType     string
	Timeout         time.Duration
	SelectorBackoff time.Duration
	EventLogPath    string
	EventWriter     minimaxauth.EventWriter
	SecretLoader    minimaxauth.SecretLoader
	Clock           func() time.Time
}

func LoadConfig() Config {
	return Config{
		ListenAddr:      getenv("BRIDGE_ADDR", "127.0.0.1:8500"),
		MiniMaxBaseURL:  getenv("MINIMAX_BASE_URL", "https://api.minimax.chat/v1"),
		APIKey:          firstAPIKey(),
		APIKeys:         loadAPIKeys(),
		KeyBindings:     loadAPIKeyBindings(),
		GroupID:         os.Getenv("MINIMAX_GROUP_ID"),
		Model:           getenv("MINIMAX_EMBEDDING_MODEL", "embo-01"),
		DefaultType:     getenv("MINIMAX_EMBEDDING_TYPE", "db"),
		Timeout:         getenvDuration("MINIMAX_TIMEOUT", 30*time.Second),
		SelectorBackoff: getenvDuration("MINIMAX_SELECTOR_BACKOFF", time.Minute),
		EventLogPath:    getenv("MINIMAX_EVENT_LOG", defaultMinimaxEventLogPath()),
	}
}

type apiKeyBinding struct {
	alias minimaxauth.KeyAlias
	key   string
}

func loadAPIKeys() []string {
	bindings := loadAPIKeyBindings()
	var keys []string
	for _, binding := range bindings {
		keys = append(keys, binding.key)
	}
	return keys
}

func loadAPIKeyBindings() []apiKeyBinding {
	var bindings []apiKeyBinding
	for _, key := range strings.Split(os.Getenv("MINIMAX_API_KEYS"), ",") {
		bindings = appendBinding(bindings, key)
	}
	bindings = appendBinding(bindings, os.Getenv("MINIMAX_API_KEY_1"))
	bindings = appendBinding(bindings, os.Getenv("MINIMAX_API_KEY_2"))
	bindings = appendBinding(bindings, os.Getenv("MINIMAX_API_KEY"))
	return bindings
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

func appendBinding(bindings []apiKeyBinding, key string) []apiKeyBinding {
	key = strings.TrimSpace(key)
	if key == "" || strings.HasPrefix(key, "TODO_") {
		return bindings
	}
	for _, existing := range bindings {
		if existing.key == key {
			return bindings
		}
	}
	alias := minimaxauth.KeyAlias("minimax-api-" + strconv.Itoa(len(bindings)+1)) // runx-public-repo-gate: allow secret_cred_ref — runtime alias required by minimaxauth package
	return append(bindings, apiKeyBinding{alias: alias, key: key})
}

func bindingsFromKeys(keys []string) []apiKeyBinding {
	var bindings []apiKeyBinding
	for _, key := range keys {
		bindings = appendBinding(bindings, key)
	}
	return bindings
}

func defaultMinimaxEventLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "logs", "runx", "minimax.ndjson")
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
