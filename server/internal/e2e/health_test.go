package e2e

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"photostead.com/server/internal/api"
	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/media"
	"photostead.com/server/internal/store/db"
)

// Health, for the admin: a share Photostead can write to is fine, one it can't is a problem with
// what to do; a folder indexed in place that's no longer there is a problem too; members can't look.
func TestHealth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s := tempDatabase(t, ctx)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Bootstrap(ctx, s, adminEmail, adminPass); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.Q.GetUserByEmail(ctx, adminEmail)
	share := t.TempDir()
	if err := os.Mkdir(filepath.Join(share, "library"), 0o755); err != nil {
		t.Fatal(err)
	}
	h, _ := api.Handler(api.Options{Store: s, ServerZone: "UTC", ManagedRoot: share, Render: media.Renderer{DataDir: t.TempDir()}})
	srv := httptest.NewServer(h)
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if code := status(t, c, "POST", srv.URL+"/api/v1/auth/login", `{"email":"`+adminEmail+`","password":"`+adminPass+`"}`); code != 200 {
		t.Fatalf("login = %d", code)
	}
	health := func() map[string]api.HealthCheck {
		t.Helper()
		var out struct{ Checks []api.HealthCheck }
		getJSON(t, c, srv.URL+"/api/v1/server/health", &out)
		m := map[string]api.HealthCheck{}
		for _, ch := range out.Checks {
			m[ch.ID] = ch
		}
		return m
	}

	got := health()
	for _, id := range []string{"share", "space", "folders", "ai", "backups", "remote"} {
		if _, ok := got[id]; !ok {
			t.Errorf("no %s check: %+v", id, got)
		}
	}
	if ch := got["share"]; ch.State != "ok" || !strings.Contains(ch.Detail, filepath.Join(share, "library")) {
		t.Errorf("a share it can write to: %+v", ch)
	}
	if ch := got["space"]; ch.State == "" || ch.Detail == "" {
		t.Errorf("space: %+v", ch)
	}
	if ch := got["folders"]; ch.State != "ok" {
		t.Errorf("no folders indexed in place: %+v", ch)
	}
	if ch := got["ai"]; ch.State != "problem" || ch.Fix == "" { // no worker in this server
		t.Errorf("no worker: %+v", ch)
	}
	if ch := got["remote"]; ch.State != "ok" || !strings.Contains(ch.Detail, "home network") {
		t.Errorf("no Tailscale: %+v", ch)
	}

	// A GPU machine run on demand (away for days) isn't missed while nothing waits that only it can
	// do; a copy of a large video waiting for it is.
	if _, err := s.Pool.Exec(ctx, `INSERT INTO workers (name, token_hash, builtin, capabilities, last_seen_at) VALUES
		('built-in', 'a', true, '{"models": ["siglip2-base-patch16-256", "h264-1920-sdr-v1"]}', now()),
		('gpu-box', 'b', false, '{"gpu": true, "models": ["siglip2-base-patch16-256", "h264-1920-sdr-v1", "h264-1920-sdr-v1-large"]}', now() - interval '3 days')`); err != nil {
		t.Fatal(err)
	}
	if ch := health()["ai"]; ch.State != "ok" || ch.Detail != "All caught up." {
		t.Errorf("a GPU machine away with nothing for it: %+v", ch)
	}
	var asset uuid.UUID
	sum := sha256.Sum256([]byte("a large video"))
	if err := s.Pool.QueryRow(ctx, `INSERT INTO assets (owner_id, type, sha256, taken_at, local_date, date_source, date_basis)
		VALUES ($1, 'video', $2, now(), current_date, 'file', 'instant') RETURNING id`, owner.ID, sum[:]).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO worker_tasks (kind, asset_id, model) VALUES ('transcode', $1, 'h264-1920-sdr-v1-large')`, asset); err != nil {
		t.Fatal(err)
	}
	if ch := health()["ai"]; ch.State != "warn" || !strings.Contains(ch.Detail, "gpu-box") || !strings.Contains(ch.Fix, "Start it") {
		t.Errorf("a large video's copy waiting for the GPU machine: %+v", ch)
	}

	// A share it can't write to (PUID:PGID not its owner), and a mapped folder that went away.
	if err := os.Chmod(filepath.Join(share, "library"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(share, "library"), 0o755) })
	if _, err := s.Q.CreateLibrary(ctx, db.CreateLibraryParams{OwnerID: owner.ID, Name: "pictures", Kind: "indexed",
		RootPath: filepath.Join(t.TempDir(), "unmapped"), ReadOnly: true, SidecarMode: "shadow"}); err != nil {
		t.Fatal(err)
	}
	got = health()
	if ch := got["share"]; os.Getuid() != 0 && (ch.State != "problem" || !strings.Contains(ch.Detail, "can't write") || !strings.Contains(ch.Fix, "New Permissions")) {
		t.Errorf("a share it can't write to: %+v", ch)
	}
	if ch := got["folders"]; ch.State != "problem" || !strings.Contains(ch.Detail, "pictures") || ch.Fix == "" {
		t.Errorf("a folder that went away: %+v", ch)
	}

	member, err := s.Q.CreateUser(ctx, db.CreateUserParams{Email: "member@example.com", Username: "member", DisplayName: "Member", Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.SetPassword(ctx, s.Q, member, "a long member password"); err != nil {
		t.Fatal(err)
	}
	mjar, _ := cookiejar.New(nil)
	m := &http.Client{Jar: mjar}
	status(t, m, "POST", srv.URL+"/api/v1/auth/login", `{"email":"member@example.com","password":"a long member password"}`)
	if code := status(t, m, "GET", srv.URL+"/api/v1/server/health", ""); code != 403 {
		t.Errorf("a member looking = %d", code)
	}
}
