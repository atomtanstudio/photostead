package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/jobs"
	"photostead.com/server/internal/libraries"
	"photostead.com/server/internal/library"
	"photostead.com/server/internal/store/db"
	"photostead.com/server/internal/uploads"
)

// Uploads: other programs (Photostead Capture, the ComfyUI sync) and later the web app add files
// to a person's library. A file is written into their managed library (the photostead share),
// filed by date like everything moved there, and indexed like any other file; what came with it
// (a description, source link, keywords, an album) is applied to its item.

const maxUpload = 50 << 30

// keyAuth is the security requirement of operations other programs may call with an API key.
var keyAuth = []map[string][]string{{"session": {}}, {"key": {}}}

// NewAPIKey makes a key for another program. It's shown once; only its hash is kept.
func NewAPIKey() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return "psk_" + base64.RawURLEncoding.EncodeToString(b)
}

// APIKeyHash is how an API key is stored.
func APIKeyHash(key string) []byte {
	sum := sha256.Sum256([]byte(key))
	return sum[:]
}

// keyUser checks an API key, returning whose it is and its name.
func (s *server) keyUser(ctx context.Context, header string) (uuid.UUID, string, bool) {
	key, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || !strings.HasPrefix(key, "psk_") {
		return uuid.Nil, "", false
	}
	k, err := s.Store.Q.UserByAPIKey(ctx, APIKeyHash(key))
	if err != nil {
		return uuid.Nil, "", false
	}
	if k.LastUsedAt == nil || time.Since(*k.LastUsedAt) > time.Hour {
		_ = s.Store.Q.TouchAPIKey(ctx, k.KeyID)
	}
	return k.UserID, k.Name, true
}

// uploader is who's uploading: an API key's owner, a paired device's, or the signed-in person
// (from the internet, only a guest).
func (s *server) uploader(r *http.Request) (uuid.UUID, string, bool) {
	internet := fromInternet(r.Context())
	if id, name, ok := s.keyUser(r.Context(), r.Header.Get("Authorization")); ok && !internet {
		return id, name, true
	}
	if t, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && strings.HasPrefix(t, devicePrefix) && !internet { // a paired device
		u, err := s.Store.Q.GetSessionUser(r.Context(), auth.TokenHash(t))
		if err != nil || !u.SessionDevice.Valid {
			return uuid.Nil, "", false
		}
		return u.ID, u.SessionDevice.String, true
	}
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" || crossSite(r) {
		return uuid.Nil, "", false
	}
	u, err := s.Store.Q.GetSessionUser(r.Context(), auth.TokenHash(c.Value))
	if err != nil || internet && u.Role != "guest" {
		return uuid.Nil, "", false
	}
	return u.ID, "web", true
}

// member is uploader for what only household members do (uploads into their own share, frames,
// trims): not guests, who add to albums shared with them through their own door.
func (s *server) member(r *http.Request) (uuid.UUID, string, bool) {
	id, client, ok := s.uploader(r)
	if !ok {
		return id, client, false
	}
	u, err := s.Store.Q.GetUser(r.Context(), id)
	if err != nil || u.Role == "guest" {
		return uuid.Nil, "", false
	}
	return id, client, true
}

// crossSite is a request that changes something, sent by another site's page with this one's
// cookie: refused. (The cookie is SameSite=Lax, which still lets a page on the same site, such as
// another app on the same server, send one; a plain form can post here without being asked.)
func crossSite(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	// Browsers say where a request comes from; older ones only give its Origin (which a proxy in
	// front may not match to Host, so its forwarded host counts too).
	if f := r.Header.Get("Sec-Fetch-Site"); f != "" {
		return f != "same-origin" && f != "none"
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		return err != nil || u.Host != r.Host && u.Host != r.Header.Get("X-Forwarded-Host")
	}
	return false
}

