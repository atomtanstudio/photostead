package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"photostead.com/server/internal/backup"
)

// Health is what the admin should know about this server, in plain words, each with what to do
// when something's wrong: whether the photo share takes files, disk space, memory, the folders it
// indexes in place, the AI work, backups and remote access. The web app shows it under Health, and
// flags it in the sidebar when anything needs a look; it replaces reading the container's log.

// HealthCheck is one thing looked at.
type HealthCheck struct {
	ID     string `json:"id" enum:"share,space,memory,folders,ai,backups,remote"`
	State  string `json:"state" enum:"ok,warn,problem" doc:"warn: worth a look; problem: something doesn't work"`
	Title  string `json:"title"`
	Detail string `json:"detail" doc:"What's so, in a sentence"`
	Fix    string `json:"fix,omitempty" doc:"What to do about it, when it isn't ok"`
}

type healthOutput struct {
	Body struct {
		Checks []HealthCheck `json:"checks"`
	}
}

func (s *server) registerHealth() {
	huma.Register(s.api, huma.Operation{
		OperationID: "health", Method: http.MethodGet, Path: "/api/v1/server/health", Tags: []string{"Server"},
		Summary: "What needs a look on this server, in plain words, with what to do", Description: "Admin only.", Security: sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*healthOutput, error) {
		if err := s.admin(ctx); err != nil {
			return nil, err
		}
		out := &healthOutput{}
		out.Body.Checks = append(out.Body.Checks, s.checkShare(), s.checkSpace())
		if c, ok := checkMemory(); ok {
			out.Body.Checks = append(out.Body.Checks, c)
		}
		for _, check := range []func(context.Context) (HealthCheck, error){s.checkFolders, s.checkAI, s.checkBackups, s.checkRemote} {
			c, err := check(ctx)
			if err != nil {
				return nil, err
			}
			out.Body.Checks = append(out.Body.Checks, c)
		}
		return out, nil
	})
}

func healthy(c HealthCheck, detail string) HealthCheck {
	c.State, c.Detail = "ok", detail
	return c
}

func unwell(c HealthCheck, state, detail, fix string) HealthCheck {
	c.State, c.Detail, c.Fix = state, detail, fix
	return c
}

// checkShare: the photostead share is there and Photostead can write to it (the usual way a new
// install fails: a share owned by another user than PUID:PGID).
func (s *server) checkShare() HealthCheck {
	c := HealthCheck{ID: "share", Title: "Photo share"}
	root := s.ManagedRoot
	if root == "" {
		return unwell(c, "warn", "No photo share is set, so uploads and phone backups have nowhere to go.",
			"Set PHOTOSTEAD_MANAGED_ROOT to the folder they should go in (/photos in the container).")
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return unwell(c, "problem", fmt.Sprintf("There's no photo share at %s, so uploads and phone backups have nowhere to go.", root),
			fmt.Sprintf("Map your photostead share to %s in the container's settings (on Unraid: Docker › Photostead › Edit, Photo library).", root))
	}
	dir := filepath.Join(root, "library")
	if _, err := os.Stat(dir); err != nil {
		dir = root
	}
	f, err := os.CreateTemp(dir, ".photostead-health-*")
	if err != nil {
		uid, gid := os.Getuid(), os.Getgid()
		return unwell(c, "problem",
			fmt.Sprintf("Photostead can't write to %s (it runs as user %d, group %d), so uploads and phone backups fail.", dir, uid, gid),
			fmt.Sprintf("Give it the folder: on Unraid, run Tools › New Permissions on the photostead share; elsewhere, chown -R %d:%d on the share, or set PUID and PGID to its owner.", uid, gid))
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	return healthy(c, fmt.Sprintf("Uploads and phone backups go to %s.", dir))
}

// diskRoom is a disk's free and total bytes, and which disk it is.
type diskRoom struct {
	free, total uint64
	fsid        [2]int32
}

// lowOn says how short a disk is: "problem" under 2 GB free, "warn" under 5% of it (at least 10 GB,
// at most 200 GB, so a big array isn't flagged with terabytes free), else "".
func lowOn(sp diskRoom) string {
	const gb = 1 << 30
	warnAt := min(max(sp.total/20, 10*gb), 200*gb)
	switch {
	case sp.free < 2*gb:
		return "problem"
	case sp.free < warnAt:
		return "warn"
	}
	return ""
}

// checkSpace: room for the database and thumbnails (the data folder), and for originals (the share).
func (s *server) checkSpace() HealthCheck {
	c := HealthCheck{ID: "space", Title: "Disk space"}
	data, hasData := diskSpace(s.Render.DataDir)
	share, hasShare := diskSpace(s.ManagedRoot)
	var parts []string
	state, fix := "ok", ""
	if hasData {
		parts = append(parts, fmt.Sprintf("%s free for the database and thumbnails (%s)", sizeText(data.free), s.Render.DataDir))
		if l := lowOn(data); l != "" {
			state, fix = l, "Free some space on that disk, or give Photostead's data folder a bigger one: when it fills up, the database stops taking changes."
		}
	}
	if hasShare && !(hasData && share.fsid == data.fsid) {
		parts = append(parts, fmt.Sprintf("%s for originals (%s)", sizeText(share.free), s.ManagedRoot))
		if l := lowOn(share); l != "" && (state == "ok" || l == "problem") {
			state, fix = l, "Free some space in the photo share, or add a disk: new uploads and phone backups stop when it's full."
		}
	} else if hasShare && hasData {
		parts[0] = fmt.Sprintf("%s free for originals, the database and thumbnails", sizeText(data.free))
	}
	if len(parts) == 0 {
		return healthy(c, "Not measured here.")
	}
	return unwell(c, state, strings.Join(parts, "; ")+".", fix)
}

// sizeText is a size as people read it (decimal, as disks are sold).
func sizeText(n uint64) string {
	switch {
	case n >= 1e12:
		return fmt.Sprintf("%.1f TB", float64(n)/1e12)
	case n >= 1e9:
		return fmt.Sprintf("%.0f GB", float64(n)/1e9)
	}
	return fmt.Sprintf("%.0f MB", float64(n)/1e6)
}

// checkMemory: enough for the database (it keeps 1 GB for itself) and search. Only on Linux, where
// it's known: the machine's memory, or the container's limit when there is one.
func checkMemory() (HealthCheck, bool) {
	total := memoryTotal()
	if total == 0 {
		return HealthCheck{}, false
	}
	c := HealthCheck{ID: "memory", Title: "Memory"}
	if total < 3500<<20 { // a "4 GB" machine reports a little under
		return unwell(c, "warn", fmt.Sprintf("Photostead has %s of memory. With less than 4 GB, the database, search and faces slow down, and a big import can stop.", sizeText(total)),
			"Give the machine, or the container, more memory."), true
	}
	return healthy(c, sizeText(total)+"."), true
}

func memoryTotal() uint64 {
	if runtime.GOOS != "linux" {
		return 0
	}
	var total uint64
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		for line := range strings.SplitSeq(string(b), "\n") {
			if f := strings.Fields(line); len(f) >= 2 && f[0] == "MemTotal:" {
				kb, _ := strconv.ParseUint(f[1], 10, 64)
				total = kb << 10
			}
		}
	}
	for _, limit := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} { // cgroup v2, v1
		if b, err := os.ReadFile(limit); err == nil {
			if n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64); err == nil && n > 0 && (total == 0 || n < total) {
				total = n
			}
		}
	}
	return total
}

