package jobs

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"photostead.com/server/internal/videos"
)

// VideosArgs probes new videos and queues playable copies of those browsers can't all play: every
// 10 minutes.
type VideosArgs struct{}

func (VideosArgs) Kind() string { return "videos" }
func (VideosArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: whileQueued}
}

type VideosWorker struct {
	river.WorkerDefaults[VideosArgs]
	d *Deps
}

func (w *VideosWorker) Timeout(*river.Job[VideosArgs]) time.Duration { return 30 * time.Minute }

func (w *VideosWorker) Work(ctx context.Context, _ *river.Job[VideosArgs]) error {
	st, err := videos.Refresh(ctx, w.d.Store, 2000)
	if st.Probed > 0 || st.Queued > 0 {
		w.d.Log.Info("videos", "probed", st.Probed, "unreadable", st.Unreadable, "copies queued", st.Queued)
	}
	return err
}

// PeriodicVideos looks at new videos every 10 minutes, and once at start-up.
func PeriodicVideos() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(10*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return VideosArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}
