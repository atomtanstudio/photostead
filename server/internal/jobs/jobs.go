// Package jobs defines the background work: scanning libraries and ingesting their files.
package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"photostead.com/server/internal/backup"
	"photostead.com/server/internal/library"
	"photostead.com/server/internal/media"
	"photostead.com/server/internal/metadata"
	"photostead.com/server/internal/provenance"
	"photostead.com/server/internal/search"
	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
	"photostead.com/server/internal/uploads"
)

const (
	QueueImages = "images"
	QueueVideos = "videos"
)

type Deps struct {
	Store  *store.Store
	Exif   *metadata.Reader
	Render media.Renderer
	Zone   *time.Location // the server's zone, for owners who haven't chosen one yet
	Log    *slog.Logger
	TmpDir string // local scratch space for short-lived copies of photos

	MoveTarget  string // where the move writes the managed share (on Unraid, the array-only view)
	ManagedRoot string // where the app reads the managed share
	Mounts      string // where existing folders are mounted to be indexed, read-only (/libraries)

	Embedder *search.Embedder // embeds text through the worker's text service; nil when there's none

	Backup backup.Config // nightly copies of the database; an empty Dir means off

	zones sync.Map // zone name -> *time.Location
}

// ownerZone is the owner's chosen time zone, or the server's until they choose one.
func (d *Deps) ownerZone(name pgtype.Text) *time.Location {
	if !name.Valid || name.String == "" {
		return d.Zone
	}
	if z, ok := d.zones.Load(name.String); ok {
		return z.(*time.Location)
	}
	z, err := time.LoadLocation(name.String)
	if err != nil {
		return d.Zone
	}
	d.zones.Store(name.String, z)
	return z
}

// inMemoryLimit is the largest photo read into memory in one pass. Bigger files (and all
// videos) are streamed from where they live.
const inMemoryLimit = 512 << 20

// Workers registers every job worker.
func Workers(d *Deps) *river.Workers {
	w := river.NewWorkers()
	river.AddWorker(w, &ScanWorker{d: d})
	river.AddWorker(w, &ScanFolderWorker{d: d})
	river.AddWorker(w, &RescanWorker{d: d})
	river.AddWorker(w, &ScrubWorker{d: d})
	river.AddWorker(w, &ProvenanceWorker{d: d})
	river.AddWorker(w, &ProvenanceFilesWorker{d: d})
	river.AddWorker(w, &CharactersWorker{d: d})
	river.AddWorker(w, &EmbedQueueWorker{d: d})
	river.AddWorker(w, &VideosWorker{d: d})
	river.AddWorker(w, &SidecarsWorker{d: d})
	river.AddWorker(w, &PlacesWorker{d: d})
	river.AddWorker(w, &DiscoverWorker{d: d})
	river.AddWorker(w, &IngestWorker{d: d})
	river.AddWorker(w, &RenderWorker{d: d})
	river.AddWorker(w, &PairWorker{d: d})
	river.AddWorker(w, &MoveRunWorker{d: d})
	river.AddWorker(w, &ImmichImportWorker{d: d})
	river.AddWorker(w, &LabelsWorker{d: d})
	river.AddWorker(w, &PurgeTrashWorker{d: d})
	river.AddWorker(w, &FacesWorker{d: d})
	river.AddWorker(w, &RefileWorker{d: d})
	river.AddWorker(w, &LookAlikesWorker{d: d})
	river.AddWorker(w, &ShareRulesWorker{d: d})
	river.AddWorker(w, &BackupWorker{d: d})
	river.AddWorker(w, &UploadCleanupWorker{d: d})
	river.AddWorker(w, &SyncPruneWorker{d: d})
	return w
}

type ScanLibraryArgs struct {
	LibraryID uuid.UUID `json:"library_id"`
}

func (ScanLibraryArgs) Kind() string { return "scan_library" }
func (ScanLibraryArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: whileQueued}
}

// whileQueued de-duplicates a job only against copies still waiting or running. (River's
// default also counts completed jobs for a day, which would silently skip a rescan, or the
// re-processing of a file that changed, within 24 hours of the last one.)
var whileQueued = river.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{
	rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning,
	rivertype.JobStateRetryable, rivertype.JobStateScheduled,
}}

type IngestFileArgs struct {
	FileID uuid.UUID `json:"file_id"`
}

func (IngestFileArgs) Kind() string { return "ingest_file" }

