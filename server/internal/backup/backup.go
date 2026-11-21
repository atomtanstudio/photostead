// Package backup keeps copies of Photostead's database. What's in the database and nowhere else
// is what losing it would cost: albums, people's names, sharing and personal links, descriptions
// written here, settings. The photos themselves are in their folders, and thumbnails can be made
// again. Each night pg_dump writes a copy (custom format, zstd) into a folder away from the
// database (on Unraid, the array, not the cache pool it lives on); the copy is read back in full
// before it counts, and the newest ones are kept.
package backup

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"photostead.com/server/internal/store/db"
)

// Config is where copies go and how many are kept. An empty Dir means backups are off.
type Config struct {
	Dir         string
	Keep        int
	DatabaseURL string
	Zone        *time.Location // copies are named by the server's local time
}

// Copy is one backup on disk.
type Copy struct {
	Name  string    `json:"name"`
	Bytes int64     `json:"bytes"`
	At    time.Time `json:"at"`
}

// Status is the last backup and the last failure, kept in server_settings ("backups").
type Status struct {
	Last    *Copy     `json:"last,omitempty"`
	Seconds float64   `json:"seconds,omitempty"` // how long the last one took
	Error   string    `json:"error,omitempty"`   // the last failure, cleared by a success
	ErrorAt time.Time `json:"errorAt,omitzero"`
}

var copyName = regexp.MustCompile(`^photostead-\d{4}-\d{2}-\d{2}-\d{4}\.dump$`)

// Run makes a copy now: into a .partial file, read back in full with pg_restore, then renamed
// into place. Older copies beyond Keep are removed.
func Run(ctx context.Context, c Config, now time.Time) (Copy, error) {
	if c.Dir == "" {
		return Copy{}, errors.New("backups are off (PHOTOSTEAD_BACKUP_DIR=off)")
	}
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return Copy{}, fmt.Errorf("making %s: %w", c.Dir, err)
	}
	name := "photostead-" + now.In(c.Zone).Format("2006-01-02-1504") + ".dump"
	final := filepath.Join(c.Dir, name)
	part := final + ".partial"
	defer os.Remove(part)
	if err := run(ctx, "pg_dump", "--format=custom", "--compress=zstd:3", "--file="+part, "--dbname="+c.DatabaseURL); err != nil {
		return Copy{}, fmt.Errorf("pg_dump: %w", err)
	}
	if err := run(ctx, "pg_restore", "--file="+os.DevNull, part); err != nil { // every table's data reads back
		return Copy{}, fmt.Errorf("reading the copy back: %w", err)
	}
	if err := syncFile(part); err != nil {
		return Copy{}, err
	}
	if err := os.Rename(part, final); err != nil {
		return Copy{}, err
	}
	if d, err := os.Open(c.Dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	info, err := os.Stat(final)
	if err != nil {
		return Copy{}, err
	}
	if err := prune(c.Dir, c.Keep); err != nil {
		return Copy{}, fmt.Errorf("removing old copies: %w", err)
	}
	return Copy{Name: name, Bytes: info.Size(), At: now}, nil
}

// List is the copies in dir, newest first.
func List(dir string) ([]Copy, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var out []Copy
	for _, e := range entries {
		if !copyName.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Copy{Name: e.Name(), Bytes: info.Size(), At: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name }) // names sort by time
	return out, nil
}

func prune(dir string, keep int) error {
	copies, err := List(dir)
	if err != nil || keep < 1 {
		return err
	}
	for _, c := range copies[min(keep, len(copies)):] {
		if err := os.Remove(filepath.Join(dir, c.Name)); err != nil {
			return err
		}
		_ = os.Remove(filepath.Join(dir, Manifest(c.Name))) // its list of files goes with it
	}
	return nil
}

const statusKey = "backups"

// Record notes how a backup went: a success replaces the last copy and clears the failure.
func Record(ctx context.Context, q *db.Queries, c Copy, took time.Duration, err error) error {
	st, _ := Read(ctx, q)
	if err != nil {
		st.Error, st.ErrorAt = err.Error(), time.Now()
	} else {
		st.Last, st.Seconds, st.Error, st.ErrorAt = &c, took.Seconds(), "", time.Time{}
	}
	b, _ := json.Marshal(st)
	return q.SetServerSetting(ctx, db.SetServerSettingParams{Key: statusKey, Value: b})
}

