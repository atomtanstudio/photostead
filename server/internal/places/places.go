// Package places names where items were taken, offline: each location is matched to the nearest town
// in GeoNames' list of places with at least 1,000 people (CC BY 4.0, https://www.geonames.org), with
// its region and country. The list is downloaded once into the data folder; no location ever leaves
// the server.
package places

import (
	"archive/zip"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

// Version of the matching. Bump it to name every item's place again.
const Version = 1

// MaxKm is how far the nearest town may be: farther (at sea, in a desert), an item has no place.
const MaxKm = 60

const source = "https://download.geonames.org/export/dump/"

// City is one entry of the gazetteer.
type City struct {
	ID                   int32
	Name                 string
	Admin1, Country, ISO string
	Lat, Lon             float64
}

// Gazetteer finds the nearest town to a point, from a grid of one-degree cells.
type Gazetteer struct {
	cells map[[2]int][]City
}

func cell(lat, lon float64) [2]int { return [2]int{int(math.Floor(lat)), int(math.Floor(lon))} }

// Nearest is the town nearest to lat, lon, and how far it is in km; ok is false when none is
// within MaxKm.
func (g *Gazetteer) Nearest(lat, lon float64) (c City, km float64, ok bool) {
	best := math.Inf(1)
	at := cell(lat, lon)
	// One degree of longitude is narrower toward the poles: look far enough east and west.
	span := 1 + int(math.Ceil(MaxKm/(111*math.Max(math.Cos(lat*math.Pi/180), 0.05))))
	for dy := -1; dy <= 1; dy++ {
		for dx := -span; dx <= span; dx++ {
			lonCell := (at[1]+dx+180+360)%360 - 180
			for _, x := range g.cells[[2]int{at[0] + dy, lonCell}] {
				if d := haversine(lat, lon, x.Lat, x.Lon); d < best {
					best, c = d, x
				}
			}
		}
	}
	return c, best, best <= MaxKm
}

func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371.0
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp, dl := (lat2-lat1)*math.Pi/180, (lon2-lon1)*math.Pi/180
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(a)))
}

// Load reads the gazetteer from dir, downloading what's missing first.
func Load(ctx context.Context, dir string) (*Gazetteer, error) {
	if err := fetch(ctx, dir); err != nil {
		return nil, err
	}
	admin1, err := table(filepath.Join(dir, "admin1CodesASCII.txt"), 0, 1)
	if err != nil {
		return nil, err
	}
	countries, err := table(filepath.Join(dir, "countryInfo.txt"), 0, 4)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(dir, "cities1000.txt"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	g := &Gazetteer{cells: map[[2]int][]City{}}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		// geonameid, name, asciiname, alternatenames, latitude, longitude, feature class, feature code,
		// country code, cc2, admin1 code, …
		col := strings.Split(sc.Text(), "\t")
		if len(col) < 11 {
			continue
		}
		id, err1 := strconv.Atoi(col[0])
		lat, err2 := strconv.ParseFloat(col[4], 64)
		lon, err3 := strconv.ParseFloat(col[5], 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		c := City{ID: int32(id), Name: col[1], ISO: col[8], Country: countries[col[8]], Admin1: admin1[col[8]+"."+col[10]], Lat: lat, Lon: lon}
		k := cell(lat, lon)
		g.cells[k] = append(g.cells[k], c)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(g.cells) == 0 {
		return nil, errors.New("the list of places is empty")
	}
	return g, nil
}

// table reads two tab-separated columns of a GeoNames file into a map (comment lines skipped).
func table(path string, key, value int) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		col := strings.Split(line, "\t")
		if len(col) > max(key, value) {
			m[col[key]] = col[value]
		}
	}
	return m, sc.Err()
}

// fetch downloads the three GeoNames files that aren't in dir yet.
func fetch(ctx context.Context, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	get := func(name, dst string) error {
		if _, err := os.Stat(dst); err == nil {
			return nil
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, source+name, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "Photostead (https://photostead.com)")
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("downloading %s: %w", name, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("downloading %s: %s", name, resp.Status)
		}
		tmp := dst + ".part"
		f, err := os.Create(tmp)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, io.LimitReader(resp.Body, 200<<20))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(tmp)
			return err
		}
		return os.Rename(tmp, dst)
	}
	if err := get("admin1CodesASCII.txt", filepath.Join(dir, "admin1CodesASCII.txt")); err != nil {
		return err
	}
	if err := get("countryInfo.txt", filepath.Join(dir, "countryInfo.txt")); err != nil {
		return err
	}
	cities := filepath.Join(dir, "cities1000.txt")
	if _, err := os.Stat(cities); err == nil {
		return nil
	}
	zipped := filepath.Join(dir, "cities1000.zip")
	if err := get("cities1000.zip", zipped); err != nil {
		return err
	}
	defer os.Remove(zipped)
	z, err := zip.OpenReader(zipped)
	if err != nil {
		return err
	}
	defer z.Close()
	for _, f := range z.File {
		if f.Name != "cities1000.txt" {
			continue
		}
		r, err := f.Open()
		if err != nil {
			return err
		}
		defer r.Close()
		out, err := os.Create(cities + ".part")
		if err != nil {
			return err
		}
		_, err = io.Copy(out, io.LimitReader(r, 500<<20))
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		return os.Rename(cities+".part", cities)
	}
	return errors.New("cities1000.zip has no cities1000.txt")
}

// Stats is what a pass did.
type Stats struct{ Located, Placed int }

// Refresh names the place of every item with a location that hasn't been looked at (or was with an
// older Version). The gazetteer is only loaded (and downloaded, the first time) when there are any.
func Refresh(ctx context.Context, s *store.Store, dataDir string) (Stats, error) {
	var st Stats
	var g *Gazetteer
	for {
		rows, err := s.Q.ToPlace(ctx, db.ToPlaceParams{Version: Version, Lim: 2000})
		if err != nil || len(rows) == 0 {
			return st, err
		}
		if g == nil {
			if g, err = Load(ctx, filepath.Join(dataDir, "geodata")); err != nil {
				return st, err
			}
		}
		for _, r := range rows {
			c, _, ok := g.Nearest(r.Lat.Float64, r.Lon.Float64)
			st.Located++
			p := db.SetPlaceParams{AssetID: r.AssetID, Version: Version}
			if ok {
				id, err := s.Q.UpsertPlace(ctx, db.UpsertPlaceParams{GeonameID: c.ID, Name: c.Name, Admin1: c.Admin1, Country: c.Country,
					CountryCode: c.ISO, Lat: c.Lat, Lon: c.Lon})
				if err != nil {
					return st, err
				}
				p.PlaceID.UUID, p.PlaceID.Valid = id, true
				st.Placed++
			}
			if err := s.Q.SetPlace(ctx, p); err != nil {
				return st, err
			}
		}
	}
}
