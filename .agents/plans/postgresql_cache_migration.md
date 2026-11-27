# PostgreSQL Cache Migration ExecPlan

## Goal

Move the reverse-geocoding adapter's durable cache and AMap daily quota counter
from its local SQLite file into the Dawarich PostgreSQL database under schema
`reverse_geocoding`.

## Constraints

- PostgreSQL is the sole runtime store after cutover; no SQLite fallback or new
  SQLite writes remain in the adapter.
- The adapter uses the existing `dawarich` database role and database, with a
  dedicated schema and tables only. It does not change Dawarich application
  tables or migrations.
- The existing SQLite cache and provider-usage rows are migrated during a
  bounded adapter stop so the high-water AMap daily count survives the change.
- Database credentials are copied server-side from OpenBao's existing Dawarich
  secret into the adapter's scoped OpenBao secret. They are never printed,
  committed, or placed in Compose.
- Post-cutover verification includes schema ownership, tables, migrated row
  counts, container health, China and overseas lookup behavior, and a
  repeated-point cache check.

## Work

1. Inspect the Dawarich role, database, current schema state, and SQLite row
   counts without showing credential values.
2. Replace the Go SQLite cache implementation with PostgreSQL transactions and
   migrations for `reverse_geocoding.cache_entries` and
   `reverse_geocoding.provider_usage`.
3. Add explicit PostgreSQL adapter configuration and test cache reuse,
   concurrent requests, quota enforcement, and startup failure on missing
   database configuration.
4. Publish a new immutable linux/amd64 image through the ACR relay.
5. Stop only the adapter, render its database credential from OpenBao, create
   the schema as the Dawarich role, copy SQLite rows transactionally, deploy
   the new image, and remove the obsolete SQLite runtime file only after
   independent PostgreSQL verification.
6. Update the NAS Compose, OpenBao render map, Wiki service page, and scoped
   configuration commits.

## Verification

- `go vet ./...` and `go test -race ./...` pass without SQLite dependencies.
- The running adapter has no `/data` bind mount and uses PostgreSQL only.
- The cache and daily usage table row counts match the prior SQLite snapshot.
- A first China lookup increases AMap usage at most once, and a repeated
  coordinate returns from cache without increasing it.
- Dawarich remains healthy and resolves its Photon provider to the adapter.
