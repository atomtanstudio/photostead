package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Phase is where the launcher is: what the menu's first line says.
type Phase string

const (
	Starting Phase = "starting"
	Setup    Phase = "setup"   // running, and the first account isn't made yet
	Running  Phase = "running" // running and set up
	Stopped  Phase = "stopped"
	Failed   Phase = "failed"
)

type State struct {
	Phase  Phase
	Detail string // why it failed, in a few words
}

func (s State) Text() string {
	switch s.Phase {
	case Starting:
		return "Photostead is starting…"
	case Setup:
		return "Photostead is running: finish setting it up"
	case Running:
		return "Photostead is running"
	case Stopped:
		return "Photostead is stopped"
	default:
		return "Photostead stopped: " + s.Detail
	}
}

// Runtime runs Postgres and the Photostead server, and stops them.
type Runtime struct {
	paths Paths
	cfg   *Config
	log   *slog.Logger

	mu        sync.Mutex
	state     State
	listeners []func(State)
	server    *exec.Cmd
	serverIn  io.WriteCloser
	exited    chan struct{}
	stopping  bool
	pgUp      bool
}

func newRuntime(p Paths, cfg *Config, log *slog.Logger) *Runtime {
	return &Runtime{paths: p, cfg: cfg, log: log, state: State{Phase: Starting}}
}

func (r *Runtime) OnChange(f func(State)) {
	r.mu.Lock()
	r.listeners = append(r.listeners, f)
	s := r.state
	r.mu.Unlock()
	f(s)
}

func (r *Runtime) State() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

func (r *Runtime) set(s State) {
	r.mu.Lock()
	if r.state == s {
		r.mu.Unlock()
		return
	}
	r.state = s
	ls := append([]func(State){}, r.listeners...)
	r.mu.Unlock()
	r.log.Info("state", "phase", s.Phase, "detail", s.Detail)
	for _, f := range ls {
		f(s)
	}
}

func (r *Runtime) URL() string { return fmt.Sprintf("http://localhost:%d/", r.cfg.Port) }

// Run starts Postgres, then the server, restarting the server if it stops on its own, until Stop.
func (r *Runtime) Run() {
	r.set(State{Phase: Starting})
	if err := r.check(); err != nil {
		r.set(State{Phase: Failed, Detail: err.Error()})
		return
	}
	if err := r.startPostgres(); err != nil {
		r.log.Error("postgres", "err", err)
		r.set(State{Phase: Failed, Detail: "its database didn't start (see Show Logs)"})
		return
	}
	go r.watchHealth()
	var quick int
	for {
		started := time.Now()
		err := r.runServer()
		r.mu.Lock()
		stopping := r.stopping
		r.mu.Unlock()
		if stopping {
			return
		}
		r.log.Error("server stopped", "err", err, "ran for", time.Since(started).Round(time.Second))
		r.set(State{Phase: Failed, Detail: "it stopped unexpectedly; starting it again"})
		if time.Since(started) < time.Minute {
			quick++
		} else {
			quick = 0
		}
		if quick >= 5 {
			r.set(State{Phase: Failed, Detail: "it keeps stopping (see Show Logs)"})
			return
		}
		time.Sleep(5 * time.Second)
	}
}

