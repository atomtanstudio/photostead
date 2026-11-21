package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"photostead.com/server/internal/store/db"
)

// Single sign-on (OpenID Connect): sign in with an identity provider the household already runs
// (Authentik, Authelia, Keycloak, Pocket ID, Google…). A provider's account is linked to a
// Photostead account by its subject: on first sign-in, to the account with the same verified email,
// or one made for them when new accounts are allowed. The provider asks for its own second step.

// OIDCConfig comes from the server's settings (PHOTOSTEAD_OIDC_*).
type OIDCConfig struct {
	Issuer, ClientID, ClientSecret string
	Name                           string // on the sign-in button: "Sign in with …"
	NewAccounts                    bool   // make an account for someone the provider knows but Photostead doesn't
}

func (c OIDCConfig) Enabled() bool { return c.Issuer != "" && c.ClientID != "" }

// OIDC talks to the provider, reading its discovery document the first time it's needed (and again
// after a failure, so a provider that was down at start-up is found later).
type OIDC struct {
	Config   OIDCConfig
	mu       sync.Mutex
	provider *oidc.Provider
}

var (
	ErrNoAccount = errors.New("no Photostead account goes with that sign-in. Ask the server's admin for one")
	ErrSignOn    = errors.New("the sign-in didn't complete. Try again")
	ErrTaken     = errors.New("that sign-in is already linked to another Photostead account")
)

// oidcFlow is what a trip to the provider remembers.
type oidcFlow struct {
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"` // PKCE
	Redirect string `json:"redirect"`
}

func (o *OIDC) discover(ctx context.Context) (*oidc.Provider, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.provider == nil {
		p, err := oidc.NewProvider(ctx, o.Config.Issuer)
		if err != nil {
			return nil, fmt.Errorf("the sign-on provider can't be reached: %w", err)
		}
		o.provider = p
	}
	return o.provider, nil
}

func (o *OIDC) oauth(p *oidc.Provider, redirect string) *oauth2.Config {
	return &oauth2.Config{ClientID: o.Config.ClientID, ClientSecret: o.Config.ClientSecret, Endpoint: p.Endpoint(),
		RedirectURL: redirect, Scopes: []string{oidc.ScopeOpenID, "email", "profile"}}
}

// Start begins a sign-in (or, with link, linking the provider's account to a signed-in user's):
// where to send the browser, and the token that comes back as the state (also kept in a cookie,
// so the trip can only finish in the browser that started it).
func (o *OIDC) Start(ctx context.Context, q *db.Queries, redirect string, link uuid.NullUUID) (to, token string, err error) {
	p, err := o.discover(ctx)
	if err != nil {
		return "", "", err
	}
	nonce, _, err := NewToken()
	if err != nil {
		return "", "", err
	}
	f := oidcFlow{Nonce: nonce, Verifier: oauth2.GenerateVerifier(), Redirect: redirect}
	if token, err = StartFlow(ctx, q, "oidc", link, f, flowTTL*2); err != nil {
		return "", "", err
	}
	return o.oauth(p, redirect).AuthCodeURL(token, oidc.Nonce(nonce), oauth2.S256ChallengeOption(f.Verifier)), token, nil
}

type oidcClaims struct {
	Subject  string `json:"sub"`
	Email    string `json:"email"`
	Verified bool   `json:"email_verified"`
	Name     string `json:"name"`
}

