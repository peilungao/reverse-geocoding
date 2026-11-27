package adapter

import (
	"strings"
	"testing"
)

func TestLoadConfigRequiresPostgreSQL(t *testing.T) {
	environment := validEnvironment()
	delete(environment, "DATABASE_PASSWORD")

	_, err := LoadConfig(func(key string) string { return environment[key] })
	if err == nil || !strings.Contains(err.Error(), "DATABASE_PASSWORD is required") {
		t.Fatalf("LoadConfig error = %v, want missing database password", err)
	}
}

func TestPostgreSQLURLEscapesCredentials(t *testing.T) {
	config := Config{
		DatabaseHost:     "postgresql",
		DatabasePort:     5432,
		DatabaseUsername: "dawarich",
		DatabasePassword: "p@ss/word?",
		DatabaseName:     "dawarich",
		DatabaseSSLMode:  "disable",
	}

	got := config.PostgreSQLURL()
	want := "postgres://dawarich:p%40ss%2Fword%3F@postgresql:5432/dawarich?sslmode=disable"
	if got != want {
		t.Fatalf("PostgreSQLURL() = %q, want %q", got, want)
	}
}

func validEnvironment() map[string]string {
	return map[string]string{
		"DATABASE_HOST":          "postgresql",
		"DATABASE_PORT":          "5432",
		"DATABASE_USERNAME":      "dawarich",
		"DATABASE_PASSWORD":      "password",
		"DATABASE_NAME":          "dawarich",
		"AMAP_API_KEY":           "amap",
		"GEOAPIFY_API_KEY":       "geoapify",
		"AMAP_COORDINATE_SYSTEM": "gcj02",
		"AMAP_RPS":               "2",
		"AMAP_DAILY_QUOTA":       "100",
		"GEOAPIFY_RPS":           "2",
	}
}
