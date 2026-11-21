package api

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/google/uuid"

	"photostead.com/server/internal/store/db"
)

const (
	maxDownloadItems = 5000
	downloadTTL      = 15 * time.Minute
)

// downloads holds short-lived download links, so a browser can stream a large zip straight to
// disk with a plain GET (a fetch would have to hold it all in memory).
type downloads struct {
	mu    sync.Mutex
	links map[string]download
}

type download struct {
	viewer  uuid.UUID
	ids     []uuid.UUID
	expires time.Time
}

func (d *downloads) add(v uuid.UUID, ids []uuid.UUID) string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	token := base64.RawURLEncoding.EncodeToString(b)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.links == nil {
		d.links = map[string]download{}
	}
	now := time.Now()
	for k, l := range d.links {
		if now.After(l.expires) {
			delete(d.links, k)
		}
	}
	d.links[token] = download{viewer: v, ids: ids, expires: now.Add(downloadTTL)}
	return token
}

func (d *downloads) get(token string, v uuid.UUID) ([]uuid.UUID, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	l, ok := d.links[token]
	if !ok || l.viewer != v || time.Now().After(l.expires) {
		return nil, false
	}
	return l.ids, true
}

type downloadInput struct {
	Body struct {
		IDs []uuid.UUID `json:"ids" minItems:"1" maxItems:"5000"`
	}
}

type downloadOutput struct {
	Body struct {
		URL   string `json:"url" doc:"GET this (signed in) within 15 minutes to receive the originals as a zip"`
		Count int    `json:"count"`
	}
}

type downloadFetch struct {
	Token string `path:"token"`
}

func (s *server) registerDownloads() {
	huma.Register(s.api, huma.Operation{
		OperationID: "createDownload", Method: http.MethodPost, Path: "/api/v1/downloads", Tags: []string{"Assets"},
		Summary: "Prepare a zip of originals", Security: sessionAuth,
	}, func(ctx context.Context, in *downloadInput) (*downloadOutput, error) {
		if len(in.Body.IDs) > maxDownloadItems {
			return nil, huma.Error422UnprocessableEntity(fmt.Sprintf("up to %d items at a time", maxDownloadItems))
		}
		out := &downloadOutput{}
		out.Body.URL = "/api/v1/downloads/" + s.downloads.add(viewer(ctx), in.Body.IDs)
		out.Body.Count = len(in.Body.IDs)
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "fetchDownload", Method: http.MethodGet, Path: "/api/v1/downloads/{token}", Tags: []string{"Assets"},
		Summary: "The originals as a zip (stored, not recompressed)", Security: sessionAuth,
		Responses: map[string]*huma.Response{"200": {Description: "Zip archive",
			Content: map[string]*huma.MediaType{"application/zip": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}}},
	}, func(ctx context.Context, in *downloadFetch) (*huma.StreamResponse, error) {
		v := viewer(ctx)
		ids, ok := s.downloads.get(in.Token, v)
		if !ok {
			return nil, huma.Error404NotFound("This download link has expired. Select the items and download again.")
		}
		rows, err := s.Store.Q.DownloadSources(ctx, db.DownloadSourcesParams{Viewer: v, Ids: ids})
		if err != nil {
			return nil, err
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			_, w := humago.Unwrap(hctx)
			name := fmt.Sprintf("photostead-%d-items-%s.zip", len(rows), time.Now().Format("2006-01-02"))
			w.Header().Set("Content-Type", "application/zip")
			w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
			zw := zip.NewWriter(w)
			used := map[string]bool{}
			for _, r := range rows {
				p := filepath.Join(r.RootPath, filepath.FromSlash(r.RelPath))
				f, err := os.OpenFile(p, os.O_RDONLY, 0)
				if err != nil {
					continue // unreachable original: leave it out rather than fail the whole zip
				}
				hdr := &zip.FileHeader{Name: uniqueName(path.Base(r.RelPath), used), Method: zip.Store, Modified: r.Mtime}
				if dst, err := zw.CreateHeader(hdr); err == nil {
					_, _ = io.Copy(dst, f)
				}
				f.Close()
			}
			_ = zw.Close()
		}}, nil
	})
}

// uniqueName keeps names unique inside a zip: "a.jpg", then "a (2).jpg", …
func uniqueName(name string, used map[string]bool) string {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 1; ; n++ {
		c := name
		if n > 1 {
			c = fmt.Sprintf("%s (%d)%s", stem, n, ext)
		}
		if !used[strings.ToLower(c)] {
			used[strings.ToLower(c)] = true
			return c
		}
	}
}
