package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"
	"golang.org/x/time/rate"
)

type service struct {
	config          Config
	cache           cacheStore
	client          *upstreamClient
	boundary        *mainlandBoundary
	amapLimiter     *rate.Limiter
	geoapifyLimiter *rate.Limiter
	clock           func() time.Time
	requests        singleflight.Group
}

// NewService creates a ready-to-serve adapter. The optional HTTP client exists
// only to make upstream behavior testable.
func NewService(config Config, httpClient *http.Client) (*service, error) {
	boundary, err := loadMainlandBoundary()
	if err != nil {
		return nil, err
	}
	cache, err := OpenCache(config)
	if err != nil {
		return nil, err
	}
	return newService(config, httpClient, cache, boundary), nil
}

func newService(config Config, httpClient *http.Client, cache cacheStore, boundary *mainlandBoundary) *service {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: config.UpstreamTimeout}
	}
	return &service{
		config:          config,
		cache:           cache,
		boundary:        boundary,
		client:          &upstreamClient{httpClient: httpClient, config: config, boundary: boundary},
		amapLimiter:     rate.NewLimiter(rate.Limit(config.AmapRPS), 1),
		geoapifyLimiter: rate.NewLimiter(rate.Limit(config.GeoapifyRPS), 1),
		clock:           time.Now,
	}
}

// Close releases the persistent cache database.
func (service *service) Close() error {
	return service.cache.Close()
}

// Handler returns the small, Photon-compatible public surface.
func (service *service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", service.health)
	mux.HandleFunc("GET /reverse", service.reverse)
	return mux
}

func (service *service) health(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(`{"status":"ok"}`))
}

func (service *service) reverse(writer http.ResponseWriter, request *http.Request) {
	location, err := requestPoint(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err)
		return
	}
	provider := "geoapify"
	if service.boundary.Contains(location) {
		provider = "amap"
	}
	cacheKey := fmt.Sprintf("%s:%s", provider, roundedCoordinateKey(location, service.config.CachePrecision))

	payload, err := service.reverseCached(request.Context(), provider, cacheKey, location)
	if err != nil {
		statusCode := http.StatusBadGateway
		if errors.Is(err, errQuotaExceeded) {
			statusCode = http.StatusTooManyRequests
		}
		if errors.Is(err, context.DeadlineExceeded) {
			statusCode = http.StatusTooManyRequests
		}
		writeError(writer, statusCode, err)
		return
	}

	writer.Header().Set("Content-Type", "application/geo+json")
	writer.Header().Set("Cache-Control", "private, max-age=0")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(payload)
}

func (service *service) reverseCached(ctx context.Context, provider, cacheKey string, location point) ([]byte, error) {
	if payload, found, err := service.cache.Get(ctx, cacheKey, service.clock()); err != nil {
		return nil, err
	} else if found {
		return payload, nil
	}

	result, err, _ := service.requests.Do(cacheKey, func() (interface{}, error) {
		if payload, found, err := service.cache.Get(ctx, cacheKey, service.clock()); err != nil {
			return nil, err
		} else if found {
			return payload, nil
		}

		if err := service.waitForProvider(ctx, provider); err != nil {
			return nil, err
		}
		if provider == "amap" {
			if err := service.cache.ConsumeQuota(ctx, provider, service.config.AmapDailyQuota, service.clock()); err != nil {
				return nil, err
			}
		}

		var response photonFeatureCollection
		var err error
		if provider == "amap" {
			response, err = service.client.reverseChina(ctx, location)
		} else {
			response, err = service.client.reverseGlobal(ctx, location)
		}
		if err != nil {
			return nil, err
		}
		payload, err := json.Marshal(response)
		if err != nil {
			return nil, fmt.Errorf("encode Photon response: %w", err)
		}
		if err := service.cache.Put(ctx, cacheKey, payload, service.clock().Add(service.config.CacheTTL)); err != nil {
			return nil, err
		}
		return payload, nil
	})
	if err != nil {
		return nil, err
	}
	payload, ok := result.([]byte)
	if !ok {
		return nil, fmt.Errorf("internal cache result has unexpected type")
	}
	return payload, nil
}

func (service *service) waitForProvider(ctx context.Context, provider string) error {
	limiter := service.geoapifyLimiter
	if provider == "amap" {
		limiter = service.amapLimiter
	}
	waitContext, cancel := context.WithTimeout(ctx, service.config.RateLimitWait)
	defer cancel()
	if err := limiter.Wait(waitContext); err != nil {
		return fmt.Errorf("wait for %s request allowance: %w", provider, err)
	}
	return nil
}

func requestPoint(request *http.Request) (point, error) {
	latitude, err := parseCoordinate(request.URL.Query().Get("lat"), "lat", -90, 90)
	if err != nil {
		return point{}, err
	}
	longitude, err := parseCoordinate(request.URL.Query().Get("lon"), "lon", -180, 180)
	if err != nil {
		return point{}, err
	}
	return point{Latitude: latitude, Longitude: longitude}, nil
}

func parseCoordinate(raw, name string, min, max float64) (float64, error) {
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < min || parsed > max {
		return 0, fmt.Errorf("%s must be a finite coordinate between %g and %g", name, min, max)
	}
	return parsed, nil
}

func roundedCoordinateKey(location point, precision int) string {
	factor := math.Pow10(precision)
	latitude := math.Round(location.Latitude*factor) / factor
	longitude := math.Round(location.Longitude*factor) / factor
	return fmt.Sprintf("%.*f,%.*f", precision, latitude, precision, longitude)
}

func writeError(writer http.ResponseWriter, statusCode int, err error) {
	slog.Warn("reverse geocoding request failed", "error", err)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	encoded, encodeErr := json.Marshal(map[string]string{"error": strings.TrimSpace(err.Error())})
	if encodeErr == nil {
		_, _ = writer.Write(encoded)
	}
}
