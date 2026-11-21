// Package search finds items by meaning: query text (or an item) becomes a SigLIP 2 embedding and
// the nearest items' embeddings win. Text is embedded by the worker's text service next to the
// server; items are embedded by workers (see internal/api/workers.go).
package search

import (
	"bytes"
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Model is the search model every worker embeds with. Embeddings of different models never mix.
const Model = "siglip2-base-patch16-256"

// OCREngine reads text in pictures (RapidOCR with PP-OCR models). Text read by another engine is
// read again.
const OCREngine = "rapidocr-ppocrv6"

// Embedder embeds query text through the worker's text service, remembering recent queries.
type Embedder struct {
	URL  string // e.g. http://127.0.0.1:4801
	HTTP *http.Client

	mu      sync.Mutex
	cache   map[string]*list.Element
	order   *list.List
	checked time.Time
	ok      bool
}

type cached struct {
	key string
	vec []float32
}

const cacheSize = 256

func (e *Embedder) client() *http.Client {
	if e.HTTP != nil {
		return e.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// Available says whether the text service answers (checked at most every 20 seconds).
func (e *Embedder) Available(ctx context.Context) bool {
	if e == nil || e.URL == "" {
		return false
	}
	e.mu.Lock()
	if time.Since(e.checked) < 20*time.Second {
		ok := e.ok
		e.mu.Unlock()
		return ok
	}
	e.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ok := false
	if req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(e.URL, "/")+"/health", nil); err == nil {
		if res, err := e.client().Do(req); err == nil {
			ok = res.StatusCode == http.StatusOK
			res.Body.Close()
		}
	}
	e.mu.Lock()
	e.checked, e.ok = time.Now(), ok
	e.mu.Unlock()
	return ok
}

// Text embeds one query.
func (e *Embedder) Text(ctx context.Context, q string) ([]float32, error) {
	key := strings.ToLower(strings.Join(strings.Fields(q), " "))
	e.mu.Lock()
	if e.cache == nil {
		e.cache, e.order = map[string]*list.Element{}, list.New()
	}
	if el, ok := e.cache[key]; ok {
		e.order.MoveToFront(el)
		v := el.Value.(*cached).vec
		e.mu.Unlock()
		return v, nil
	}
	e.mu.Unlock()

	body, _ := json.Marshal(map[string]any{"texts": []string{q}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(e.URL, "/")+"/embed/text", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := e.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var out struct {
		Model      string      `json:"model"`
		Embeddings [][]float32 `json:"embeddings"`
		Error      string      `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK || len(out.Embeddings) != 1 {
		return nil, fmt.Errorf("text embedding: %d %s", res.StatusCode, out.Error)
	}
	if out.Model != Model {
		return nil, fmt.Errorf("text embedding: the service runs %s, search needs %s", out.Model, Model)
	}
	v := out.Embeddings[0]
	e.mu.Lock()
	e.cache[key] = e.order.PushFront(&cached{key, v})
	for e.order.Len() > cacheSize {
		old := e.order.Back()
		e.order.Remove(old)
		delete(e.cache, old.Value.(*cached).key)
	}
	e.mu.Unlock()
	return v, nil
}

// Literal is a vector in pgvector's text form.
func Literal(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'g', 6, 32))
	}
	b.WriteByte(']')
	return b.String()
}
