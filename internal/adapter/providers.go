package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type upstreamClient struct {
	httpClient *http.Client
	config     Config
	boundary   *mainlandBoundary
}

type photonFeatureCollection struct {
	Type     string          `json:"type"`
	Features []photonFeature `json:"features"`
}

type photonFeature struct {
	Type       string                 `json:"type"`
	Geometry   photonGeometry         `json:"geometry"`
	Properties map[string]interface{} `json:"properties"`
}

type photonGeometry struct {
	Type        string    `json:"type"`
	Coordinates []float64 `json:"coordinates"`
}

type amapReverseResponse struct {
	Status    string `json:"status"`
	Info      string `json:"info"`
	InfoCode  string `json:"infocode"`
	Regeocode struct {
		FormattedAddress string `json:"formatted_address"`
		AddressComponent struct {
			Country    string          `json:"country"`
			Province   string          `json:"province"`
			City       json.RawMessage `json:"city"`
			District   string          `json:"district"`
			Township   string          `json:"township"`
			StreetData struct {
				Street string `json:"street"`
				Number string `json:"number"`
			} `json:"streetNumber"`
		} `json:"addressComponent"`
	} `json:"regeocode"`
}

type geoapifyResponse struct {
	Type     string `json:"type"`
	Features []struct {
		Type     string `json:"type"`
		Geometry struct {
			Type        string    `json:"type"`
			Coordinates []float64 `json:"coordinates"`
		} `json:"geometry"`
		Properties struct {
			Name        string `json:"name"`
			Formatted   string `json:"formatted"`
			Country     string `json:"country"`
			CountryCode string `json:"country_code"`
			State       string `json:"state"`
			City        string `json:"city"`
			County      string `json:"county"`
			District    string `json:"district"`
			Postcode    string `json:"postcode"`
			Street      string `json:"street"`
			HouseNumber string `json:"housenumber"`
			ResultType  string `json:"result_type"`
		} `json:"properties"`
	} `json:"features"`
}

func (client *upstreamClient) reverseChina(context context.Context, original point) (photonFeatureCollection, error) {
	requestPoint := original
	if client.config.AmapCoordinateSystem == "gcj02" {
		requestPoint = WGS84ToGCJ02(original, client.boundary)
	}

	requestURL, err := url.Parse(client.config.AmapBaseURL)
	if err != nil {
		return photonFeatureCollection{}, fmt.Errorf("parse AMap base URL: %w", err)
	}
	query := requestURL.Query()
	query.Set("key", client.config.AmapAPIKey)
	query.Set("location", fmt.Sprintf("%.6f,%.6f", requestPoint.Longitude, requestPoint.Latitude))
	query.Set("extensions", "base")
	query.Set("output", "JSON")
	requestURL.RawQuery = query.Encode()

	body, err := client.get(context, requestURL.String())
	if err != nil {
		return photonFeatureCollection{}, fmt.Errorf("AMap reverse geocoding: %w", err)
	}

	var response amapReverseResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return photonFeatureCollection{}, fmt.Errorf("decode AMap response: %w", err)
	}
	if response.Status != "1" {
		return photonFeatureCollection{}, fmt.Errorf("AMap returned %s (%s, code %s)", response.Status, response.Info, response.InfoCode)
	}

	component := response.Regeocode.AddressComponent
	cityValue, err := amapText(component.City)
	if err != nil {
		return photonFeatureCollection{}, fmt.Errorf("decode AMap city: %w", err)
	}
	city := nonEmpty(cityValue, component.Province)
	name := nonEmpty(response.Regeocode.FormattedAddress, joinNonEmpty("", component.Province, city, component.District, component.Township, component.StreetData.Street, component.StreetData.Number))
	return photonFeatureCollection{
		Type: "FeatureCollection",
		Features: []photonFeature{{
			Type: "Feature",
			Geometry: photonGeometry{
				Type:        "Point",
				Coordinates: []float64{original.Longitude, original.Latitude},
			},
			Properties: map[string]interface{}{
				"name":        name,
				"country":     nonEmpty(component.Country, "中国"),
				"countrycode": "CN",
				"state":       component.Province,
				"city":        city,
				"district":    component.District,
				"locality":    component.Township,
				"street":      component.StreetData.Street,
				"housenumber": component.StreetData.Number,
				"osm_type":    "N",
				"osm_id":      "",
				"osm_key":     "place",
				"osm_value":   "address",
			},
		}},
	}, nil
}

// amapText normalizes AMap's documented polymorphic address fields. Some
// direct-administered cities return city as an empty JSON array instead of a
// string; both forms mean that the province should be used as the city label.
func amapText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}

	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, nil
	}

	var values []string
	if err := json.Unmarshal(raw, &values); err == nil {
		return joinNonEmpty("", values...), nil
	}

	return "", fmt.Errorf("expected string or string array")
}

func (client *upstreamClient) reverseGlobal(context context.Context, original point) (photonFeatureCollection, error) {
	requestURL, err := url.Parse(client.config.GeoapifyBaseURL)
	if err != nil {
		return photonFeatureCollection{}, fmt.Errorf("parse Geoapify base URL: %w", err)
	}
	query := requestURL.Query()
	query.Set("apiKey", client.config.GeoapifyAPIKey)
	query.Set("lat", strconv.FormatFloat(original.Latitude, 'f', 6, 64))
	query.Set("lon", strconv.FormatFloat(original.Longitude, 'f', 6, 64))
	query.Set("lang", "en")
	query.Set("limit", "1")
	query.Set("format", "geojson")
	requestURL.RawQuery = query.Encode()

	body, err := client.get(context, requestURL.String())
	if err != nil {
		return photonFeatureCollection{}, fmt.Errorf("Geoapify reverse geocoding: %w", err)
	}

	var response geoapifyResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return photonFeatureCollection{}, fmt.Errorf("decode Geoapify response: %w", err)
	}
	if response.Type != "FeatureCollection" {
		return photonFeatureCollection{}, fmt.Errorf("Geoapify returned unexpected GeoJSON type %q", response.Type)
	}
	if len(response.Features) == 0 {
		return photonFeatureCollection{Type: "FeatureCollection", Features: []photonFeature{}}, nil
	}

	feature := response.Features[0]
	properties := feature.Properties
	name := nonEmpty(properties.Name, properties.Formatted, joinNonEmpty(" ", properties.HouseNumber, properties.Street), properties.City)
	return photonFeatureCollection{
		Type: "FeatureCollection",
		Features: []photonFeature{{
			Type: "Feature",
			Geometry: photonGeometry{
				Type:        "Point",
				Coordinates: []float64{original.Longitude, original.Latitude},
			},
			Properties: map[string]interface{}{
				"name":        name,
				"country":     properties.Country,
				"countrycode": strings.ToUpper(properties.CountryCode),
				"state":       properties.State,
				"city":        properties.City,
				"county":      properties.County,
				"district":    properties.District,
				"postcode":    properties.Postcode,
				"street":      properties.Street,
				"housenumber": properties.HouseNumber,
				"osm_type":    "N",
				"osm_id":      "",
				"osm_key":     "place",
				"osm_value":   properties.ResultType,
			},
		}},
	}, nil
}

func (client *upstreamClient) get(context context.Context, requestURL string) ([]byte, error) {
	request, err := http.NewRequestWithContext(context, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create upstream request: %w", err)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send upstream request: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read upstream response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream returned HTTP %d", response.StatusCode)
	}
	return body, nil
}

func nonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func joinNonEmpty(separator string, values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, separator)
}
