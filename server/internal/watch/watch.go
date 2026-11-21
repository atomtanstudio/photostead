// Package watch notices new, changed and removed files in watched libraries as it happens and
// queues a scan of just the folder that changed, so new photos show up on their own.
//
// It uses the kernel's change notifications (inotify), one watch per folder. On Unraid those
// fire for anything written through the share (/mnt/user: network copies, other apps) and, when
// a library is mounted from a disk path such as /mnt/cache, for everything written there. A file
// written straight to a disk path behind a share isn't seen; the nightly rescan finds it.
package watch

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"photostead.com/server/internal/jobs"
	"photostead.com/server/internal/library"
	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

// Jobs queues background work; River's client is one.
type Jobs interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// State is what the watcher is doing, for the app's status line.
type State struct {
	Libraries int       `json:"libraries" doc:"Libraries being watched"`
	Folders   int       `json:"folders" doc:"Folders being watched"`
	Since     time.Time `json:"since,omitzero"`
	Problem   string    `json:"problem,omitempty" doc:"Why some folders aren't watched, if any aren't"`
}

type Watcher struct {
	Store *store.Store
	Jobs  Jobs
	Log   *slog.Logger

	Quiet   time.Duration // how long a folder must be still before it's scanned
	MaxWait time.Duration // the longest a folder that keeps changing waits
	Reload  time.Duration // how often the list of watched libraries is checked

	mu    sync.Mutex
	state State
}

func (w *Watcher) State() State {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state
}

func (w *Watcher) setState(s State) {
	w.mu.Lock()
	w.state = s
	w.mu.Unlock()
}

// Run watches until ctx ends, starting over whenever the watched libraries change (one is
// added, excluded folders change, watching is turned on or off).
func (w *Watcher) Run(ctx context.Context) {
	if w.Quiet == 0 {
		w.Quiet = 10 * time.Second
	}
	if w.MaxWait == 0 {
		w.MaxWait = 2 * time.Minute
	}
	if w.Reload == 0 {
		w.Reload = time.Minute
	}
	for ctx.Err() == nil {
		libs, err := w.Store.Q.WatchedLibraries(ctx)
		if err != nil {
			w.Log.Warn("watch: couldn't list libraries", "error", err)
			sleep(ctx, w.Reload)
			continue
		}
		if err := w.session(ctx, libs); err != nil && ctx.Err() == nil {
			w.Log.Warn("watch: stopped; starting again shortly", "error", err)
			w.setState(State{Problem: "Not watching: " + err.Error()})
			sleep(ctx, w.Reload)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// signature changes when anything the watcher depends on changes.
func signature(libs []db.Library) string {
	var b strings.Builder
	for _, l := range libs {
		b.WriteString(l.ID.String() + "|" + l.RootPath + "|" + strings.Join(l.Exclude, ",") + "\n")
	}
	return b.String()
}

type dirRef struct {
	lib int    // index into session.libs
	rel string // folder relative to the library, "" for its top
}

type scanKey struct {
	lib     uuid.UUID
	folder  string
	shallow bool
}

type pending struct{ first, last time.Time }

type session struct {
	w       *Watcher
	fw      *fsnotify.Watcher
	libs    []db.Library
	dirs    map[string]dirRef // absolute folder -> where it is
	pending map[scanKey]*pending
	full    map[uuid.UUID]bool // libraries needing a full scan (missed events)
	problem string
}

func (w *Watcher) session(ctx context.Context, libs []db.Library) error {
	fw, err := fsnotify.NewBufferedWatcher(16384)
	if err != nil {
		return err
	}
	defer fw.Close()
	s := &session{w: w, fw: fw, libs: libs, dirs: map[string]dirRef{}, pending: map[scanKey]*pending{}, full: map[uuid.UUID]bool{}}
	started := time.Now()
	var ids []uuid.UUID
	for i, l := range libs {
		ids = append(ids, l.ID)
		// Catch up on what changed while nothing was watching: folders whose contents changed
		// since the watcher last ran (or since the last full scan, the first time).
		var since *time.Time
		switch {
		case l.WatchedAt != nil:
			since = l.WatchedAt
		case l.LastScanStartedAt != nil:
			since = l.LastScanStartedAt
		default:
			s.full[l.ID] = true // never scanned
		}
		changed, err := s.add(i, l.RootPath, since)
		if err != nil {
			s.problem = err.Error()
			w.Log.Warn("watch: not watching all of a library; the nightly rescan covers the rest", "library", l.Name, "error", err)
		}
		for _, rel := range changed {
			s.queue(i, rel, true)
		}
		if len(changed) > 0 {
			w.Log.Info("watch: catching up", "library", l.Name, "folders changed while not watching", len(changed))
		}
	}
	w.Log.Info("watching for new files", "libraries", len(libs), "folders", len(s.dirs), "took", time.Since(started).Round(time.Millisecond))
	if err := w.Store.Q.MarkWatched(ctx, ids); err != nil {
		return err
	}
	s.publish(started)

	sig := signature(libs)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	heartbeat := time.NewTicker(5 * time.Minute)
	defer heartbeat.Stop()
	reload := time.NewTicker(w.Reload)
	defer reload.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-fw.Events:
			if !ok {
				return errors.New("change notifications closed")
			}
			s.handle(ev)
		case err, ok := <-fw.Errors:
			if !ok {
				return errors.New("change notifications closed")
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				// Too many changes at once to follow one by one: rescan everything.
				w.Log.Warn("watch: too many changes at once; rescanning the watched libraries")
				for _, l := range libs {
					s.full[l.ID] = true
				}
			} else {
				w.Log.Warn("watch: error", "error", err)
			}
		case <-tick.C:
			s.flush(ctx)
			s.publish(started)
		case <-heartbeat.C:
			if err := w.Store.Q.MarkWatched(ctx, ids); err != nil {
				w.Log.Warn("watch: couldn't record the heartbeat", "error", err)
			}
		case <-reload.C:
			now, err := w.Store.Q.WatchedLibraries(ctx)
			if err == nil && signature(now) != sig {
				w.Log.Info("watch: libraries changed; watching again")
				s.flush(ctx)
				return nil
			}
		}
	}
}

func (s *session) publish(since time.Time) {
	s.w.setState(State{Libraries: len(s.libs), Folders: len(s.dirs), Since: since, Problem: s.problem})
}

var errLimit = errors.New("the system's limit on watched folders was reached (fs.inotify.max_user_watches)")

// add watches a folder and everything below it, and returns the folders (relative to the
// library) whose contents changed after since.
func (s *session) add(lib int, dir string, since *time.Time) ([]string, error) {
	l := s.libs[lib]
	var changed []string
	stack := []string{dir}
	for len(stack) > 0 {
		d := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		rel, err := filepath.Rel(l.RootPath, d)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			rel = ""
		}
		if rel != "" && (library.Ignored(filepath.Base(d), true) || library.Excluded(rel, l.Exclude)) {
			continue
		}
		if err := s.fw.Add(d); err != nil {
			if errors.Is(err, syscall.ENOSPC) {
				return changed, errLimit
			}
			continue // unreadable or gone: nothing to watch
		}
		s.dirs[d] = dirRef{lib: lib, rel: rel}
		if since != nil {
			// A folder's time changes when files are added, removed or renamed in it.
			if info, err := os.Stat(d); err == nil && info.ModTime().After(since.Add(-2*time.Minute)) {
				changed = append(changed, rel)
			}
		}
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() { // not following symlinks: a library never leaves its root
				stack = append(stack, filepath.Join(d, e.Name()))
			}
		}
	}
	return changed, nil
}

