// Package api is Photostead's HTTP API (/api/v1), described by an OpenAPI 3.1 document generated
// from these Go types and served at /api/v1/openapi.json.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/backup"
	"photostead.com/server/internal/media"
	"photostead.com/server/internal/remote"
	"photostead.com/server/internal/search"
	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
	"photostead.com/server/internal/watch"
)

const (
	Version    = "0.1.0"
	cookieName = "ps_session"
)

type Options struct {
	Store         *store.Store
	Render        media.Renderer
	Jobs          Jobs               // queues background work (imports); nil in tests that don't need it
	Watching      func() watch.State // the folder watcher's state; nil when it isn't running
	Embedder      *search.Embedder   // embeds search text; nil or unreachable means search by names only
	WebDir        string
	SecureCookies bool
	ServerZone    string          // default time zone until a person chooses theirs
	PublicURL     string          // the address people reach Photostead at; empty: each request's own
	Proxies       Proxies         // reverse proxies whose X-Forwarded-For is believed (ParseProxies); the zero value: none
	OIDC          *auth.OIDC      // single sign-on; nil or not configured: off
	Tailnet       *remote.Tailnet // Photostead's own Tailscale node; nil: none
	Backup        backup.Config   // nightly copies of the database; an empty Dir means off
	Map           MapConfig       // the map's styles; the zero value is OpenFreeMap's (see DefaultMap)
	ManagedRoot   string          // the photostead share: each person's uploads go to its library/<username>
	UploadPiece   int64           // the most a phone sends in one request, in bytes; 0: whole (pieces through Cloudflare)
	// A demo server's (App Review's): uploads are answered as filed, then thrown away. A phone's
	// items are still noted as sent, so it doesn't send them again.
	DiscardUploads bool
}

// Jobs queues background work; River's client is one.
type Jobs interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

type server struct {
	Options
	api       huma.API
	mux       *http.ServeMux // the API's routes, for the MCP server's in-process calls
	downloads downloads
	resumable sync.Map // upload token -> *inflight
}

type viewerKey struct{}

// sessionKey holds the signed-in session (API keys have none): its id, and until when its vault
// is unlocked.
type sessionKey struct{}

type session struct {
	id         uuid.UUID
	vaultUntil time.Time
}

var sessionAuth = []map[string][]string{{"session": {}}}

// internetKey marks a request that came through Funnel: from anyone on the internet, not the
// tailnet or the home network. From there Photostead is share links and guests only.
type internetKey struct{}

func fromInternet(ctx context.Context) bool {
	v, _ := ctx.Value(internetKey{}).(bool)
	return v
}

// guestDoor is what the internet may do without a session, besides share links (/api/v1/public/):
// a guest's way in.
var guestDoor = map[string]bool{"setupStatus": true, "login": true, "secondStep": true, "logout": true,
	"passkeySignInBegin": true, "passkeySignInFinish": true, "invitation": true, "acceptInvite": true, "openGuestInvite": true}

// guestOps is what a guest's session may do from the internet: what the guest app needs.
var guestOps = map[string]bool{"me": true, "listAlbums": true, "getAlbum": true, "timelineBuckets": true,
	"timelineMonth": true, "getAsset": true, "getThumbnail": true, "getPreview": true, "getOriginal": true,
	"albumActivity": true, "albumComments": true, "addAlbumComment": true, "deleteAlbumComment": true,
	"albumPicks": true, "pickAlbumItem": true, "unpickAlbumItem": true}

const awayMessage = "From the internet, only guests can sign in. Turn on Tailscale on this device to open your library."

// awayKey marks a request from a paired device through Funnel, signed with its own token (see
// PairedAway): it reaches the API as it would at home, but for notAway.
type awayKey struct{}

func pairedAway(ctx context.Context) bool {
	v, _ := ctx.Value(awayKey{}).(bool)
	return v
}

// notAway is what a paired device can't do from the internet, though it can at home or over
// Tailscale: sign in again, add a way in (a pairing code, or one that lasts, a passkey, two-step,
// single sign-on, a household invitation), change who's in the household or remove a guest, or
// change remote access. A lost phone, once signed out, leaves no way in behind it.
var notAway = map[string]bool{"setup": true, "login": true, "secondStep": true, "passkeySignInBegin": true, "passkeySignInFinish": true,
	"signOnStart": true, "signOnCallback": true, "signOnLink": true, "signOnUnlink": true,
	"passkeyAddBegin": true, "passkeyAddFinish": true, "passkeyDelete": true,
	"twoStepSetup": true, "twoStepConfirm": true, "twoStepOff": true, "twoStepRecovery": true,
	"pairingCode": true, "pairDevice": true, "makeLastingCode": true, "revokeLastingCode": true, "invite": true, "cancelInvite": true, "setMemberRole": true,
	"removeGuest": true, "setTailscale": true, "setFunnel": true, "setFunnelDevices": true, "forgetTailscale": true}

