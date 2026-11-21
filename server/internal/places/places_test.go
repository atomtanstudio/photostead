package places

import "testing"

func TestNearest(t *testing.T) {
	g := &Gazetteer{cells: map[[2]int][]City{}}
	for _, c := range []City{
		{ID: 1, Name: "Redmond", Lat: 47.674, Lon: -122.1215},
		{ID: 2, Name: "Kirkland", Lat: 47.6815, Lon: -122.2087},
		{ID: 3, Name: "Suva", Lat: -18.1416, Lon: 178.4419},       // near the date line
		{ID: 4, Name: "Taveuni", Lat: -16.85, Lon: -179.95},       // across it
		{ID: 5, Name: "Longyearbyen", Lat: 78.2232, Lon: 15.6267}, // far north, where degrees of longitude are short
	} {
		k := cell(c.Lat, c.Lon)
		g.cells[k] = append(g.cells[k], c)
	}
	for _, c := range []struct {
		lat, lon float64
		want     string
	}{
		{47.6801, -122.1268, "Redmond"},
		{47.68, -122.19, "Kirkland"},
		{-16.9, 179.98, "Taveuni"},   // nearest is over the date line
		{78.3, 16.9, "Longyearbyen"}, // two degrees of longitude away, about 30 km
		{30, -40, ""},                // mid-Atlantic: nothing within 60 km
	} {
		got, km, ok := g.Nearest(c.lat, c.lon)
		if (c.want == "") == ok || (ok && got.Name != c.want) {
			t.Errorf("Nearest(%v, %v) = %s (%.0f km, %v), want %q", c.lat, c.lon, got.Name, km, ok, c.want)
		}
	}
}
