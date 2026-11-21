// Command photostead runs the Photostead server and its admin commands.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
	_ "time/tzdata" // every IANA zone, whatever the container ships

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"photostead.com/server/internal/api"
	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/backup"
	"photostead.com/server/internal/characters"
	"photostead.com/server/internal/config"
	"photostead.com/server/internal/faces"
	"photostead.com/server/internal/immich"
	"photostead.com/server/internal/jobs"
	"photostead.com/server/internal/labels"
	"photostead.com/server/internal/libraries"
	"photostead.com/server/internal/library"
	"photostead.com/server/internal/media"
	"photostead.com/server/internal/metadata"
	"photostead.com/server/internal/move"
	"photostead.com/server/internal/places"
	"photostead.com/server/internal/provenance"
	"photostead.com/server/internal/remote"
	"photostead.com/server/internal/search"
	"photostead.com/server/internal/sidecar"
	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
	"photostead.com/server/internal/videos"
	"photostead.com/server/internal/watch"
	"photostead.com/server/internal/worker"
)

const usage = `Usage: photostead <command>

  serve                          run the server (applies migrations first)
  migrate                        apply database migrations and exit
  library add -name N -path P    index an existing folder, read-only
  library list                   show libraries
  library remove -name N         stop indexing a folder whose items all have a copy elsewhere (unmount
                                 it first, or it's found again); nothing on disk is touched
  library watch -name N on|off   watch a library for new files (on for indexed libraries by default)
  library exclude -name N -folder F   leave a folder out of a library (and the index)
  library include -name N -folder F   stop excluding a folder (the next scan picks it up)
  library scan [name]            scan one library, or all of them
  render [-failed] [-blank-posters]
                                 queue renders for assets not rendered yet (or that failed, or
                                 videos whose poster is blank, from before posters skipped black)
  pair                           link Live Photos and RAW+JPEG pairs now (also runs every 15 minutes)
  move plan [-owner EMAIL]       dry run: where every original would go in the managed share
  move run -plan ID [-limit N]   copy and verify a reviewed plan (originals are never changed)
  move status -plan ID           progress of a move
  redate                         re-date items with no date inside them (file times and names)
  worker add -name N | list | revoke -name N
                                 AI workers' tokens (shown once) and search-embedding progress
  provenance status|read-files   AI provenance counts; read-files reads the files whose AI details
                                 the index cut short (queue provenance)
  import immich [-owner EMAIL] [-dry-run] [-url URL]
                                 bring favorites, the archive, albums and people's names over from
                                 Immich (the connection saved in the app, or URL with the key in
                                 PHOTOSTEAD_IMMICH_API_KEY); Immich is only ever read
  backup now | list | check [-copy NAME] | restore -copy NAME
                                 copy the database now (it's also copied every night at 3:30), list
                                 the copies, prove one restores (into a scratch database, every
                                 table's rows compared with the live one; the newest by default), or
                                 bring one back with Photostead stopped (see docs/backups.md)
  apikey add -name N [-owner EMAIL] | list | revoke -name N
                                 API keys for other programs that upload (shown once)
  pairing add [-email EMAIL] [-days N] [-devices M] [-note TEXT] | list | revoke -id ID
                                 pairing codes that last (shown once), for a phone that can't come
                                 to the home network to pair, such as App Review's: into an account
                                 (the admin's by default), for 14 days and 5 devices unless you say,
                                 with a short note to tell it apart if you like; revoking one leaves
                                 the devices it paired signed in
  labels refresh|status          sort items into screenshots, documents, receipts, IDs and QR codes,
                                 and label adult content again by the current rule (from its scans)
  videos probe|status            probe videos and queue copies every browser plays
  sidecars write|status          write XMP sidecars of changed items now (the server does it every 5 minutes)
  places                         name the places of items with a location now (offline, from GeoNames)
  library sidecars -name N beside|shadow|off
                                 where a library's XMP sidecars go (a read-only library's are never beside)
                                 now (also runs every 15 minutes), or show how far it's got
  queue pause|resume NAME        pause or resume a job queue (images, videos, default)
  status                         files, renders and queued jobs
  openapi                        print the OpenAPI document
`

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Run by the desktop app (PHOTOSTEAD_STOP_ON_STDIN=1): it stops Photostead by closing its input,
	// which works the same on Windows, where a parent can't send it a signal to stop cleanly.
	if os.Getenv("PHOTOSTEAD_STOP_ON_STDIN") == "1" {
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			log.Info("input closed: stopping")
			stop()
		}()
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(ctx, log)
	case "migrate":
		err = withStore(ctx, func(s *store.Store, _ config.Config) error { return s.Migrate(ctx) })
	case "library":
		err = libraryCmd(ctx, os.Args[2:])
	case "render":
		err = renderCmd(ctx, os.Args[2:])
	case "queue":
		err = queueCmd(ctx, os.Args[2:])
	case "move":
		err = moveCmd(ctx, os.Args[2:])
	case "import":
		err = importCmd(ctx, log, os.Args[2:])
	case "provenance":
		err = provenanceCmd(ctx, os.Args[2:])
	case "characters":
		err = charactersCmd(ctx, os.Args[2:])
	case "worker":
		err = workerCmd(ctx, os.Args[2:])
	case "labels":
		err = labelsCmd(ctx, os.Args[2:])
	case "videos":
		err = videosCmd(ctx, os.Args[2:])
	case "sidecars":
		err = sidecarsCmd(ctx, os.Args[2:])
	case "places":
		err = withStore(ctx, func(s *store.Store, cfg config.Config) error {
			st, err := places.Refresh(ctx, s, cfg.DataDir)
			if err == nil {
				fmt.Printf("Places: %d items with a location looked at, %d named\n", st.Located, st.Placed)
			}
			return err
		})
	case "apikey":
		err = apikeyCmd(ctx, os.Args[2:])
	case "pairing":
		err = pairingCmd(ctx, os.Args[2:])
	case "backup":
		err = backupCmd(ctx, os.Args[2:])
	case "redate":
		err = withStore(ctx, func(s *store.Store, cfg config.Config) error {
			n, err := jobs.Redate(ctx, s.Q, nil, cfg.Zone)
			if err == nil {
				fmt.Printf("Re-dated %d items from their file times and names\n", n)
			}
			return err
		})
	case "pair":
		err = withStore(ctx, func(s *store.Store, _ config.Config) error {
			p, err := jobs.Pair(ctx, s.Q)
			if err == nil {
				fmt.Printf("Live Photos linked: %d by identifier, %d by name. RAW+JPEG stacks: %d.\n", p.LivePhotosByIdentifier, p.LivePhotosByName, p.RawJpegStacks)
			}
			return err
		})
	case "status":
		err = withStore(ctx, status)
	case "openapi":
		_, a := api.Handler(api.Options{})
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		err = enc.Encode(a.OpenAPI())
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "photostead:", err)
		os.Exit(1)
	}
}

