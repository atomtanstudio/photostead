package e2e

import (
	"bufio"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"photostead.com/server/internal/backup"
	"photostead.com/server/internal/jobs"
)

// The integrity scrub: a file damaged in place (the same size and time, other bytes) is found and
// shown; one changed on purpose (another size) and the rest aren't called damaged. And the manifest
// written with a backup lists every file with its hash.
func TestIntegrity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s, _, url, c := signedIn(t, ctx)
	type file struct {
		path  string
		mtime time.Time
	}
	var picked []file
	rows, _ := s.Pool.Query(ctx, `SELECT l.root_path || '/' || f.rel_path, f.mtime FROM files f JOIN libraries l ON l.id = f.library_id
		WHERE f.state = 'present' AND f.kind = 'image' AND f.size > 1000 ORDER BY f.rel_path LIMIT 2`)
	for rows.Next() {
		var f file
		_ = rows.Scan(&f.path, &f.mtime)
		picked = append(picked, f)
	}
	rows.Close()
	if len(picked) != 2 {
		t.Fatalf("picked %d files", len(picked))
	}
	// Damaged in place: one byte flipped, its size and time as they were.
	b, _ := os.ReadFile(picked[0].path)
	b[len(b)/2] ^= 0xff
	if err := os.WriteFile(picked[0].path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(picked[0].path, picked[0].mtime, picked[0].mtime)
	// Changed on purpose: longer.
	f, _ := os.OpenFile(picked[1].path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.Write([]byte("more"))
	f.Close()

	// Checked when they were indexed: as if that was a while ago, so they're due again.
	if _, err := s.Pool.Exec(ctx, `UPDATE files SET last_verified_at = now() - interval '10 days'`); err != nil {
		t.Fatal(err)
	}
	insert, err := river.NewClient(riverpgxv5.New(s.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := insert.Insert(ctx, jobs.ScrubArgs{Minutes: 1}, nil); err != nil {
		t.Fatal(err)
	}
	waitForJobs(t, ctx, s)

	var out struct {
		Files struct {
			Files, Checked int64
			Damaged        []struct{ Library, Path string }
			Last           jobs.ScrubStatus
		}
	}
	getJSON(t, c, url+"/api/v1/server/backups", &out)
	fs := out.Files
	if len(fs.Damaged) != 1 || !strings.HasSuffix(picked[0].path, fs.Damaged[0].Path) || fs.Last.Damaged != 1 || fs.Last.Changed != 1 ||
		fs.Last.Checked < 2 || !fs.Last.Finished || fs.Checked != fs.Files {
		t.Errorf("after the check: %+v", fs)
	}
	var status struct{ Backup string }
	getJSON(t, c, url+"/api/v1/status", &status)
	if !strings.Contains(status.Backup, "no longer matches") {
		t.Errorf("the status line = %q", status.Backup)
	}

	// The manifest: a line for every file present, with its hash.
	dir := t.TempDir()
	if err := backup.WriteManifest(ctx, s.Q, dir, "photostead-2026-01-01-0000.dump"); err != nil {
		t.Fatal(err)
	}
	m, err := os.Open(filepath.Join(dir, "photostead-2026-01-01-0000.files.tsv.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	z, err := gzip.NewReader(m)
	if err != nil {
		t.Fatal(err)
	}
	var lines int
	sc := bufio.NewScanner(z)
	for sc.Scan() {
		if lines > 0 && len(strings.Split(sc.Text(), "\t")[4]) != 64 {
			t.Errorf("a line without a hash: %q", sc.Text())
		}
		lines++
	}
	var present int
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM files WHERE state = 'present'`).Scan(&present)
	if lines != present+1 {
		t.Errorf("manifest has %d lines for %d files", lines, present)
	}
}