// UploadMeta is what may come with an uploaded file (the "meta" part, JSON).
type UploadMeta struct {
	FileName    string    `json:"fileName"`
	TakenAt     time.Time `json:"takenAt"` // when it was made: files it by day, and dates it when the file doesn't say
	Description string    `json:"description"`
	SourceURL   string    `json:"sourceUrl"`
	Keywords    []string  `json:"keywords"`
	Album       string    `json:"album"`
	SHA256      string    `json:"sha256"`     // the file's, if known: checked once it's all here
	DeviceItem  string    `json:"deviceItem"` // the phone's name for it ("<Photos item>|photo"), to ask later whether it came (resumable uploads)
	// From importers (Google Takeout's JSON, XMP sidecars): applied once it's an item, as the rest is.
	Favorite  bool     `json:"favorite"`
	Archived  bool     `json:"archived"`
	Rating    int16    `json:"rating"`   // stars, 1-5
	Latitude  *float64 `json:"latitude"` // where it was taken, when the file doesn't say
	Longitude *float64 `json:"longitude"`
	// From an importer that knows what an AI item was asked for (a ChatGPT export): its prompt,
	// shown only when the file shows it's AI and doesn't say.
	Prompt string `json:"prompt"`
}

// extras is what an upload brings besides the file, to apply once it's an item.
func (m UploadMeta) extras() uploads.Extras {
	e := uploads.Extras{Description: m.Description, SourceURL: m.SourceURL, Keywords: m.Keywords, Album: m.Album, Favorite: m.Favorite, Archived: m.Archived,
		Prompt: m.Prompt}
	if m.Rating >= 1 && m.Rating <= 5 {
		e.Rating = m.Rating
	}
	if m.Latitude != nil && m.Longitude != nil && *m.Latitude >= -90 && *m.Latitude <= 90 && *m.Longitude >= -180 && *m.Longitude <= 180 &&
		(*m.Latitude != 0 || *m.Longitude != 0) {
		e.Lat, e.Lon = m.Latitude, m.Longitude
	}
	return e
}

type uploadReply struct {
	Status  string     `json:"status"` // accepted (being indexed) or duplicate (already in the library)
	SHA256  string     `json:"sha256"`
	AssetID *uuid.UUID `json:"assetId,omitempty"`
	Path    string     `json:"path,omitempty"` // where it was filed, in the library
}

func replyJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// replyError answers with a problem. The server's own failures go to its log, and the answer
// says only that something went wrong: their details name folders on the server.
func replyError(w http.ResponseWriter, status int, detail string) {
	if status >= 500 {
		slog.Warn("request failed", "status", status, "err", detail)
		detail = "Something went wrong on the server (it's in Photostead's log)."
	}
	replyJSON(w, status, map[string]any{"status": status, "detail": detail})
}

// stalledAfter is how long an unfinished upload may go without a byte before it no longer counts
// as on its way.
const stalledAfter = 10 * time.Minute

// sendingNow is which of a phone's items are on their way now: an unfinished upload of it, begun in
// the last two hours, that is still moving (begun, or added to, in the last ten minutes). One that
// stopped (the phone went out of reach, the server restarted, iOS gave up) no longer holds the item
// back: the app, or iOS's own uploads, sends it instead. appOnly: only the app's own uploads.
func (s *server) sendingNow(ctx context.Context, owner uuid.UUID, items []string, appOnly bool) ([]string, error) {
	rows, err := s.Store.Q.SendingDeviceItems(ctx, db.SendingDeviceItemsParams{OwnerID: owner, Items: items})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range rows {
		if appOnly && !r.FromApp || slices.Contains(out, r.Item) {
			continue
		}
		last := r.CreatedAt
		if fi, err := os.Stat(r.Path); err == nil && fi.ModTime().After(last) {
			last = fi.ModTime()
		}
		if time.Since(last) < stalledAfter {
			out = append(out, r.Item)
		}
	}
	return out, nil
}

// cleanName keeps a file name safe to write: its base name, without control characters.
func cleanName(name string) string {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' {
			return -1
		}
		return r
	}, name)
	if name == "." || name == ".." || strings.HasPrefix(name, ".") {
		name = "upload" + name
	}
	if len(name) > 200 {
		ext := filepath.Ext(name)
		if len(ext) > 16 {
			ext = ""
		}
		name = cutRunes(strings.TrimSuffix(name, filepath.Ext(name)), 200-len(ext)) + ext
	}
	return name
}

