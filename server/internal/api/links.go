package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/store/db"
)

// Share links: an album anyone with the link can open, at /s/{token}, from the internet when
// Funnel is on (or Photostead has a public address). The owner chooses a password, a last day,
// and whether visitors may download (with the location taken out of the files) or add their own
// photos to the album. What a link shows is read as the album's owner sees it.

const linkCookie = "ps_link"

// Link is a share link, as its owner sees it.
type Link struct {
	ID            uuid.UUID  `json:"id"`
	URL           string     `json:"url"`
	QR            string     `json:"qr" doc:"The address as a QR code (PNG data URL): to print for an event, or show on a screen"`
	Public        bool       `json:"public" doc:"Whether the address works from the internet (Funnel on, or a public address set)"`
	HasPassword   bool       `json:"hasPassword"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
	AllowDownload bool       `json:"allowDownload"`
	AllowUpload   bool       `json:"allowUpload"`
	Views         int32      `json:"views"`
	LastViewedAt  *time.Time `json:"lastViewedAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
}

type linkSettings struct {
	Password      string     `json:"password,omitempty" maxLength:"200" doc:"Visitors type it first"`
	ClearPassword bool       `json:"clearPassword,omitempty" doc:"Take the password away (when changing a link)"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty" doc:"It stops working then; none: until it's deleted"`
	AllowDownload bool       `json:"allowDownload,omitempty" doc:"Visitors may download the files (with their location taken out)"`
	AllowUpload   bool       `json:"allowUpload,omitempty" doc:"Visitors may add their own photos and videos to the album"`
}

type newLinkInput struct {
	ID   uuid.UUID `path:"id"`
	Body linkSettings
	requestAddress
}

type editLinkInput struct {
	ID   uuid.UUID `path:"id"`
	Body linkSettings
	requestAddress
}

type albumLinksInput struct {
	ID uuid.UUID `path:"id"`
	requestAddress
}

type linkOutput struct{ Body Link }

type linksOutput struct {
	Body struct {
		Links []Link `json:"links"`
	}
}

