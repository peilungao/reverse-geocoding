package adapter

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
)

//go:embed assets/china_geometry.json
var chinaGeometryJSON []byte

type geoJSONGeometry struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

type point struct {
	Longitude float64
	Latitude  float64
}

type mainlandBoundary struct {
	polygons [][][]point
}

func loadMainlandBoundary() (*mainlandBoundary, error) {
	var geometry geoJSONGeometry
	if err := json.Unmarshal(chinaGeometryJSON, &geometry); err != nil {
		return nil, fmt.Errorf("decode embedded mainland boundary: %w", err)
	}
	if geometry.Type != "MultiPolygon" {
		return nil, fmt.Errorf("embedded mainland boundary must be a MultiPolygon")
	}

	var rawPolygons [][][][]float64
	if err := json.Unmarshal(geometry.Coordinates, &rawPolygons); err != nil {
		return nil, fmt.Errorf("decode mainland polygon coordinates: %w", err)
	}

	boundary := &mainlandBoundary{polygons: make([][][]point, 0, len(rawPolygons))}
	for _, rawPolygon := range rawPolygons {
		polygon := make([][]point, 0, len(rawPolygon))
		for _, rawRing := range rawPolygon {
			ring := make([]point, 0, len(rawRing))
			for _, rawPoint := range rawRing {
				if len(rawPoint) != 2 {
					return nil, fmt.Errorf("invalid mainland boundary coordinate")
				}
				ring = append(ring, point{Longitude: rawPoint[0], Latitude: rawPoint[1]})
			}
			polygon = append(polygon, ring)
		}
		boundary.polygons = append(boundary.polygons, polygon)
	}
	return boundary, nil
}

// Contains reports whether a WGS-84 point falls inside the embedded Natural
// Earth China geometry. The outer ring includes a point and inner rings remove
// holes, matching GeoJSON polygon semantics.
func (boundary *mainlandBoundary) Contains(location point) bool {
	for _, polygon := range boundary.polygons {
		if len(polygon) == 0 || !ringContains(polygon[0], location) {
			continue
		}
		insideHole := false
		for _, hole := range polygon[1:] {
			if ringContains(hole, location) {
				insideHole = true
				break
			}
		}
		if !insideHole {
			return true
		}
	}
	return false
}

func ringContains(ring []point, location point) bool {
	if len(ring) < 3 {
		return false
	}
	inside := false
	for current, previous := 0, len(ring)-1; current < len(ring); previous, current = current, current+1 {
		currentPoint := ring[current]
		previousPoint := ring[previous]
		intersects := (currentPoint.Latitude > location.Latitude) != (previousPoint.Latitude > location.Latitude) &&
			location.Longitude < (previousPoint.Longitude-currentPoint.Longitude)*(location.Latitude-currentPoint.Latitude)/(previousPoint.Latitude-currentPoint.Latitude)+currentPoint.Longitude
		if intersects {
			inside = !inside
		}
	}
	return inside
}

// WGS84ToGCJ02 converts a WGS-84 coordinate for an AMap domestic API call.
// AMap documents GCJ-02 for its domestic mapping services; points outside the
// mainland boundary are returned unchanged.
func WGS84ToGCJ02(location point, boundary *mainlandBoundary) point {
	if !boundary.Contains(location) {
		return location
	}

	const (
		a  = 6378245.0
		ee = 0.00669342162296594323
	)
	latitudeDelta := transformLatitude(location.Longitude-105.0, location.Latitude-35.0)
	longitudeDelta := transformLongitude(location.Longitude-105.0, location.Latitude-35.0)
	radLatitude := location.Latitude / 180.0 * math.Pi
	magic := math.Sin(radLatitude)
	magic = 1 - ee*magic*magic
	sqrtMagic := math.Sqrt(magic)
	latitudeDelta = (latitudeDelta * 180.0) / ((a * (1 - ee)) / (magic * sqrtMagic) * math.Pi)
	longitudeDelta = (longitudeDelta * 180.0) / (a / sqrtMagic * math.Cos(radLatitude) * math.Pi)
	return point{Longitude: location.Longitude + longitudeDelta, Latitude: location.Latitude + latitudeDelta}
}

func transformLatitude(longitude, latitude float64) float64 {
	result := -100.0 + 2.0*longitude + 3.0*latitude + 0.2*latitude*latitude + 0.1*longitude*latitude + 0.2*math.Sqrt(math.Abs(longitude))
	result += (20.0*math.Sin(6.0*longitude*math.Pi) + 20.0*math.Sin(2.0*longitude*math.Pi)) * 2.0 / 3.0
	result += (20.0*math.Sin(latitude*math.Pi) + 40.0*math.Sin(latitude/3.0*math.Pi)) * 2.0 / 3.0
	result += (160.0*math.Sin(latitude/12.0*math.Pi) + 320*math.Sin(latitude*math.Pi/30.0)) * 2.0 / 3.0
	return result
}

func transformLongitude(longitude, latitude float64) float64 {
	result := 300.0 + longitude + 2.0*latitude + 0.1*longitude*longitude + 0.1*longitude*latitude + 0.1*math.Sqrt(math.Abs(longitude))
	result += (20.0*math.Sin(6.0*longitude*math.Pi) + 20.0*math.Sin(2.0*longitude*math.Pi)) * 2.0 / 3.0
	result += (20.0*math.Sin(longitude*math.Pi) + 40.0*math.Sin(longitude/3.0*math.Pi)) * 2.0 / 3.0
	result += (150.0*math.Sin(longitude/12.0*math.Pi) + 300.0*math.Sin(longitude/30.0*math.Pi)) * 2.0 / 3.0
	return result
}