// RenderAssetArgs (re)makes an asset's previews, for example after a failed render.
type RenderAssetArgs struct {
	AssetID uuid.UUID `json:"asset_id"`
	// Keep what was read from the picture (faces, text, the search embedding): an edit that only
	// changed its light or color.
	Keep bool `json:"keep,omitempty"`
}

func (RenderAssetArgs) Kind() string { return "render_asset" }

// RenderOpts puts a render job on the queue for its media kind.
func RenderOpts(kind string) *river.InsertOpts { return ingestOpts(kind) }

func ingestOpts(kind string) *river.InsertOpts {
	q := QueueImages
	if kind == "video" {
		q = QueueVideos
	}
	return &river.InsertOpts{Queue: q, MaxAttempts: 5, UniqueOpts: whileQueued}
}

// ScanWorker walks a library, records every file it finds and queues new or changed media.
type ScanWorker struct {
	river.WorkerDefaults[ScanLibraryArgs]
	d *Deps
}

func (w *ScanWorker) Timeout(*river.Job[ScanLibraryArgs]) time.Duration { return 6 * time.Hour }

func (w *ScanWorker) Work(ctx context.Context, job *river.Job[ScanLibraryArgs]) error {
	q := w.d.Store.Q
	lib, err := q.GetLibrary(ctx, job.Args.LibraryID)
	if err != nil {
		return err
	}
	if err := q.MarkScanStarted(ctx, lib.ID); err != nil {
		return err
	}
	r, err := w.d.scan(ctx, lib, "", false)
	if err != nil {
		return err
	}
	w.d.Log.Info("scan finished", "library", lib.Name, "seen", r.seen, "queued", r.queued, "skipped", r.skipped, "missing", r.missing)
	return q.MarkScanFinished(ctx, lib.ID)
}

// ScanFolderArgs scans one folder of a library after the watcher saw it change. Shallow covers
// just the folder's own files; otherwise its subfolders too (a folder that appeared or went).
type ScanFolderArgs struct {
	LibraryID uuid.UUID `json:"library_id"`
	Folder    string    `json:"folder"` // relative to the library, "" for its top folder
	Shallow   bool      `json:"shallow"`
}

func (ScanFolderArgs) Kind() string { return "scan_folder" }

// No de-duplication: the watcher already groups a folder's changes, and a change made while a
// scan of that folder runs needs a scan of its own that starts after it.
func (ScanFolderArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 3} }

type ScanFolderWorker struct {
	river.WorkerDefaults[ScanFolderArgs]
	d *Deps
}

func (w *ScanFolderWorker) Timeout(*river.Job[ScanFolderArgs]) time.Duration { return time.Hour }

func (w *ScanFolderWorker) Work(ctx context.Context, job *river.Job[ScanFolderArgs]) error {
	lib, err := w.d.Store.Q.GetLibrary(ctx, job.Args.LibraryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // the library was removed
	} else if err != nil {
		return err
	}
	folder := strings.Trim(path.Clean("/"+job.Args.Folder), "/") // never above the library
	if library.Excluded(folder, lib.Exclude) {
		return nil
	}
	r, err := w.d.scan(ctx, lib, folder, job.Args.Shallow)
	if err != nil {
		return err
	}
	if r.queued+r.missing > 0 {
		w.d.Log.Info("folder changed", "library", lib.Name, "folder", "/"+folder, "new or changed", r.queued, "gone", r.missing)
	}
	return nil
}

type scanResult struct{ seen, queued, skipped, missing int }

