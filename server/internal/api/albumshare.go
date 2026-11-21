package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/store/db"
)

// Albums shared with people: someone in the household, or a guest (no library of their own: most
// come in by a personal link, see personal.go). A viewer sees the album; a contributor also adds to it:
// from their library, or by uploading (into the album owner's library, noted as theirs). What's
// shared this way shows in the album, not in their timeline.

// AlbumPerson is someone an album is (or could be) shared with.
type AlbumPerson struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Email string    `json:"email"`
	Guest bool      `json:"guest,omitempty" doc:"A guest: no library of their own"`
	Role  string    `json:"role,omitempty" enum:"viewer,contributor"`
	// Link: their personal link, when you made it.
	Link *PersonalLink `json:"link,omitempty"`
}

type albumPeopleOutput struct {
	Body struct {
		Members    []AlbumPerson `json:"members"`
		Candidates []AlbumPerson `json:"candidates" doc:"The household, and guests of your other albums"`
		Invites    []Invite      `json:"invites" doc:"Guests invited and not joined yet"`
	}
}

type albumIDInput struct {
	requestAddress
	ID uuid.UUID `path:"id"`
}

type albumPersonInput struct {
	requestAddress
	ID   uuid.UUID `path:"id"`
	User uuid.UUID `path:"user"`
	Body struct {
		Role string `json:"role" enum:"viewer,contributor"`
	}
}

type albumInviteInput struct {
	requestAddress
	ID     uuid.UUID `path:"id"`
	Invite uuid.UUID `path:"invite"`
}

type albumPersonRemoveInput struct {
	requestAddress
	ID   uuid.UUID `path:"id"`
	User uuid.UUID `path:"user"`
}

type guestInviteInput struct {
	requestAddress
	ID   uuid.UUID `path:"id"`
	Body struct {
		Name  string `json:"name,omitempty" maxLength:"100"`
		Email string `json:"email,omitempty" maxLength:"320"`
		Role  string `json:"role" enum:"viewer,contributor"`
	}
}

func (s *server) ownAlbum(ctx context.Context, id uuid.UUID) (db.AlbumRoleRow, error) {
	r, err := s.Store.Q.AlbumRole(ctx, db.AlbumRoleParams{AlbumID: id, Viewer: viewer(ctx)})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && r.Role != "owner") {
		return r, huma.Error404NotFound("No such album of yours.")
	}
	if err == nil && r.Kind == "smart" {
		return r, huma.Error409Conflict("Smart albums can't be shared with people yet. Make an album of their items instead.")
	}
	return r, err
}

func (s *server) albumPeople(ctx context.Context, id uuid.UUID, here string) (*albumPeopleOutput, error) {
	members, err := s.Store.Q.AlbumMembers(ctx, db.AlbumMembersParams{AlbumID: id, OwnerID: viewer(ctx)})
	if err != nil {
		return nil, err
	}
	candidates, err := s.Store.Q.ShareCandidates(ctx, viewer(ctx))
	if err != nil {
		return nil, err
	}
	links, err := s.personalLinks(ctx, here)
	if err != nil {
		return nil, err
	}
	out := &albumPeopleOutput{}
	out.Body.Members, out.Body.Candidates, out.Body.Invites = []AlbumPerson{}, []AlbumPerson{}, []Invite{}
	in := map[uuid.UUID]bool{}
	for _, m := range members {
		in[m.ID] = true
		out.Body.Members = append(out.Body.Members, AlbumPerson{ID: m.ID, Name: m.Name, Email: shownEmail(m.Email), Guest: m.Account == "guest", Role: m.Role, Link: links[m.ID]})
	}
	for _, c := range candidates {
		if !in[c.ID] {
			out.Body.Candidates = append(out.Body.Candidates, AlbumPerson{ID: c.ID, Name: c.Name, Email: shownEmail(c.Email), Guest: c.Account == "guest"})
		}
	}
	invites, err := s.Store.Q.PendingInvites(ctx)
	if err != nil {
		return nil, err
	}
	for _, i := range invites {
		var d inviteData
		_ = json.Unmarshal(i.Data, &d)
		if d.Role == "guest" && d.Album == id {
			out.Body.Invites = append(out.Body.Invites, Invite{ID: i.ID, Name: d.Name, Email: d.Email, Role: d.AlbumRole, ExpiresAt: i.ExpiresAt})
		}
	}
	return out, nil
}

