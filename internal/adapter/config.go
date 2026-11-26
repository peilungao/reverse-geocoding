package adapter

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Config contains all configuration needed by the adapter. Secrets are never
// logged or returned by health endpoints.
type Config struct {
	ListenAddress        string
	DatabasePath         string
	AmapBaseURL          string
	AmapAPIKey           string
	AmapCoordinateSystem string
	AmapRPS              float64
	AmapDailyQuota       int
	GeoapifyBaseURL      string
	GeoapifyAPIKey       string
	GeoapifyRPS          float64
	CachePrecision       int
	CacheTTL             time.Duration
	UpstreamTimeout      time.Duration
	RateLimitWait        time.Duration
}

// LoadConfig parses mandatory environment configuration and rejects unsafe or
// ambiguous settings before the HTTP server starts.
func LoadConfig(getenv func(string) string) (Config, error) {
	config := Config{
		ListenAddress:        valueOr(getenv("LISTEN_ADDRESS"), ":8080"),
		DatabasePath:         valueOr(getenv("DATABASE_PATH"), "/data/cache.sqlite"),
		AmapBaseURL:          valueOr(getenv("AMAP_BASE_URL"), "https://restapi.amap.com/v3/geocode/regeo"),
		AmapAPIKey:           strings.TrimSpace(getenv("AMAP_API_KEY")),
		AmapCoordinateSystem: strings.ToLower(strings.TrimSpace(getenv("AMAP_COORDINATE_SYSTEM"))),
		GeoapifyBaseURL:      valueOr(getenv("GEOAPIFY_BASE_URL"), "https://api.geoapify.com/v1/geocode/reverse"),
		GeoapifyAPIKey:       strings.TrimSpace(getenv("GEOAPIFY_API_KEY")),
	}

	if config.AmapAPIKey == "" {
		return Config{}, fmt.Errorf("AMAP_API_KEY is required")
	}
	if config.GeoapifyAPIKey == "" {
		return Config{}, fmt.Errorf("GEOAPIFY_API_KEY is required")
	}
	if config.AmapCoordinateSystem != "gcj02" && config.AmapCoordinateSystem != "wgs84" {
		return Config{}, fmt.Errorf("AMAP_COORDINATE_SYSTEM must be gcj02 or wgs84")
	}

	var err error
	if config.AmapRPS, err = requiredPositiveFloat(getenv, "AMAP_RPS"); err != nil {
		return Config{}, err
	}
	if config.GeoapifyRPS, err = requiredPositiveFloat(getenv, "GEOAPIFY_RPS"); err != nil {
		return Config{}, err
	}
	if config.AmapDailyQuota, err = requiredPositiveInt(getenv, "AMAP_DAILY_QUOTA"); err != nil {
		return Config{}, err
	}
	if config.CachePrecision, err = intWithDefault(getenv, "CACHE_PRECISION", 4); err != nil {
		return Config{}, err
	}
	if config.CachePrecision < 3 || config.CachePrecision > 7 {
		return Config{}, fmt.Errorf("CACHE_PRECISION must be between 3 and 7")
	}
	if config.CacheTTL, err = durationWithDefault(getenv, "CACHE_TTL", 720*time.Hour); err != nil {
		return Config{}, err
	}
	if config.UpstreamTimeout, err = durationWithDefault(getenv, "UPSTREAM_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}
	if config.RateLimitWait, err = durationWithDefault(getenv, "RATE_LIMIT_WAIT", 15*time.Second); err != nil {
		return Config{}, err
	}

	return config, nil
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func requiredPositiveFloat(getenv func(string) string, key string) (float64, error) {
	value := strings.TrimSpace(getenv(key))
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive number", key)
	}
	return parsed, nil
}

func requiredPositiveInt(getenv func(string) string, key string) (int, error) {
	value := strings.TrimSpace(getenv(key))
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return parsed, nil
}

func intWithDefault(getenv func(string) string, key string, fallback int) (int, error) {
	value := strings.TrimSpace(getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return parsed, nil
}

func durationWithDefault(getenv func(string) string, key string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive Go duration", key)
	}
	return parsed, nil
}
