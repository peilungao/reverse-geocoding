# Reverse Geocoding Adapter

This is a small internal, Photon-compatible reverse-geocoding service for
Dawarich. It is deliberately limited to two routes:

- `GET /reverse?lat=<latitude>&lon=<longitude>`
- `GET /healthz`

The service expects WGS-84 input coordinates and returns a Photon GeoJSON
`FeatureCollection` that Dawarich's Photon provider can consume.

## Provider routing

| Coordinate | Upstream | Result language |
| --- | --- | --- |
| Inside the embedded Natural Earth China geometry | AMap reverse geocoding | Chinese |
| Everywhere else | Geoapify reverse geocoding | English (`lang=en`) |

The response geometry always keeps the original WGS-84 coordinate. AMap's
request coordinate system is explicit: set `AMAP_COORDINATE_SYSTEM=gcj02` for
the usual domestic AMap endpoint, or `wgs84` only when the configured AMap API
endpoint explicitly documents WGS-84 input.

## Quota controls

The adapter prevents point-level Dawarich imports from blindly consuming every
provider request:

- A rounded coordinate grid is persisted in SQLite. The default precision of
  four decimals corresponds to roughly 11 m, so nearby repeated fixes reuse a
  cached result.
- Concurrent requests for the same provider/grid are coalesced into one
  upstream call.
- AMap and Geoapify each have a separate requests-per-second limit.
- AMap has a required daily ceiling. Once reached, the service returns HTTP 429
  and does not silently use Geoapify for a China request.

The cache directory must be durable. Deleting it resets the cache and the local
AMap quota counter.

## Required configuration

```text
AMAP_API_KEY=<OpenBao-rendered secret>
GEOAPIFY_API_KEY=<OpenBao-rendered secret>
AMAP_COORDINATE_SYSTEM=gcj02
AMAP_RPS=2
AMAP_DAILY_QUOTA=<the quota of your AMap key>
GEOAPIFY_RPS=2
```

Optional settings:

```text
LISTEN_ADDRESS=:8080
DATABASE_PATH=/data/cache.sqlite
CACHE_PRECISION=4
CACHE_TTL=720h
UPSTREAM_TIMEOUT=10s
RATE_LIMIT_WAIT=15s
AMAP_BASE_URL=https://restapi.amap.com/v3/geocode/regeo
GEOAPIFY_BASE_URL=https://api.geoapify.com/v1/geocode/reverse
```

The process refuses to start if the two API keys, provider rates, daily AMap
quota, or AMap coordinate-system declaration are absent or invalid. Do not put
the keys in Compose files or this repository.

## Dawarich configuration

Select **Photon** in Dawarich and configure its host as the Docker service
name and port:

```text
reverse_geocoding:8080
```

Use HTTP for the internal Docker path. Set Photon language to `en`; the adapter
does not depend on it because it chooses Chinese or English based on the point
itself.

## NAS deployment

The Compose manifest in `deploy/docker-compose.yaml` is installed at
`/volume2/docker/reverse_geocoding/docker-compose.yaml`. Its credentials and
`AMAP_DAILY_QUOTA` are rendered from OpenBao into
`/volume2/docker/reverse_geocoding/.env`. The durable SQLite cache and quota
counter live at `/volume2/appdata/reverse_geocoding/cache.sqlite`.

`AMAP_DAILY_QUOTA` is intentionally not guessed. Set it below the daily quota
for the exact AMap key in use, leaving margin for requests made outside this
adapter. The service refuses to start until it is supplied.

## Development

```sh
go test ./...
docker build -t reverse-geocoding:dev .
```

The China boundary asset is derived from Natural Earth Admin 0 country data,
which is public domain. It is intentionally embedded so routing remains
offline and deterministic.
