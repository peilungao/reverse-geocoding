package adapter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var errQuotaExceeded = errors.New("daily provider quota exceeded")

// Cache persists normalized Photon payloads and provider quota counters.
type Cache struct {
	database *sql.DB
}

type cacheStore interface {
	Get(context.Context, string, time.Time) ([]byte, bool, error)
	Put(context.Context, string, []byte, time.Time) error
	ConsumeQuota(context.Context, string, int, time.Time) error
	Close() error
}

// OpenCache creates the adapter-owned PostgreSQL schema. It deliberately has
// no local fallback so quota protection and cached results are shared and
// durable with Dawarich's PostgreSQL deployment.
func OpenCache(config Config) (*Cache, error) {
	database, err := sql.Open("pgx", config.PostgreSQLURL())
	if err != nil {
		return nil, fmt.Errorf("open cache database: %w", err)
	}
	if err := database.PingContext(context.Background()); err != nil {
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
		CREATE SCHEMA IF NOT EXISTS reverse_geocoding;
		CREATE TABLE IF NOT EXISTS reverse_geocoding.cache_entries (
			cache_key TEXT PRIMARY KEY,
			payload JSONB NOT NULL,
			expires_at TIMESTAMPTZ NOT NULL
		);
		CREATE INDEX IF NOT EXISTS cache_entries_expires_at_idx
			ON reverse_geocoding.cache_entries (expires_at);
		CREATE TABLE IF NOT EXISTS reverse_geocoding.provider_usage (
			provider TEXT NOT NULL,
			usage_day DATE NOT NULL,
			request_count INTEGER NOT NULL CHECK (request_count >= 0),
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
		`SELECT payload::text FROM reverse_geocoding.cache_entries WHERE cache_key = $1 AND expires_at > $2`,
		key,
		now.UTC(),
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
		`INSERT INTO reverse_geocoding.cache_entries(cache_key, payload, expires_at) VALUES($1, $2::jsonb, $3)
		 ON CONFLICT(cache_key) DO UPDATE SET payload = excluded.payload, expires_at = excluded.expires_at`,
		key,
		payload,
		expiresAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("write cache entry: %w", err)
	}
	return nil
}

// ConsumeQuota records one request without exceeding the configured shared
// daily ceiling. The single statement is atomic across adapter instances.
func (cache *Cache) ConsumeQuota(context context.Context, provider string, limit int, now time.Time) error {
	day := now.UTC().Format("2006-01-02")
	var count int
	err := cache.database.QueryRowContext(
		context,
		`INSERT INTO reverse_geocoding.provider_usage(provider, usage_day, request_count)
		 VALUES($1, $2::date, 1)
		 ON CONFLICT(provider, usage_day) DO UPDATE SET request_count = reverse_geocoding.provider_usage.request_count + 1
		 WHERE reverse_geocoding.provider_usage.request_count < $3
		 RETURNING request_count`,
		provider,
		day,
		limit,
	).Scan(&count)
	if errors.Is(err, sql.ErrNoRows) {
		return errQuotaExceeded
	}
	if err != nil {
		return fmt.Errorf("increment quota usage: %w", err)
	}
	return nil
}

// Close closes the cache database.
func (cache *Cache) Close() error {
	return cache.database.Close()
}