const notAwayMessage = "From the internet, a paired phone can’t do that. Do it at home, or with Tailscale on."

// mayStart refuses a household member a session started from the internet: there, only guests.
func mayStart(ctx context.Context, u db.User) error {
	if fromInternet(ctx) && u.Role != "guest" {
		return huma.Error403Forbidden(awayMessage)
	}
	return nil
}

// Handler returns the HTTP handler for the API and, when WebDir is set, the web app.
func Handler(o Options) (http.Handler, huma.API) {
	mux := http.NewServeMux()
	cfg := huma.DefaultConfig("Photostead API", Version)
	cfg.OpenAPIPath = "/api/v1/openapi"
	cfg.DocsPath = "/api/v1/docs"
	cfg.SchemasPath = "/api/v1/schemas"
	cfg.CreateHooks = nil // no $schema links in every response body
	cfg.Info.Description = "Self-hosted photo and video library: your folders indexed as they are, uploads, search, people, " +
		"albums and sharing, and edits kept beside the originals."
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"session": {Type: "apiKey", In: "cookie", Name: cookieName, Description: "Session cookie set by POST /api/v1/auth/login"},
		"worker":  {Type: "http", Scheme: "bearer", Description: "A worker token from `photostead worker add`"},
		"key":     {Type: "http", Scheme: "bearer", Description: "An API key from `photostead apikey add` (psk_…), for other programs"},
	}
	if o.Map.Light == "" && !o.Map.Off {
		o.Map = DefaultMap
	}
	s := &server{Options: o, mux: mux}
	s.api = humago.New(mux, cfg)
	s.api.UseMiddleware(s.requireSession)
	s.registerAuth()
	s.registerTimeline()
	s.registerRecent()
	s.registerAssets()
	s.registerSearch()
	s.registerAsk()
	s.registerDownloads()
	s.registerStatus()
	s.registerHealth()
	s.registerCollections()
	s.registerPeople()
	s.registerAlbums()
	s.registerStacks()
	s.registerGeneration()
	s.registerCreate()
	s.registerCharacters()
	s.registerLabels()
	s.registerEdits()
	s.registerVault()
	s.registerMemories()
	s.registerSignIn()
	s.registerRemote()
	s.registerLastingCodes()
	s.registerHousehold()
	s.registerTalk()
	s.registerPhotoEdits()
	s.registerPersonalLinks()
	s.registerBackups()
	s.registerUploads(mux)
	s.registerResumable(mux)
	s.registerSync(mux)
	s.registerLinks(mux)
	s.registerAlbumSharing(mux)
	s.registerImports()
	s.registerWorkers()
	s.registerTranscode(mux)
	s.registerPlaces()
	s.registerMCP(mux)
	if o.WebDir != "" {
		mux.Handle("/", spa(o.WebDir))
	}
	return securityHeaders(mux, o.Map.origins()), s.api
}