// cutRunes shortens s to at most n bytes, not splitting a character.
func cutRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// upload receives one file (multipart: an optional "meta" part, then "file").
func (s *server) upload(w http.ResponseWriter, r *http.Request) {
	owner, client, ok := s.member(r)
	if !ok {
		replyError(w, http.StatusUnauthorized, "Sign in, or send an API key (Authorization: Bearer psk_…).")
		return
	}
	s.receive(w, r, owner, client, nil)
}

// receive takes one uploaded file into the owner's photostead share. adjust, if given, settles
// what came with it (a share link's album, and who sent it).
func (s *server) receive(w http.ResponseWriter, r *http.Request, owner uuid.UUID, client string, adjust func(*UploadMeta)) {
	ctx := r.Context()
	lib, ok := s.managedLibrary(ctx, w, owner)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	mr, err := r.MultipartReader()
	if err != nil {
		replyError(w, http.StatusBadRequest, "Send multipart/form-data with a file part.")
		return
	}
	staging := filepath.Join(lib.RootPath, ".uploads")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		replyError(w, http.StatusInternalServerError, "The share can't be written to right now.")
		return
	}
	sweepStaging(staging)
	var meta UploadMeta
	var tmp string
	var sum []byte
	var size int64
	defer func() {
		if tmp != "" {
			_ = os.Remove(tmp)
		}
	}()
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			replyError(w, http.StatusBadRequest, "The upload was cut short.")
			return
		}
		switch part.FormName() {
		case "meta":
			if err := json.NewDecoder(io.LimitReader(part, 1<<20)).Decode(&meta); err != nil {
				replyError(w, http.StatusBadRequest, "meta isn't valid JSON: "+err.Error())
				return
			}
		case "file":
			if tmp != "" {
				replyError(w, http.StatusBadRequest, "Send one file at a time.")
				return
			}
			if meta.FileName == "" {
				meta.FileName = part.FileName()
			}
			f, err := os.CreateTemp(staging, "upload-*.part")
			if err != nil {
				replyError(w, http.StatusInternalServerError, err.Error())
				return
			}
			tmp = f.Name()
			h := sha256.New()
			size, err = io.Copy(io.MultiWriter(f, h), part)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				replyError(w, http.StatusBadRequest, "The file didn't arrive whole.")
				return
			}
			sum = h.Sum(nil)
		}
		part.Close()
	}
	s.file(ctx, w, owner, client, lib, tmp, sum, size, meta, adjust)
}