func withStore(ctx context.Context, fn func(*store.Store, config.Config) error) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	s, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer s.Close()
	return fn(s, cfg)
}

func serve(ctx context.Context, log *slog.Logger) error {
	return withStore(ctx, func(s *store.Store, cfg config.Config) error {
		proxies, err := api.ParseProxies(cfg.TrustedProxies)
		if err != nil {
			return fmt.Errorf("PHOTOSTEAD_TRUSTED_PROXIES: %w", err)
		}
		if err := s.Migrate(ctx); err != nil {
			return err
		}
		if created, err := auth.Bootstrap(ctx, s, cfg.AdminEmail, cfg.AdminPassword); err != nil {
			return err
		} else if created {
			log.Info("created the first admin account", "email", cfg.AdminEmail)
		}
		if err := media.Start(2); err != nil {
			return err
		}
		exif, err := metadata.NewReader(cfg.ImageWorkers + cfg.VideoWorkers)
		if err != nil {
			return err
		}
		defer exif.Close()
		render := media.Renderer{DataDir: cfg.DataDir}
		tmp := filepath.Join(cfg.DataDir, "tmp")
		_ = os.RemoveAll(tmp) // leftovers from an interrupted run
		if err := os.MkdirAll(tmp, 0o700); err != nil {
			return err
		}
		embedder := &search.Embedder{URL: cfg.TextEmbedder}
		backups := backup.Config{Dir: cfg.BackupDir, Keep: cfg.BackupKeep, DatabaseURL: cfg.DatabaseURL, Zone: cfg.Zone}
		deps := &jobs.Deps{Store: s, Exif: exif, Render: render, Zone: cfg.Zone, Log: log, TmpDir: tmp,
			MoveTarget: cfg.MoveTarget, ManagedRoot: cfg.ManagedRoot, Mounts: cfg.Mounts, Embedder: embedder, Backup: backups}
		periodic := []*river.PeriodicJob{jobs.PeriodicPairing(), jobs.NightlyRescan(cfg.Zone), jobs.PeriodicCharacters(), jobs.PeriodicEmbedQueue(),
			jobs.PeriodicLabels(), jobs.PeriodicVideos(), jobs.PeriodicSidecars(), jobs.PeriodicPlaces(), jobs.PeriodicDiscover(), jobs.NightlyPurge(cfg.Zone), jobs.PeriodicFaces(), jobs.PeriodicLookAlikes(), jobs.PeriodicShareRules(),
			jobs.NightlyBackup(cfg.Zone), jobs.PeriodicUploadCleanup(), jobs.NightlySyncPrune(cfg.Zone), jobs.NightlyScrub(cfg.Zone)}
		rc, err := river.NewClient(riverpgxv5.New(s.Pool), &river.Config{
			Logger: log.With("component", "jobs"),
			Queues: map[string]river.QueueConfig{
				river.QueueDefault:   {MaxWorkers: 4}, // scans, imports, pairing
				jobs.QueueImages:     {MaxWorkers: cfg.ImageWorkers},
				jobs.QueueVideos:     {MaxWorkers: cfg.VideoWorkers},
				jobs.QueueProvenance: {MaxWorkers: 1},
				jobs.QueueMove:       {MaxWorkers: 1}, // one move job at a time
			},
			Workers:      jobs.Workers(deps),
			PeriodicJobs: periodic,
		})
		if err != nil {
			return err
		}
		if err := rc.Start(ctx); err != nil {
			return err
		}
		// AI provenance for anything the current parser hasn't read (cheap: no files are read).
		if _, err := rc.Insert(ctx, jobs.ProvenanceArgs{}, nil); err != nil {
			return err
		}
		opts := api.Options{Store: s, Render: render, Jobs: rc, WebDir: cfg.WebDir, ServerZone: cfg.ZoneName, Backup: backups, ManagedRoot: cfg.ManagedRoot,
			SecureCookies: os.Getenv("PHOTOSTEAD_SECURE_COOKIES") == "true", Embedder: embedder, PublicURL: cfg.PublicURL, Proxies: proxies, UploadPiece: int64(cfg.UploadPieceMiB) << 20, DiscardUploads: cfg.DiscardUploads,
			OIDC: &auth.OIDC{Config: auth.OIDCConfig{Issuer: cfg.OIDCIssuer, ClientID: cfg.OIDCClientID, ClientSecret: cfg.OIDCClientSecret,
				Name: cfg.OIDCName, NewAccounts: cfg.OIDCNewAccounts}}}
		switch {
		case cfg.MapOff:
			opts.Map = api.MapConfig{Off: true}
		case cfg.MapLight != "":
			dark := cfg.MapDark
			if dark == "" {
				dark = cfg.MapLight
			}
			opts.Map = api.MapConfig{Light: cfg.MapLight, Dark: dark, Hosts: strings.Fields(cfg.MapHosts)}
		}
		if cfg.WorkerCmd != "" {
			_, port, _ := net.SplitHostPort(cfg.Listen)
			b := &worker.Builtin{Store: s, Log: log.With("component", "worker"), Cmd: cfg.WorkerCmd, Args: cfg.WorkerArgs, Server: "http://127.0.0.1:" + port,
				ModelsDir: filepath.Join(cfg.DataDir, "models"), TokenHash: api.WorkerTokenHash}
			go b.Run(ctx)
		}
		if cfg.Watch {
			w := &watch.Watcher{Store: s, Jobs: rc, Log: log.With("component", "watch")}
			go w.Run(ctx)
			opts.Watching = w.State
		} else {
			log.Info("not watching libraries for new files (PHOTOSTEAD_WATCH=off)")
		}
		if cfg.DiscardUploads {
			log.Warn("a demo server: uploads are answered as filed and thrown away (PHOTOSTEAD_DISCARD_UPLOADS=on)")
		}
		opts.Tailnet = &remote.Tailnet{Dir: filepath.Join(cfg.DataDir, "tailscale"), Log: log.With("component", "tailscale")}
		handler, _ := api.Handler(opts)
		opts.Tailnet.Handler, opts.Tailnet.Public = handler, api.PublicOnly(handler)
		opts.Tailnet.Devices = api.PairedAway(handler, opts.Tailnet.Public, s.Q)
		on, hostname, funnel, devices := api.TailscaleSetting(ctx, s.Q)
		opts.Tailnet.SetDevices(devices)
		if on { // on the tailnet as before (no approving again)
			opts.Tailnet.SetFunnel(funnel)
			if err := opts.Tailnet.Start(hostname, ""); err != nil {
				log.Warn("tailscale didn't start", "err", err)
			}
			defer opts.Tailnet.Stop()
		}
		srv := &http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
		errc := make(chan error, 1)
		go func() { errc <- srv.ListenAndServe() }()
		log.Info("Photostead is running", "url", "http://"+cfg.Listen, "data", cfg.DataDir, "server time zone", cfg.ZoneName)
		select {
		case <-ctx.Done():
		case err := <-errc:
			if !errors.Is(err, http.ErrServerClosed) {
				return err
			}
		}
		log.Info("shutting down")
		shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
		if err := rc.Stop(shutdown); err == nil {
			return nil
		}
		// A long job (a copy chunk) is still going: cancel it so it's marked for retry and picked
		// up on the next start, instead of sitting as "running" until River rescues it an hour later.
		// Both waits together stay under the container's 60-second stop timeout.
		hard, cancelHard := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancelHard()
		return rc.StopAndCancel(hard)
	})
}

func libraryCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("library: expected add, list, remove, scan, watch, exclude, include or sidecars")
	}
	return withStore(ctx, func(s *store.Store, cfg config.Config) error {
		switch args[0] {
		case "add":
			fs := flag.NewFlagSet("library add", flag.ContinueOnError)
			name := fs.String("name", "", "library name")
			path := fs.String("path", "", "folder to index (read-only)")
			owner := fs.String("owner", cfg.AdminEmail, "owner's email")
			if err := fs.Parse(args[1:]); err != nil {
				return err
			}
			if *name == "" || *path == "" {
				return errors.New("library add: -name and -path are required")
			}
			root, err := filepath.Abs(*path)
			if err != nil {
				return err
			}
			if info, err := os.Stat(root); err != nil || !info.IsDir() {
				return fmt.Errorf("library add: %s is not a readable folder", root)
			}
			u, err := s.Q.GetUserByEmail(ctx, *owner)
			if err != nil {
				return fmt.Errorf("library add: no user %q (start the server once to create the admin)", *owner)
			}
			lib, err := s.Q.CreateLibrary(ctx, db.CreateLibraryParams{OwnerID: u.ID, Name: *name, Kind: "indexed",
				RootPath: filepath.Clean(root), ReadOnly: true, SidecarMode: "shadow", Watch: true})
			if err != nil {
				return err
			}
			fmt.Printf("Added library %q at %s (read-only, watched for new files), owned by %s\n", lib.Name, lib.RootPath, u.Email)
			return nil
		case "sidecars":
			fs := flag.NewFlagSet("library sidecars", flag.ContinueOnError)
			name := fs.String("name", "", "library name")
			if err := fs.Parse(args[1:]); err != nil {
				return err
			}
			mode := fs.Arg(0)
			if fs.NArg() != 1 || (mode != "beside" && mode != "shadow" && mode != "off") {
				return errors.New("library sidecars: expected -name N beside|shadow|off")
			}
			n, err := s.Q.SetSidecarMode(ctx, db.SetSidecarModeParams{Name: *name, Mode: mode})
			if err != nil {
				return err
			}
			if n == 0 {
				return fmt.Errorf("library sidecars: no library named %q", *name)
			}
			q, err := s.Q.QueueLibrarySidecars(ctx, *name)
			if err == nil {
				fmt.Printf("Library %q: sidecars %s; %d items to look at again\n", *name, mode, q)
			}
			return err
		case "watch":
			fs := flag.NewFlagSet("library watch", flag.ContinueOnError)
			name := fs.String("name", "", "library name")
			if err := fs.Parse(args[1:]); err != nil {
				return err
			}
			if fs.NArg() != 1 || (fs.Arg(0) != "on" && fs.Arg(0) != "off") {
				return errors.New("library watch: expected -name N on|off")
			}
			libs, err := s.Q.ListLibraries(ctx)
			if err != nil {
				return err
			}
			for _, l := range libs {
				if l.Name == *name {
					if _, err := s.Q.SetLibraryWatch(ctx, db.SetLibraryWatchParams{ID: l.ID, Watch: fs.Arg(0) == "on"}); err != nil {
						return err
					}
					fmt.Printf("Watching %s for new files: %s (the server picks this up within a few minutes)\n", l.Name, fs.Arg(0))
					return nil
				}
			}
			return fmt.Errorf("library watch: no library named %q", *name)
		case "remove":
			fs := flag.NewFlagSet("library remove", flag.ContinueOnError)
			name := fs.String("name", "", "library name")
			if err := fs.Parse(args[1:]); err != nil {
				return err
			}
			libs, err := s.Q.ListLibraries(ctx)
			if err != nil {
				return err
			}
			var found []db.Library
			for _, l := range libs {
				if l.Name == *name {
					found = append(found, l)
				}
			}
			if len(found) != 1 {
				return fmt.Errorf("library remove: %d libraries named %q", len(found), *name)
			}
			moved, err := libraries.Remove(ctx, s, found[0])
			if err != nil {
				return fmt.Errorf("library remove: %w", err)
			}
			fmt.Printf("Removed library %q (%s): %d items are now served from their copies elsewhere. Nothing on disk was touched.\n", found[0].Name, found[0].RootPath, moved)
			return nil
		case "list":
			libs, err := s.Q.ListLibraries(ctx)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tPATH\tREAD-ONLY\tWATCHED\tLAST SCAN\tEXCLUDED FOLDERS")
			for _, l := range libs {
				last := "never"
				if l.LastScanFinishedAt != nil {
					last = l.LastScanFinishedAt.Local().Format("2006-01-02 15:04")
				}
				ex := "-"
				if len(l.Exclude) > 0 {
					ex = strings.Join(l.Exclude, ", ")
				}
				fmt.Fprintf(tw, "%s\t%s\t%v\t%v\t%s\t%s\n", l.Name, l.RootPath, l.ReadOnly, l.Watch && l.Kind == "indexed", last, ex)
			}
			return tw.Flush()
		case "exclude", "include":
			fs := flag.NewFlagSet("library "+args[0], flag.ContinueOnError)
			name := fs.String("name", "", "library name")
			folder := fs.String("folder", "", "folder inside the library, e.g. lora-datasets")
			if err := fs.Parse(args[1:]); err != nil {
				return err
			}
			f, err := library.CleanFolder(*folder)
			if err != nil {
				return err
			}
			libs, err := s.Q.ListLibraries(ctx)
			if err != nil {
				return err
			}
			var lib *db.Library
			for i := range libs {
				if libs[i].Name == *name {
					if lib != nil {
						return fmt.Errorf("library %s: more than one library has that name", args[0])
					}
					lib = &libs[i]
				}
			}
			if lib == nil {
				return fmt.Errorf("library %s: no library named %q", args[0], *name)
			}
			list := slices.DeleteFunc(slices.Clone(lib.Exclude), func(x string) bool { return x == f })
			if args[0] == "exclude" {
				if info, err := os.Stat(filepath.Join(lib.RootPath, filepath.FromSlash(f))); err != nil || !info.IsDir() {
					return fmt.Errorf("library exclude: %s isn't a folder in %s", f, lib.RootPath)
				}
				list = append(list, f)
			}
			slices.Sort(list)
			if _, err := s.Q.SetLibraryExclude(ctx, db.SetLibraryExcludeParams{ID: lib.ID, Exclude: list}); err != nil {
				return err
			}
			if args[0] == "include" {
				fmt.Printf("%s/%s is no longer excluded; run `photostead library scan %s` to index it\n", lib.Name, f, lib.Name)
				return nil
			}
			p, err := jobs.PruneFolder(ctx, s, media.Renderer{DataDir: cfg.DataDir}, cfg.ZoneName, lib.ID, f)
			if err != nil {
				return err
			}
			fmt.Printf("Excluded %s/%s: %d files left the index, %d items removed, %d items kept (they have a copy elsewhere). Nothing in the folder was touched.\n",
				lib.Name, f, p.Files, p.Items, p.Kept)
			return nil
		case "scan":
			libs, err := s.Q.ListLibraries(ctx)
			if err != nil {
				return err
			}
			rc, err := river.NewClient[pgx.Tx](riverpgxv5.New(s.Pool), &river.Config{})
			if err != nil {
				return err
			}
			n := 0
			for _, l := range libs {
				if len(args) > 1 && args[1] != l.Name {
					continue
				}
				res, err := rc.Insert(ctx, jobs.ScanLibraryArgs{LibraryID: l.ID}, nil)
				if err != nil {
					return err
				}
				if res.UniqueSkippedAsDuplicate {
					fmt.Printf("A scan of %q is already queued or running\n", l.Name)
				} else {
					fmt.Printf("Queued a scan of %q\n", l.Name)
				}
				n++
			}
			if n == 0 {
				return errors.New("library scan: no matching library")
			}
			return nil
		}
		return fmt.Errorf("library: unknown command %q", args[0])
	})
}