func (s *server) registerAlbumSharing(mux *http.ServeMux) {
	huma.Register(s.api, huma.Operation{
		OperationID: "albumPeople", Method: http.MethodGet, Path: "/api/v1/albums/{id}/people", Tags: []string{"Sharing"},
		Summary: "Who an album is shared with, and who it could be", Security: sessionAuth,
	}, func(ctx context.Context, in *albumIDInput) (*albumPeopleOutput, error) {
		if _, err := s.ownAlbum(ctx, in.ID); err != nil {
			return nil, err
		}
		return s.albumPeople(ctx, in.ID, in.base)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "shareAlbumWith", Method: http.MethodPut, Path: "/api/v1/albums/{id}/people/{user}", Tags: []string{"Sharing"},
		Summary: "Share an album with someone, or change what they may do", Security: sessionAuth,
	}, func(ctx context.Context, in *albumPersonInput) (*albumPeopleOutput, error) {
		if _, err := s.ownAlbum(ctx, in.ID); err != nil {
			return nil, err
		}
		ok := false
		cands, err := s.Store.Q.ShareCandidates(ctx, viewer(ctx))
		if err != nil {
			return nil, err
		}
		for _, c := range cands {
			ok = ok || c.ID == in.User
		}
		if !ok {
			return nil, huma.Error404NotFound("They're not in your household or one of your albums. Invite them as a guest instead.")
		}
		if _, err := s.Store.Q.SetAlbumMember(ctx, db.SetAlbumMemberParams{AlbumID: in.ID, OwnerID: viewer(ctx), UserID: in.User, Role: in.Body.Role}); err != nil {
			return nil, err
		}
		return s.albumPeople(ctx, in.ID, in.base)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "unshareAlbumWith", Method: http.MethodDelete, Path: "/api/v1/albums/{id}/people/{user}", Tags: []string{"Sharing"},
		Summary: "Stop sharing an album with someone", Description: "What they added stays in the album.", Security: sessionAuth,
	}, func(ctx context.Context, in *albumPersonRemoveInput) (*albumPeopleOutput, error) {
		if _, err := s.ownAlbum(ctx, in.ID); err != nil {
			return nil, err
		}
		if _, err := s.Store.Q.RemoveAlbumMember(ctx, db.RemoveAlbumMemberParams{AlbumID: in.ID, OwnerID: viewer(ctx), UserID: in.User}); err != nil {
			return nil, err
		}
		return s.albumPeople(ctx, in.ID, in.base)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "cancelGuestInvite", Method: http.MethodDelete, Path: "/api/v1/albums/{id}/invites/{invite}", Tags: []string{"Sharing"},
		Summary: "Cancel a guest's invitation to an album", Description: "Its link stops working.", Security: sessionAuth,
	}, func(ctx context.Context, in *albumInviteInput) (*albumPeopleOutput, error) {
		if _, err := s.ownAlbum(ctx, in.ID); err != nil {
			return nil, err
		}
		n, err := s.Store.Q.CancelGuestInvite(ctx, db.CancelGuestInviteParams{ID: in.Invite, AlbumID: in.ID, OwnerID: viewer(ctx)})
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, huma.Error404NotFound("No such invitation to this album.")
		}
		return s.albumPeople(ctx, in.ID, in.base)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "inviteGuest", Method: http.MethodPost, Path: "/api/v1/albums/{id}/guests", Tags: []string{"Sharing"},
		Summary:     "Invite a guest to an album",
		Description: "A link (url: on the internet address when there is one), good once for 7 days: they make an account (with no library of its own) and see the album, or add to it as a contributor.",
		Security:    sessionAuth, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *guestInviteInput) (*inviteOutput, error) {
		if _, err := s.ownAlbum(ctx, in.ID); err != nil {
			return nil, err
		}
		d := inviteData{Name: strings.TrimSpace(in.Body.Name), Email: strings.TrimSpace(in.Body.Email), Role: "guest", Album: in.ID, AlbumRole: in.Body.Role}
		token, err := auth.StartFlow(ctx, s.Store.Q, "invite", uuid.NullUUID{UUID: viewer(ctx), Valid: true}, d, inviteTTL)
		if err != nil {
			return nil, err
		}
		out := &inviteOutput{}
		base, public := s.linkBase(ctx, in.base) // guests come in from anywhere, like share links
		out.Body.Token, out.Body.URL, out.Body.Public, out.Body.ExpiresAt = token, base+"/#/invite/"+token, public, time.Now().Add(inviteTTL)
		return out, nil
	})

	// A contributor adding photos (a guest has no library: they go into the album owner's,
	// noted as theirs).
	mux.HandleFunc("POST /api/v1/albums/{id}/upload", func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		who, _, ok := s.uploader(r)
		if !ok {
			replyError(w, http.StatusUnauthorized, "Sign in first.")
			return
		}
		role, err := s.Store.Q.AlbumRole(r.Context(), db.AlbumRoleParams{AlbumID: id, Viewer: who})
		if err != nil || (role.Role != "owner" && role.Role != "contributor") || role.Kind != "manual" {
			replyError(w, http.StatusForbidden, "You can't add to this album.")
			return
		}
		name := "someone"
		if u, err := s.Store.Q.GetUser(r.Context(), who); err == nil {
			name = firstNonEmpty(u.DisplayName, u.Email)
		}
		s.receive(w, r, role.OwnerID, "album: "+name, func(m *UploadMeta) {
			m.Album, m.Keywords = role.Title, nil
			if role.OwnerID != who {
				m.Description = strings.TrimSpace(fmt.Sprintf("Added by %s. %s", name, m.Description))
			}
		})
	})
}