// scan records every file in a library, or in one folder of it, queues new and changed media,
// and marks files it no longer finds as missing. A folder that no longer exists has no files.
func (d *Deps) scan(ctx context.Context, lib db.Library, folder string, shallow bool) (scanResult, error) {
	q := d.Store.Q
	var r scanResult
	startedAt, err := q.DBNow(ctx)
	if err != nil {
		return r, err
	}
	client := river.ClientFromContext[pgx.Tx](ctx)
	var batch []river.InsertManyParams
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		_, err := client.InsertMany(ctx, batch)
		batch = batch[:0]
		return err
	}
	err = library.WalkFolder(ctx, lib.RootPath, folder, shallow, lib.Exclude, func(e library.Entry) error {
		r.seen++
		state, reason := "pending", pgtype.Text{}
		switch {
		case e.Size == 0:
			state, reason = "skipped", pgtype.Text{String: "empty file (0 bytes)", Valid: true}
		case e.Kind == "sidecar":
			state, reason = "skipped", pgtype.Text{String: "XMP sidecar (read with its photo in a later slice)", Valid: true}
		case e.Kind == "other":
			state, reason = "skipped", pgtype.Text{String: "not a photo or video", Valid: true}
		}
		row, err := q.UpsertSeenFile(ctx, db.UpsertSeenFileParams{
			LibraryID: lib.ID, RelPath: e.RelPath, Size: e.Size, Mtime: e.ModTime, Kind: e.Kind, State: state, Reason: reason,
		})
		if err != nil {
			return fmt.Errorf("record %s: %w", e.RelPath, err)
		}
		switch row.State {
		case "pending":
			r.queued++
			batch = append(batch, river.InsertManyParams{Args: IngestFileArgs{FileID: row.ID}, InsertOpts: ingestOpts(e.Kind)})
			if len(batch) >= 500 {
				return flush()
			}
		case "skipped":
			r.skipped++
		}
		if r.seen%5000 == 0 {
			d.Log.Info("scanning", "library", lib.Name, "seen", r.seen, "queued", r.queued)
		}
		return nil
	})
	if folder != "" && errors.Is(err, fs.ErrNotExist) {
		err = nil // the folder went away: everything that was in it is missing
	}
	if err != nil {
		return r, err
	}
	if err := flush(); err != nil {
		return r, err
	}
	var n int64
	if folder == "" && !shallow {
		n, err = q.MarkMissing(ctx, db.MarkMissingParams{LibraryID: lib.ID, LastSeenAt: startedAt})
	} else {
		prefix := ""
		if folder != "" {
			prefix = folder + "/"
		}
		n, err = q.MarkMissingIn(ctx, db.MarkMissingInParams{LibraryID: lib.ID, StartedAt: startedAt, Prefix: prefix, Shallow: shallow})
	}
	r.missing = int(n)
	return r, err
}

// IngestWorker hashes one file, links it to an asset (new or existing), reads its metadata
// and renders its previews.
type IngestWorker struct {
	river.WorkerDefaults[IngestFileArgs]
	d *Deps
}

func (w *IngestWorker) Timeout(*river.Job[IngestFileArgs]) time.Duration { return 30 * time.Minute }