// workerCmd manages AI workers' tokens. A token is shown once, when it's made.
func workerCmd(ctx context.Context, args []string) error {
	if len(args) == 0 || (args[0] != "add" && args[0] != "list" && args[0] != "revoke") {
		return errors.New("worker: expected add -name N, list, or revoke -name N")
	}
	fs := flag.NewFlagSet("worker "+args[0], flag.ContinueOnError)
	name := fs.String("name", "", "the worker's name, e.g. legion")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	return withStore(ctx, func(s *store.Store, _ config.Config) error {
		switch args[0] {
		case "add":
			if *name == "" || *name == "built-in" {
				return errors.New("worker add: give it a -name (not built-in)")
			}
			token := worker.NewToken()
			if _, err := s.Q.CreateWorker(ctx, db.CreateWorkerParams{Name: *name, TokenHash: api.WorkerTokenHash(token)}); err != nil {
				return err
			}
			fmt.Printf("Worker %q can connect with this token (shown once; adding the same name again replaces it):\n%s\n", *name, token)
		case "revoke":
			n, err := s.Q.RevokeWorker(ctx, *name)
			if err != nil {
				return err
			}
			if n == 0 {
				return fmt.Errorf("worker revoke: no active worker named %q", *name)
			}
			fmt.Printf("Revoked %s\n", *name)
		case "list":
			rows, err := s.Q.ListWorkers(ctx)
			if err != nil {
				return err
			}
			p, err := s.Q.EmbeddingProgress(ctx, search.Model)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tVERSION\tLAST SEEN\tHOLDING\tSTATE")
			for _, w := range rows {
				seen := "never"
				if w.LastSeenAt != nil {
					seen = w.LastSeenAt.Local().Format("2006-01-02 15:04:05")
				}
				state := "active"
				if w.RevokedAt != nil {
					state = "revoked"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", w.Name, w.Version.String, seen, w.Leased, state)
			}
			_ = tw.Flush()
			fmt.Printf("\nSearch embeddings (%s): %d done, %d waiting, %d failed\n", search.Model, p.Embedded, p.Waiting, p.Failed)
			if o, err := s.Q.OCRProgress(ctx, search.OCREngine); err == nil {
				fmt.Printf("Text in pictures (%s): %d done, %d waiting, %d failed\n", search.OCREngine, o.Done, o.Waiting, o.Failed)
			}
			if f, err := s.Q.FaceProgress(ctx, faces.Engine); err == nil {
				fmt.Printf("Faces (%s): %d items looked at, %d faces found, %d waiting, %d failed\n", faces.Engine, f.Scanned, f.Faces, f.Waiting, f.Failed)
			}
			if p, err := s.Q.PetProgress(ctx, faces.PetEngine); err == nil {
				fmt.Printf("Pets (%s): %d items looked at, %d cats and dogs found, %d waiting, %d failed\n", faces.PetEngine, p.Scanned, p.Pets, p.Waiting, p.Failed)
			}
			if a, err := s.Q.AdultProgress(ctx, labels.AdultEngine); err == nil {
				fmt.Printf("Adult content (%s): %d items looked at, %d adult, %d waiting, %d failed\n", labels.AdultEngine, a.Scanned, a.Adult, a.Waiting, a.Failed)
			}
		}
		return nil
	})
}

// charactersCmd imports character definitions (the prototype's research format:
// apikeyCmd makes, lists and revokes API keys for other programs (Photostead Capture, the
// ComfyUI sync). A key is shown once; only its hash is kept.
func backupCmd(ctx context.Context, args []string) error {
	if len(args) == 0 || (args[0] != "now" && args[0] != "list" && args[0] != "check" && args[0] != "restore") {
		return errors.New("backup: expected now, list, check [-copy NAME], or restore -copy NAME")
	}
	fs := flag.NewFlagSet("backup "+args[0], flag.ContinueOnError)
	name := fs.String("copy", "", "which copy to check (default: the newest)")
	admin := fs.String("admin", "postgres", "a database role that can create a scratch database")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	return withStore(ctx, func(s *store.Store, cfg config.Config) error {
		c := backup.Config{Dir: cfg.BackupDir, Keep: cfg.BackupKeep, DatabaseURL: cfg.DatabaseURL, Zone: cfg.Zone}
		switch args[0] {
		case "now":
			start := time.Now()
			copied, err := backup.Run(ctx, c, start)
			if rerr := backup.Record(ctx, s.Q, copied, time.Since(start), err); rerr != nil {
				return rerr
			}
			if err != nil {
				return err
			}
			fmt.Printf("Copied the database to %s (%s, %s)\n", filepath.Join(c.Dir, copied.Name), humanBytes(copied.Bytes), time.Since(start).Round(time.Second))
		case "list":
			copies, err := backup.List(c.Dir)
			if err != nil {
				return err
			}
			fmt.Printf("%d copies in %s (the newest %d are kept)\n", len(copies), c.Dir, c.Keep)
			for _, k := range copies {
				fmt.Printf("  %s  %s\n", k.Name, humanBytes(k.Bytes))
			}
		case "check":
			copies, err := backup.List(c.Dir)
			if err != nil {
				return err
			}
			if *name == "" && len(copies) > 0 {
				*name = copies[0].Name
			}
			if *name == "" {
				return fmt.Errorf("backup check: no copies in %s yet", c.Dir)
			}
			start := time.Now()
			fmt.Printf("Restoring %s into a scratch database…\n", *name)
			tables, err := backup.Check(ctx, filepath.Join(c.Dir, filepath.Base(*name)), cfg.DatabaseURL, *admin, 4)
			if err != nil {
				return err
			}
			short, rows := 0, int64(0)
			for _, t := range tables {
				rows += max(0, t.Restored)
				if t.Restored < 0 || t.Restored > t.Live {
					fmt.Printf("  %s: %d live, %d in the copy\n", t.Name, t.Live, t.Restored)
					short++
				}
			}
			fmt.Printf("Restored %d tables, %d rows, in %s.\n", len(tables), rows, time.Since(start).Round(time.Second))
			if short > 0 {
				return fmt.Errorf("backup check: %d tables missing or with more rows in the copy than live (shown above)", short)
			}
			fmt.Println("Every table is there, with no more rows than the live database (it may have more: written since the copy). The copy restores.")
		case "restore":
			if *name == "" {
				return errors.New("backup restore: say which copy, -copy NAME (see backup list)")
			}
			s.Close() // the restore needs the database to itself
			start := time.Now()
			kept, err := backup.Restore(ctx, filepath.Join(c.Dir, filepath.Base(*name)), cfg.DatabaseURL, *admin, 4)
			if err != nil {
				return err
			}
			fmt.Printf("Restored %s in %s. The database from before is kept as %s; once all is well, drop it with:\n  psql -U %s -d postgres -c 'DROP DATABASE %s'\n",
				*name, time.Since(start).Round(time.Second), kept, *admin, kept)
		}
		return nil
	})
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d KB", n>>10)
}

func apikeyCmd(ctx context.Context, args []string) error {
	if len(args) == 0 || (args[0] != "add" && args[0] != "list" && args[0] != "revoke") {
		return errors.New("apikey: expected add -name N, list, or revoke -name N")
	}
	fs := flag.NewFlagSet("apikey "+args[0], flag.ContinueOnError)
	name := fs.String("name", "", "what the key is for, e.g. \"ComfyUI sync\"")
	email := fs.String("owner", "", "whose key (default: the only account)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	return withStore(ctx, func(s *store.Store, _ config.Config) error {
		u, err := owner(ctx, s, *email)
		if err != nil {
			return err
		}
		switch args[0] {
		case "add":
			if strings.TrimSpace(*name) == "" {
				return errors.New("apikey add: give it a -name")
			}
			key := api.NewAPIKey()
			if _, err := s.Q.CreateAPIKey(ctx, db.CreateAPIKeyParams{UserID: u.ID, Name: *name, TokenHash: api.APIKeyHash(key)}); err != nil {
				return fmt.Errorf("apikey add: %w (revoke the old %q first to replace it)", err, *name)
			}
			fmt.Printf("API key %q for %s (shown once):\n%s\n", *name, u.Email, key)
		case "revoke":
			n, err := s.Q.RevokeAPIKey(ctx, db.RevokeAPIKeyParams{UserID: u.ID, Name: *name})
			if err != nil {
				return err
			}
			if n == 0 {
				return fmt.Errorf("apikey revoke: no key named %q", *name)
			}
			fmt.Printf("Revoked %s\n", *name)
		case "list":
			rows, err := s.Q.ListAPIKeys(ctx, u.ID)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tMADE\tLAST USED")
			for _, k := range rows {
				used := "never"
				if k.LastUsedAt != nil {
					used = k.LastUsedAt.Local().Format("2006-01-02 15:04")
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", k.Name, k.CreatedAt.Local().Format("2006-01-02"), used)
			}
			return tw.Flush()
		}
		return nil
	})
}

// pairingCmd makes, lists and revokes pairing codes that last. A code is shown once; only its hash
// is kept. A code is named by its id (as pairing list shows it), as the code itself isn't kept.
func pairingCmd(ctx context.Context, args []string) error {
	if len(args) == 0 || (args[0] != "add" && args[0] != "list" && args[0] != "revoke") {
		return errors.New("pairing: expected add [-email EMAIL] [-days N] [-devices M] [-note TEXT], list, or revoke -id ID")
	}
	fs := flag.NewFlagSet("pairing "+args[0], flag.ContinueOnError)
	email := fs.String("email", "", "the account its devices pair into (default: the admin's)")
	days := fs.Int("days", 14, "how many days it works, 1 to 90")
	devices := fs.Int("devices", 5, "how many devices may pair with it, 1 to 50")
	note := fs.String("note", "", `a short note to tell it apart, up to 60 characters ("App Review")`)
	id := fs.String("id", "", "which code: its id from pairing list (the start of it is enough)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	return withStore(ctx, func(s *store.Store, cfg config.Config) error {
		switch args[0] {
		case "add":
			var u db.User
			if *email != "" {
				var err error
				if u, err = s.Q.GetUserByEmail(ctx, *email); err != nil {
					return fmt.Errorf("pairing add: no account with the email %s", *email)
				}
			} else {
				admin, err := s.Q.FirstAdmin(ctx)
				if err != nil {
					return errors.New("pairing add: no admin yet: set Photostead up first, or say whose account with -email")
				}
				if u, err = s.Q.GetUser(ctx, admin); err != nil {
					return err
				}
			}
			code, lc, err := api.MakeLastingCode(ctx, s.Q, u.ID, *days, *devices, *note)
			if err != nil {
				return fmt.Errorf("pairing add: %w", err)
			}
			link, err := api.PairingLink(ctx, s.Q, code, cfg.PublicURL)
			if err != nil {
				return err
			}
			what := "A pairing code"
			if lc.Note != "" {
				what += fmt.Sprintf(" (%q)", lc.Note)
			}
			fmt.Printf("%s for %s (%s), for %s until %s (shown once):\n\n  %s\n\n%s\n", what, lc.Name, lc.Email, plural(int(lc.MaxDevices), "device"),
				lc.ExpiresAt.Local().Format("2006-01-02 15:04"), code, link)
			if cfg.PublicURL == "" {
				fmt.Println("\nThe link has the home address only (PHOTOSTEAD_PUBLIC_URL isn't set). For one with the tailnet's too, make the code in the web app.")
			}
		case "list":
			codes, err := api.LastingCodes(ctx, s.Q)
			if err != nil {
				return err
			}
			if len(codes) == 0 {
				fmt.Println("No pairing codes that last. Make one with: photostead pairing add")
				return nil
			}
			ids := make([]string, len(codes))
			for i, c := range codes {
				ids[i] = c.ID.String()
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tFOR\tDEVICES\tUNTIL\tMADE\tNOTE")
			for i, c := range codes {
				fmt.Fprintf(tw, "%s\t%s (%s)\t%d of %d\t%s\t%s\t%s\n", shortID(ids, i), c.Name, c.Email, c.Devices, c.MaxDevices,
					c.ExpiresAt.Local().Format("2006-01-02 15:04"), c.CreatedAt.Local().Format("2006-01-02"), c.Note)
			}
			return tw.Flush()
		case "revoke":
			if *id == "" {
				return errors.New("pairing revoke: say which code, -id ID (see pairing list)")
			}
			codes, err := api.LastingCodes(ctx, s.Q)
			if err != nil {
				return err
			}
			var found []api.LastingCode
			for _, c := range codes {
				if strings.HasPrefix(c.ID.String(), strings.ToLower(*id)) {
					found = append(found, c)
				}
			}
			switch {
			case len(found) == 0:
				return fmt.Errorf("pairing revoke: no code that still works has an id starting %q (see pairing list)", *id)
			case len(found) > 1:
				return fmt.Errorf("pairing revoke: %d codes have an id starting %q; give more of it", len(found), *id)
			}
			if _, err := s.Q.RevokePairingCode(ctx, found[0].ID); err != nil {
				return err
			}
			c, left := found[0], ""
			switch {
			case c.Devices == 1:
				left = " The device it paired stays signed in: sign it out in " + c.Name + "'s Devices & remote access."
			case c.Devices > 1:
				left = fmt.Sprintf(" The %d devices it paired stay signed in: sign them out in %s's Devices & remote access.", c.Devices, c.Name)
			}
			which := "the code"
			if c.Note != "" {
				which += fmt.Sprintf(" %q", c.Note)
			}
			fmt.Printf("Revoked %s for %s: no more devices pair with it.%s\n", which, c.Name, left)
		}
		return nil
	})
}

// shortID is ids[i] cut to the fewest characters (eight at least) that no other id starts with.
func shortID(ids []string, i int) string {
	for n := 8; n < len(ids[i]); n++ {
		if ids[i][n-1] == '-' {
			continue
		}
		unique := true
		for j, other := range ids {
			if j != i && strings.HasPrefix(other, ids[i][:n]) {
				unique = false
				break
			}
		}
		if unique {
			return ids[i][:n]
		}
	}
	return ids[i]
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// labelsCmd sorts items into the utility collections now, or reports progress.
func labelsCmd(ctx context.Context, args []string) error {
	if len(args) != 1 || (args[0] != "refresh" && args[0] != "status") {
		return errors.New("labels: expected refresh or status")
	}
	return withStore(ctx, func(s *store.Store, cfg config.Config) error {
		if args[0] == "refresh" {
			n, err := jobs.RelabelAdult(ctx, s)
			if err != nil {
				return err
			}
			fmt.Printf("Adult content: %d items labelled again by the current rule.\n", n)
			st, err := labels.Refresh(ctx, s, &search.Embedder{URL: cfg.TextEmbedder})
			if err != nil {
				return err
			}
			if st.Checked == 0 && !(&search.Embedder{URL: cfg.TextEmbedder}).Available(ctx) {
				return errors.New("the text service isn't answering (it starts with the server)")
			}
			fmt.Printf("Looked at %d items; %d labels.\n", st.Checked, st.Labeled)
		}
		p, err := s.Q.LabelProgress(ctx, labels.Version)
		if err != nil {
			return err
		}
		fmt.Printf("%d items sorted by the current rules; %d labels in use.\n", p.Checked, p.Labeled)
		return nil
	})
}

// {"characters": {"key": ["lora pattern", ...]}}, named from their patterns) or refreshes members.
func charactersCmd(ctx context.Context, args []string) error {
	if len(args) == 0 || (args[0] != "import" && args[0] != "refresh") {
		return errors.New("characters: expected import FILE or refresh")
	}
	fs := flag.NewFlagSet("characters "+args[0], flag.ContinueOnError)
	email := fs.String("owner", "", "whose characters (default: the only account)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	return withStore(ctx, func(s *store.Store, _ config.Config) error {
		u, err := owner(ctx, s, *email)
		if err != nil {
			return err
		}
		if args[0] == "import" {
			if fs.NArg() != 1 {
				return errors.New("characters import: expected a file")
			}
			b, err := os.ReadFile(fs.Arg(0))
			if err != nil {
				return err
			}
			var f struct {
				Characters map[string][]string `json:"characters"`
			}
			if err := json.Unmarshal(b, &f); err != nil {
				return err
			}
			keys := make([]string, 0, len(f.Characters))
			for k := range f.Characters {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				pats := f.Characters[k]
				if _, err := characters.Compile(pats); err != nil {
					return err
				}
				name := characters.Name(pats)
				if _, err := s.Q.CreateCharacter(ctx, db.CreateCharacterParams{OwnerID: u.ID, Name: name, Loras: pats}); err != nil {
					fmt.Printf("skipped %s: %v\n", name, err)
					continue
				}
				fmt.Printf("added %s (%s)\n", name, strings.Join(pats, ", "))
			}
		}
		st, err := characters.Refresh(ctx, s, u.ID)
		if err == nil {
			fmt.Printf("%d characters: %d items from their LoRAs, %d videos from images of them\nStart images found: %d\n",
				st.Characters, st.Metadata, st.Lineage, st.Starts)
		}
		return err
	})
}

func provenanceCmd(ctx context.Context, args []string) error {
	if len(args) != 1 || (args[0] != "status" && args[0] != "read-files") {
		return errors.New("provenance: expected status or read-files")
	}
	return withStore(ctx, func(s *store.Store, _ config.Config) error {
		if args[0] == "read-files" {
			rc, err := river.NewClient[pgx.Tx](riverpgxv5.New(s.Pool), &river.Config{})
			if err != nil {
				return err
			}
			if _, err := rc.Insert(ctx, jobs.ProvenanceFilesArgs{}, nil); err != nil {
				return err
			}
			fmt.Println("Queued: reading the files whose AI details were cut short in the index (queue provenance)")
		}
		c, err := s.Q.ProvenanceCounts(ctx, pgtype.Int2{Int16: provenance.Version, Valid: true})
		if err != nil {
			return err
		}
		fmt.Printf("AI generated: %d   AI edited: %d   not read yet: %d   to read from the file: %d\n", c.Generated, c.Edited, c.Pending, c.NeedsFile)
		return nil
	})
}

// owner finds the account by email, or the only account when there's just one.
func owner(ctx context.Context, s *store.Store, email string) (db.User, error) {
	if email != "" {
		return s.Q.GetUserByEmail(ctx, email)
	}
	if n, err := s.Q.CountUsers(ctx); err != nil || n != 1 {
		return db.User{}, errors.New("say whose library with -owner EMAIL")
	}
	var id uuid.UUID
	if err := s.Pool.QueryRow(ctx, "SELECT id FROM users LIMIT 1").Scan(&id); err != nil {
		return db.User{}, err
	}
	return s.Q.GetUser(ctx, id)
}

func importCmd(ctx context.Context, log *slog.Logger, args []string) error {
	if len(args) == 0 || args[0] != "immich" {
		return errors.New("import: expected immich")
	}
	fs := flag.NewFlagSet("import immich", flag.ContinueOnError)
	email := fs.String("owner", "", "whose library (default: the only account)")
	dry := fs.Bool("dry-run", false, "work out what it would do, and change nothing")
	addr := fs.String("url", "", "the Immich server, instead of the connection saved in the app")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	return withStore(ctx, func(s *store.Store, _ config.Config) error {
		u, err := owner(ctx, s, *email)
		if err != nil {
			return err
		}
		client := &immich.Client{URL: *addr, Key: os.Getenv("PHOTOSTEAD_IMMICH_API_KEY")}
		if *addr == "" {
			c, err := s.Q.GetConnection(ctx, db.GetConnectionParams{OwnerID: u.ID, Kind: "immich"})
			if err != nil {
				return errors.New("no Immich server is connected: connect one in the app, or pass -url")
			}
			client = &immich.Client{URL: c.Url, Key: c.Secret}
		}
		im := &immich.Importer{Store: s, Client: client, Log: log, DryRun: *dry}
		var sum immich.Summary
		if *dry {
			sum, err = im.Run(ctx, u.ID)
		} else {
			sum, err = immich.Record(ctx, im, u.ID, uuid.Nil)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(sum)
		return err
	})
}

func moveCmd(ctx context.Context, args []string) error {
	if len(args) > 0 && (args[0] == "run" || args[0] == "status") {
		return moveRunCmd(ctx, args)
	}
	if len(args) == 0 || args[0] != "plan" {
		return errors.New("move: expected plan, run or status")
	}
	fs := flag.NewFlagSet("move plan", flag.ContinueOnError)
	owner := fs.String("owner", "", "whose library (default: the only account)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	return withStore(ctx, func(s *store.Store, cfg config.Config) error {
		var u db.User
		var err error
		if *owner != "" {
			u, err = s.Q.GetUserByEmail(ctx, *owner)
		} else if n, cerr := s.Q.CountUsers(ctx); cerr != nil || n != 1 {
			return errors.New("move plan: say whose library with -owner EMAIL")
		} else {
			err = s.Pool.QueryRow(ctx, "SELECT id FROM users LIMIT 1").Scan(&u.ID)
			if err == nil {
				u, err = s.Q.GetUser(ctx, u.ID)
			}
		}
		if err != nil {
			return err
		}
		if p, err := jobs.Pair(ctx, s.Q); err != nil { // so new pairs are placed together
			return err
		} else if p.LivePhotosByIdentifier+p.LivePhotosByName+p.RawJpegStacks > 0 {
			fmt.Printf("Paired first: %d Live Photos, %d RAW+JPEG stacks\n", p.LivePhotosByIdentifier+p.LivePhotosByName, p.RawJpegStacks)
		}
		res, err := move.MakePlan(ctx, s, u, cfg.DataDir)
		if err != nil {
			return err
		}
		fmt.Printf("Dry run %s: %d items, %d files to copy (nothing copied).\nReview: %s/summary.md and plan.csv\n",
			res.PlanID, res.Summary.Items, len(res.Entries), res.Dir)
		return nil
	})
}

func moveRunCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("move "+args[0], flag.ContinueOnError)
	planFlag := fs.String("plan", "", "plan ID (from `photostead move plan`)")
	limit := fs.Int("limit", 0, "copy at most this many files (a pilot run); 0 means all")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	planID, err := uuid.Parse(*planFlag)
	if err != nil {
		return errors.New("move: -plan needs a plan ID")
	}
	return withStore(ctx, func(s *store.Store, cfg config.Config) error {
		if _, err := s.Q.GetMovePlan(ctx, planID); err != nil {
			return fmt.Errorf("move: no plan %s", planID)
		}
		if args[0] == "run" {
			rc, err := river.NewClient[pgx.Tx](riverpgxv5.New(s.Pool), &river.Config{})
			if err != nil {
				return err
			}
			if _, err := rc.Insert(ctx, jobs.MoveRunArgs{PlanID: planID, Limit: *limit}, nil); err != nil {
				return err
			}
			fmt.Printf("Copying plan %s into %s (read by the app at %s). Follow it with: photostead move status -plan %s\n", planID, cfg.MoveTarget, cfg.ManagedRoot, planID)
			return nil
		}
		prog, fails, err := jobs.MoveStatus(ctx, s.Q, planID)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "STATE\tROLE\tFILES\tBYTES")
		for _, p := range prog {
			fmt.Fprintf(tw, "%s\t%s\t%d\t%d\n", p.State, p.Role, p.Files, p.Bytes)
		}
		_ = tw.Flush()
		for _, f := range fails {
			fmt.Printf("failed: %s -> %s: %s\n", f.SrcRel, f.DstRel, f.Error.String)
		}
		return nil
	})
}

