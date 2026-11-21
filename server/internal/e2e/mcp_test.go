package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"photostead.com/server/internal/api"
	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/store/db"
)

// TestMCP: the MCP server answers only with a key, lists its tools, and runs them as the key's owner.
func TestMCP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s := tempDatabase(t, ctx)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := auth.Setup(ctx, s, "Rich", "rich@example.test", "a long enough password", "Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	key := api.NewAPIKey()
	if _, err := s.Q.CreateAPIKey(ctx, db.CreateAPIKeyParams{UserID: u.ID, Name: "mcp", TokenHash: api.APIKeyHash(key)}); err != nil {
		t.Fatal(err)
	}
	h, _ := api.Handler(api.Options{Store: s, ServerZone: "Europe/London"})
	srv := httptest.NewServer(h)
	defer srv.Close()

	rpc := func(auth, body string) (int, map[string]any) {
		req, _ := http.NewRequest("POST", srv.URL+"/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.NewDecoder(bytes.NewReader(b)).Decode(&out)
		return resp.StatusCode, out
	}
	if code, _ := rpc("", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); code != 401 {
		t.Errorf("without a key: %d, want 401", code)
	}
	if code, _ := rpc("psk_not-a-real-key", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); code != 401 {
		t.Errorf("with a wrong key: %d, want 401", code)
	}
	code, out := rpc(key, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	if code != 200 || out["result"] == nil {
		t.Fatalf("initialize: %d %v", code, out)
	}
	_, out = rpc(key, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	var names []string
	for _, tool := range out["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	for _, want := range []string{"search", "ask", "get_item", "view_item", "library_counts", "create_album", "add_media"} {
		if !strings.Contains(strings.Join(names, " "), want) {
			t.Errorf("tools/list lacks %s: %v", want, names)
		}
	}
	// A tool runs as the key's owner, through the same API: an empty library, and an album made for them.
	_, out = rpc(key, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"library_counts","arguments":{}}}`)
	if r, ok := out["result"].(map[string]any); !ok || r["isError"] == true || r["structuredContent"].(map[string]any)["all"] != float64(0) {
		t.Errorf("library_counts: %v", out)
	}
	_, out = rpc(key, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"create_album","arguments":{"title":"From MCP"}}}`)
	if r, ok := out["result"].(map[string]any); !ok || r["isError"] == true {
		t.Fatalf("create_album: %v", out)
	}
	var owner string
	if err := s.Pool.QueryRow(ctx, `SELECT owner_id::text FROM albums WHERE title = 'From MCP'`).Scan(&owner); err != nil || owner != u.ID.String() {
		t.Errorf("the album belongs to %q (%v), want %s", owner, err, u.ID)
	}
}