// Read is the recorded status (empty before the first backup).
func Read(ctx context.Context, q *db.Queries) (Status, error) {
	var st Status
	b, err := q.ServerSetting(ctx, statusKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	} else if err != nil {
		return st, err
	}
	return st, json.Unmarshal(b, &st)
}

// Problem is what to warn about, if anything: the last backup failed, or none for more than a
// day and a half (a missed night).
func (st Status) Problem(now time.Time) string {
	switch {
	case st.Error != "" && (st.Last == nil || st.ErrorAt.After(st.Last.At)):
		return "The last backup of the database failed: " + st.Error
	case st.Last == nil:
		return ""
	case now.Sub(st.Last.At) > 36*time.Hour:
		return "No backup of the database since " + st.Last.At.Format("Jan 2") + "."
	}
	return ""
}

// Table is a table's row count in the live database and in a restored copy.
type Table struct {
	Name     string `json:"name"`
	Live     int64  `json:"live"`
	Restored int64  `json:"restored"`
}

// Check restores a copy into a scratch database and compares every table's rows with the live
// database: the proof that a copy can be brought back. The scratch database is made and dropped
// as admin (a role that can create databases and extensions: in the container, postgres over the
// local socket). Tables written since the copy was made have more rows live, which is expected.
func Check(ctx context.Context, file, liveURL, admin string, jobs int) ([]Table, error) {
	const scratch = "photostead_restore_check"
	adminURL, err := withUserAndDB(liveURL, admin, "postgres")
	if err != nil {
		return nil, err
	}
	scratchURL, err := withUserAndDB(liveURL, admin, scratch)
	if err != nil {
		return nil, err
	}
	a, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return nil, fmt.Errorf("connecting as %s: %w", admin, err)
	}
	defer a.Close(context.Background())
	if _, err := a.Exec(ctx, "DROP DATABASE IF EXISTS "+scratch+" WITH (FORCE)"); err != nil {
		return nil, err
	}
	if _, err := a.Exec(ctx, "CREATE DATABASE "+scratch); err != nil {
		return nil, err
	}
	defer a.Exec(context.Background(), "DROP DATABASE IF EXISTS "+scratch+" WITH (FORCE)") //nolint:errcheck
	if err := restore(ctx, "--no-owner", "--exit-on-error", fmt.Sprintf("--jobs=%d", max(1, jobs)), "--dbname="+scratchURL, file); err != nil {
		return nil, fmt.Errorf("pg_restore: %w", err)
	}
	live, err := counts(ctx, liveURL)
	if err != nil {
		return nil, err
	}
	restored, err := counts(ctx, scratchURL)
	if err != nil {
		return nil, err
	}
	var out []Table
	for name, n := range live {
		r, ok := restored[name]
		if !ok {
			r = -1 // not in the copy
		}
		out = append(out, Table{Name: name, Live: n, Restored: r})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Restore brings a copy back as the live database, with Photostead stopped (in the container:
// docker stop photostead, then a one-off docker run of the image with "backup restore"). The
// database there now isn't dropped: it's renamed photostead_before_restore_<date>, to drop by hand
// once all is well. Returns that name.
func Restore(ctx context.Context, file, liveURL, admin string, jobs int) (string, error) {
	u, err := url.Parse(liveURL)
	if err != nil {
		return "", err
	}
	live, owner := strings.TrimPrefix(u.Path, "/"), u.User.Username()
	if live == "" || owner == "" {
		return "", errors.New("the database URL needs a user and a database")
	}
	adminURL, err := withUserAndDB(liveURL, admin, "postgres")
	if err != nil {
		return "", err
	}
	restoreURL, err := withUserAndDB(liveURL, admin, live)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(file); err != nil {
		return "", err
	}
	a, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return "", fmt.Errorf("connecting as %s: %w", admin, err)
	}
	defer a.Close(context.Background())
	var others int
	if err := a.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = $1`, live).Scan(&others); err != nil {
		return "", err
	}
	if others > 0 {
		return "", fmt.Errorf("%d connections to %s: stop Photostead first", others, live)
	}
	kept := live + "_before_restore_" + time.Now().Format("20060102_1504")
	for _, stmt := range []string{
		"ALTER DATABASE " + pgx.Identifier{live}.Sanitize() + " RENAME TO " + pgx.Identifier{kept}.Sanitize(),
		"CREATE DATABASE " + pgx.Identifier{live}.Sanitize() + " OWNER " + pgx.Identifier{owner}.Sanitize(),
	} {
		if _, err := a.Exec(ctx, stmt); err != nil {
			return "", err
		}
	}
	// As the admin, keeping the copy's owners (Photostead's role), so it's as it was.
	if err := restore(ctx, "--exit-on-error", fmt.Sprintf("--jobs=%d", max(1, jobs)), "--dbname="+restoreURL, file); err != nil {
		return kept, fmt.Errorf("pg_restore (the database before is kept as %s): %w", kept, err)
	}
	return kept, nil
}

func counts(ctx context.Context, dbURL string) (map[string]int64, error) {
	c, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return nil, err
	}
	defer c.Close(context.Background())
	rows, err := c.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, n := range names {
		var k int64
		if err := c.QueryRow(ctx, `SELECT count(*) FROM `+pgx.Identifier{n}.Sanitize()).Scan(&k); err != nil {
			return nil, err
		}
		out[n] = k
	}
	return out, nil
}

// withUserAndDB is a database URL with another user and database (the same host or socket).
func withUserAndDB(dbURL, user, database string) (string, error) {
	u, err := url.Parse(dbURL)
	if err != nil {
		return "", err
	}
	u.User = url.User(user)
	u.Path = "/" + database
	return u.String(), nil
}

// restore runs pg_restore building each index in one process: a parallel build of the vector
// indexes needs more shared memory than a container gets by default (Docker's /dev/shm is 64 MB).
func restore(ctx context.Context, args ...string) error {
	return runEnv(ctx, append(os.Environ(), "PGOPTIONS=-c max_parallel_maintenance_workers=0"), "pg_restore", args...)
}

func run(ctx context.Context, name string, args ...string) error {
	return runEnv(ctx, nil, name, args...)
}

func runEnv(ctx context.Context, env []string, name string, args ...string) error {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env, cmd.Stderr = env, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%w: %s", err, errorLine(msg))
		}
		return err
	}
	return nil
}

// errorLine is the line that says what went wrong: the first error (pg_restore follows it with
// the command it was running, and a parallel restore with "a worker process died"), else the last.
func errorLine(s string) string {
	lines := strings.Split(s, "\n")
	for _, l := range lines {
		if strings.Contains(l, "error:") || strings.Contains(l, "ERROR:") {
			return strings.TrimSpace(l)
		}
	}
	return strings.TrimSpace(lines[len(lines)-1])
}

// syncFile makes sure a file is on the disk. Opened for writing: Windows won't flush a file opened
// only to read it ("Access is denied").
func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// Manifest is the name of the list of files written beside a copy of the database.
func Manifest(copy string) string { return strings.TrimSuffix(copy, ".dump") + ".files.tsv.gz" }

// WriteManifest lists every file Photostead has beside a copy of the database: its library, path,
// size, time and SHA-256 (gzip'd tab-separated text). Restoring the files from elsewhere (another
// disk, a cloud copy) can be checked against it, and it says what each item's files were.
func WriteManifest(ctx context.Context, q *db.Queries, dir, copy string) error {
	rows, err := q.ManifestFiles(ctx)
	if err != nil {
		return err
	}
	final := filepath.Join(dir, Manifest(copy))
	part := final + ".partial"
	defer os.Remove(part)
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	z := gzip.NewWriter(f)
	w := bufio.NewWriter(z)
	fmt.Fprintln(w, "library\tpath\tbytes\tmodified\tsha256\titem")
	for _, r := range rows {
		item := ""
		if r.AssetID.Valid {
			item = r.AssetID.UUID.String()
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%x\t%s\n", tsv(r.Library), tsv(r.RelPath), r.Size, r.Mtime.UTC().Format(time.RFC3339), r.Sha256, item)
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := z.Close(); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(part, final)
}

// tsv keeps a field on its line: tabs and line breaks in a name become spaces.
func tsv(s string) string { return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(s) }