func queueCmd(ctx context.Context, args []string) error {
	if len(args) != 2 || (args[0] != "pause" && args[0] != "resume") {
		return errors.New("queue: expected pause|resume NAME")
	}
	return withStore(ctx, func(s *store.Store, _ config.Config) error {
		rc, err := river.NewClient[pgx.Tx](riverpgxv5.New(s.Pool), &river.Config{})
		if err != nil {
			return err
		}
		if args[0] == "pause" {
			err = rc.QueuePause(ctx, args[1], nil)
		} else {
			err = rc.QueueResume(ctx, args[1], nil)
		}
		if err == nil {
			fmt.Printf("Queue %q %sd\n", args[1], args[0])
		}
		return err
	})
}

func renderCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	failed := fs.Bool("failed", false, "also retry renders that failed")
	blank := fs.Bool("blank-posters", false, "also redo videos whose poster frame is blank (their search, text and faces are then redone too)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	states := []string{"pending"}
	if *failed {
		states = append(states, "failed")
	}
	return withStore(ctx, func(s *store.Store, _ config.Config) error {
		rows, err := s.Q.AssetsToRender(ctx, states)
		if err != nil {
			return err
		}
		if *blank {
			videos, err := s.Q.RenderedVideos(ctx)
			if err != nil {
				return err
			}
			for _, v := range videos {
				if spread, ok := media.HashSpread(v.Thumbhash); ok && spread < media.FlatSpread {
					rows = append(rows, db.AssetsToRenderRow{ID: v.ID, Type: "video"})
				}
			}
		}
		rc, err := river.NewClient[pgx.Tx](riverpgxv5.New(s.Pool), &river.Config{})
		if err != nil {
			return err
		}
		batch := make([]river.InsertManyParams, 0, len(rows))
		for _, r := range rows {
			batch = append(batch, river.InsertManyParams{Args: jobs.RenderAssetArgs{AssetID: r.ID}, InsertOpts: jobs.RenderOpts(r.Type)})
		}
		for len(batch) > 0 {
			n := min(len(batch), 1000)
			if _, err := rc.InsertMany(ctx, batch[:n]); err != nil {
				return err
			}
			batch = batch[n:]
		}
		fmt.Printf("Queued %d renders\n", len(rows))
		return nil
	})
}

