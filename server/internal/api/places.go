package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"photostead.com/server/internal/store/db"
)

// Places and the map. Places are named offline (internal/places); the map's tiles come from the
// style's server (OpenFreeMap by default), which sees the areas looked at, never where items are.

// MapConfig is the map's look: a MapLibre style for light and one for dark, or none (off).
type MapConfig struct {
	Light, Dark string   // style URLs
	Hosts       []string // origins the styles load tiles, glyphs and sprites from (for the CSP)
	Off         bool
}

// DefaultMap is OpenFreeMap's Positron and Dark: free, no key, no tracking.
var DefaultMap = MapConfig{Light: "https://tiles.openfreemap.org/styles/positron", Dark: "https://tiles.openfreemap.org/styles/dark",
	Hosts: []string{"https://tiles.openfreemap.org"}}

// origins is every origin the map loads from, for connect-src.
func (m MapConfig) origins() []string {
	if m.Off {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range append([]string{m.Light, m.Dark}, m.Hosts...) {
		u, err := url.Parse(s)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			continue
		}
		o := u.Scheme + "://" + u.Host
		if !seen[o] {
			seen[o] = true
			out = append(out, o)
		}
	}
	return out
}

type Place struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	Region      string     `json:"region,omitempty"`
	Country     string     `json:"country,omitempty"`
	CountryCode string     `json:"countryCode,omitempty"`
	Lat         float64    `json:"lat"`
	Lon         float64    `json:"lon"`
	Count       int32      `json:"count"`
	Cover       *uuid.UUID `json:"cover,omitempty" doc:"Its newest item, to show for it"`
	CoverHash   []byte     `json:"coverThumbhash,omitempty"`
}

type placesOutput struct {
	Body struct {
		Places []Place `json:"places" doc:"By country, then most items first. List a place's items with the timeline endpoints and collection=place:{id}"`
		Map    *struct {
			Light string `json:"light"`
			Dark  string `json:"dark"`
		} `json:"map,omitempty" doc:"The map's styles (MapLibre), unless the map is off"`
		Attribution string `json:"attribution"`
	}
}

type MapPoint struct {
	ID        uuid.UUID `json:"id"`
	Lat       float64   `json:"lat"`
	Lon       float64   `json:"lon"`
	Video     bool      `json:"video,omitempty"`
	ThumbHash []byte    `json:"thumbhash,omitempty"`
}

type mapOutput struct {
	Body struct {
		Points []MapPoint `json:"points" doc:"Newest first"`
	}
}

func (s *server) registerPlaces() {
	huma.Register(s.api, huma.Operation{
		OperationID: "listPlaces", Method: http.MethodGet, Path: "/api/v1/places", Tags: []string{"Places"},
		Summary: "Where your items were taken", Description: "Named offline from GeoNames: the nearest town within 60 km.",
		Security: sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*placesOutput, error) {
		rows, err := s.Store.Q.ListPlaces(ctx, db.ListPlacesParams{Viewer: viewer(ctx), HideAdult: hideAdult(ctx)})
		if err != nil {
			return nil, err
		}
		out := &placesOutput{}
		out.Body.Places = make([]Place, len(rows))
		for i, r := range rows {
			cover := r.Cover
			out.Body.Places[i] = Place{ID: r.ID, Name: r.Name, Region: r.Admin1, Country: r.Country, CountryCode: r.CountryCode,
				Lat: r.Lat, Lon: r.Lon, Count: r.Count, Cover: &cover, CoverHash: r.CoverHash}
		}
		if !s.Map.Off && s.Map.Light != "" {
			out.Body.Map = &struct {
				Light string `json:"light"`
				Dark  string `json:"dark"`
			}{s.Map.Light, s.Map.Dark}
		}
		out.Body.Attribution = "Places from GeoNames (CC BY 4.0)"
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "mapPoints", Method: http.MethodGet, Path: "/api/v1/map", Tags: []string{"Places"},
		Summary: "Every item with a location, for the map", Security: sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*mapOutput, error) {
		rows, err := s.Store.Q.MapPoints(ctx, db.MapPointsParams{Viewer: viewer(ctx), HideAdult: hideAdult(ctx)})
		if err != nil {
			return nil, err
		}
		out := &mapOutput{}
		out.Body.Points = make([]MapPoint, len(rows))
		for i, r := range rows {
			out.Body.Points[i] = MapPoint{ID: r.ID, Lat: r.Lat, Lon: r.Lon, Video: r.Type == "video", ThumbHash: r.Thumbhash}
		}
		return out, nil
	})
}

// placeName is a place as a line: "Kyoto, Kyoto, Japan" without the repeats.
func placeName(name, region, country string) string {
	parts := []string{name}
	if region != "" && region != name {
		parts = append(parts, region)
	}
	if country != "" {
		parts = append(parts, country)
	}
	return strings.Join(parts, ", ")
}
