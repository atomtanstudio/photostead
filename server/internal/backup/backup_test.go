package backup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Only copies are listed and pruned (newest first by name), never anything else in the folder.
func TestListAndPrune(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"photostead-2026-10-20-0330.dump", "photostead-2026-10-22-0330.dump", "photostead-2026-10-21-0330.dump",
		"photostead-2026-10-22-0331.dump.partial", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	copies, err := List(dir)
	if err != nil || len(copies) != 3 || copies[0].Name != "photostead-2026-10-22-0330.dump" || copies[2].Name != "photostead-2026-10-20-0330.dump" {
		t.Fatalf("List = %+v %v", copies, err)
	}
	if err := prune(dir, 2); err != nil {
		t.Fatal(err)
	}
	left, _ := os.ReadDir(dir)
	var names []string
	for _, e := range left {
		names = append(names, e.Name())
	}
	if got := strings.Join(names, " "); got != "notes.txt photostead-2026-10-21-0330.dump photostead-2026-10-22-0330.dump photostead-2026-10-22-0331.dump.partial" {
		t.Errorf("after pruning to 2: %s", got)
	}
	if c, _ := List(filepath.Join(dir, "none")); c != nil {
		t.Errorf("a folder that isn't there lists %v", c)
	}
}

func TestProblem(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	night := &Copy{Name: "x", At: now.Add(-9 * time.Hour)}
	for _, c := range []struct {
		st   Status
		want string
	}{
		{Status{}, ""},
		{Status{Last: night}, ""},
		{Status{Last: &Copy{At: now.Add(-40 * time.Hour)}}, "No backup of the database since Oct 20."},
		{Status{Error: "pg_dump: disk full", ErrorAt: now.Add(-time.Hour)}, "The last backup of the database failed: pg_dump: disk full"},
		{Status{Last: night, Error: "pg_dump: disk full", ErrorAt: now.Add(-time.Hour)}, "The last backup of the database failed: pg_dump: disk full"},
		{Status{Last: night, Error: "old", ErrorAt: now.Add(-20 * time.Hour)}, ""}, // a success since
	} {
		if got := c.st.Problem(now); got != c.want {
			t.Errorf("%+v: %q, want %q", c.st, got, c.want)
		}
	}
}

func TestWithUserAndDB(t *testing.T) {
	got, err := withUserAndDB("postgres://photostead@/photostead?host=/run/postgresql", "postgres", "photostead_restore_check")
	if err != nil || got != "postgres://postgres@/photostead_restore_check?host=/run/postgresql" {
		t.Errorf("= %q %v", got, err)
	}
}

// A real copy, where pg_dump is installed (in the container it is; on a Mac usually not).
func TestRun(t *testing.T) {
	url := os.Getenv("PHOTOSTEAD_DATABASE_URL")
	if _, err := exec.LookPath("pg_dump"); err != nil || url == "" {
		t.Skip("needs pg_dump and PHOTOSTEAD_DATABASE_URL")
	}
	dir := t.TempDir()
	c := Config{Dir: dir, Keep: 2, DatabaseURL: url, Zone: time.UTC}
	for i := range 3 {
		if _, err := Run(context.Background(), c, time.Date(2026, 10, 20+i, 3, 30, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}
	copies, _ := List(dir)
	if len(copies) != 2 || copies[0].Name != "photostead-2026-10-22-0330.dump" || copies[0].Bytes == 0 {
		t.Errorf("copies = %+v", copies)
	}
}

func TestErrorLine(t *testing.T) {
	msg := "pg_restore: error: could not execute query: ERROR:  could not resize shared memory segment\nCommand was: CREATE INDEX …\npg_restore: error: a worker process died unexpectedly"
	if got := errorLine(msg); !strings.Contains(got, "could not resize shared memory") {
		t.Errorf("= %q", got)
	}
	if got := errorLine("one\ntwo"); got != "two" {
		t.Errorf("= %q", got)
	}
}