// checkFolders: every folder indexed in place is still there (a mapping dropped from the container
// leaves its items unopenable), and the watcher has no trouble.
func (s *server) checkFolders(ctx context.Context) (HealthCheck, error) {
	c := HealthCheck{ID: "folders", Title: "Your folders"}
	libs, err := s.Store.Q.ListLibraries(ctx)
	if err != nil {
		return c, err
	}
	var indexed int
	var gone []string
	for _, l := range libs {
		if l.Kind != "indexed" {
			continue
		}
		indexed++
		if info, err := os.Stat(l.RootPath); err != nil || !info.IsDir() {
			gone = append(gone, fmt.Sprintf("%s (%s)", l.Name, l.RootPath))
		}
	}
	switch {
	case len(gone) > 0:
		return unwell(c, "problem", fmt.Sprintf("%s %s isn't there any more, so its photos can't be opened.", map[bool]string{true: "This folder", false: "These folders"}[len(gone) == 1], strings.Join(gone, ", ")),
			"Map it again in the container's settings (a read-only path under /libraries). If it's gone for good and its photos have a copy in the share: photostead library remove -name <name>."), nil
	case s.Watching != nil && s.Watching().Problem != "":
		return unwell(c, "warn", s.Watching().Problem, "New files there still show up after the nightly rescan."), nil
	case indexed == 0:
		return healthy(c, "None indexed in place: everything is in the photo share."), nil
	}
	return healthy(c, fmt.Sprintf("%s %s indexed in place, read-only%s.", thousands(int64(indexed)), map[bool]string{true: "folder", false: "folders"}[indexed == 1],
		map[bool]string{true: ", and watched for new files", false: ""}[s.Watching != nil && s.Watching().Libraries > 0])), nil
}

