// Package worker runs the built-in AI worker next to the server: a Python process (photostead-worker
// builtin) with the text service search queries need and a gentle CPU worker for new items. Its
// token is made fresh at every start and handed over in its environment.
package worker

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

// NewToken is a random worker token.
func NewToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return "psw_" + base64.RawURLEncoding.EncodeToString(b)
}

type Builtin struct {
	Store     *store.Store
	Log       *slog.Logger
	Cmd       string   // the photostead-worker executable
	Args      []string // before "builtin": the desktop app's `-m photostead_worker`
	Server    string   // how the worker reaches the server, e.g. http://127.0.0.1:4800
	ModelsDir string
	TokenHash func(string) []byte
}

// Run keeps the worker running until ctx ends, starting it again (after a pause) if it stops.
func (b *Builtin) Run(ctx context.Context) {
	if _, err := os.Stat(b.Cmd); err != nil {
		b.Log.Info("no built-in AI worker here; search by meaning needs one", "looked for", b.Cmd)
		return
	}
	for ctx.Err() == nil {
		token := NewToken()
		if _, err := b.Store.Q.CreateWorker(ctx, db.CreateWorkerParams{Name: "built-in", TokenHash: b.TokenHash(token), Builtin: true}); err != nil {
			b.Log.Warn("built-in worker: couldn't register", "err", err)
			sleep(ctx, time.Minute)
			continue
		}
		cmd := exec.CommandContext(ctx, b.Cmd, append(append([]string{}, b.Args...), "builtin")...)
		cmd.Env = append(os.Environ(),
			"PHOTOSTEAD_WORKER_TOKEN="+token,
			"PHOTOSTEAD_SERVER="+b.Server,
			"PHOTOSTEAD_MODELS_DIR="+filepath.Clean(b.ModelsDir),
		)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		cmd.WaitDelay = 10 * time.Second
		started := time.Now()
		err := cmd.Run()
		if ctx.Err() != nil {
			return
		}
		b.Log.Warn("built-in worker stopped; starting it again", "err", err, "ran for", time.Since(started).Round(time.Second))
		if time.Since(started) < time.Minute {
			sleep(ctx, time.Minute) // failing straight away: don't spin
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