// Finish takes the provider back: the account signed in to (or linked).
func (o *OIDC) Finish(ctx context.Context, q *db.Queries, state, code string) (db.User, error) {
	f, err := q.TakeFlow(ctx, db.TakeFlowParams{TokenHash: TokenHash(state), Kind: "oidc"})
	if err != nil {
		return db.User{}, ErrSignOn
	}
	_ = q.EndFlow(ctx, f.ID)
	var flow oidcFlow
	if json.Unmarshal(f.Data, &flow) != nil {
		return db.User{}, ErrSignOn
	}
	p, err := o.discover(ctx)
	if err != nil {
		return db.User{}, err
	}
	tok, err := o.oauth(p, flow.Redirect).Exchange(ctx, code, oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		return db.User{}, ErrSignOn
	}
	raw, _ := tok.Extra("id_token").(string)
	idt, err := p.Verifier(&oidc.Config{ClientID: o.Config.ClientID}).Verify(ctx, raw)
	if err != nil || idt.Nonce != flow.Nonce {
		return db.User{}, ErrSignOn
	}
	var c oidcClaims
	if err := idt.Claims(&c); err != nil || c.Subject == "" {
		return db.User{}, ErrSignOn
	}
	if c.Email == "" || !c.Verified { // some providers leave it out of the ID token
		if ui, err := p.UserInfo(ctx, oauth2.StaticTokenSource(tok)); err == nil {
			var more oidcClaims
			if ui.Claims(&more) == nil && more.Subject == c.Subject {
				c.Email, c.Verified, c.Name = firstOf(c.Email, more.Email), c.Verified || more.Verified, firstOf(c.Name, more.Name)
			}
		}
	}
	link := func(userID uuid.UUID) error {
		data, _ := json.Marshal(map[string]string{"issuer": idt.Issuer, "subject": c.Subject, "email": c.Email})
		_, err := q.AddCredential(ctx, db.AddCredentialParams{UserID: userID, Kind: "oidc", Name: firstOf(c.Email, c.Subject), Data: data})
		return err
	}
	linked, err := q.OIDCUser(ctx, db.OIDCUserParams{Issuer: idt.Issuer, Subject: c.Subject})
	switch {
	case err == nil && f.UserID.Valid && linked.UserID != f.UserID.UUID:
		return db.User{}, ErrTaken
	case err == nil:
		_ = q.UpdateCredential(ctx, db.UpdateCredentialParams{ID: linked.ID, Data: mustJSON(map[string]string{"issuer": idt.Issuer, "subject": c.Subject, "email": c.Email})})
		return q.GetUser(ctx, linked.UserID)
	case f.UserID.Valid: // linking to the signed-in account
		if err := link(f.UserID.UUID); err != nil {
			return db.User{}, err
		}
		return q.GetUser(ctx, f.UserID.UUID)
	}
	if c.Email != "" && c.Verified {
		if u, err := q.GetUserByEmail(ctx, c.Email); err == nil {
			return u, link(u.ID)
		}
	}
	if !o.Config.NewAccounts || c.Email == "" || !c.Verified {
		return db.User{}, ErrNoAccount
	}
	u, err := NewMember(ctx, q, c.Email, c.Name)
	if err != nil {
		return db.User{}, err
	}
	return u, link(u.ID)
}

// NewMember makes a member's account (for single sign-on, or an invitation): a free username, and
// no password yet.
func NewMember(ctx context.Context, q *db.Queries, email, name string) (db.User, error) {
	return newAccount(email, func(username string) (db.User, error) {
		return q.CreateMember(ctx, db.CreateMemberParams{Email: strings.TrimSpace(email), Username: username, DisplayName: name})
	})
}

// NewGuest makes a guest's account: someone an album is shared with, with no library of their own.
func NewGuest(ctx context.Context, q *db.Queries, email, name string) (db.User, error) {
	return newAccount(email, func(username string) (db.User, error) {
		return q.CreateGuest(ctx, db.CreateGuestParams{Email: strings.TrimSpace(email), Username: username, DisplayName: name})
	})
}

func newAccount(email string, create func(username string) (db.User, error)) (db.User, error) {
	base := UsernameFrom(email)
	for i := 1; i < 100; i++ {
		username := base
		if i > 1 {
			suffix := fmt.Sprintf("-%d", i)
			username = base[:min(len(base), 32-len(suffix))] + suffix
		}
		u, err := create(username)
		if err == nil {
			return u, nil
		}
		if !strings.Contains(err.Error(), "username") {
			return db.User{}, err
		}
	}
	return db.User{}, errors.New("couldn't find a free username")
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
