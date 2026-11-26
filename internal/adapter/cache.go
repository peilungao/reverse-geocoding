package adapter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

var errQuotaExceeded = errors.New("daily provider quota exceeded")

// Cache persists normalized Photon payloads and provider quota counters.
type Cache struct {
	database *sql.DB
}

// OpenCache creates the schema needed by the adapter. SQLite is intentionally
// used so cache and daily quota state survive container recreation.
func OpenCache(path string) (*Cache, error) {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open cache database: %w", err)
	}
	// One SQLite connection serializes short cache and quota writes. This avoids
	// SQLITE_BUSY errors while singleflight coalesces same-grid misses.
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	if err := database.Ping(); err != nil {
		database.Close()
		return nil, fmt.Errorf("ping cache database: %w", err)
	}

	cache := &Cache{database: database}
	if err := cache.initialize(context.Background()); err != nil {
		database.Close()
		return nil, err
	}
	return cache, nil
}

func (cache *Cache) initialize(context context.Context) error {
	_, err := cache.database.ExecContext(context, `
		CREATE TABLE IF NOT EXISTS cache_entries (
			cache_key TEXT PRIMARY KEY,
			payload BLOB NOT NULL,
			expires_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS provider_usage (
			provider TEXT NOT NULL,
			usage_day TEXT NOT NULL,
			request_count INTEGER NOT NULL,
			PRIMARY KEY (provider, usage_day)
		);
	`)
	if err != nil {
		return fmt.Errorf("initialize cache database: %w", err)
	}
	return nil
}

// Get returns only non-expired payloads.
func (cache *Cache) Get(context context.Context, key string, now time.Time) ([]byte, bool, error) {
	var payload []byte
	err := cache.database.QueryRowContext(
		context,
		`SELECT payload FROM cache_entries WHERE cache_key = ? AND expires_at > ?`,
		key,
		now.Unix(),
	).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read cache entry: %w", err)
	}
	return payload, true, nil
}

// Put stores a successful response and replaces older data for the same grid.
func (cache *Cache) Put(context context.Context, key string, payload []byte, expiresAt time.Time) error {
	_, err := cache.database.ExecContext(
		context,
		`INSERT INTO cache_entries(cache_key, payload, expires_at) VALUES(?, ?, ?)
		 ON CONFLICT(cache_key) DO UPDATE SET payload = excluded.payload, expires_at = excluded.expires_at`,
		key,
		payload,
		expiresAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("write cache entry: %w", err)
	}
	return nil
}

// ConsumeQuota records one request without exceeding the configured local
// daily ceiling. It uses a transaction so concurrent misses cannot overrun it.
func (cache *Cache) ConsumeQuota(context context.Context, provider string, limit int, now time.Time) error {
	transaction, err := cache.database.BeginTx(context, nil)
	if err != nil {
		return fmt.Errorf("start quota transaction: %w", err)
	}
	defer transaction.Rollback()

	day := now.UTC().Format("2006-01-02")
	var count int
	err = transaction.QueryRowContext(
		context,
		`SELECT request_count FROM provider_usage WHERE provider = ? AND usage_day = ?`,
		provider,
		day,
	).Scan(&count)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read quota usage: %w", err)
	}
	if count >= limit {
		return errQuotaExceeded
	}

	_, err = transaction.ExecContext(
		context,
		`INSERT INTO provider_usage(provider, usage_day, request_count) VALUES(?, ?, 1)
		 ON CONFLICT(provider, usage_day) DO UPDATE SET request_count = request_count + 1`,
		provider,
		day,
	)
	if err != nil {
		return fmt.Errorf("increment quota usage: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit quota transaction: %w", err)
	}
	return nil
}

// Close closes the cache database.
func (cache *Cache) Close() error {
	return cache.database.Close()
}
