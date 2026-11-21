package e2e

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"photostead.com/server/internal/api"
	"photostead.com/server/internal/backup"
)

// The backups screen: the copies kept and how the last one went, for the admin only, and a
// failure shown in the admin's status line until the next success.
func TestBackups(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s, _, url, c := signedIn(t, ctx)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "photostead-2026-10-21-0330.dump"), make([]byte, 2048), 0o600)
	h, _ := api.Handler(api.Options{Store: s, Backup: backup.Config{Dir: dir, Keep: 14, Zone: time.UTC}})
	srv := httptest.NewServer(h)
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	admin := &http.Client{Jar: jar}
	if code := status(t, admin, "POST", srv.URL+"/api/v1/auth/login", `{"email":"`+adminEmail+`","password":"`+adminPass+`"}`); code != 200 {
		t.Fatalf("login = %d", code)
	}
	var b struct {
		On      bool
		Keep    int
		Problem string
		Copies  []backup.Copy
		NextAt  time.Time
	}
	getJSON(t, admin, srv.URL+"/api/v1/server/backups", &b)
	if !b.On || b.Keep != 14 || len(b.Copies) != 1 || b.Copies[0].Bytes != 2048 || b.Problem != "" || b.NextAt.Before(time.Now()) {
		t.Errorf("backups = %+v", b)
	}

	// A failure shows, there and in the status line, until a success.
	if err := backup.Record(ctx, s.Q, backup.Copy{}, 0, errors.New("pg_dump: no space left on device")); err != nil {
		t.Fatal(err)
	}
	var st struct{ Backup string }
	getJSON(t, admin, srv.URL+"/api/v1/status", &st)
	if st.Backup != "The last backup of the database failed: pg_dump: no space left on device" {
		t.Errorf("status says %q", st.Backup)
	}
	_ = backup.Record(ctx, s.Q, backup.Copy{Name: "photostead-2026-10-22-0330.dump", Bytes: 2048, At: time.Now()}, time.Second, nil)
	st.Backup = ""
	getJSON(t, admin, srv.URL+"/api/v1/status", &st)
	if st.Backup != "" {
		t.Errorf("after a success, status says %q", st.Backup)
	}

	// Someone else in the household doesn't see or start backups.
	var inv struct{ Token string }
	doJSON(t, c, "POST", url+"/api/v1/household/invites", `{"role":"adult"}`, &inv)
	jar2, _ := cookiejar.New(nil)
	sam := &http.Client{Jar: jar2}
	status(t, sam, "POST", srv.URL+"/api/v1/invites/"+inv.Token, mustJSON(map[string]string{"displayName": "Sam", "email": "sam@example.com", "password": "a long password for sam"}))
	for _, m := range []string{"GET", "POST"} {
		if code := status(t, sam, m, srv.URL+"/api/v1/server/backups", ""); code != 403 {
			t.Errorf("a member: %s backups = %d", m, code)
		}
	}
}