// requireSession lets operations without a security requirement through and checks the
// session cookie for the rest, putting the viewer's user ID in the request context.
func (s *server) requireSession(ctx huma.Context, next func(huma.Context)) {
	op, internet := ctx.Operation(), fromInternet(ctx.Context())
	if internet && !strings.HasPrefix(op.Path, "/api/v1/public/") && !guestDoor[op.OperationID] && !guestOps[op.OperationID] {
		_ = huma.WriteErr(s.api, ctx, http.StatusNotFound, "Not found.")
		return
	}
	if pairedAway(ctx.Context()) && notAway[op.OperationID] {
		_ = huma.WriteErr(s.api, ctx, http.StatusForbidden, notAwayMessage)
		return
	}
	if len(op.Security) == 0 {
		next(ctx)
		return
	}
	if _, ok := op.Security[0]["worker"]; ok {
		s.requireWorker(ctx, next)
		return
	}
	// An MCP tool calling the API in-process, as the person whose key it was given (see mcp.go): only
	// the server's own code can put this in a request's context.
	if tv, ok := ctx.Context().Value(trustedKey{}).(trustedViewer); ok && tv.id != uuid.Nil {
		next(huma.WithValue(huma.WithValue(ctx, viewerKey{}, tv.id), adultKey{}, tv.adult))
		return
	}
	for _, req := range op.Security {
		if _, ok := req["key"]; ok && !internet {
			if id, _, ok := s.keyUser(ctx.Context(), ctx.Header("Authorization")); ok {
				next(huma.WithValue(ctx, viewerKey{}, id))
				return
			}
		}
	}
	// A browser's session is its cookie; a paired device's, its bearer token (psd_…).
	token := ""
	if t, ok := strings.CutPrefix(ctx.Header("Authorization"), "Bearer "); ok && strings.HasPrefix(t, devicePrefix) {
		token = t
	} else if c, err := huma.ReadCookie(ctx, cookieName); err == nil {
		token = c.Value
	}
	if token == "" {
		_ = huma.WriteErr(s.api, ctx, http.StatusUnauthorized, "Sign in to continue.")
		return
	}
	row, err := s.Store.Q.GetSessionUser(ctx.Context(), auth.TokenHash(token))
	if err != nil {
		_ = huma.WriteErr(s.api, ctx, http.StatusUnauthorized, "Your session has ended. Sign in again.")
		return
	}
	if internet && row.Role != "guest" {
		_ = huma.WriteErr(s.api, ctx, http.StatusUnauthorized, awayMessage)
		return
	}
	// A guest reaches what guests may, wherever they are (at home too, not only from the internet).
	if row.Role == "guest" && !guestDoor[op.OperationID] && !guestOps[op.OperationID] {
		_ = huma.WriteErr(s.api, ctx, http.StatusNotFound, "Not found.")
		return
	}
	if time.Since(row.SessionSeenAt) > time.Hour { // slide the expiry, at most hourly
		ttl := auth.SessionTTL
		if row.SessionDevice.Valid || row.SessionViaLink {
			ttl = deviceTTL
		}
		_ = s.Store.Q.TouchSession(ctx.Context(), db.TouchSessionParams{ID: row.SessionID, ExpiresAt: time.Now().Add(ttl)})
	}
	if ua := ctx.Header("User-Agent"); ua != "" && row.SessionUserAgent.String != ua {
		_ = s.Store.Q.SetSessionUserAgent(ctx.Context(), db.SetSessionUserAgentParams{ID: row.SessionID, UserAgent: pgtype.Text{String: ua, Valid: true}})
	}
	s.noteAddress(ctx)
	sess := session{id: row.SessionID}
	if row.VaultUntil != nil {
		sess.vaultUntil = *row.VaultUntil
	}
	if left := time.Until(sess.vaultUntil); left > 0 && left < vaultOpen-time.Minute { // in use: keep it open, at most a minute's slide at a time
		if s.Store.Q.SlideVault(ctx.Context(), db.SlideVaultParams{SessionID: row.SessionID, Minutes: int32(vaultOpen / time.Minute)}) == nil {
			sess.vaultUntil = time.Now().Add(vaultOpen)
		}
	}
	var set Settings
	_ = json.Unmarshal(row.Settings, &set)
	next(huma.WithValue(huma.WithValue(huma.WithValue(ctx, viewerKey{}, row.ID), sessionKey{}, sess), adultKey{}, set.Adult))
}

func viewer(ctx context.Context) uuid.UUID {
	id, _ := ctx.Value(viewerKey{}).(uuid.UUID)
	return id
}

// adultKey holds a signed-in person's "Adult content" setting: show (or empty), blur or hide.
type adultKey struct{}

// hideAdult says whether the viewer keeps adult items out of what they browse and search. API keys
// and devices without a session see everything, as the setting is a person's own browsing choice.
func hideAdult(ctx context.Context) bool {
	mode, _ := ctx.Value(adultKey{}).(string)
	return mode == "hide"
}

// spa serves the built web app, falling back to index.html for client-side routes.
func spa(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		p := filepath.Join(dir, filepath.FromSlash(filepath.Clean("/"+r.URL.Path)))
		if info, err := os.Stat(p); err != nil || info.IsDir() {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") { // Vite's hashed bundles never change
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

// PublicOnly is what the internet may reach through Funnel: share links (their pages, photos and
// API), personal links (/p/…), the web app for guests, and the API marked as from the internet, where requireSession
// lets through only share links, a guest's way in, and what a guest's session may do. The API's
// documentation and adding to your own library aren't there.
func PublicOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, get := r.URL.Path, r.Method == http.MethodGet || r.Method == http.MethodHead
		api := apiPath(p) && !strings.HasPrefix(p, "/api/v1/uploads")
		app := get && (p == "/" || p == "/index.html" || strings.HasPrefix(p, "/s/") || strings.HasPrefix(p, "/p/") || strings.HasPrefix(p, "/assets/") ||
			p == "/favicon.svg" || p == "/favicon.ico")
		if !api && !app {
			http.NotFound(w, r)
			return
		}
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), internetKey{}, true)))
	})
}