// check finds what's needed before starting: the programs, the library folder, a free port.
func (r *Runtime) check() error {
	if _, err := os.Stat(r.exe("bin", "photostead")); err != nil {
		return fmt.Errorf("its programs are missing (%s)", r.paths.Runtime)
	}
	if err := os.MkdirAll(r.cfg.Library, 0o755); err != nil {
		return fmt.Errorf("the photo library folder can't be made: %v", err)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", r.cfg.Port))
	if err != nil {
		return fmt.Errorf("port %d is in use by another program", r.cfg.Port)
	}
	ln.Close()
	return nil
}

// exe is a bundled program's path (.exe on Windows).
func (r *Runtime) exe(parts ...string) string {
	return filepath.Join(append([]string{r.paths.Runtime}, parts...)...) + exeSuffix
}

func (r *Runtime) pgBin(name string) string { return r.exe("postgres", "bin", name) }

func (r *Runtime) startPostgres() error {
	pgdata := filepath.Join(r.paths.Data, "postgres")
	if _, err := os.Stat(filepath.Join(pgdata, "PG_VERSION")); err != nil {
		r.log.Info("creating the database", "at", pgdata)
		if err := r.initdb(pgdata); err != nil {
			_ = os.RemoveAll(pgdata) // half made: start again next time
			return fmt.Errorf("initdb: %w", err)
		}
	}
	opts := "-c shared_buffers=256MB -c effective_cache_size=1GB -c maintenance_work_mem=128MB -c max_connections=60"
	cmd := exec.Command(r.pgBin("pg_ctl"), "-D", pgdata, "-w", "-t", "120",
		"-l", filepath.Join(r.paths.Logs, "postgres.log"), "-o", opts+pgListen(r), "start")
	// What pg_ctl says goes to a file, not a pipe: on Windows the Postgres it starts inherits a pipe
	// and keeps it open, so reading one would never end.
	outPath := filepath.Join(r.paths.Logs, "pg_ctl.log")
	outFile, err := os.Create(outPath)
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr, cmd.SysProcAttr = outFile, outFile, hidden()
	err = cmd.Run()
	outFile.Close()
	if err != nil {
		out := lastLines(outPath, 20)
		if strings.Contains(out, "another server might be running") {
			r.log.Info("postgres was already running")
		} else {
			return fmt.Errorf("pg_ctl start: %v: %s", err, out)
		}
	}
	r.mu.Lock()
	r.pgUp = true
	r.mu.Unlock()
	// Photostead's own role and database, and pgvector (extensions need the superuser).
	for _, sql := range []string{
		"DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'photostead') THEN CREATE ROLE photostead LOGIN; END IF; END $$",
		"ALTER ROLE photostead PASSWORD '" + r.cfg.DBPassword + "'",
	} {
		if out, err := r.psql("postgres", sql); err != nil {
			return fmt.Errorf("psql: %v: %s", err, out)
		}
	}
	if out, _ := r.psql("postgres", "SELECT 1 FROM pg_database WHERE datname = 'photostead'"); !strings.Contains(out, "1") {
		if out, err := r.psql("postgres", "CREATE DATABASE photostead OWNER photostead"); err != nil {
			return fmt.Errorf("createdb: %v: %s", err, out)
		}
	}
	if out, err := r.psql("photostead", "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		return fmt.Errorf("pgvector: %v: %s", err, out)
	}
	return nil
}

func (r *Runtime) psql(db, sql string) (string, error) {
	args := append(pgConnArgs(r), "-X", "-q", "-t", "-A", "-v", "ON_ERROR_STOP=1", "-d", db, "-c", sql)
	cmd := exec.Command(r.pgBin("psql"), args...)
	cmd.Env = append(os.Environ(), pgEnv(r)...)
	return r.run(cmd)
}

func (r *Runtime) stopPostgres() {
	r.mu.Lock()
	up := r.pgUp
	r.pgUp = false
	r.mu.Unlock()
	if !up {
		return
	}
	pgdata := filepath.Join(r.paths.Data, "postgres")
	if out, err := r.run(exec.Command(r.pgBin("pg_ctl"), "-D", pgdata, "-m", "fast", "-w", "-t", "60", "stop")); err != nil {
		r.log.Warn("pg_ctl stop", "err", err, "out", out)
		return
	}
	afterPostgres(r)
}

// run runs a short command, hidden, and returns what it said.
func (r *Runtime) run(cmd *exec.Cmd) (string, error) {
	cmd.SysProcAttr = hidden()
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// serverEnv is everything the Photostead server is told.
func (r *Runtime) serverEnv() []string {
	sep := string(os.PathListSeparator)
	path := strings.Join([]string{filepath.Join(r.paths.Runtime, "bin"), filepath.Join(r.paths.Runtime, "postgres", "bin"), systemPath()}, sep)
	data := filepath.Join(r.paths.Data, "server")
	_ = os.MkdirAll(filepath.Join(r.paths.Data, "libraries"), 0o755)
	env := []string{
		"PATH=" + path,
		"PHOTOSTEAD_DATABASE_URL=" + pgURL(r),
		"PHOTOSTEAD_DATA_DIR=" + data,
		"PHOTOSTEAD_LISTEN=0.0.0.0:" + strconv.Itoa(r.cfg.Port),
		"PHOTOSTEAD_WEB_DIR=" + filepath.Join(r.paths.Runtime, "web"),
		"PHOTOSTEAD_MANAGED_ROOT=" + r.cfg.Library,
		"PHOTOSTEAD_LIBRARIES=" + filepath.Join(r.paths.Data, "libraries"),
		"PHOTOSTEAD_WORKER_CMD=" + pythonPath(r.paths.Runtime),
		"PHOTOSTEAD_WORKER_ARGS=-m photostead_worker",
		"PHOTOSTEAD_TEXT_PORT=" + strconv.Itoa(r.cfg.Port+1),
		"PHOTOSTEAD_TEXT_EMBEDDER=http://127.0.0.1:" + strconv.Itoa(r.cfg.Port+1),
		"PHOTOSTEAD_STOP_ON_STDIN=1",
		"PYTHONNOUSERSITE=1",
		// Python's compiled files in the data folder, never inside the app: writing there would break
		// its signature (the Mac) or fail in a folder only an administrator may write to (Windows).
		"PYTHONPYCACHEPREFIX=" + filepath.Join(r.paths.Data, "pycache"),
	}
	env = append(env, platformEnv(r)...)
	// Everything else from the person's environment, except what's set above.
	set := map[string]bool{}
	for _, e := range env {
		set[strings.ToUpper(strings.SplitN(e, "=", 2)[0])] = true
	}
	for _, e := range os.Environ() {
		k := strings.ToUpper(strings.SplitN(e, "=", 2)[0])
		if !set[k] && !strings.HasPrefix(k, "PHOTOSTEAD_") && !strings.HasPrefix(k, "PG") {
			env = append(env, e)
		}
	}
	return env
}

// runServer runs the Photostead server until it stops; it's stopped by closing its input.
func (r *Runtime) runServer() error {
	logPath := filepath.Join(r.paths.Logs, "server.log")
	if st, err := os.Stat(logPath); err == nil && st.Size() > 50<<20 {
		_ = os.Rename(logPath, logPath+".1")
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(r.exe("bin", "photostead"), "serve")
	cmd.Env = r.serverEnv()
	cmd.Dir = r.paths.Data
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = hidden()
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan struct{})
	r.mu.Lock()
	r.server, r.serverIn, r.exited = cmd, in, exited
	r.mu.Unlock()
	err = cmd.Wait()
	close(exited)
	return err
}

// watchHealth asks the server every few seconds whether it's up, and whether it's set up.
func (r *Runtime) watchHealth() {
	client := &http.Client{Timeout: 4 * time.Second}
	for {
		r.mu.Lock()
		stopping := r.stopping
		r.mu.Unlock()
		if stopping {
			return
		}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/v1/setup", r.cfg.Port))
		if err == nil {
			var body struct {
				Needed bool `json:"needed"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				if body.Needed {
					r.set(State{Phase: Setup})
				} else {
					r.set(State{Phase: Running})
				}
			}
		} else if s := r.State(); s.Phase == Running || s.Phase == Setup {
			r.set(State{Phase: Starting})
		}
		time.Sleep(3 * time.Second)
	}
}

// Stop stops the server (closing its input: it finishes what it's doing) and then Postgres.
func (r *Runtime) Stop() {
	r.mu.Lock()
	if r.stopping {
		r.mu.Unlock()
		return
	}
	r.stopping = true
	in, cmd, exited := r.serverIn, r.server, r.exited
	r.mu.Unlock()
	r.log.Info("stopping")
	if in != nil {
		_ = in.Close()
		select {
		case <-exited:
		case <-time.After(45 * time.Second):
			r.log.Warn("the server didn't stop in time; ending it")
			_ = cmd.Process.Kill()
			<-exited
		}
	}
	r.stopPostgres()
	r.set(State{Phase: Stopped})
}

// Restart stops and starts again (after the library folder changes).
func (r *Runtime) Restart() {
	r.Stop()
	r.mu.Lock()
	r.stopping = false
	r.server, r.serverIn, r.exited = nil, nil, nil
	r.mu.Unlock()
	go r.Run()
}

// lastLines is the end of a log, for a failure's detail.
func lastLines(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return strings.Join(lines, "\n")
}
