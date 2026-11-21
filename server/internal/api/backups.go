package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"photostead.com/server/internal/backup"
	"photostead.com/server/internal/jobs"
)

// Backups of the database (see package backup): how they're going, the copies kept, and backing
// up now; and the integrity scrub (jobs/scrub.go): how many files were checked lately, what's
// damaged, and checking now. Admin only.

type Damaged struct {
	Library string    `json:"library"`
	Path    string    `json:"path"`
	At      time.Time `json:"at"`
}

type Integrity struct {
	Files   int64            `json:"files" doc:"Files with a hash to check against"`
	Checked int64            `json:"checked" doc:"Of those, checked in the last 30 days"`
	Damaged []Damaged        `json:"damaged" doc:"Files that no longer match what they were (up to 100)"`
	Last    jobs.ScrubStatus `json:"last" doc:"The last check"`
	Running bool             `json:"running"`
	NextAt  time.Time        `json:"nextAt"`
}

type backupsOutput struct {
	Body struct {
		On      bool          `json:"on" doc:"Off with PHOTOSTEAD_BACKUP_DIR=off"`
		Dir     string        `json:"dir" doc:"Where copies go, as the server sees it"`
		Keep    int           `json:"keep" doc:"How many copies are kept"`
		Status  backup.Status `json:"status"`
		Problem string        `json:"problem,omitempty" doc:"The last one failed, or none for a day and a half"`
		Copies  []backup.Copy `json:"copies" doc:"Newest first"`
		Running bool          `json:"running" doc:"A backup is queued or under way"`
		NextAt  time.Time     `json:"nextAt" doc:"When the nightly one runs next"`
		Files   Integrity     `json:"files" doc:"The integrity scrub"`
	}
}

func (s *server) backups(ctx context.Context) (*backupsOutput, error) {
	out := &backupsOutput{}
	b := &out.Body
	b.On, b.Dir, b.Keep, b.Copies = s.Backup.Dir != "", s.Backup.Dir, s.Backup.Keep, []backup.Copy{}
	st, err := backup.Read(ctx, s.Store.Q)
	if err != nil {
		return nil, err
	}
	b.Status, b.Problem = st, st.Problem(time.Now())
	if b.On {
		if copies, err := backup.List(s.Backup.Dir); err == nil && copies != nil {
			b.Copies = copies
		}
		zone := s.Backup.Zone
		if zone == nil {
			zone = time.Local
		}
		b.NextAt = jobs.Daily{Hour: 3, Minute: 30, Zone: zone}.Next(time.Now())
	}
	b.Running, err = s.Store.JobQueued(ctx, jobs.BackupArgs{}.Kind())
	if err != nil {
		return nil, err
	}
	n, err := s.Store.Q.IntegrityCounts(ctx)
	if err != nil {
		return nil, err
	}
	f := &b.Files
	f.Files, f.Checked, f.Last, f.Damaged = n.Files, n.Checked, jobs.ReadScrub(ctx, s.Store.Q), []Damaged{}
	if n.Damaged > 0 {
		rows, err := s.Store.Q.DamagedFiles(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			f.Damaged = append(f.Damaged, Damaged{Library: r.Library, Path: r.RelPath, At: r.DamagedAt})
		}
	}
	zone := s.Backup.Zone
	if zone == nil {
		zone = time.Local
	}
	f.NextAt = jobs.Daily{Hour: 4, Minute: 15, Zone: zone}.Next(time.Now())
	f.Running, err = s.Store.JobQueued(ctx, jobs.ScrubArgs{}.Kind())
	return out, err
}

func (s *server) registerBackups() {
	huma.Register(s.api, huma.Operation{
		OperationID: "backups", Method: http.MethodGet, Path: "/api/v1/server/backups", Tags: []string{"Server"},
		Summary: "Backups of the database", Description: "The last one, any problem, and the copies kept. Admin only.", Security: sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*backupsOutput, error) {
		if err := s.admin(ctx); err != nil {
			return nil, err
		}
		return s.backups(ctx)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "backUpNow", Method: http.MethodPost, Path: "/api/v1/server/backups", Tags: []string{"Server"},
		Summary: "Back up the database now", Description: "Queued; it takes about a minute. Admin only.", Security: sessionAuth,
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, _ *struct{}) (*backupsOutput, error) {
		if err := s.admin(ctx); err != nil {
			return nil, err
		}
		if s.Backup.Dir == "" {
			return nil, huma.Error409Conflict("Backups are off on this server (PHOTOSTEAD_BACKUP_DIR=off).")
		}
		if s.Jobs != nil {
			if _, err := s.Jobs.Insert(ctx, jobs.BackupArgs{}, nil); err != nil {
				return nil, err
			}
		}
		return s.backups(ctx)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "checkFilesNow", Method: http.MethodPost, Path: "/api/v1/server/integrity", Tags: []string{"Server"},
		Summary: "Check files against their hashes now", Description: "An hour of the nightly check, now: the longest unchecked first. Admin only.", Security: sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*backupsOutput, error) {
		if err := s.admin(ctx); err != nil {
			return nil, err
		}
		if s.Jobs != nil {
			if _, err := s.Jobs.Insert(ctx, jobs.ScrubArgs{Minutes: 60}, nil); err != nil {
				return nil, err
			}
		}
		return s.backups(ctx)
	})
}
