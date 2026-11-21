package move

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCopyVerified(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.jpg")
	data := bytes.Repeat([]byte("photostead"), 100_000)
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}
	mtime := time.Date(2019, 7, 4, 12, 0, 0, 0, time.UTC)
	dst := filepath.Join(dir, "out", "2019", "dst.jpg")

	sum, n, err := copyVerified(context.Background(), src, dst, mtime)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(data)
	if !bytes.Equal(sum, want[:]) || n != int64(len(data)) {
		t.Fatalf("sum %x size %d, want %x %d", sum, n, want, len(data))
	}
	if info, err := os.Stat(dst); err != nil || !info.ModTime().Equal(mtime) {
		t.Fatalf("copy missing or mtime not kept: %v %v", info, err)
	}
	// Running again over a finished copy accepts it.
	if _, _, err := copyVerified(context.Background(), src, dst, mtime); err != nil {
		t.Fatalf("rerun: %v", err)
	}
}

func TestCopyVerifiedStopsWhenCancelled(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "clip.mov")
	if err := os.WriteFile(src, make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out", "clip.mov")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := copyVerified(ctx, src, dst, time.Now()); err == nil {
		t.Fatal("a cancelled copy succeeded")
	}
	for _, p := range []string{dst, dst + partialSuffix} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s left behind", filepath.Base(p))
		}
	}
}