func (w *IngestWorker) Work(ctx context.Context, job *river.Job[IngestFileArgs]) error {
	d, q := w.d, w.d.Store.Q
	f, err := q.GetFileForIngest(ctx, job.Args.FileID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // the file or its library was removed
	}
	if err != nil {
		return err
	}
	if f.State != "pending" {
		return nil
	}
	path, err := within(f.RootPath, f.RelPath)
	if err != nil {
		return d.fail(ctx, f.ID, err.Error())
	}
	size, mtime, err := library.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return q.SetFileState(ctx, db.SetFileStateParams{ID: f.ID, State: "missing"})
	}
	if err != nil {
		return err
	}
	// Photos are read once, sequentially; hashing, metadata and decoding all use that copy.
	var hashes library.Hashes
	var data []byte
	if f.Kind == "image" && size <= inMemoryLimit {
		hashes, data, err = library.ReadFile(path, size, mtime)
	} else {
		hashes, err = library.HashFile(path, size, mtime)
	}
	if errors.Is(err, library.ErrChanged) {
		return river.JobSnooze(time.Minute) // still being written; look again shortly
	}
	if err != nil {
		return err
	}
	alt := fmt.Appendf(nil, `{"sha1":"%x"}`, hashes.SHA1)

	zone := d.ownerZone(f.OwnerTimeZone)
	if existing, err := q.FindAssetByHash(ctx, db.FindAssetByHashParams{OwnerID: f.OwnerID, Sha256: hashes.SHA256}); err == nil {
		// The same bytes are already in the library: this file is another copy of that asset.
		if err := q.SetFilePresent(ctx, db.SetFilePresentParams{ID: f.ID, Sha256: hashes.SHA256, AltHashes: alt, AssetID: uuid.NullUUID{UUID: existing.ID, Valid: true}}); err != nil {
			return err
		}
		if err := uploads.ApplyWaiting(ctx, d.Store, f.ID, existing.ID); err != nil {
			return err
		}
		_, err := Redate(ctx, q, []uuid.UUID{existing.ID}, d.Zone) // another copy may carry an earlier date
		return err
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	metaPath := path
	if data != nil {
		tmp, cleanup, err := d.localCopy(f.ID, path, data, mtime)
		if err != nil {
			return err
		}
		defer cleanup()
		metaPath = tmp
	}
	tags, exifErr := d.Exif.Read(ctx, metaPath)
	if exifErr != nil {
		tags = metadata.Tags{}
		d.Log.Warn("metadata unreadable", "file", f.RelPath, "err", exifErr)
	}
	info := metadata.Normalize(tags, zone, mtime, time.Now())
	if info.DateSource == "file-modified" { // no date inside the file: its name may hold one
		dd := metadata.DateFromCopies([]metadata.CopyTime{{Rel: f.RelPath, Mtime: mtime}}, zone, time.Now())
		info.TakenAt, info.LocalDate, info.DateSource, info.DateBasis, info.OffsetMin = dd.TakenAt, dd.LocalDate, dd.Source, dd.Basis, nil
	}
	raw, err := metadata.RawJSON(tags)
	if err != nil {
		return err
	}
	width, height := info.Oriented()

	var assetID uuid.UUID
	var created bool
	err = d.Store.Tx(ctx, func(tq *db.Queries) error {
		row, err := tq.InsertAsset(ctx, db.InsertAssetParams{
			OwnerID: f.OwnerID, Type: f.Kind, Sha256: hashes.SHA256,
			PrimaryFileID:  uuid.NullUUID{UUID: f.ID, Valid: true},
			Mime:           text(info.MIME),
			TakenAt:        info.TakenAt,
			TakenOffsetMin: int2(info.OffsetMin),
			LocalDate:      pgtype.Date{Time: info.LocalDate, Valid: true},
			DateSource:     info.DateSource,
			DateBasis:      info.DateBasis,
			Width:          int4(width), Height: int4(height),
			Orientation:       pgtype.Int2{Int16: int16(info.Orientation), Valid: info.Orientation > 0},
			DurationMs:        int4(info.DurationMs),
			ContentIdentifier: text(info.ContentIdentifier),
		})
		if err != nil {
			return err
		}
		assetID, created = row.ID, row.Created
		if created {
			if err := tq.UpsertAssetMetadata(ctx, db.UpsertAssetMetadataParams{
				AssetID: assetID, Make: text(info.Make), Model: text(info.Model), Lens: text(info.Lens),
				FocalMm: float4(info.FocalMM), Fnumber: float4(info.FNumber), ExposureS: float4(info.ExposureS),
				Iso: intPtr4(info.ISO), Lat: float8(info.Lat), Lon: float8(info.Lon), Altitude: float8(info.Alt),
				Title: text(info.Title), Description: text(info.Description), Raw: raw,
			}); err != nil {
				return err
			}
			if info.Rating != 0 || info.Label != "" {
				if err := tq.SeedMarks(ctx, db.SeedMarksParams{AssetID: assetID, OwnerID: f.OwnerID,
					Rating: pgtype.Int2{Int16: int16(info.Rating), Valid: info.Rating > 0},
					Flag:   pgtype.Text{String: "reject", Valid: info.Rating == -1},
					Color:  text(info.Label)}); err != nil {
					return err
				}
			}
		}
		return tq.SetFilePresent(ctx, db.SetFilePresentParams{ID: f.ID, Sha256: hashes.SHA256, AltHashes: alt,
			Mime: text(info.MIME), AssetID: uuid.NullUUID{UUID: assetID, Valid: true}})
	})
	if err != nil {
		return err
	}
	if err := uploads.ApplyWaiting(ctx, d.Store, f.ID, assetID); err != nil {
		return err
	}
	if !created { // another copy was indexed at the same moment
		_, err := Redate(ctx, q, []uuid.UUID{assetID}, d.Zone)
		return err
	}

	// AI provenance, from the whole of the metadata (the index keeps a shortened copy).
	g, needsFile := provenance.Parse(tags, f.Kind == "video")
	if err := provenance.Save(ctx, d.Store, assetID, f.OwnerID, g, needsFile); err != nil {
		d.Log.Warn("provenance not recorded; the next provenance pass will try again", "file", f.RelPath, "err", err)
	}
	return d.render(ctx, assetID, path, data, f.Kind, info.DurationMs, width, height, nil)
}

// RenderWorker re-renders one asset from its primary file.
type RenderWorker struct {
	river.WorkerDefaults[RenderAssetArgs]
	d *Deps
}

func (w *RenderWorker) Timeout(*river.Job[RenderAssetArgs]) time.Duration { return 10 * time.Minute }

