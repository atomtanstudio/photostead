// Package immich imports from an Immich server: favorites, the archive, albums and people's
// names. It only ever reads from Immich, through its API with a key the owner gives, and never
// touches a file.
package immich

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client reads an Immich server's API.
type Client struct {
	URL  string // the server, e.g. http://192.168.1.10:2283
	Key  string
	HTTP *http.Client
}

// Asset is the part of an Immich item the import uses.
type Asset struct {
	ID           string `json:"id"`
	Type         string `json:"type"` // IMAGE, VIDEO, AUDIO, OTHER
	OriginalPath string `json:"originalPath"`
	// Base64 SHA-1 of the file's bytes, or, for items in Immich external libraries, of
	// "path:" + its path.
	Checksum   string    `json:"checksum"`
	IsFavorite bool      `json:"isFavorite"`
	IsTrashed  bool      `json:"isTrashed"`
	Visibility string    `json:"visibility"` // timeline, archive, hidden (a Live Photo's clip), locked
	ExifInfo   *ExifInfo `json:"exifInfo"`   // with withExif in the search
}

// ExifInfo is the part of an item's details the import uses.
type ExifInfo struct {
	Description string `json:"description"` // set in Immich (or read from the file or its sidecar)
	Rating      *int   `json:"rating"`      // stars, 1-5 (-1 is a reject); nil when unrated
}

// Rating is the item's stars in Immich (1-5), -1 for a reject, or 0 when it has none.
func (a Asset) Rating() int {
	if a.ExifInfo == nil || a.ExifInfo.Rating == nil || *a.ExifInfo.Rating < -1 || *a.ExifInfo.Rating > 5 {
		return 0
	}
	return *a.ExifInfo.Rating
}

// Description is the item's description in Immich, if it has one.
func (a Asset) Description() string {
	if a.ExifInfo == nil {
		return ""
	}
	return strings.TrimSpace(a.ExifInfo.Description)
}

// Tag is one of a user's Immich tags. Value is its full path ("travel/japan").
type Tag struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Album struct {
	ID          string    `json:"id"`
	Name        string    `json:"albumName"`
	Description string    `json:"description"`
	CoverID     string    `json:"albumThumbnailAssetId"`
	AssetCount  int       `json:"assetCount"`
	CreatedAt   time.Time `json:"createdAt"`
}

type Person struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	BirthDate  string `json:"birthDate"` // YYYY-MM-DD, or empty
	IsHidden   bool   `json:"isHidden"`
	IsFavorite bool   `json:"isFavorite"`
}

// Error is a request Immich turned down.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("Immich answered %d", e.Status)
	}
	return fmt.Sprintf("Immich answered %d: %s", e.Status, e.Message)
}

// MissingPermission is the permission a key lacks ("album.read"), when that's why Immich
// refused.
func MissingPermission(err error) string {
	var e *Error
	if errors.As(err, &e) && e.Status == http.StatusForbidden {
		if _, p, ok := strings.Cut(e.Message, "Missing required permission: "); ok {
			return strings.TrimSpace(p)
		}
	}
	return ""
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", c.Key)
	req.Header.Set("accept", "application/json")
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 2 * time.Minute}
	}
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		var e struct {
			Message any `json:"message"` // a string, or a list of validation messages
		}
		_ = json.Unmarshal(msg, &e)
		text := fmt.Sprint(e.Message)
		if e.Message == nil {
			text = strings.TrimSpace(string(msg))
		}
		return &Error{Status: res.StatusCode, Message: text}
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// Version is the server's version, such as "3.0.1".
func (c *Client) Version(ctx context.Context) (string, error) {
	var v struct{ Major, Minor, Patch int }
	if err := c.do(ctx, http.MethodGet, "/api/server/version", nil, &v); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch), nil
}

// Albums are the albums the key's user owns or that are shared with them.
// Tags lists the user's tags (it needs the tag.read permission).
func (c *Client) Tags(ctx context.Context) ([]Tag, error) {
	var out []Tag
	err := c.do(ctx, http.MethodGet, "/api/tags", nil, &out)
	return out, err
}

func (c *Client) Albums(ctx context.Context) ([]Album, error) {
	var out []Album
	err := c.do(ctx, http.MethodGet, "/api/albums", nil, &out)
	return out, err
}

// People are every person Immich has, named or not, hidden included.
func (c *Client) People(ctx context.Context) ([]Person, error) {
	var all []Person
	for page := 1; ; page++ {
		var res struct {
			People      []Person `json:"people"`
			HasNextPage bool     `json:"hasNextPage"`
		}
		q := url.Values{"withHidden": {"true"}, "size": {"1000"}, "page": {fmt.Sprint(page)}}
		if err := c.do(ctx, http.MethodGet, "/api/people?"+q.Encode(), nil, &res); err != nil {
			return nil, err
		}
		all = append(all, res.People...)
		if !res.HasNextPage || len(res.People) == 0 {
			return all, nil
		}
	}
}

// Search pages through the items matching a metadata search (Immich's /search/metadata
// filters, such as {"albumIds": [...]} or {"visibility": "archive"}), a thousand at a time.
func (c *Client) Search(ctx context.Context, filter map[string]any, each func([]Asset) error) error {
	for page := 1; page > 0; {
		body := map[string]any{"page": page, "size": 1000}
		for k, v := range filter {
			body[k] = v
		}
		var res struct {
			Assets struct {
				Items    []Asset `json:"items"`
				NextPage *string `json:"nextPage"`
			} `json:"assets"`
		}
		if err := c.do(ctx, http.MethodPost, "/api/search/metadata", body, &res); err != nil {
			return err
		}
		if err := each(res.Assets.Items); err != nil {
			return err
		}
		page = 0
		if res.Assets.NextPage != nil {
			fmt.Sscan(*res.Assets.NextPage, &page)
		}
	}
	return nil
}

// Check is what a key can read on a server.
type Check struct {
	Version string `json:"version"`
	Items   string `json:"items"`  // "ok", or why not
	Albums  string `json:"albums"` // "ok", or why not
	People  string `json:"people"` // "ok", or why not
	Tags    string `json:"tags"`   // "ok", or why not
}

// CanImport says whether the import can run at all: it needs to read the items.
func (c Check) CanImport() bool { return c.Items == "ok" }

// Check tries each kind of read the import makes, without reading much.
func (c *Client) Check(ctx context.Context) (Check, error) {
	v, err := c.Version(ctx)
	if err != nil {
		return Check{}, err
	}
	out := Check{Version: v}
	why := func(err error) string {
		if err == nil {
			return "ok"
		}
		if p := MissingPermission(err); p != "" {
			return "The key needs the " + p + " permission"
		}
		var e *Error
		if errors.As(err, &e) && e.Status == http.StatusUnauthorized {
			return "Immich didn't accept the key"
		}
		return err.Error()
	}
	var none struct{}
	out.Items = why(c.do(ctx, http.MethodPost, "/api/search/metadata", map[string]any{"size": 1}, &none))
	out.Albums = why(c.do(ctx, http.MethodGet, "/api/albums", nil, &[]Album{}))
	out.People = why(c.do(ctx, http.MethodGet, "/api/people?size=1", nil, &none))
	out.Tags = why(c.do(ctx, http.MethodGet, "/api/tags", nil, &[]Tag{}))
	return out, nil
}
