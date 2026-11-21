package library

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	RelPath string
	Size    int64
	ModTime time.Time
	Kind    string
}

// walkParallelism is how many directory reads and file stats run at once. Network shares
// answer each one with a round trip, so doing them concurrently is what makes scans fast.
const walkParallelism = 32

// Walk calls fn (from a single goroutine) for every regular file under root, skipping
// housekeeping folders, OS clutter and the excluded folders (paths relative to root). Symlinks
// are not followed, so a library can never escape its root. Unreadable subfolders are skipped;
// their files show up as missing.
func Walk(ctx context.Context, root string, exclude []string, fn func(Entry) error) error {
	return WalkFolder(ctx, root, "", false, exclude, fn)
}

// WalkFolder walks one folder of a library (relative to root; "" is the whole library), the same
// way. Shallow stops at the folder's own files. The library's root must be readable, so an
// unmounted share never reads as empty; a folder that no longer exists returns fs.ErrNotExist.
func WalkFolder(ctx context.Context, root, folder string, shallow bool, exclude []string, fn func(Entry) error) error {
	excluded := make(map[string]bool, len(exclude))
	for _, e := range exclude {
		excluded[e] = true
	}
	if _, err := os.ReadDir(root); err != nil {
		return err
	}
	start := root
	if folder != "" {
		start = filepath.Join(root, filepath.FromSlash(folder))
		if _, err := os.ReadDir(start); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan Entry, 1024)
	sem := make(chan struct{}, walkParallelism)
	var wg sync.WaitGroup

	var visit func(dir string)
	visit = func(dir string) {
		defer wg.Done()
		sem <- struct{}{}
		entries, err := os.ReadDir(dir)
		<-sem
		if err != nil {
			return
		}
		for _, d := range entries {
			if ctx.Err() != nil {
				return
			}
			name := d.Name()
			p := filepath.Join(dir, name)
			switch {
			case d.IsDir():
				rel, err := filepath.Rel(root, p)
				if !shallow && !skipDir(name) && err == nil && !excluded[filepath.ToSlash(rel)] {
					wg.Add(1)
					go visit(p)
				}
			case d.Type().IsRegular() && !skipFile(name):
				wg.Add(1)
				sem <- struct{}{}
				go func() {
					defer wg.Done()
					defer func() { <-sem }()
					info, err := d.Info()
					if err != nil {
						return
					}
					rel, err := filepath.Rel(root, p)
					if err != nil || strings.HasPrefix(rel, "..") {
						return
					}
					select {
					case results <- Entry{RelPath: filepath.ToSlash(rel), Size: info.Size(), ModTime: info.ModTime(), Kind: Kind(name)}:
					case <-ctx.Done():
					}
				}()
			}
		}
	}
	wg.Add(1)
	go visit(start)
	go func() { wg.Wait(); close(results) }()

	var firstErr error
	for e := range results {
		if firstErr != nil {
			continue // drain so the walkers can finish
		}
		if err := fn(e); err != nil {
			firstErr = err
			cancel()
		}
	}
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

// Ignored says whether a folder or file name is one a library never records (hidden folders,
// NAS and OS housekeeping, OS clutter), as the walk does.
func Ignored(name string, dir bool) bool {
	if dir {
		return skipDir(name)
	}
	return skipFile(name)
}

// Excluded says whether a folder (relative, forward slashes) is one of the excluded folders or
// inside one.
func Excluded(folder string, exclude []string) bool {
	for _, e := range exclude {
		if folder == e || strings.HasPrefix(folder, e+"/") {
			return true
		}
	}
	return false
}

// Stat returns the current size and modification time of a file.
func Stat(path string) (int64, time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, time.Time{}, err
	}
	return info.Size(), info.ModTime(), nil
}

// CleanFolder turns a folder given by a person ("lora-datasets/", "./videos/slug/SD Backup")
// into the form stored in a library's exclude list: relative, forward slashes, no trailing slash.
func CleanFolder(folder string) (string, error) {
	f := filepath.ToSlash(filepath.Clean(strings.TrimSpace(folder)))
	f = strings.Trim(f, "/")
	if f == "" || f == "." || f == ".." || strings.HasPrefix(f, "../") {
		return "", fmt.Errorf("%q isn't a folder inside the library", folder)
	}
	return f, nil
}