// sweepStaging removes what uploads left behind (cut off, or the server stopped) over a day ago.
// Resumable uploads keep their own files there, under other names, until they finish or expire.
func sweepStaging(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	old := time.Now().Add(-24 * time.Hour)
	for _, e := range entries {
		n := e.Name()
		if !(strings.HasPrefix(n, "upload-") && strings.HasSuffix(n, ".part")) && !strings.HasPrefix(n, "frame-") && !strings.HasPrefix(n, "trim-") {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(old) {
			_ = os.Remove(filepath.Join(dir, n))
		}
	}
}

// managedLibrary is where the owner's uploads go, or (having said why) none.
func (s *server) managedLibrary(ctx context.Context, w http.ResponseWriter, owner uuid.UUID) (db.ManagedLibraryRow, bool) {
	lib, err := s.Store.Q.ManagedLibrary(ctx, owner)
	if errors.Is(err, pgx.ErrNoRows) { // a new account: their folder in the share, if there's one
		if u, uerr := s.Store.Q.GetUser(ctx, owner); uerr == nil && u.Role != "guest" {
			if ok, _ := libraries.Managed(ctx, s.Store.Q, s.ManagedRoot, owner, u.Username); ok {
				lib, err = s.Store.Q.ManagedLibrary(ctx, owner)
			}
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		replyError(w, http.StatusConflict, "There's no writable photostead share to upload into yet.")
		return lib, false
	} else if err != nil {
		replyError(w, http.StatusInternalServerError, err.Error())
		return lib, false
	}
	return lib, true
}

// file takes a received file (tmp, in the share's .uploads, with its SHA-256) into the owner's
// library: a duplicate only adds what came with it; a new one is filed by day and indexed. It
// moves tmp away when it files it; otherwise tmp is left for the caller.
func (s *server) file(ctx context.Context, w http.ResponseWriter, owner uuid.UUID, client string, lib db.ManagedLibraryRow,
	tmp string, sum []byte, size int64, meta UploadMeta, adjust func(*UploadMeta)) {
	if tmp == "" || size == 0 {
		replyError(w, http.StatusBadRequest, "No file (or an empty one) was sent.")
		return
	}
	if want := strings.ToLower(strings.TrimSpace(meta.SHA256)); want != "" && want != hex.EncodeToString(sum) {
		replyError(w, http.StatusUnprocessableEntity, "The file that arrived isn't the one described: its SHA-256 is "+hex.EncodeToString(sum)+", not "+want+".")
		return
	}
	if adjust != nil {
		adjust(&meta)
		// Into someone's album, or through a share link: the owner's favorites, archive, stars,
		// places and prompts are theirs to set.
		meta.Favorite, meta.Archived, meta.Rating, meta.Latitude, meta.Longitude, meta.Prompt = false, false, 0, nil, nil, ""
	}
	name := cleanName(meta.FileName)
	kind := library.Kind(name)
	if kind != "image" && kind != "video" {
		replyError(w, http.StatusUnsupportedMediaType, "Only photos and videos can be uploaded ("+name+" isn't one).")
		return
	}
	if s.DiscardUploads {
		// A demo server: answered as a new item is, so the sender sees it work, then thrown away
		// (the caller removes the file). Nothing about it is kept, its name included.
		slog.Info("upload thrown away (a demo server)", "client", client, "bytes", size)
		replyJSON(w, http.StatusAccepted, uploadReply{Status: "accepted", SHA256: hex.EncodeToString(sum)})
		return
	}
	extras := meta.extras()
	rec := db.RecordUploadParams{OwnerID: owner, Sha256: sum, FileName: name, Client: client, Description: meta.Description,
		SourceUrl: meta.SourceURL, Keywords: nonEmpty(meta.Keywords), Album: meta.Album, Favorite: extras.Favorite, Archived: extras.Archived,
		Rating: pgtype.Int2{Int16: extras.Rating, Valid: extras.Rating > 0}, Prompt: extras.Prompt}
	if extras.Lat != nil {
		rec.Lat, rec.Lon = pgtype.Float8{Float64: *extras.Lat, Valid: true}, pgtype.Float8{Float64: *extras.Lon, Valid: true}
	}

	// Already in the library: keep what came with it, not a second copy.
	if existing, err := s.Store.Q.AssetByHash(ctx, db.AssetByHashParams{OwnerID: owner, Sha256: sum}); err == nil {
		if adjust != nil {
			// From someone else (a share link, an album's contributor): they aren't told whether the
			// owner had it, and what came with it doesn't reach the owner's vault or trash.
			if a, err := s.Store.Q.GetVisibleAsset(ctx, db.GetVisibleAssetParams{Viewer: owner, VaultViewer: uuid.Nil, ID: existing}); err == nil && a.TrashedAt == nil {
				if err := uploads.Apply(ctx, s.Store, owner, existing, extras); err != nil {
					replyError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
			replyJSON(w, http.StatusAccepted, uploadReply{Status: "accepted", SHA256: hex.EncodeToString(sum)})
			return
		}
		if err := uploads.Apply(ctx, s.Store, owner, existing, extras); err != nil {
			replyError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rec.AssetID = uuid.NullUUID{UUID: existing, Valid: true}
		_, _ = s.Store.Q.RecordUpload(ctx, rec)
		replyJSON(w, http.StatusOK, uploadReply{Status: "duplicate", SHA256: hex.EncodeToString(sum), AssetID: &existing})
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		replyError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// File it by day, like the rest of the share: photos/2026/2026-10-17/name.png.
	taken := meta.TakenAt
	if taken.IsZero() || taken.After(time.Now().Add(24*time.Hour)) {
		taken = time.Now()
	}
	if u, err := s.Store.Q.GetUser(ctx, owner); err == nil {
		if loc, err := time.LoadLocation(auth.EffectiveZone(u, s.ServerZone)); err == nil {
			taken = taken.In(loc)
		}
	}
	folder := map[string]string{"image": "photos", "video": "videos"}[kind]
	day := taken.Format("2006-01-02")
	if err := os.MkdirAll(filepath.Join(lib.RootPath, folder, day[:4], day), 0o755); err != nil {
		replyError(w, http.StatusInternalServerError, "The share can't be written to right now.")
		return
	}
	// The name is taken (created, empty) before the file is moved there, so two uploads of the
	// same name at once don't land on each other.
	rel := ""
	for n := 1; ; n++ {
		candidate := name
		if n > 1 {
			ext := filepath.Ext(name)
			candidate = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(name, ext), n, ext)
		}
		rel = path.Join(folder, day[:4], day, candidate)
		f, err := os.OpenFile(filepath.Join(lib.RootPath, filepath.FromSlash(rel)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			break
		}
		if !errors.Is(err, os.ErrExist) || n > 10000 {
			replyError(w, http.StatusInternalServerError, "The share can't be written to right now.")
			return
		}
	}
	dst := filepath.Join(lib.RootPath, filepath.FromSlash(rel))
	if !meta.TakenAt.IsZero() {
		_ = os.Chtimes(tmp, taken, taken) // dates the item when the file itself doesn't
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(dst)
		replyError(w, http.StatusInternalServerError, "The share can't be written to right now.")
		return
	}
	_, mtime, err := library.Stat(dst)
	if err != nil {
		replyError(w, http.StatusInternalServerError, err.Error())
		return
	}
	row, err := s.Store.Q.UpsertSeenFile(ctx, db.UpsertSeenFileParams{LibraryID: lib.ID, RelPath: rel, Size: size, Mtime: mtime, Kind: kind, State: "pending", Reason: pgtype.Text{}})
	if err != nil {
		replyError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rec.FileID = uuid.NullUUID{UUID: row.ID, Valid: true}
	if _, err := s.Store.Q.RecordUpload(ctx, rec); err != nil {
		replyError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.Jobs != nil {
		if _, err := s.Jobs.Insert(ctx, jobs.IngestFileArgs{FileID: row.ID}, jobs.RenderOpts(kind)); err != nil {
			replyError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	replyJSON(w, http.StatusAccepted, uploadReply{Status: "accepted", SHA256: hex.EncodeToString(sum), Path: rel})
}

func nonEmpty(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

type checkInput struct {
	Body struct {
		SHA256 []string `json:"sha256" maxItems:"5000" doc:"Hex SHA-256 of files you might send"`
		Album  string   `json:"album,omitempty" maxLength:"200" doc:"Put the ones already here in this album too (made if need be), as an upload would"`
	}
}

type checkOutput struct {
	Body struct {
		Known []string `json:"known" doc:"The ones already in the library (deleted ones included): no need to send them"`
	}
}

type deviceCheckInput struct {
	Body struct {
		Items []string `json:"items" maxItems:"2000" doc:"A phone's names for items it might send (deviceItem in an upload's meta)"`
	}
}

type deviceCheckOutput struct {
	Body struct {
		Known   []string `json:"known" doc:"The ones it sent before (or said it had): no need to send them"`
		Sending []string `json:"sending" doc:"The ones on their way now (an upload begun in the last two hours, not finished): leave them to it"`
	}
}

type deviceItemsInput struct {
	Body struct {
		Items []struct {
			Item   string   `json:"item" maxLength:"500"`
			SHA256 []string `json:"sha256" maxItems:"10" doc:"Hex SHA-256 of the item's files"`
		} `json:"items" maxItems:"2000"`
	}
}

type deviceItemsOutput struct {
	Body struct {
		Recorded int64 `json:"recorded" doc:"How many were noted: only files in the library are"`
	}
}

func (s *server) registerUploads(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/uploads", s.upload)
	mux.HandleFunc("POST /api/v1/assets/{id}/frame", s.saveFrame)
	mux.HandleFunc("POST /api/v1/assets/{id}/trim", s.saveTrim)

	huma.Register(s.api, huma.Operation{
		OperationID: "checkUploads", Method: http.MethodPost, Path: "/api/v1/uploads/check", Tags: []string{"Uploads"},
		Summary: "Which of these files are already in the library",
		Description: "Upload with POST /api/v1/uploads (multipart: an optional \"meta\" JSON part with fileName, takenAt, description, " +
			"sourceUrl, keywords, album and an AI item's prompt, then the \"file\"). Sign in, or send an API key from `photostead apikey add`.",
		Security: keyAuth,
	}, func(ctx context.Context, in *checkInput) (*checkOutput, error) {
		hashes := make([][]byte, 0, len(in.Body.SHA256))
		for _, h := range in.Body.SHA256 {
			b, err := hex.DecodeString(strings.TrimSpace(h))
			if err != nil || len(b) != sha256.Size {
				return nil, huma.Error422UnprocessableEntity("sha256 values must be 64 hex digits")
			}
			hashes = append(hashes, b)
		}
		rows, err := s.Store.Q.KnownHashes(ctx, db.KnownHashesParams{OwnerID: viewer(ctx), Hashes: hashes})
		if err != nil {
			return nil, err
		}
		out := &checkOutput{}
		out.Body.Known = make([]string, len(rows))
		ids := make([]uuid.UUID, len(rows))
		for i, r := range rows {
			out.Body.Known[i], ids[i] = hex.EncodeToString(r.Sha256), r.ID
		}
		if err := uploads.AddToAlbum(ctx, s.Store, viewer(ctx), in.Body.Album, ids); err != nil {
			return nil, err
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "checkDeviceItems", Method: http.MethodPost, Path: "/api/v1/uploads/device-items/check", Tags: []string{"Uploads"},
		Summary: "Which of a phone's items it has sent",
		Description: "A phone names what it sends (deviceItem in a resumable upload's Photostead-Meta), then asks here before " +
			"sending more, for uploads that can't hash a file first (iOS's background uploads), and so that the app and iOS " +
			"don't both send the same file.",
		Security: keyAuth,
	}, func(ctx context.Context, in *deviceCheckInput) (*deviceCheckOutput, error) {
		known, err := s.Store.Q.KnownDeviceItems(ctx, db.KnownDeviceItemsParams{OwnerID: viewer(ctx), Items: in.Body.Items})
		if err != nil {
			return nil, err
		}
		sending, err := s.sendingNow(ctx, viewer(ctx), in.Body.Items, false)
		if err != nil {
			return nil, err
		}
		out := &deviceCheckOutput{}
		out.Body.Known, out.Body.Sending = known, sending
		if out.Body.Known == nil {
			out.Body.Known = []string{}
		}
		if out.Body.Sending == nil {
			out.Body.Sending = []string{}
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "recordDeviceItems", Method: http.MethodPost, Path: "/api/v1/uploads/device-items", Tags: []string{"Uploads"},
		Summary:     "Note which of a phone's items it sent before",
		Description: "For what a phone sent before its uploads named their items: the item, and its files' SHA-256. Only files in the library are noted.",
		Security:    keyAuth,
	}, func(ctx context.Context, in *deviceItemsInput) (*deviceItemsOutput, error) {
		var items []string
		var hashes [][]byte
		for _, it := range in.Body.Items {
			for _, h := range it.SHA256 {
				b, err := hex.DecodeString(strings.TrimSpace(h))
				if err != nil || len(b) != sha256.Size {
					return nil, huma.Error422UnprocessableEntity("sha256 values must be 64 hex digits")
				}
				items, hashes = append(items, it.Item), append(hashes, b)
			}
		}
		n, err := s.Store.Q.RecordDeviceItems(ctx, db.RecordDeviceItemsParams{OwnerID: viewer(ctx), Items: items, Hashes: hashes})
		if err != nil {
			return nil, err
		}
		out := &deviceItemsOutput{}
		out.Body.Recorded = n
		return out, nil
	})
}
