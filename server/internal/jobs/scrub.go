package jobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/riverqueue/river"

	"photostead.com/server/internal/store/db"
)

// The integrity scrub: every night, for up to an hour, files are read again and checked against
// the hash taken when they came in, the longest unchecked first, so a whole library is covered
// over a week or so without keeping the disks busy for long. A file that looks the same (its size
// and time) but no longer matches is damaged: noted, and shown as a problem. One whose size or
// time changed was changed on purpose (the rescan reads it again), and a missing one is the
// rescan's to notice; both are passed over.

type ScrubArgs struct {
	Minutes int `json:"minutes,omitempty"` // how long to spend (0: the nightly hour)
}

func (ScrubArgs) Kind() string { return "scrub" }
func (ScrubArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 1, UniqueOpts: whileQueued}
}

type ScrubWorker struct {
	river.WorkerDefaults[ScrubArgs]
	d *Deps
}

const scrubMinutes = 60

func (w *ScrubWorker) Timeout(j *river.Job[ScrubArgs]) time.Duration {
	return time.Duration(max(j.Args.Minutes, scrubMinutes)+10) * time.Minute
}

// ScrubStatus is how the last scrub went (in server_settings, under "integrity").
type ScrubStatus struct {
	At       time.Time `json:"at"`
	Seconds  float64   `json:"seconds"`
	Checked  int       `json:"checked"`
	Bytes    int64     `json:"bytes"`
	Damaged  int       `json:"damaged"` // found this time
	Missing  int       `json:"missing"`
	Changed  int       `json:"changed"`
	Finished bool      `json:"finished" doc:"Everything had been checked in the last week when it stopped"`
}

const scrubKey = "integrity"

func (w *ScrubWorker) Work(ctx context.Context, j *river.Job[ScrubArgs]) error {
	minutes := j.Args.Minutes
	if minutes <= 0 {
		minutes = scrubMinutes
	}
	start := time.Now()
	deadline := start.Add(time.Duration(minutes) * time.Minute)
	week := start.Add(-7 * 24 * time.Hour)
	st := ScrubStatus{At: start}
	buf := make([]byte, 4<<20)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		batch, err := w.d.Store.Q.FilesToVerify(ctx, db.FilesToVerifyParams{Before: &week, Lim: 200})
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			st.Finished = true
			break
		}
		for _, f := range batch {
			if time.Now().After(deadline) || ctx.Err() != nil {
				break
			}
			verdict, n := checkFile(ctx, filepath.Join(f.RootPath, filepath.FromSlash(f.RelPath)), f.Size, f.Mtime, f.Sha256, buf)
			st.Bytes += n
			switch verdict {
			case fileOK:
				st.Checked++
				err = w.d.Store.Q.FileVerified(ctx, f.ID)
			case fileDamaged:
				st.Checked++
				st.Damaged++
				w.d.Log.Warn("integrity: a file no longer matches what it was", "file", filepath.Join(f.RootPath, f.RelPath))
				err = w.d.Store.Q.FileDamaged(ctx, f.ID)
			case fileMissing:
				st.Missing++
				err = w.d.Store.Q.FileVerifyPassed(ctx, f.ID)
			case fileChanged:
				st.Changed++
				err = w.d.Store.Q.FileVerifyPassed(ctx, f.ID)
			case fileUnread:
				err = nil // stopped partway (shutting down): next time
			}
			if err != nil {
				return err
			}
		}
	}
	st.Seconds = time.Since(start).Seconds()
	b, _ := json.Marshal(st)
	if err := w.d.Store.Q.SetServerSetting(ctx, db.SetServerSettingParams{Key: scrubKey, Value: b}); err != nil {
		return err
	}
	w.d.Log.Info("integrity: checked files", "checked", st.Checked, "damaged", st.Damaged, "missing", st.Missing, "changed", st.Changed,
		"bytes", st.Bytes, "took", time.Since(start).Round(time.Second))
	return nil
}

type verdict int

const (
	fileOK verdict = iota
	fileDamaged
	fileMissing
	fileChanged
	fileUnread
)

// checkFile reads a file and compares it with its hash: how many bytes it read, and what it found.
func checkFile(ctx context.Context, path string, size int64, mtime time.Time, want []byte, buf []byte) (verdict, int64) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileMissing, 0
	} else if err != nil {
		return fileUnread, 0
	}
	if info.Size() != size || info.ModTime().Sub(mtime).Abs() > 2*time.Second {
		return fileChanged, 0
	}
	f, err := os.Open(path)
	if err != nil {
		return fileUnread, 0
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.CopyBuffer(h, ctxReader{ctx, f}, buf)
	if err != nil {
		return fileUnread, n
	}
	if !bytes.Equal(h.Sum(nil), want) {
		return fileDamaged, n
	}
	return fileOK, n
}

// ctxReader stops reading when the job is told to stop.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// ReadScrub is the last scrub's status (empty before the first).
func ReadScrub(ctx context.Context, q *db.Queries) ScrubStatus {
	var st ScrubStatus
	if b, err := q.ServerSetting(ctx, scrubKey); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

// NightlyScrub checks files at 4:15 local time, after the backup and before morning.
func NightlyScrub(zone *time.Location) *river.PeriodicJob {
	return river.NewPeriodicJob(Daily{Hour: 4, Minute: 15, Zone: zone},
		func() (river.JobArgs, *river.InsertOpts) { return ScrubArgs{}, nil }, nil)
}
