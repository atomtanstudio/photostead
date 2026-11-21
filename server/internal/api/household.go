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

// The Household: the server's accounts and one shared space. Everyone keeps their own library;
// what's shared (by hand, or by their auto-share rule) shows to every member beside their own
// (the gate lets them see it). Only whoever added an item can trash it; any member can take it out
// of the space. The owner (or the server's admin) invites people with a link and sets their roles.

const inviteTTL = 7 * 24 * time.Hour

// Member is someone in the household.
type Member struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Email    string    `json:"email"`
	Role     string    `json:"role" enum:"owner,adult,member"`
	JoinedAt time.Time `json:"joinedAt"`
	Shared   int32     `json:"shared" doc:"Items they've shared"`
	Me       bool      `json:"me,omitempty"`
}

// Invite is an invitation not used yet.
type Invite struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name,omitempty"`
	Email     string    `json:"email,omitempty"`
	Role      string    `json:"role" enum:"adult,member"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Guest is someone albums are shared with from outside the household, as the server's admin sees
// them (to remove them).
type Guest struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Email   string    `json:"email,omitempty"`
	AddedBy string    `json:"addedBy,omitempty" doc:"Who shared with them first (made their personal link)"`
	AddedAt time.Time `json:"addedAt"`
	Albums  int32     `json:"albums" doc:"Albums shared with them"`
	Devices int32     `json:"devices" doc:"Browsers and devices signed in as them"`
}

// ShareRule is a person's auto-share rule.
type ShareRule struct {
	Enabled   bool        `json:"enabled"`
	From      string      `json:"from,omitempty" pattern:"^([0-9]{4}-[0-9]{2}-[0-9]{2})?$" doc:"Items from this day on (YYYY-MM-DD); empty: all of them"`
	People    []uuid.UUID `json:"people,omitempty" doc:"Only items with any of these people (your own People); empty: anyone"`
	IncludeAI bool        `json:"includeAi,omitempty" doc:"AI-made images too (private to you otherwise)"`
}

// Household is the household as one member sees it.
type Household struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Role      string    `json:"role" doc:"Yours"`
	CanManage bool      `json:"canManage" doc:"Whether you can invite people and set roles (the owner, or the server's admin)"`
	Shared    int32     `json:"shared" doc:"Items in the Household space"`
	Members   []Member  `json:"members"`
	Invites   []Invite  `json:"invites" doc:"Not used yet (only for those who can manage)"`
	Guests    []Guest   `json:"guests" doc:"Everyone albums are shared with from outside the household, in an album now or not (only for the server's admin, who can remove them)"`
	Rule      ShareRule `json:"rule" doc:"Your auto-share rule"`
}

type householdOutput struct{ Body Household }

type householdNameInput struct {
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"100"`
	}
}

type inviteInput struct {
	requestAddress
	Body struct {
		Name  string `json:"name,omitempty" maxLength:"100"`
		Email string `json:"email,omitempty" maxLength:"320"`
		Role  string `json:"role" enum:"adult,member" default:"adult"`
	}
}

type inviteOutput struct {
	Body struct {
		Token     string    `json:"token" doc:"For the link: /#/invite/{token}. Shown this once"`
		URL       string    `json:"url" doc:"The link to send"`
		Public    bool      `json:"public" doc:"The link works from the internet (Funnel, or the public address); else only from where you are"`
		ExpiresAt time.Time `json:"expiresAt"`
	}
}

type roleInput struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Role string `json:"role" enum:"adult,member"`
	}
}

type tokenInput struct {
	Token string `path:"token" maxLength:"200"`
}

type invitationOutput struct {
	Body struct {
		Household string `json:"household"`
		InvitedBy string `json:"invitedBy"`
		Name      string `json:"name,omitempty"`
		Email     string `json:"email,omitempty"`
		Guest     bool   `json:"guest,omitempty" doc:"A guest's invitation: open it (POST …/open), no account to make"`
	}
}

type acceptInput struct {
	Token string `path:"token" maxLength:"200"`
	Body  struct {
		DisplayName string `json:"displayName" minLength:"1" maxLength:"100"`
		Email       string `json:"email" format:"email" maxLength:"320"`
		Password    string `json:"password" minLength:"12" maxLength:"1024"`
	}
}

type ruleInput struct{ Body ShareRule }

// inviteData is what an invitation remembers.
type inviteData struct {
	Name      string    `json:"name,omitempty"`
	Email     string    `json:"email,omitempty"`
	Role      string    `json:"role"`                // adult or member; guest for someone an album is shared with
	Album     uuid.UUID `json:"album,omitempty"`     // a guest's album
	AlbumRole string    `json:"albumRole,omitempty"` // viewer or contributor
}

