package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReverseRoutesChinaToAmapAndGlobalToGeoapify(t *testing.T) {
	var amapCalls atomic.Int32
	var geoapifyCalls atomic.Int32

	amap := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		amapCalls.Add(1)
		if got := request.URL.Query().Get("location"); got != "116.397499,39.908722" {
			t.Errorf("AMap location = %q", got)
		}
		_, _ = writer.Write([]byte(`{"status":"1","info":"OK","regeocode":{"formatted_address":"北京市东城区景山前街","addressComponent":{"country":"中国","province":"北京市","city":[],"district":"东城区","township":"景山街道","streetNumber":{"street":"景山前街","number":[]}}}}`))
	}))
	defer amap.Close()

	geoapify := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		geoapifyCalls.Add(1)
		if got := request.URL.Query().Get("lang"); got != "en" {
			t.Errorf("Geoapify lang = %q", got)
		}
		_, _ = writer.Write([]byte(`{"type":"FeatureCollection","features":[{"type":"Feature","geometry":{"type":"Point","coordinates":[-73.9857,40.7484]},"properties":{"name":"Empire State Building","formatted":"Empire State Building, New York, United States","country":"United States","country_code":"us","state":"New York","city":"New York","postcode":"10118","street":"5th Avenue","housenumber":"20","result_type":"amenity"}}]}`))
	}))
	defer geoapify.Close()

	service := newTestService(t, Config{
		AmapBaseURL:          amap.URL,
		AmapAPIKey:           "amap-test-key",
		AmapCoordinateSystem: "wgs84",
		AmapRPS:              20,
		AmapDailyQuota:       10,
		GeoapifyBaseURL:      geoapify.URL,
		GeoapifyAPIKey:       "geoapify-test-key",
		GeoapifyRPS:          20,
	})

	chinaResponse := callReverse(t, service, 39.908722, 116.397499)
	if chinaResponse.Code != http.StatusOK {
		t.Fatalf("China response status = %d, body = %s", chinaResponse.Code, chinaResponse.Body.String())
	}
	assertPhotonProperty(t, chinaResponse.Body.Bytes(), "country", "中国")
	assertPhotonProperty(t, chinaResponse.Body.Bytes(), "city", "北京市")
	assertPhotonProperty(t, chinaResponse.Body.Bytes(), "housenumber", "")

	globalResponse := callReverse(t, service, 40.7484, -73.9857)
	if globalResponse.Code != http.StatusOK {
		t.Fatalf("global response status = %d, body = %s", globalResponse.Code, globalResponse.Body.String())
	}
	assertPhotonProperty(t, globalResponse.Body.Bytes(), "country", "United States")
	assertPhotonProperty(t, globalResponse.Body.Bytes(), "countrycode", "US")

	if amapCalls.Load() != 1 {
		t.Fatalf("AMap calls = %d, want 1", amapCalls.Load())
	}
	if geoapifyCalls.Load() != 1 {
		t.Fatalf("Geoapify calls = %d, want 1", geoapifyCalls.Load())
	}
}

func TestUpstreamTransportFailureDoesNotExposeQuerySecrets(t *testing.T) {
	client := &upstreamClient{
		httpClient: &http.Client{Transport: failingRoundTripper{}},
	}

	_, err := client.get(t.Context(), "https://provider.example/reverse?apiKey=secret-value")
	if err == nil {
		t.Fatal("transport error = nil")
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("transport error exposed a query credential: %s", err)
	}
}

type failingRoundTripper struct{}

func (failingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("Get %q: EOF", request.URL.String())
}

func TestReverseCachesCoordinateGrid(t *testing.T) {
	var calls atomic.Int32
	amap := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = writer.Write([]byte(`{"status":"1","info":"OK","regeocode":{"formatted_address":"北京","addressComponent":{"country":"中国","province":"北京市","city":"","district":"东城区","streetNumber":{}}}}`))
	}))
	defer amap.Close()

	service := newTestService(t, testConfig(amap.URL, "http://invalid.example"))
	first := callReverse(t, service, 39.908722, 116.397499)
	second := callReverse(t, service, 39.908724, 116.397496)
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("cache response statuses = %d, %d", first.Code, second.Code)
	}
	if calls.Load() != 1 {
		t.Fatalf("AMap calls = %d, want 1 for points in one cache grid", calls.Load())
	}
}

func TestReverseCoalescesConcurrentRequests(t *testing.T) {
	var calls atomic.Int32
	amap := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		time.Sleep(100 * time.Millisecond)
		_, _ = writer.Write([]byte(`{"status":"1","info":"OK","regeocode":{"formatted_address":"北京","addressComponent":{"country":"中国","province":"北京市","city":"","district":"东城区","streetNumber":{}}}}`))
	}))
	defer amap.Close()

	service := newTestService(t, testConfig(amap.URL, "http://invalid.example"))
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			response := callReverse(t, service, 39.908722, 116.397499)
			if response.Code != http.StatusOK {
				t.Errorf("concurrent response status = %d", response.Code)
			}
		}()
	}
	group.Wait()
	if calls.Load() != 1 {
		t.Fatalf("AMap calls = %d, want 1 after request coalescing", calls.Load())
	}
}

