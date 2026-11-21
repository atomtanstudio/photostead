package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/store/db"
)

// Personal links: sharing albums with someone by name, with no account for them to make. Their
// link (/p/{token}) is the way in: the first few devices to open it are signed in as them, a
// guest, and see every album shared with them; after that it's turned away. Whoever made it can
// copy it again, sign its devices out, or replace it (the old one stops).

// linkGuestEmail is the address a personal link's guest has (accounts need one): .invalid never
// receives mail, and nobody signs in with it.
const linkGuestEmail = "@personal-link.invalid"

// PersonalLink is a guest's way in, as the person who made it sees it.
type PersonalLink struct {
	URL        string    `json:"url"`
	Public     bool      `json:"public" doc:"It works from the internet (Funnel, or the public address); else only from where you are"`
	Devices    int32     `json:"devices" doc:"Devices it's open on"`
	MaxDevices int32     `json:"maxDevices"`
	Opened     bool      `json:"opened" doc:"Opened on a device at least once"`
	LastSeenAt time.Time `json:"lastSeenAt" doc:"When one of its devices was last used (when it was made, if never)"`
}

// shownEmail leaves out a personal link guest's made-up address.
func shownEmail(email string) string {
	if strings.HasSuffix(email, linkGuestEmail) {
		return ""
	}
	return email
}

// personalLinks are the links the viewer made, by guest.
func (s *server) personalLinks(ctx context.Context, here string) (map[uuid.UUID]*PersonalLink, error) {
	rows, err := s.Store.Q.PersonalLinksBy(ctx, viewer(ctx))
	if err != nil {
		return nil, err
	}
	base, public := s.linkBase(ctx, here)
	links := map[uuid.UUID]*PersonalLink{}
	for _, r := range rows {
		links[r.UserID] = &PersonalLink{URL: base + "/p/" + r.Token, Public: public, Devices: r.Devices, MaxDevices: r.MaxDevices, Opened: r.Opened, LastSeenAt: r.LastSeenAt}
	}
	return links, nil
}