// awayLog keeps the server's log saying whether phones away from home get in through Funnel,
// without a line for every request: each kind of arrival is noted at most once in a while.
var awayLog struct {
	sync.Mutex
	last map[string]time.Time
}

func noteAway(key string, every time.Duration, msg string, args ...any) {
	awayLog.Lock()
	defer awayLog.Unlock()
	if t, ok := awayLog.last[key]; ok && time.Since(t) < every {
		return
	}
	if awayLog.last == nil || len(awayLog.last) > 1000 {
		awayLog.last = map[string]time.Time{}
	}
	awayLog.last[key] = time.Now()
	slog.Info(msg, args...)
}

// apiPath is the API's own paths, without its documentation.
func apiPath(p string) bool {
	return strings.HasPrefix(p, "/api/v1/") && !strings.HasPrefix(p, "/api/v1/docs") && !strings.HasPrefix(p, "/api/v1/openapi") &&
		!strings.HasPrefix(p, "/api/v1/schemas")
}

// PairedAway is what the internet reaches through Funnel when paired devices may come in from away
// (on by default, beside Funnel): a phone with Photostead's app backs up and browses from anywhere,
// with no Tailscale on it. A request signed with a paired device's own token (psd_…) reaches the
// API as it would at home, but for notAway; "which server is this" answers, so the phone knows
// it found its own; and everything else goes to public (PublicOnly: share links, personal links,
// guests). Signing in on the web stays at home or on the tailnet.
func PairedAway(h, public http.Handler, q *db.Queries) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if !apiPath(p) {
			public.ServeHTTP(w, r)
			return
		}
		away := r.WithContext(context.WithValue(r.Context(), awayKey{}, true))
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || !strings.HasPrefix(token, devicePrefix) {
			if p == "/api/v1/server" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
				noteAway("look "+r.UserAgent(), 10*time.Minute, "Something looked for this server through Funnel", "user agent", r.UserAgent())
				h.ServeHTTP(w, away)
				return
			}
			public.ServeHTTP(w, r)
			return
		}
		row, err := q.GetSessionUser(r.Context(), auth.TokenHash(token))
		if err != nil || !row.SessionDevice.Valid || row.Role == "guest" {
			noteAway("refused", 10*time.Minute, "A phone that isn't paired (or was signed out) tried to come in through Funnel", "path", p)
			replyError(w, http.StatusUnauthorized, "This phone was signed out of Photostead. Pair it again at home.")
			return
		}
		noteAway(row.SessionID.String(), time.Hour, "A paired phone came in through Funnel", "device", row.SessionDevice.String, "path", p)
		// What it sends (uploads), and anything that goes wrong, is logged: how backups from away
		// are going shows in the server's log.
		rec, start := &statusWriter{ResponseWriter: w}, time.Now()
		h.ServeHTTP(rec, away)
		if r.Method != http.MethodGet || rec.status >= 400 {
			slog.Info("phone through Funnel", "device", row.SessionDevice.String, "method", r.Method, "path", p, "status", rec.code(),
				"took", time.Since(start).Round(time.Millisecond))
		}
	})
}

// statusWriter notes a response's final status (not an interim 1xx one). Unwrap lets
// http.ResponseController reach the connection beneath (flushing, read deadlines).
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if code >= 200 && w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) code() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// securityHeaders sets a strict content security policy: the app's own origin, Google's fonts, and
// the map's tile server (mapHosts) and its workers (MapLibre runs them from blobs).
func securityHeaders(next http.Handler, mapHosts []string) http.Handler {
	connect := strings.Join(append([]string{"'self'"}, mapHosts...), " ")
	csp := "default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; " +
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; " +
		"connect-src " + connect + "; worker-src 'self' blob:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		if !strings.HasPrefix(r.URL.Path, "/api/v1/docs") {
			h.Set("Content-Security-Policy", csp)
		}
		next.ServeHTTP(w, r)
	})
}

var errNotFound = huma.Error404NotFound("No such item.")

func notFound(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "no rows") {
		return errNotFound
	}
	return err
}