func TestReverseRefusesAmapQuotaOverage(t *testing.T) {
	var calls atomic.Int32
	amap := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = writer.Write([]byte(`{"status":"1","info":"OK","regeocode":{"formatted_address":"北京","addressComponent":{"country":"中国","province":"北京市","city":"","district":"东城区","streetNumber":{}}}}`))
	}))
	defer amap.Close()

	config := testConfig(amap.URL, "http://invalid.example")
	config.AmapDailyQuota = 1
	service := newTestService(t, config)

	if response := callReverse(t, service, 39.908722, 116.397499); response.Code != http.StatusOK {
		t.Fatalf("first response status = %d", response.Code)
	}
	if response := callReverse(t, service, 31.230416, 121.473701); response.Code != http.StatusTooManyRequests {
		t.Fatalf("quota response status = %d, body = %s", response.Code, response.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("AMap calls = %d, want 1 after quota refusal", calls.Load())
	}
}

func TestMainlandBoundaryAndGCJConversion(t *testing.T) {
	boundary, err := loadMainlandBoundary()
	if err != nil {
		t.Fatal(err)
	}
	if !boundary.Contains(point{Latitude: 39.908722, Longitude: 116.397499}) {
		t.Fatal("Beijing must be within the China boundary")
	}
	if boundary.Contains(point{Latitude: 40.7484, Longitude: -73.9857}) {
		t.Fatal("New York must not be within the China boundary")
	}

	beijing := point{Latitude: 39.908722, Longitude: 116.397499}
	converted := WGS84ToGCJ02(beijing, boundary)
	if converted == beijing {
		t.Fatal("WGS84 Beijing coordinate was not converted to GCJ-02")
	}
	newYork := point{Latitude: 40.7484, Longitude: -73.9857}
	if converted := WGS84ToGCJ02(newYork, boundary); converted != newYork {
		t.Fatal("non-China coordinate must be unchanged")
	}
}

func newTestService(t *testing.T, config Config) *service {
	t.Helper()
	config.ListenAddress = ":0"
	config.CachePrecision = 4
	config.CacheTTL = time.Hour
	config.UpstreamTimeout = time.Second
	config.RateLimitWait = time.Second
	boundary, err := loadMainlandBoundary()
	if err != nil {
		t.Fatal(err)
	}
	service := newService(config, &http.Client{Timeout: time.Second}, newMemoryCache(), boundary)
	t.Cleanup(func() { _ = service.Close() })
	return service
}

type memoryCache struct {
	entries map[string]memoryEntry
	usage   map[string]int
	mutex   sync.Mutex
}

type memoryEntry struct {
	payload   []byte
	expiresAt time.Time
}

func newMemoryCache() *memoryCache {
	return &memoryCache{entries: map[string]memoryEntry{}, usage: map[string]int{}}
}

func (cache *memoryCache) Get(_ context.Context, key string, now time.Time) ([]byte, bool, error) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	entry, found := cache.entries[key]
	if !found || !entry.expiresAt.After(now) {
		return nil, false, nil
	}
	return append([]byte(nil), entry.payload...), true, nil
}

func (cache *memoryCache) Put(_ context.Context, key string, payload []byte, expiresAt time.Time) error {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	cache.entries[key] = memoryEntry{payload: append([]byte(nil), payload...), expiresAt: expiresAt}
	return nil
}

func (cache *memoryCache) ConsumeQuota(_ context.Context, provider string, limit int, now time.Time) error {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	key := provider + ":" + now.UTC().Format("2006-01-02")
	if cache.usage[key] >= limit {
		return errQuotaExceeded
	}
	cache.usage[key]++
	return nil
}

func (*memoryCache) Close() error { return nil }

func testConfig(amapURL, geoapifyURL string) Config {
	return Config{
		AmapBaseURL:          amapURL,
		AmapAPIKey:           "amap-test-key",
		AmapCoordinateSystem: "wgs84",
		AmapRPS:              100,
		AmapDailyQuota:       10,
		GeoapifyBaseURL:      geoapifyURL,
		GeoapifyAPIKey:       "geoapify-test-key",
		GeoapifyRPS:          100,
	}
}

func callReverse(t *testing.T, service *service, latitude, longitude float64) *httptest.ResponseRecorder {
	t.Helper()
	requestURL := "/reverse?" + url.Values{
		"lat": []string{strconv.FormatFloat(latitude, 'f', 6, 64)},
		"lon": []string{strconv.FormatFloat(longitude, 'f', 6, 64)},
	}.Encode()
	request := httptest.NewRequest(http.MethodGet, requestURL, nil)
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	return response
}

func assertPhotonProperty(t *testing.T, payload []byte, key, want string) {
	t.Helper()
	var response photonFeatureCollection
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	if response.Type != "FeatureCollection" || len(response.Features) != 1 {
		t.Fatalf("invalid Photon response: %#v", response)
	}
	if got, _ := response.Features[0].Properties[key].(string); got != want {
		t.Fatalf("Photon property %s = %q, want %q", key, got, want)
	}
}