type newPersonInput struct {
	requestAddress
	ID   uuid.UUID `path:"id"`
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"100"`
		Role string `json:"role,omitempty" enum:"viewer,contributor" default:"viewer"`
	}
}

type guestInput struct {
	requestAddress
	ID uuid.UUID `path:"id"`
}

type personalLinkOutput struct{ Body PersonalLink }

type openLinkInput struct {
	Token     string `path:"token" maxLength:"100"`
	Session   string `cookie:"ps_session"`
	UserAgent string `header:"User-Agent"`
}

func (s *server) registerPersonalLinks() {
	huma.Register(s.api, huma.Operation{
		OperationID: "shareWithSomeoneNew", Method: http.MethodPost, Path: "/api/v1/albums/{id}/people", Tags: []string{"Sharing"},
		Summary:     "Share an album with someone new, by name",
		Description: "Makes them a guest with a personal link (no account for them to make): send them the link. It opens on the first 3 devices they use, and every album shared with them is there.",
		Security:    sessionAuth, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *newPersonInput) (*albumPeopleOutput, error) {
		if _, err := s.ownAlbum(ctx, in.ID); err != nil {
			return nil, err
		}
		name := strings.TrimSpace(in.Body.Name)
		if name == "" {
			return nil, huma.Error422UnprocessableEntity("Give their name, so you know who it's for.")
		}
		err := s.Store.Tx(ctx, func(q *db.Queries) error {
			u, err := newLinkGuest(ctx, q, name, viewer(ctx))
			if err != nil {
				return err
			}
			_, err = q.SetAlbumMember(ctx, db.SetAlbumMemberParams{AlbumID: in.ID, OwnerID: viewer(ctx), UserID: u.ID, Role: in.Body.Role})
			return err
		})
		if err != nil {
			return nil, err
		}
		return s.albumPeople(ctx, in.ID, in.base)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "signOutLinkDevices", Method: http.MethodDelete, Path: "/api/v1/guests/{id}/devices", Tags: []string{"Sharing"},
		Summary:     "Sign a personal link's devices out",
		Description: "Its devices stop seeing the albums until the link is opened again, which then works on 3 devices again. Only whoever made the link.",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *guestInput) (*personalLinkOutput, error) {
		if _, err := s.Store.Q.SignOutLinkDevices(ctx, db.SignOutLinkDevicesParams{UserID: in.ID, CreatedBy: viewer(ctx)}); err != nil {
			return nil, err
		}
		return s.personalLinkOf(ctx, in.ID, in.base)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "replacePersonalLink", Method: http.MethodPost, Path: "/api/v1/guests/{id}/link", Tags: []string{"Sharing"},
		Summary:     "Replace a personal link",
		Description: "The old link stops working and its devices are signed out; send them the new one. Only whoever made the link.",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *guestInput) (*personalLinkOutput, error) {
		err := s.Store.Tx(ctx, func(q *db.Queries) error {
			if _, err := q.ReplacePersonalLink(ctx, db.ReplacePersonalLinkParams{UserID: in.ID, CreatedBy: viewer(ctx), Token: newLinkToken()}); err != nil {
				return err
			}
			_, err := q.SignOutLinkDevices(ctx, db.SignOutLinkDevicesParams{UserID: in.ID, CreatedBy: viewer(ctx)})
			return err
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("No personal link of yours for them.")
		}
		if err != nil {
			return nil, err
		}
		return s.personalLinkOf(ctx, in.ID, in.base)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "openPersonalLink", Method: http.MethodPost, Path: "/api/v1/public/personal/{token}", Tags: []string{"Public"},
		Summary:     "Open a personal link on this device",
		Description: "Signs this browser in as the guest it's for, when it's one of the first devices to open it; where it's already open, it carries on.",
	}, func(ctx context.Context, in *openLinkInput) (*loginOutput, error) {
		gone := huma.Error404NotFound("This link doesn't work any more. Ask whoever sent it for a new one.")
		if row, ok := s.signedInHere(ctx, in.Session); ok { // as them, carry on; as someone else, say so
			if pl, err := s.Store.Q.PersonalLinkFor(ctx, in.Token); err != nil {
				return nil, gone
			} else if pl != row.ID {
				return nil, signedInAsSomeoneElse(row)
			}
			u, err := s.Store.Q.GetUser(ctx, row.ID)
			if err != nil {
				return nil, err
			}
			return &loginOutput{SetCookie: s.cookie(ctx, in.Session, deviceTTL), Body: s.userOf(u)}, nil
		}
		var u db.User
		var token string
		err := s.Store.Tx(ctx, func(q *db.Queries) error {
			pl, err := q.PersonalLinkByToken(ctx, in.Token)
			if err != nil {
				return gone
			}
			if n, err := q.PersonalLinkDevices(ctx, pl.UserID); err != nil {
				return err
			} else if n >= pl.MaxDevices {
				return huma.Error403Forbidden(fmt.Sprintf("This link is already open on %d devices. Ask %s to sign one out, or for a new link.", pl.MaxDevices, pl.SharedBy))
			}
			if u, err = q.GetUser(ctx, pl.UserID); err != nil {
				return err
			}
			token, err = linkSession(ctx, q, u.ID, in.UserAgent)
			return err
		})
		if err != nil {
			return nil, err
		}
		return &loginOutput{SetCookie: s.cookie(ctx, token, deviceTTL), Body: s.userOf(u)}, nil
	})

	// A guest invitation (made before personal links) opens the same way: no account to make. It
	// becomes their personal link's first device, and the inviter sees the link in the album.
	huma.Register(s.api, huma.Operation{
		OperationID: "openGuestInvite", Method: http.MethodPost, Path: "/api/v1/invites/{token}/open", Tags: []string{"Sharing"},
		Summary:     "Open a guest invitation on this device",
		Description: "Makes the guest it's for, with a personal link (from whoever invited them), and signs this browser in as them. Household invitations still need an account.",
	}, func(ctx context.Context, in *openLinkInput) (*loginOutput, error) {
		if row, ok := s.signedInHere(ctx, in.Session); ok {
			return nil, signedInAsSomeoneElse(row)
		}
		var u db.User
		var token string
		err := s.Store.Tx(ctx, func(q *db.Queries) error {
			f, err := q.TakeFlow(ctx, db.TakeFlowParams{TokenHash: auth.TokenHash(in.Token), Kind: "invite"})
			if err != nil || !f.UserID.Valid {
				return huma.Error404NotFound("This invitation has been used, or has run out. Ask for a new one.")
			}
			var d inviteData
			_ = json.Unmarshal(f.Data, &d)
			if d.Role != "guest" {
				return huma.Error409Conflict("This invitation is to join the household: it needs an account.")
			}
			if u, err = newLinkGuest(ctx, q, firstNonEmpty(d.Name, "Guest"), f.UserID.UUID); err != nil {
				return err
			}
			if _, err := q.SetAlbumMember(ctx, db.SetAlbumMemberParams{AlbumID: d.Album, OwnerID: f.UserID.UUID, UserID: u.ID, Role: d.AlbumRole}); err != nil {
				return err
			}
			if err := q.EndFlow(ctx, f.ID); err != nil {
				return err
			}
			token, err = linkSession(ctx, q, u.ID, in.UserAgent)
			return err
		})
		if err != nil {
			return nil, err
		}
		return &loginOutput{SetCookie: s.cookie(ctx, token, deviceTTL), Body: s.userOf(u)}, nil
	})
}

// newLinkGuest makes a guest, named by whoever shares with them, with a personal link they made.
func newLinkGuest(ctx context.Context, q *db.Queries, name string, by uuid.UUID) (db.User, error) {
	u, err := auth.NewGuest(ctx, q, "guest-"+randomHex(5)+linkGuestEmail, name)
	if err != nil {
		return u, err
	}
	_, err = q.CreatePersonalLink(ctx, db.CreatePersonalLinkParams{UserID: u.ID, Token: newLinkToken(), CreatedBy: by})
	return u, err
}

// linkSession signs a device in as a personal link's guest: one of its devices, for a year that
// slides with use.
func linkSession(ctx context.Context, q *db.Queries, guest uuid.UUID, userAgent string) (string, error) {
	token, hash, err := auth.NewToken()
	if err != nil {
		return "", err
	}
	_, err = q.CreateLinkSession(ctx, db.CreateLinkSessionParams{TokenHash: hash, UserID: guest, ExpiresAt: time.Now().Add(deviceTTL),
		UserAgent: pgtype.Text{String: userAgent, Valid: userAgent != ""}})
	return token, err
}

// signedInHere is who this browser's session is, if it has one.
func (s *server) signedInHere(ctx context.Context, session string) (db.GetSessionUserRow, bool) {
	if session == "" {
		return db.GetSessionUserRow{}, false
	}
	row, err := s.Store.Q.GetSessionUser(ctx, auth.TokenHash(session))
	return row, err == nil
}

func signedInAsSomeoneElse(row db.GetSessionUserRow) error {
	return huma.Error409Conflict(fmt.Sprintf("You're signed in here as %s. Open the link in a private window, or sign out first.",
		firstNonEmpty(row.DisplayName, row.Username)))
}

func (s *server) personalLinkOf(ctx context.Context, guest uuid.UUID, here string) (*personalLinkOutput, error) {
	links, err := s.personalLinks(ctx, here)
	if err != nil {
		return nil, err
	}
	l, ok := links[guest]
	if !ok {
		return nil, huma.Error404NotFound("No personal link of yours for them.")
	}
	return &personalLinkOutput{Body: *l}, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
