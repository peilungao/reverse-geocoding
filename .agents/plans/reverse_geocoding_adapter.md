# Reverse Geocoding Adapter ExecPlan

## Goal

Deploy a small Go service on the NAS that exposes Dawarich's Photon-compatible
reverse endpoint. It routes WGS-84 points in mainland China to AMap with
Chinese results and all other points to Geoapify with English results, then
returns one normalized Photon GeoJSON feature collection.

## Constraints and decisions

- The adapter exposes only `GET /reverse` and `GET /healthz`; it is internal to
  the Dawarich Docker network and has no public ingress.
- China detection uses an embedded, simplified mainland-China polygon rather
  than a bounding box.
- The inbound coordinates and the output geometry remain WGS-84. AMap request
  coordinates are configurable explicitly as `gcj02` or `wgs84`.
- AMap and Geoapify API keys remain external secrets. The process fails at
  startup when the required configuration is absent.
- Persistent SQLite caching uses a rounded WGS-84 coordinate grid and separate
  provider keys. A singleflight group coalesces concurrent cache misses.
- Each upstream has independently configured rate limits and bounded waits.
  AMap requests are additionally guarded by a daily quota ceiling to prevent a
  dense Dawarich import from exhausting its quota.
- A non-successful upstream response is returned as an error; the service does
  not silently switch providers or fabricate an address.

## Work

1. Build the Go HTTP service, embedded boundary data, upstream clients,
   provider-to-Photon normalization, SQLite cache, rate/quota controls, and
   tests using `httptest` fixtures.
2. Build a multi-stage, non-root Docker image and verify its `/healthz` and
   Photon `/reverse` behavior against fixture upstream servers.
3. Add a NAS Compose declaration using only OpenBao-rendered secret references,
   join it to the Dawarich network, and configure Dawarich's Photon host only
   after secret provisioning is verified.
4. Publish the image through the approved ACR workflow, pull its ACR reference
   on the NAS, start the service, and verify the direct container route plus
   Dawarich's provider test path.
5. Update the NAS Wiki with service purpose, secrets metadata, cache/quota
   behavior, verification, and operations; then report pending scoped commit
   paths if commit authority was not supplied.

## Verification

- `go test ./...` covers China routing, language selection, response shape,
  cache reuse, concurrent request coalescing, rate limiting, quota exhaustion,
  invalid inputs, and upstream errors.
- Docker image responds to `/healthz` only when configuration and cache are
  ready.
- NAS container health, persistent cache mount, Docker-network DNS, and a
  Dawarich-compatible `/reverse?lat=...&lon=...` request are checked
  independently.
- Verify a repeated point produces one upstream request and the cached response
  thereafter; verify a deliberately exhausted AMap quota returns an explicit
  error rather than falling back to Geoapify.