func (s *server) household(ctx context.Context) (db.HouseholdRow, error) {
	h, err := s.Store.Q.Household(ctx, viewer(ctx))
	if errors.Is(err, pgx.ErrNoRows) {
		return h, huma.Error404NotFound("You're not in a household.")
	}
	return h, err
}

// manager is a household's owner, or the server's admin.
func (s *server) manager(ctx context.Context) (db.HouseholdRow, error) {
	h, err := s.household(ctx)
	if err != nil {
		return h, err
	}
	if h.Role == "owner" {
		return h, nil
	}
	if s.admin(ctx) != nil {
		return h, huma.Error403Forbidden("Only the household's owner can do that.")
	}
	return h, nil
}

func (s *server) householdView(ctx context.Context) (Household, error) {
	h, err := s.household(ctx)
	if err != nil {
		return Household{}, err
	}
	out := Household{ID: h.ID, Name: h.Name, Role: h.Role, Shared: h.Shared, Members: []Member{}, Invites: []Invite{}, Guests: []Guest{}, Rule: ShareRule{People: []uuid.UUID{}}}
	admin := s.admin(ctx) == nil
	out.CanManage = h.Role == "owner" || admin
	members, err := s.Store.Q.HouseholdMembers(ctx, h.ID)
	if err != nil {
		return out, err
	}
	for _, m := range members {
		out.Members = append(out.Members, Member{ID: m.ID, Name: firstNonEmpty(m.DisplayName, m.Email), Email: m.Email, Role: m.Role,
			JoinedAt: m.JoinedAt, Shared: m.Shared, Me: m.ID == viewer(ctx)})
	}
	if out.CanManage {
		invites, err := s.Store.Q.PendingInvites(ctx)
		if err != nil {
			return out, err
		}
		for _, i := range invites {
			var d inviteData
			_ = json.Unmarshal(i.Data, &d)
			if d.Role != "guest" { // guests are invited to an album, not the household
				out.Invites = append(out.Invites, Invite{ID: i.ID, Name: d.Name, Email: d.Email, Role: d.Role, ExpiresAt: i.ExpiresAt})
			}
		}
	}
	if admin { // every guest, including those no album is shared with any more
		guests, err := s.Store.Q.Guests(ctx)
		if err != nil {
			return out, err
		}
		for _, g := range guests {
			out.Guests = append(out.Guests, Guest{ID: g.ID, Name: g.Name, Email: shownEmail(g.Email), AddedBy: g.AddedBy, AddedAt: g.CreatedAt,
				Albums: g.Albums, Devices: g.Devices})
		}
	}
	if r, err := s.Store.Q.ShareRule(ctx, db.ShareRuleParams{Viewer: viewer(ctx), SpaceID: h.SpaceID}); err == nil {
		out.Rule = ShareRule{Enabled: r.Enabled, From: day(r.FromDate), People: r.People, IncludeAI: r.IncludeAi}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	return out, nil
}

func (s *server) registerHousehold() {
	huma.Register(s.api, huma.Operation{
		OperationID: "household", Method: http.MethodGet, Path: "/api/v1/household", Tags: []string{"Household"},
		Summary: "Your household: its members, invitations, what's shared, and your auto-share rule", Security: sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*householdOutput, error) {
		h, err := s.householdView(ctx)
		return &householdOutput{Body: h}, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "renameHousehold", Method: http.MethodPatch, Path: "/api/v1/household", Tags: []string{"Household"},
		Summary: "Rename the household", Security: sessionAuth,
	}, func(ctx context.Context, in *householdNameInput) (*householdOutput, error) {
		h, err := s.manager(ctx)
		if err != nil {
			return nil, err
		}
		if err := s.Store.Q.RenameHousehold(ctx, db.RenameHouseholdParams{ID: h.ID, Name: strings.TrimSpace(in.Body.Name)}); err != nil {
			return nil, err
		}
		v, err := s.householdView(ctx)
		return &householdOutput{Body: v}, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "invite", Method: http.MethodPost, Path: "/api/v1/household/invites", Tags: []string{"Household"},
		Summary:     "Invite someone to the household",
		Description: "A link for them (/#/invite/{token}), good once for 7 days: they choose their name and password, and join.",
		Security:    sessionAuth, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *inviteInput) (*inviteOutput, error) {
		if _, err := s.manager(ctx); err != nil {
			return nil, err
		}
		role := in.Body.Role
		if role == "" {
			role = "adult"
		}
		d := inviteData{Name: strings.TrimSpace(in.Body.Name), Email: strings.TrimSpace(in.Body.Email), Role: role}
		token, err := auth.StartFlow(ctx, s.Store.Q, "invite", uuid.NullUUID{UUID: viewer(ctx), Valid: true}, d, inviteTTL)
		if err != nil {
			return nil, err
		}
		out := &inviteOutput{}
		// For the household: the address you're on (members come in at home, or on the tailnet).
		out.Body.Token, out.Body.URL, out.Body.ExpiresAt = token, in.base+"/#/invite/"+token, time.Now().Add(inviteTTL)
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "cancelInvite", Method: http.MethodDelete, Path: "/api/v1/household/invites/{id}", Tags: []string{"Household"},
		Summary: "Cancel an invitation", Security: sessionAuth, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		if _, err := s.manager(ctx); err != nil {
			return nil, err
		}
		n, err := s.Store.Q.CancelInvite(ctx, in.ID)
		if err == nil && n == 0 {
			return nil, huma.Error404NotFound("No such invitation.")
		}
		return nil, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "setMemberRole", Method: http.MethodPatch, Path: "/api/v1/household/members/{id}", Tags: []string{"Household"},
		Summary: "Change a member's role", Description: "Adult or member; the owner stays the owner.", Security: sessionAuth,
	}, func(ctx context.Context, in *roleInput) (*householdOutput, error) {
		h, err := s.manager(ctx)
		if err != nil {
			return nil, err
		}
		n, err := s.Store.Q.SetMemberRole(ctx, db.SetMemberRoleParams{HouseholdID: h.ID, UserID: in.ID, Role: in.Body.Role})
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, huma.Error404NotFound("No such member (the owner's role doesn't change).")
		}
		v, err := s.householdView(ctx)
		return &householdOutput{Body: v}, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "removeGuest", Method: http.MethodDelete, Path: "/api/v1/household/guests/{id}", Tags: []string{"Household"},
		Summary: "Remove a guest",
		Description: "Their account goes, with their sign-ins and devices, personal link, album shares, comments and picks. The albums and photos stay, " +
			"with what they added. Only guests, and only the server's admin.",
		Security: sessionAuth, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		if err := s.admin(ctx); err != nil {
			return nil, err
		}
		return nil, s.Store.Tx(ctx, func(q *db.Queries) error {
			g, err := q.GuestToRemove(ctx, in.ID)
			if errors.Is(err, pgx.ErrNoRows) {
				return huma.Error404NotFound("No such guest.")
			} else if err != nil {
				return err
			}
			if g.Role != "guest" {
				return huma.Error409Conflict(fmt.Sprintf("%s is in the household, not a guest. Only guests can be removed.", g.Name))
			}
			// A guest owns nothing, unless given a library or an API key from the command line: their
			// albums would go with the account, and a library or items would stop it. Refused instead.
			if g.Libraries+g.Assets+g.Albums > 0 {
				return huma.Error409Conflict(fmt.Sprintf("%s has a library, items or albums of their own, which removing them would delete. Move or delete those first.", g.Name))
			}
			_, err = q.RemoveGuest(ctx, in.ID)
			return err
		})
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "invitation", Method: http.MethodGet, Path: "/api/v1/invites/{token}", Tags: []string{"Household"},
		Summary: "What an invitation is for (before signing up)",
	}, func(ctx context.Context, in *tokenInput) (*invitationOutput, error) {
		f, err := s.Store.Q.TakeFlow(ctx, db.TakeFlowParams{TokenHash: auth.TokenHash(in.Token), Kind: "invite"})
		if err != nil || f.Attempts > 50 {
			return nil, huma.Error404NotFound("This invitation has been used, or has run out. Ask for a new one.")
		}
		var d inviteData
		_ = json.Unmarshal(f.Data, &d)
		out := &invitationOutput{}
		out.Body.Name, out.Body.Email, out.Body.Guest = d.Name, d.Email, d.Role == "guest"
		if f.UserID.Valid {
			if u, err := s.Store.Q.GetUser(ctx, f.UserID.UUID); err == nil {
				out.Body.InvitedBy = firstNonEmpty(u.DisplayName, u.Email)
				if h, err := s.Store.Q.Household(ctx, u.ID); err == nil {
					out.Body.Household = h.Name
				}
			}
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "acceptInvite", Method: http.MethodPost, Path: "/api/v1/invites/{token}", Tags: []string{"Household"},
		Summary: "Accept an invitation", Description: "Makes the account, joins the household, and signs in.",
	}, func(ctx context.Context, in *acceptInput) (*loginOutput, error) {
		f, err := s.Store.Q.TakeFlow(ctx, db.TakeFlowParams{TokenHash: auth.TokenHash(in.Token), Kind: "invite"})
		if err != nil {
			return nil, huma.Error404NotFound("This invitation has been used, or has run out. Ask for a new one.")
		}
		var d inviteData
		_ = json.Unmarshal(f.Data, &d)
		if fromInternet(ctx) && d.Role != "guest" {
			return nil, huma.Error403Forbidden("This invitation is to the household: open it at home, or with Tailscale on.")
		}
		if _, err := s.Store.Q.GetUserByEmail(ctx, in.Body.Email); err == nil {
			return nil, huma.Error409Conflict("There's already an account with that email. Sign in instead.")
		}
		var u db.User
		err = s.Store.Tx(ctx, func(q *db.Queries) error {
			var err error
			name := strings.TrimSpace(in.Body.DisplayName)
			if d.Role == "guest" {
				u, err = auth.NewGuest(ctx, q, in.Body.Email, name)
			} else {
				u, err = auth.NewMember(ctx, q, in.Body.Email, name)
			}
			if err != nil {
				return err
			}
			if err := auth.SetPassword(ctx, q, u, in.Body.Password); err != nil {
				return huma.Error422UnprocessableEntity(err.Error())
			}
			if d.Role == "guest" { // into the album they were invited to
				if _, err := q.SetAlbumMember(ctx, db.SetAlbumMemberParams{AlbumID: d.Album, OwnerID: f.UserID.UUID, UserID: u.ID, Role: d.AlbumRole}); err != nil {
					return err
				}
			} else if _, err := q.SetMemberRole(ctx, db.SetMemberRoleParams{HouseholdID: householdOf(ctx, q, u.ID), UserID: u.ID, Role: d.Role}); err != nil {
				return err
			}
			return q.EndFlow(ctx, f.ID)
		})
		if err != nil {
			return nil, err
		}
		return s.signedIn(ctx, u)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "shareItems", Method: http.MethodPost, Path: "/api/v1/household/share", Tags: []string{"Household"},
		Summary: "Share your items with the household", Security: sessionAuth,
	}, func(ctx context.Context, in *itemsInput) (*changedOutput, error) {
		h, err := s.household(ctx)
		if err != nil {
			return nil, err
		}
		ids, err := s.Store.Q.ShareItems(ctx, db.ShareItemsParams{Ids: in.Body.AssetIDs, Viewer: viewer(ctx), SpaceID: h.SpaceID})
		out := &changedOutput{}
		out.Body.Changed = int64(len(ids))
		return out, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "unshareItems", Method: http.MethodPost, Path: "/api/v1/household/unshare", Tags: []string{"Household"},
		Summary:     "Take items out of the household",
		Description: "Any member may. They stay in their owner's library, and auto-share rules won't put them back.",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *itemsInput) (*changedOutput, error) {
		h, err := s.household(ctx)
		if err != nil {
			return nil, err
		}
		ids, err := s.Store.Q.UnshareItems(ctx, db.UnshareItemsParams{SpaceID: h.SpaceID, Ids: in.Body.AssetIDs})
		out := &changedOutput{}
		out.Body.Changed = int64(len(ids))
		return out, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "setShareRule", Method: http.MethodPut, Path: "/api/v1/household/rule", Tags: []string{"Household"},
		Summary:     "Set your auto-share rule",
		Description: "It applies at once: matching items are shared, and ones it alone shared that no longer match come out. New items follow within 10 minutes.",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *ruleInput) (*householdOutput, error) {
		h, err := s.household(ctx)
		if err != nil {
			return nil, err
		}
		r := in.Body
		people := r.People
		if people == nil {
			people = []uuid.UUID{}
		}
		err = s.Store.Tx(ctx, func(q *db.Queries) error {
			id, err := q.SetShareRule(ctx, db.SetShareRuleParams{Viewer: viewer(ctx), SpaceID: h.SpaceID, FromDate: parseDay(r.From),
				People: people, IncludeAi: r.IncludeAI, Enabled: r.Enabled})
			if err != nil {
				return err
			}
			if _, err := q.UnshareFromRule(ctx, uuid.NullUUID{UUID: id, Valid: true}); err != nil {
				return err
			}
			if r.Enabled {
				_, err = q.ApplyShareRules(ctx, []uuid.UUID{id})
			}
			return err
		})
		if err != nil {
			return nil, err
		}
		v, err := s.householdView(ctx)
		return &householdOutput{Body: v}, err
	})
}

// householdOf is the household a user is in.
func householdOf(ctx context.Context, q *db.Queries, userID uuid.UUID) uuid.UUID {
	h, err := q.Household(ctx, userID)
	if err != nil {
		return uuid.Nil
	}
	return h.ID
}