// forget stops watching a folder that went away, and everything that was below it.
func (s *session) forget(dir string) {
	for d := range s.dirs {
		if d == dir || strings.HasPrefix(d, dir+string(filepath.Separator)) {
			_ = s.fw.Remove(d)
			delete(s.dirs, d)
		}
	}
}

func (s *session) handle(ev fsnotify.Event) {
	if ev.Has(fsnotify.Chmod) && !ev.Has(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) {
		return
	}
	parent, ok := s.dirs[filepath.Dir(ev.Name)]
	if !ok {
		return
	}
	name := filepath.Base(ev.Name)
	l := s.libs[parent.lib]
	rel := path.Join(parent.rel, name)
	switch {
	case ev.Has(fsnotify.Create):
		if info, err := os.Lstat(ev.Name); err == nil && info.IsDir() {
			if library.Ignored(name, true) || library.Excluded(rel, l.Exclude) {
				return
			}
			// A new folder (made, copied in or moved here): watch it and scan all of it.
			if _, err := s.add(parent.lib, ev.Name, nil); err != nil {
				s.problem = err.Error()
			}
			s.queue(parent.lib, rel, false)
			return
		}
	case ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename):
		if _, was := s.dirs[ev.Name]; was {
			// A folder went (deleted or moved away): its files are missing from here.
			s.forget(ev.Name)
			s.queue(parent.lib, rel, false)
			return
		}
	}
	if library.Ignored(name, false) {
		return
	}
	s.queue(parent.lib, parent.rel, true)
}

// queue notes that a folder needs scanning; it's scanned once it has been quiet for a while.
func (s *session) queue(lib int, folder string, shallow bool) {
	id := s.libs[lib].ID
	if shallow {
		if _, deep := s.pending[scanKey{id, folder, false}]; deep {
			return // a scan of the folder and everything in it is already coming
		}
	} else {
		delete(s.pending, scanKey{id, folder, true})
	}
	now := time.Now()
	k := scanKey{id, folder, shallow}
	if p := s.pending[k]; p != nil {
		p.last = now
	} else {
		s.pending[k] = &pending{first: now, last: now}
	}
}

// flush queues scans for folders that have gone quiet (or waited long enough).
func (s *session) flush(ctx context.Context) {
	w := s.w
	for id := range s.full {
		if _, err := w.Jobs.Insert(ctx, jobs.ScanLibraryArgs{LibraryID: id}, nil); err != nil {
			w.Log.Warn("watch: couldn't queue a rescan", "error", err)
			return
		}
		delete(s.full, id)
	}
	now := time.Now()
	var due []scanKey
	for k, p := range s.pending {
		if now.Sub(p.last) >= w.Quiet || now.Sub(p.first) >= w.MaxWait {
			due = append(due, k)
		}
	}
	// Parents first, so a deep scan makes the scans of folders inside it unnecessary.
	slices.SortFunc(due, func(a, b scanKey) int { return strings.Compare(a.folder, b.folder) })
	var deep []scanKey
	for _, k := range due {
		covered := slices.ContainsFunc(deep, func(d scanKey) bool {
			return d.lib == k.lib && (d.folder == "" || k.folder == d.folder || strings.HasPrefix(k.folder, d.folder+"/"))
		})
		if !covered {
			if _, err := w.Jobs.Insert(ctx, jobs.ScanFolderArgs{LibraryID: k.lib, Folder: k.folder, Shallow: k.shallow}, nil); err != nil {
				w.Log.Warn("watch: couldn't queue a folder scan", "error", err)
				return // try again on the next tick
			}
			if !k.shallow {
				deep = append(deep, k)
			}
		}
		delete(s.pending, k)
	}
}