// checkAI: something is working through search, faces and text, and what's queued can be done.
// A GPU machine may run on demand (it stops once its work is done), so it's only missed when work
// waits that no running worker can do: copies of large videos, say.
func (s *server) checkAI(ctx context.Context) (HealthCheck, error) {
	c := HealthCheck{ID: "ai", Title: "Search, faces and text"}
	t, err := s.Store.Q.WorkerTaskCounts(ctx)
	if err != nil {
		return c, err
	}
	workers, err := s.Store.Q.ListWorkers(ctx)
	if err != nil {
		return c, err
	}
	stuck, err := s.Store.Q.WorkNoWorkerTakes(ctx)
	if err != nil {
		return c, err
	}
	active := 0
	for _, w := range workers {
		if w.RevokedAt == nil {
			active++
		}
	}
	since := "it started"
	if t.LastSeen.Year() > 1970 {
		since = t.LastSeen.Local().Format("Jan 2, 15:04")
	}
	switch {
	case active == 0:
		return unwell(c, "problem", "No worker is set up, so search by meaning, faces and text in pictures don't run.",
			"Photostead's own worker starts with the container: restart it, and see its log if this stays."), nil
	case t.Waiting > 0 && time.Since(t.LastSeen) > 10*time.Minute:
		return unwell(c, "problem", fmt.Sprintf("%s waiting, and no worker has checked in since %s.", plural(t.Waiting, "picture is", "pictures are"), since),
			"Photostead's own worker runs inside the container: restart the container, and see its log if this comes back."), nil
	case len(stuck) > 0:
		var waiting int64
		var who []string
		for _, st := range stuck {
			waiting += st.Waiting
			for _, w := range workers {
				var caps struct{ Models []string }
				_ = json.Unmarshal(w.Capabilities, &caps)
				if w.RevokedAt == nil && slices.Contains(caps.Models, st.Model) && !slices.Contains(who, w.Name) {
					who = append(who, w.Name)
				}
			}
		}
		if len(who) == 0 {
			return unwell(c, "warn", fmt.Sprintf("%s waiting for a model no worker has.", plural(waiting, "task is", "tasks are")),
				"A worker with it picks them up: update the GPU worker, or see Photostead's log."), nil
		}
		return unwell(c, "warn", fmt.Sprintf("%s waiting for %s, which isn't running: only it can do them (copies of large videos, say).", plural(waiting, "task is", "tasks are"), strings.Join(who, " or ")),
			"Start it on its machine: it works through them, and stops by itself when they're done."), nil
	case t.Waiting > 0:
		return healthy(c, fmt.Sprintf("Working through %s.", plural(t.Waiting, "picture", "pictures"))), nil
	}
	return healthy(c, "All caught up."), nil
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return thousands(n) + " " + many
}

// thousands writes a count as people read it: 5,523.
func thousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// checkBackups: the nightly copies of the database are on and working, and no original has changed
// since it came in.
func (s *server) checkBackups(ctx context.Context) (HealthCheck, error) {
	c := HealthCheck{ID: "backups", Title: "Backups"}
	if n, err := s.Store.Q.DamagedCount(ctx); err == nil && n > 0 {
		return unwell(c, "problem", fmt.Sprintf("%s no longer %s what %s when %s came in.", plural(n, "file", "files"), map[bool]string{true: "matches", false: "match"}[n == 1],
			map[bool]string{true: "it was", false: "they were"}[n == 1], map[bool]string{true: "it", false: "they"}[n == 1]),
			"Open Backups: it lists them. Put them back from your second copy."), nil
	}
	if s.Backup.Dir == "" {
		return unwell(c, "warn", "Nightly copies of the database are off: albums, people, sharing and descriptions live only in the database.",
			"Leave PHOTOSTEAD_BACKUP_DIR unset (they go in the photo share) or set it to a folder on another disk."), nil
	}
	st, err := backup.Read(ctx, s.Store.Q)
	if err != nil {
		return c, err
	}
	if p := st.Problem(time.Now()); p != "" {
		return unwell(c, "problem", p, "Open Backups to see why, and make one now."), nil
	}
	if st.Last == nil {
		return healthy(c, "The first copy of the database is made tonight."), nil
	}
	return healthy(c, "Last copy of the database: "+st.Last.At.Local().Format("Jan 2, 15:04")+"."), nil
}

// checkRemote: when Photostead is on the tailnet, that it's in, and that Funnel opened if it's on.
func (s *server) checkRemote(ctx context.Context) (HealthCheck, error) {
	c := HealthCheck{ID: "remote", Title: "Reaching it from away"}
	t := readTailscale(ctx, s.Store.Q)
	if s.Tailnet == nil || !t.On {
		if s.PublicURL != "" {
			return healthy(c, "At "+s.PublicURL+"."), nil
		}
		return healthy(c, "Only on your home network. To reach it from away, set up Tailscale in Devices & remote access."), nil
	}
	st := s.Tailnet.State(ctx)
	switch {
	case st.Status == "error":
		return unwell(c, "problem", "Tailscale stopped: "+st.Error, "Open Devices & remote access, turn Tailscale off and on again, and approve it if Tailscale asks."), nil
	case st.Status == "needs-login":
		return unwell(c, "warn", "Waiting for you to approve Photostead in Tailscale.", "Open Devices & remote access and choose Approve in Tailscale."), nil
	case st.Status != "running":
		return healthy(c, "Joining your tailnet."), nil
	case t.Funnel && st.FunnelError != "":
		return unwell(c, "warn", "At "+st.URL+", but Funnel didn't open: "+st.FunnelError,
			"Allow Funnel in your Tailscale access controls (login.tailscale.com › Access controls), then turn it on again in Devices & remote access."), nil
	case t.Funnel && st.Funnel:
		return healthy(c, "At "+st.URL+", with share links and paired phones reaching it from the internet."), nil
	}
	return healthy(c, "At "+st.URL+", on your tailnet."), nil
}