func (w *RenderWorker) Work(ctx context.Context, job *river.Job[RenderAssetArgs]) error {
	a, err := w.d.Store.Q.GetAssetForRender(ctx, job.Args.AssetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	path, err := within(a.RootPath, a.RelPath)
	if err != nil {
		return err
	}
	var data []byte
	if a.Type == "image" {
		if data, err = os.ReadFile(path); err != nil {
			return err
		}
	}
	before := a.Thumbhash
	if job.Args.Keep {
		before = nil
	}
	return w.d.render(ctx, a.ID, path, data, a.Type, int(a.DurationMs.Int32), int(a.Width.Int32), int(a.Height.Int32), before)
}

// render makes the previews and records the result. A failed render is recorded (with the
// reason) rather than retried endlessly; `photostead render -failed` tries those again. before is
// the ThumbHash of an earlier render: when the picture changed, it's analysed again.
func (d *Deps) render(ctx context.Context, assetID uuid.UUID, path string, data []byte, kind string, durationMs, width, height int, before []byte) error {
	q := d.Store.Q
	// A photo's edit is applied to its previews, and its size is the edited size.
	var rec *media.Recipe
	if kind == "image" {
		if e, err := q.AssetEdit(ctx, assetID); err == nil {
			var r media.Recipe
			if json.Unmarshal(e.Recipe, &r) == nil && r.Check() == nil {
				rec = &r
				width, height = r.Size(int(e.OrigWidth), int(e.OrigHeight))
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	var res media.Result
	var err error
	switch {
	case kind == "video":
		res, err = d.Render.Video(ctx, path, assetID, durationMs)
	case data == nil:
		if data, err = os.ReadFile(path); err == nil {
			res, err = d.Render.ImageEdited(data, assetID, rec)
		}
	default:
		res, err = d.Render.ImageEdited(data, assetID, rec)
	}
	if err != nil {
		msg := media.Reason(err)
		d.Log.Warn("render failed", "file", path, "err", msg)
		return q.SetAssetRendered(ctx, db.SetAssetRenderedParams{ID: assetID, RenderState: "failed", RenderError: text(msg)})
	}
	// The rendered preview is the truth about which way round the picture is.
	switch {
	case width == 0 || height == 0:
		width, height = res.Width, res.Height
	case (width > height) != (res.Width > res.Height) && res.Width != res.Height:
		width, height = height, width
	}
	return d.Store.Tx(ctx, func(q *db.Queries) error {
		if err := q.SetAssetRendered(ctx, db.SetAssetRenderedParams{ID: assetID, Thumbhash: res.ThumbHash, RenderState: "done",
			Width: int4(width), Height: int4(height)}); err != nil {
			return err
		}
		if before == nil || bytes.Equal(before, res.ThumbHash) {
			return nil
		}
		return q.ForgetPictureAnalyses(ctx, assetID)
	})
}

// localCopy writes a photo's bytes to local scratch space with the original's name extension
// and modification time, so exiftool reads the same metadata (including the file date fallback)
// without another trip to the network share.
func (d *Deps) localCopy(id uuid.UUID, original string, data []byte, mtime time.Time) (string, func(), error) {
	dir := d.TmpDir
	if dir == "" {
		dir = os.TempDir()
	}
	p := filepath.Join(dir, id.String()+strings.ToLower(filepath.Ext(original)))
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return "", nil, err
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		_ = os.Remove(p)
		return "", nil, err
	}
	return p, func() { _ = os.Remove(p) }, nil
}

func (d *Deps) fail(ctx context.Context, id uuid.UUID, reason string) error {
	return d.Store.Q.SetFileState(ctx, db.SetFileStateParams{ID: id, State: "failed", Reason: text(reason)})
}

// within joins a library root and a stored relative path, refusing anything that escapes the root.
func within(root, rel string) (string, error) {
	p := filepath.Join(root, filepath.FromSlash(rel))
	if p != root && !strings.HasPrefix(p, filepath.Clean(root)+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside its library", rel)
	}
	return p, nil
}

func text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

func int4(v int) pgtype.Int4 { return pgtype.Int4{Int32: int32(v), Valid: v > 0} }

func intPtr4(v *int) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*v), Valid: true}
}

func int2(v *int16) pgtype.Int2 {
	if v == nil {
		return pgtype.Int2{}
	}
	return pgtype.Int2{Int16: *v, Valid: true}
}

func float4(v *float64) pgtype.Float4 {
	if v == nil {
		return pgtype.Float4{}
	}
	return pgtype.Float4{Float32: float32(*v), Valid: true}
}

func float8(v *float64) pgtype.Float8 {
	if v == nil {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: *v, Valid: true}
}