// SharedAlbum is what a link's visitor sees first.
type SharedAlbum struct {
	Title         string     `json:"title"`
	Description   string     `json:"description,omitempty"`
	Owner         string     `json:"owner"`
	NeedsPassword bool       `json:"needsPassword" doc:"Give the password (POST …/unlock) to see it"`
	AllowDownload bool       `json:"allowDownload"`
	AllowUpload   bool       `json:"allowUpload"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
}

// SharedItem is one of a shared album's photos or videos.
type SharedItem struct {
	ID         uuid.UUID  `json:"id"`
	Type       string     `json:"type" enum:"image,video"`
	TakenAt    time.Time  `json:"takenAt"`
	LocalDate  string     `json:"localDate"`
	Width      int32      `json:"width"`
	Height     int32      `json:"height"`
	DurationMs int32      `json:"durationMs,omitempty"`
	ThumbHash  []byte     `json:"thumbhash,omitempty"`
	MotionID   *uuid.UUID `json:"motionId,omitempty"`
}

type publicLinkInput struct {
	Token  string `path:"token" maxLength:"100"`
	Access string `cookie:"ps_link"`
}

type sharedAlbumOutput struct{ Body SharedAlbum }

type sharedItemsOutput struct {
	Body struct {
		Items []SharedItem `json:"items" doc:"Newest first"`
	}
}

type unlockInput struct {
	Token string `path:"token" maxLength:"100"`
	Body  struct {
		Password string `json:"password" maxLength:"200"`
	}
	asker asker
}

func (u *unlockInput) Resolve(ctx huma.Context) []error {
	u.asker = askerOf(ctx)
	return nil
}

type unlockOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

type publicItemInput struct {
	Token  string    `path:"token" maxLength:"100"`
	ID     uuid.UUID `path:"id"`
	Access string    `cookie:"ps_link"`
}

func newLinkToken() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// linkBase is where links point: the tailnet's address when Funnel makes it public, else the
// public address, else the address this request came in on (then only reachable from there).
func (s *server) linkBase(ctx context.Context, here string) (string, bool) {
	if s.Tailnet != nil {
		if st := s.Tailnet.State(ctx); st.Funnel && st.URL != "" {
			return st.URL, true
		}
	}
	if s.PublicURL != "" {
		return s.PublicURL, true
	}
	return here, false
}

func (s *server) linkOf(ctx context.Context, l db.ShareLink, here string) Link {
	base, public := s.linkBase(ctx, here)
	url := base + "/s/" + l.Token
	qr, _ := qrPNG(url)
	return Link{ID: l.ID, URL: url, QR: qr, Public: public, HasPassword: l.PasswordHash.Valid, ExpiresAt: l.ExpiresAt,
		AllowDownload: l.AllowDownload, AllowUpload: l.AllowUpload, Views: l.Views, LastViewedAt: l.LastViewedAt, CreatedAt: l.CreatedAt}
}

func passwordHash(pw string) (pgtype.Text, error) {
	if pw == "" {
		return pgtype.Text{}, nil
	}
	h, err := auth.HashPassword(pw)
	return pgtype.Text{String: h, Valid: err == nil}, err
}

// openLink is a link a visitor may look at: there, not run out, and unlocked if it has a password.
func (s *server) openLink(ctx context.Context, token, access string) (db.LinkByTokenRow, error) {
	l, err := s.Store.Q.LinkByToken(ctx, token)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, huma.Error404NotFound("This link doesn’t work anymore. Ask for a new one.")
	}
	if err != nil {
		return l, err
	}
	if l.PasswordHash.Valid && !s.unlocked(ctx, l.ID, access) {
		return l, huma.Error401Unauthorized("This album needs its password.")
	}
	return l, nil
}

func (s *server) unlocked(ctx context.Context, link uuid.UUID, access string) bool {
	if access == "" {
		return false
	}
	raw, err := s.Store.Q.LinkAccess(ctx, auth.TokenHash(access))
	if err != nil {
		return false
	}
	var d struct{ Link uuid.UUID }
	return json.Unmarshal(raw, &d) == nil && d.Link == link
}

func (s *server) registerLinks(mux *http.ServeMux) {
	huma.Register(s.api, huma.Operation{
		OperationID: "albumLinks", Method: http.MethodGet, Path: "/api/v1/albums/{id}/links", Tags: []string{"Sharing"},
		Summary: "An album's share links", Security: sessionAuth,
	}, func(ctx context.Context, in *albumLinksInput) (*linksOutput, error) {
		rows, err := s.Store.Q.AlbumLinks(ctx, db.AlbumLinksParams{AlbumID: in.ID, OwnerID: viewer(ctx)})
		if err != nil {
			return nil, err
		}
		out := &linksOutput{}
		out.Body.Links = make([]Link, len(rows))
		for i, l := range rows {
			out.Body.Links[i] = s.linkOf(ctx, l, in.base)
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "newLink", Method: http.MethodPost, Path: "/api/v1/albums/{id}/links", Tags: []string{"Sharing"},
		Summary: "Make a share link for an album", Security: sessionAuth, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *newLinkInput) (*linkOutput, error) {
		r, err := s.Store.Q.AlbumRules(ctx, db.AlbumRulesParams{ID: in.ID, Viewer: viewer(ctx)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("No such album.")
		}
		if err != nil {
			return nil, err
		}
		if r.Kind == "smart" {
			return nil, huma.Error409Conflict("Smart albums can't be shared by link yet. Make an album of their items instead.")
		}
		pw, err := passwordHash(in.Body.Password)
		if err != nil {
			return nil, err
		}
		l, err := s.Store.Q.CreateLink(ctx, db.CreateLinkParams{Token: newLinkToken(), OwnerID: viewer(ctx), AlbumID: in.ID, PasswordHash: pw,
			ExpiresAt: in.Body.ExpiresAt, AllowDownload: in.Body.AllowDownload, AllowUpload: in.Body.AllowUpload})
		if err != nil {
			return nil, err
		}
		return &linkOutput{Body: s.linkOf(ctx, l, in.base)}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "editLink", Method: http.MethodPatch, Path: "/api/v1/links/{id}", Tags: []string{"Sharing"},
		Summary: "Change a share link", Description: "Its password (a new one, or clearPassword), last day, downloads and uploads.", Security: sessionAuth,
	}, func(ctx context.Context, in *editLinkInput) (*linkOutput, error) {
		pw, err := passwordHash(in.Body.Password)
		if err != nil {
			return nil, err
		}
		l, err := s.Store.Q.UpdateLink(ctx, db.UpdateLinkParams{ID: in.ID, OwnerID: viewer(ctx), SetPassword: in.Body.Password != "" || in.Body.ClearPassword,
			PasswordHash: pw, ExpiresAt: in.Body.ExpiresAt, AllowDownload: in.Body.AllowDownload, AllowUpload: in.Body.AllowUpload})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("No such link.")
		}
		if err != nil {
			return nil, err
		}
		return &linkOutput{Body: s.linkOf(ctx, l, in.base)}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "deleteLink", Method: http.MethodDelete, Path: "/api/v1/links/{id}", Tags: []string{"Sharing"},
		Summary: "Delete a share link", Description: "It stops working at once.", Security: sessionAuth, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		n, err := s.Store.Q.DeleteLink(ctx, db.DeleteLinkParams{ID: in.ID, OwnerID: viewer(ctx)})
		if err == nil && n == 0 {
			return nil, huma.Error404NotFound("No such link.")
		}
		return nil, err
	})

	// What visitors use: no account, only the link (and its password).
	huma.Register(s.api, huma.Operation{
		OperationID: "sharedAlbum", Method: http.MethodGet, Path: "/api/v1/public/links/{token}", Tags: []string{"Public"},
		Summary: "A shared album: its title, whose it is, and what visitors may do",
	}, func(ctx context.Context, in *publicLinkInput) (*sharedAlbumOutput, error) {
		l, err := s.Store.Q.LinkByToken(ctx, in.Token)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("This link doesn’t work anymore. Ask for a new one.")
		}
		if err != nil {
			return nil, err
		}
		out := &sharedAlbumOutput{Body: SharedAlbum{Title: l.AlbumTitle, Owner: l.OwnerName, NeedsPassword: l.PasswordHash.Valid && !s.unlocked(ctx, l.ID, in.Access),
			AllowDownload: l.AllowDownload, AllowUpload: l.AllowUpload, ExpiresAt: l.ExpiresAt}}
		if !out.Body.NeedsPassword {
			out.Body.Description = l.AlbumDescription
			_ = s.Store.Q.LinkViewed(ctx, l.ID)
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "unlockLink", Method: http.MethodPost, Path: "/api/v1/public/links/{token}/unlock", Tags: []string{"Public"},
		Summary: "Give a shared album's password", Description: "This browser may then see it for a day.",
	}, func(ctx context.Context, in *unlockInput) (*unlockOutput, error) {
		if !s.tryAllowed(in.asker, false) {
			return nil, huma.Error429TooManyRequests("Too many wrong passwords. Wait a few minutes.")
		}
		l, err := s.Store.Q.LinkByToken(ctx, in.Token)
		if err != nil {
			return nil, huma.Error404NotFound("This link doesn’t work anymore. Ask for a new one.")
		}
		if l.PasswordHash.Valid {
			if ok, _ := auth.CheckPassword(in.Body.Password, l.PasswordHash.String); !ok {
				s.tryAllowed(in.asker, true)
				return nil, huma.Error403Forbidden("That isn’t the password.")
			}
		}
		token, err := auth.StartFlow(ctx, s.Store.Q, "link_access", uuid.NullUUID{}, map[string]uuid.UUID{"link": l.ID}, 24*time.Hour)
		if err != nil {
			return nil, err
		}
		return &unlockOutput{SetCookie: http.Cookie{Name: linkCookie, Value: token, Path: "/api/v1/public/links/" + in.Token, HttpOnly: true,
			Secure: s.SecureCookies || fromInternet(ctx), SameSite: http.SameSiteLaxMode, MaxAge: 24 * 3600}}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "sharedItems", Method: http.MethodGet, Path: "/api/v1/public/links/{token}/items", Tags: []string{"Public"},
		Summary: "A shared album's photos and videos",
	}, func(ctx context.Context, in *publicLinkInput) (*sharedItemsOutput, error) {
		l, err := s.openLink(ctx, in.Token, in.Access)
		if err != nil {
			return nil, err
		}
		rows, err := s.Store.Q.LinkItems(ctx, db.LinkItemsParams{OwnerID: l.OwnerID, AlbumID: l.AlbumID})
		if err != nil {
			return nil, err
		}
		out := &sharedItemsOutput{}
		out.Body.Items = make([]SharedItem, len(rows))
		for i, r := range rows {
			it := SharedItem{ID: r.ID, Type: r.Type, TakenAt: r.TakenAt, LocalDate: day(r.LocalDate), Width: r.Width.Int32, Height: r.Height.Int32,
				DurationMs: r.DurationMs.Int32, ThumbHash: r.Thumbhash}
			if r.LiveMotionID.Valid {
				id := r.LiveMotionID.UUID
				it.MotionID = &id
			}
			out.Body.Items[i] = it
		}
		return out, nil
	})

	for _, size := range []string{"thumbnail", "preview"} {
		size := size
		huma.Register(s.api, huma.Operation{
			OperationID: "shared" + upper(size), Method: http.MethodGet, Path: "/api/v1/public/links/{token}/assets/{id}/" + size, Tags: []string{"Public"},
			Summary: "A shared item's " + size + " (WebP, no metadata)",
			Responses: map[string]*huma.Response{"200": {Description: "WebP image",
				Content: map[string]*huma.MediaType{"image/webp": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}}},
		}, func(ctx context.Context, in *publicItemInput) (*huma.StreamResponse, error) {
			l, err := s.openLink(ctx, in.Token, in.Access)
			if err != nil {
				return nil, err
			}
			if _, err := s.Store.Q.LinkItem(ctx, db.LinkItemParams{OwnerID: l.OwnerID, ID: in.ID, AlbumID: l.AlbumID}); err != nil {
				return nil, errNotFound
			}
			thumb, preview := s.Render.Paths(in.ID)
			p := map[string]string{"thumbnail": thumb, "preview": preview}[size]
			if _, err := os.Stat(p); err != nil {
				return nil, huma.Error404NotFound("Not rendered yet.")
			}
			return &huma.StreamResponse{Body: func(hctx huma.Context) {
				r, w := humago.Unwrap(hctx)
				w.Header().Set("Content-Type", "image/webp")
				w.Header().Set("Cache-Control", "private, max-age=3600")
				http.ServeFile(w, r, p)
			}}, nil
		})
	}

	// Videos (to play) and downloads, as raw handlers: the file itself, or, when it records where
	// it was taken, a copy with that taken out, made as it's sent.
	mux.HandleFunc("GET /api/v1/public/links/{token}/assets/{id}/{what}", func(w http.ResponseWriter, r *http.Request) {
		what := r.PathValue("what")
		if what != "video" && what != "download" {
			http.NotFound(w, r)
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		access := ""
		if c, err := r.Cookie(linkCookie); err == nil {
			access = c.Value
		}
		l, err := s.openLink(r.Context(), r.PathValue("token"), access)
		if err != nil {
			var se huma.StatusError
			if errors.As(err, &se) {
				http.Error(w, se.Error(), se.GetStatus())
			} else {
				http.Error(w, "unavailable", http.StatusInternalServerError)
			}
			return
		}
		if what == "download" && !l.AllowDownload {
			http.Error(w, "Downloads aren't allowed for this album.", http.StatusForbidden)
			return
		}
		it, err := s.Store.Q.LinkItem(r.Context(), db.LinkItemParams{OwnerID: l.OwnerID, ID: id, AlbumID: l.AlbumID})
		if err != nil || (what == "video" && it.Type != "video") {
			http.NotFound(w, r)
			return
		}
		path, err := fileIn(it.RootPath, it.RelPath)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		name := filepath.Base(path)
		if what == "download" {
			w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		}
		w.Header().Set("Cache-Control", "private, max-age=3600")
		if !it.Located { // nothing to take out: the file as it is (with ranges, for seeking)
			f, err := os.Open(path)
			if err != nil {
				http.Error(w, "The file isn't reachable right now.", http.StatusNotFound)
				return
			}
			defer f.Close()
			if ct := it.Mime.String; ct != "" {
				w.Header().Set("Content-Type", ct)
			}
			info, _ := f.Stat()
			http.ServeContent(w, r, name, info.ModTime(), f)
			return
		}
		if it.Type == "video" {
			w.Header().Set("Content-Type", "video/mp4")
			cmd := exec.CommandContext(r.Context(), "ffmpeg", "-nostdin", "-loglevel", "error", "-i", path,
				// Picture and sound only: data tracks can carry the location as it moved (Apple's
				// timed metadata, GoPro's GPS), and streams have tags of their own.
				"-map", "0:v", "-map", "0:a?", "-dn", "-map_metadata", "-1", "-map_metadata:s:v", "-1", "-map_metadata:s:a", "-1",
				"-c", "copy", "-movflags", "frag_keyframe+empty_moov", "-f", "mp4", "-")
			cmd.Stdout = w
			if err := cmd.Run(); err != nil && r.Context().Err() == nil {
				s.logStrip(path, err)
			}
			return
		}
		// A photo is made without its location first, then sent: if that fails the visitor is told,
		// never sent nothing (or the original). It goes to exiftool on its standard input, so a file
		// whose name says another format than it is (a JPEG called .png) is still read.
		in, err := os.Open(path)
		if err != nil {
			http.Error(w, "The file isn't reachable right now.", http.StatusNotFound)
			return
		}
		defer in.Close()
		out, err := os.CreateTemp("", "shared-*")
		if err != nil {
			slog.Warn("shared photo: no scratch file", "err", err)
			http.Error(w, "This photo couldn't be got ready.", http.StatusInternalServerError)
			return
		}
		defer os.Remove(out.Name())
		defer out.Close()
		cmd := exec.CommandContext(r.Context(), "exiftool", "-q", "-q", "-m", "-gps:all=", "-xmp-exif:all=",
			// Places by name, too (IPTC and XMP), and IPTC Extension's locations, which can hold coordinates.
			"-iptc:City=", "-iptc:Sub-location=", "-iptc:Province-State=", "-iptc:Country-PrimaryLocationName=", "-iptc:Country-PrimaryLocationCode=",
			"-xmp-photoshop:City=", "-xmp-photoshop:State=", "-xmp-photoshop:Country=", "-xmp-iptcCore:Location=", "-xmp-iptcCore:CountryCode=",
			"-xmp-iptcExt:LocationCreated=", "-xmp-iptcExt:LocationShown=",
			"-o", "-", "-")
		cmd.Stdin, cmd.Stdout = in, out
		if err := cmd.Run(); err != nil {
			if r.Context().Err() == nil {
				s.logStrip(path, err)
				http.Error(w, "This photo couldn't be got ready without its location. Try again later.", http.StatusInternalServerError)
			}
			return
		}
		if ct := it.Mime.String; ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		_, _ = out.Seek(0, io.SeekStart)
		http.ServeContent(w, r, name, time.Time{}, out)
	})

	mux.HandleFunc("POST /api/v1/public/links/{token}/upload", func(w http.ResponseWriter, r *http.Request) {
		access := ""
		if c, err := r.Cookie(linkCookie); err == nil {
			access = c.Value
		}
		l, err := s.openLink(r.Context(), r.PathValue("token"), access)
		if err != nil {
			replyError(w, http.StatusNotFound, "This link doesn’t work anymore.")
			return
		}
		if !l.AllowUpload {
			replyError(w, http.StatusForbidden, "Adding photos isn't allowed for this album.")
			return
		}
		from := strings.TrimSpace(r.URL.Query().Get("from"))
		if len(from) > 80 {
			from = from[:80]
		}
		s.receive(w, r, l.OwnerID, "share link", func(m *UploadMeta) {
			m.Album, m.Keywords, m.SourceURL = l.AlbumTitle, nil, ""
			m.Description = "Added through a share link"
			if from != "" {
				m.Description = fmt.Sprintf("Added by %s through a share link", from)
			}
		})
	})
}

func (s *server) logStrip(path string, err error) {
	slog.Default().Warn("sending a shared file without its location failed", "file", path, "err", err)
}

// fileIn joins a library root and a stored relative path, refusing anything outside the root.
func fileIn(root, rel string) (string, error) {
	p := filepath.Join(root, filepath.FromSlash(rel))
	if p != root && !strings.HasPrefix(p, filepath.Clean(root)+string(filepath.Separator)) {
		return "", errors.New("outside its library")
	}
	return p, nil
}