func status(s *store.Store, _ config.Config) error {
	ctx := context.Background()
	counts, err := s.Q.LibraryFileCounts(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "LIBRARY\tKIND\tSTATE\tFILES")
	for _, c := range counts {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", c.Name, c.Kind, c.State, c.Count)
	}
	_ = tw.Flush()
	jc, err := s.JobCounts(ctx)
	if err != nil {
		return err
	}
	fmt.Println()
	tw = tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "QUEUE\tJOB STATE\tJOBS")
	for _, c := range jc {
		fmt.Fprintf(tw, "%s\t%s\t%d\n", c.Queue, c.State, c.Count)
	}
	return tw.Flush()
}

// videosCmd probes videos and queues their playable copies now (the server does it every 10
// minutes), or says how far that has got.
func videosCmd(ctx context.Context, args []string) error {
	if len(args) != 1 || (args[0] != "probe" && args[0] != "status") {
		return errors.New("videos: expected probe or status")
	}
	return withStore(ctx, func(s *store.Store, _ config.Config) error {
		if args[0] == "probe" {
			for {
				st, err := videos.Refresh(ctx, s, 500)
				if err != nil {
					return err
				}
				fmt.Printf("Probed %d videos (%d unreadable); queued %d copies\n", st.Probed, st.Unreadable, st.Queued)
				if st.Probed == 0 {
					break
				}
			}
		}
		p, err := s.Q.VideoProgress(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("Videos probed: %d. Needing a copy: %d; made: %d, waiting: %d, failed: %d\n", p.Probed, p.NeedCopies, p.Copies, p.Waiting, p.Failed)
		return nil
	})
}

// sidecarsCmd writes the XMP sidecars of changed items now, or says how many wait.
func sidecarsCmd(ctx context.Context, args []string) error {
	if len(args) != 1 || (args[0] != "write" && args[0] != "status") {
		return errors.New("sidecars: expected write or status")
	}
	return withStore(ctx, func(s *store.Store, cfg config.Config) error {
		if args[0] == "write" {
			for {
				st, err := sidecar.Write(ctx, s, cfg.DataDir, 500)
				if err != nil {
					return err
				}
				if st.Written+st.Unchanged+st.Skipped+st.Failed == 0 {
					break
				}
				fmt.Printf("Sidecars: %d written, %d unchanged, %d with nothing to write, %d failed\n", st.Written, st.Unchanged, st.Skipped, st.Failed)
			}
		}
		p, err := s.Q.SidecarProgress(ctx)
		if err == nil {
			fmt.Printf("Sidecars written: %d; waiting: %d\n", p.Written, p.Waiting)
		}
		return err
	})
}
