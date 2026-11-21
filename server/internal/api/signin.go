package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/store/db"
)

// Sign-in beyond a password: two-step codes from an authenticator app, passkeys and single sign-on
// (internal/auth has how each works). Turning them on or adding one asks for the password again,
// so a session left open somewhere can't add a way in.

const oidcCookie = "ps_oidc"

// Passkey is one of a person's passkeys.
type Passkey struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}

// SignOnLink is a single sign-on account linked to this one.
type SignOnLink struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email" doc:"As the provider gave it"`
}

// Security is how a person signs in.
type Security struct {
	HasPassword  bool         `json:"hasPassword"`
	TwoStep      bool         `json:"twoStep" doc:"A code from an authenticator app after the password"`
	RecoveryLeft int          `json:"recoveryLeft" doc:"Recovery codes not used yet"`
	Passkeys     []Passkey    `json:"passkeys"`
	SignOn       string       `json:"signOn,omitempty" doc:"The single sign-on provider's name, when one is set up"`
	SignOnLinks  []SignOnLink `json:"signOnLinks"`
}

type securityOutput struct{ Body Security }

type confirmInput struct {
	Body struct {
		Password string `json:"password,omitempty" maxLength:"1024" doc:"The account's password (not needed by an account without one)"`
	}
}

type twoStepSetupOutput struct {
	Body struct {
		Secret string `json:"secret" doc:"For typing into the app by hand"`
		URI    string `json:"uri" doc:"otpauth:// address"`
		QR     string `json:"qr" doc:"A QR code of the address (PNG data URL) for the app to scan"`
	}
}

type codeInput struct {
	Body struct {
		Code string `json:"code" minLength:"6" maxLength:"20"`
	}
}

type recoveryOutput struct {
	Body struct {
		RecoveryCodes []string `json:"recoveryCodes" doc:"Each signs in once instead of a code. Shown this once"`
	}
}

type twoStepOffInput struct {
	Body struct {
		Password string `json:"password,omitempty" maxLength:"1024"`
		Code     string `json:"code,omitempty" maxLength:"20" doc:"Or a code from the app"`
	}
}

type secondStepInput struct {
	Body struct {
		Ticket string `json:"ticket" minLength:"1" maxLength:"200"`
		Code   string `json:"code" minLength:"6" maxLength:"20" doc:"From the authenticator app, or a recovery code"`
	}
}

type originInput struct {
	Origin string `header:"Origin" doc:"The address the browser is at: a passkey belongs to its host name"`
}

type passkeyBeginInput struct {
	Origin string `header:"Origin"`
	Body   struct {
		Password string `json:"password,omitempty" maxLength:"1024"`
	}
}

type passkeyOptionsOutput struct {
	Body struct {
		Flow    string          `json:"flow" doc:"Give it back with the browser's answer"`
		Options json.RawMessage `json:"options" doc:"For navigator.credentials.create() or .get()"`
	}
}

type passkeyFinishInput struct {
	Origin string `header:"Origin"`
	Body   struct {
		Flow       string          `json:"flow" minLength:"1" maxLength:"200"`
		Name       string          `json:"name,omitempty" maxLength:"100" doc:"What to call it (the device it's on, say)"`
		Credential json.RawMessage `json:"credential" doc:"The PublicKeyCredential the browser made or signed with, as JSON"`
	}
}

type passkeyOutput struct{ Body Passkey }

type redirectOutput struct {
	Status    int
	Location  string        `header:"Location"`
	SetCookie []http.Cookie `header:"Set-Cookie"`
}

// requestAddress is where a request came in: the scheme and host (behind a proxy, as it says).
type requestAddress struct{ base string }

func (r *requestAddress) Resolve(ctx huma.Context) []error {
	scheme, host := "http", ctx.Host()
	if ctx.TLS() != nil {
		scheme = "https"
	}
	if p := ctx.Header("X-Forwarded-Proto"); p != "" {
		scheme = strings.TrimSpace(strings.Split(p, ",")[0])
	}
	if h := ctx.Header("X-Forwarded-Host"); h != "" {
		host = strings.TrimSpace(strings.Split(h, ",")[0])
	}
	r.base = scheme + "://" + host
	return nil
}

type oidcStartInput struct {
	requestAddress
}

type oidcLinkInput struct {
	requestAddress
	Body struct {
		Password string `json:"password,omitempty" maxLength:"1024"`
	}
}

type oidcLinkOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      struct {
		URL string `json:"url" doc:"Send the browser here; it comes back signed in, linked"`
	}
}

type oidcCallbackInput struct {
	requestAddress
	Code   string `query:"code"`
	State  string `query:"state"`
	Error  string `query:"error"`
	Cookie string `cookie:"ps_oidc"`
}

// confirm asks for the password again before a new way in is added (an account made by single
// sign-on has none, and its provider vouched for it just now).
func (s *server) confirm(ctx context.Context, password string) error {
	if _, err := s.Store.Q.GetPassword(ctx, viewer(ctx)); err != nil {
		return nil
	}
	if !auth.CheckUserPassword(ctx, s.Store.Q, viewer(ctx), password) {
		return huma.Error403Forbidden("That isn’t your password.")
	}
	return nil
}

func (s *server) security(ctx context.Context) (Security, error) {
	sec := Security{Passkeys: []Passkey{}, SignOnLinks: []SignOnLink{}}
	if _, err := s.Store.Q.GetPassword(ctx, viewer(ctx)); err == nil {
		sec.HasPassword = true
	}
	if _, d, ok, err := auth.TwoStep(ctx, s.Store.Q, viewer(ctx)); err != nil {
		return sec, err
	} else if ok && d.Confirmed {
		sec.TwoStep, sec.RecoveryLeft = true, len(d.Recovery)
	}
	keys, err := s.Store.Q.Credentials(ctx, db.CredentialsParams{UserID: viewer(ctx), Kind: "passkey"})
	if err != nil {
		return sec, err
	}
	for _, k := range keys {
		sec.Passkeys = append(sec.Passkeys, Passkey{ID: k.ID, Name: k.Name, CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt})
	}
	links, err := s.Store.Q.Credentials(ctx, db.CredentialsParams{UserID: viewer(ctx), Kind: "oidc"})
	if err != nil {
		return sec, err
	}
	for _, l := range links {
		var d struct{ Email string }
		_ = json.Unmarshal(l.Data, &d)
		sec.SignOnLinks = append(sec.SignOnLinks, SignOnLink{ID: l.ID, Email: firstNonEmpty(d.Email, l.Name)})
	}
	if s.signOnOn() {
		sec.SignOn = s.OIDC.Config.Name
	}
	return sec, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (s *server) signOnOn() bool { return s.OIDC != nil && s.OIDC.Config.Enabled() }

// signOnReturn is where the provider sends the browser back to.
func (s *server) signOnReturn(base string) string {
	if s.PublicURL != "" {
		base = s.PublicURL
	}
	return base + "/api/v1/auth/oidc/callback"
}

func (s *server) relyingParty(origin string) (*webauthn.WebAuthn, error) {
	rp, err := auth.RelyingParty(origin)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("Passkeys need the address Photostead is reached at (the browser's Origin).")
	}
	return rp, nil
}

func (s *server) signedIn(ctx context.Context, u db.User) (*loginOutput, error) {
	if err := mayStart(ctx, u); err != nil {
		return nil, err
	}
	token, err := auth.StartSession(context.Background(), s.Store, u)
	if err != nil {
		return nil, err
	}
	return &loginOutput{SetCookie: s.cookie(ctx, token, auth.SessionTTL), Body: s.userOf(u)}, nil
}

func (s *server) registerSignIn() {
	huma.Register(s.api, huma.Operation{
		OperationID: "secondStep", Method: http.MethodPost, Path: "/api/v1/auth/login/two-step", Tags: []string{"Auth"},
		Summary: "Sign in: the code after the password", Description: "With the ticket a password sign-in gave. Starts a session.",
	}, func(ctx context.Context, in *secondStepInput) (*loginOutput, error) {
		f, err := s.Store.Q.TakeFlow(ctx, db.TakeFlowParams{TokenHash: auth.TokenHash(in.Body.Ticket), Kind: "second_step"})
		if err != nil || !f.UserID.Valid {
			return nil, huma.Error401Unauthorized("That sign-in has run out. Start again with your password.")
		}
		if f.Attempts > 5 {
			_ = s.Store.Q.EndFlow(ctx, f.ID)
			return nil, huma.Error401Unauthorized("Too many wrong codes. Start again with your password.")
		}
		if err := auth.CheckSecondStep(ctx, s.Store.Q, f.UserID.UUID, in.Body.Code); errors.Is(err, auth.ErrBadCode) {
			return nil, huma.Error403Forbidden("That code isn’t right, or it was used already.")
		} else if err != nil {
			return nil, err
		}
		_ = s.Store.Q.EndFlow(ctx, f.ID)
		u, err := s.Store.Q.GetUser(ctx, f.UserID.UUID)
		if err != nil {
			return nil, err
		}
		return s.signedIn(ctx, u)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "security", Method: http.MethodGet, Path: "/api/v1/auth/security", Tags: []string{"Auth"},
		Summary: "How you sign in: password, two-step codes, passkeys, single sign-on", Security: sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*securityOutput, error) {
		sec, err := s.security(ctx)
		return &securityOutput{Body: sec}, err
	})

	// Two-step sign-in.
	huma.Register(s.api, huma.Operation{
		OperationID: "twoStepSetup", Method: http.MethodPost, Path: "/api/v1/auth/two-step/setup", Tags: []string{"Auth"},
		Summary:     "Start turning on two-step sign-in",
		Description: "A new secret for an authenticator app to scan. It's on once a code from the app is given back (/two-step/confirm).",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *confirmInput) (*twoStepSetupOutput, error) {
		if err := s.confirm(ctx, in.Body.Password); err != nil {
			return nil, err
		}
		if on, err := auth.TwoStepOn(ctx, s.Store.Q, viewer(ctx)); err != nil {
			return nil, err
		} else if on {
			return nil, huma.Error409Conflict("Two-step sign-in is on already. Turn it off first to use another app.")
		}
		u, err := s.Store.Q.GetUser(ctx, viewer(ctx))
		if err != nil {
			return nil, err
		}
		out := &twoStepSetupOutput{}
		out.Body.Secret, out.Body.URI, out.Body.QR, err = auth.NewTwoStep(ctx, s.Store.Q, u)
		return out, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "twoStepConfirm", Method: http.MethodPost, Path: "/api/v1/auth/two-step/confirm", Tags: []string{"Auth"},
		Summary: "Turn on two-step sign-in with a first code from the app", Security: sessionAuth,
	}, func(ctx context.Context, in *codeInput) (*recoveryOutput, error) {
		codes, err := auth.ConfirmTwoStep(ctx, s.Store.Q, viewer(ctx), in.Body.Code)
		switch {
		case errors.Is(err, auth.ErrBadCode):
			return nil, huma.Error403Forbidden("That code isn’t right. Check the app’s clock, and try the newest code.")
		case errors.Is(err, auth.ErrNoTwoStep):
			return nil, huma.Error409Conflict("Start again: there's no new secret waiting.")
		case err != nil:
			return nil, err
		}
		out := &recoveryOutput{}
		out.Body.RecoveryCodes = codes
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "twoStepRecovery", Method: http.MethodPost, Path: "/api/v1/auth/two-step/recovery", Tags: []string{"Auth"},
		Summary: "New recovery codes (the old ones stop working)", Security: sessionAuth,
	}, func(ctx context.Context, in *confirmInput) (*recoveryOutput, error) {
		if err := s.confirm(ctx, in.Body.Password); err != nil {
			return nil, err
		}
		codes, err := auth.NewRecovery(ctx, s.Store.Q, viewer(ctx))
		if errors.Is(err, auth.ErrNoTwoStep) {
			return nil, huma.Error409Conflict("Two-step sign-in isn't on.")
		}
		out := &recoveryOutput{}
		out.Body.RecoveryCodes = codes
		return out, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "twoStepOff", Method: http.MethodPost, Path: "/api/v1/auth/two-step/off", Tags: []string{"Auth"},
		Summary: "Turn off two-step sign-in", Description: "With the password, or a code from the app.", Security: sessionAuth, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *twoStepOffInput) (*struct{}, error) {
		if in.Body.Code != "" {
			if err := auth.CheckSecondStep(ctx, s.Store.Q, viewer(ctx), in.Body.Code); err != nil {
				return nil, huma.Error403Forbidden("That code isn’t right, or it was used already.")
			}
		} else if err := s.confirm(ctx, in.Body.Password); err != nil {
			return nil, err
		}
		_, err := s.Store.Q.DeleteCredentials(ctx, db.DeleteCredentialsParams{UserID: viewer(ctx), Kind: "totp"})
		return nil, err
	})

	// Passkeys.
	huma.Register(s.api, huma.Operation{
		OperationID: "passkeyAddBegin", Method: http.MethodPost, Path: "/api/v1/auth/passkeys/begin", Tags: []string{"Auth"},
		Summary: "Start adding a passkey", Description: "Options for navigator.credentials.create(); finish with /passkeys/finish.", Security: sessionAuth,
	}, func(ctx context.Context, in *passkeyBeginInput) (*passkeyOptionsOutput, error) {
		if err := s.confirm(ctx, in.Body.Password); err != nil {
			return nil, err
		}
		rp, err := s.relyingParty(in.Origin)
		if err != nil {
			return nil, err
		}
		u, err := s.Store.Q.GetUser(ctx, viewer(ctx))
		if err != nil {
			return nil, err
		}
		creation, flow, err := auth.BeginPasskey(ctx, s.Store.Q, rp, u)
		if err != nil {
			return nil, err
		}
		out := &passkeyOptionsOutput{}
		out.Body.Flow = flow
		out.Body.Options, err = json.Marshal(creation)
		return out, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "passkeyAddFinish", Method: http.MethodPost, Path: "/api/v1/auth/passkeys/finish", Tags: []string{"Auth"},
		Summary: "Keep the passkey the browser made", Security: sessionAuth, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *passkeyFinishInput) (*passkeyOutput, error) {
		rp, err := s.relyingParty(in.Origin)
		if err != nil {
			return nil, err
		}
		u, err := s.Store.Q.GetUser(ctx, viewer(ctx))
		if err != nil {
			return nil, err
		}
		id, err := auth.FinishPasskey(ctx, s.Store.Q, rp, u, in.Body.Flow, strings.TrimSpace(in.Body.Name), in.Body.Credential)
		if errors.Is(err, auth.ErrPasskey) {
			return nil, huma.Error422UnprocessableEntity("That passkey couldn't be added. Try again.")
		}
		if err != nil {
			return nil, err
		}
		return &passkeyOutput{Body: Passkey{ID: id, Name: firstNonEmpty(strings.TrimSpace(in.Body.Name), "Passkey"), CreatedAt: time.Now()}}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "passkeyDelete", Method: http.MethodDelete, Path: "/api/v1/auth/passkeys/{id}", Tags: []string{"Auth"},
		Summary: "Remove a passkey", Security: sessionAuth, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		if err := s.keepAWayIn(ctx, "passkey"); err != nil {
			return nil, err
		}
		n, err := s.Store.Q.DeleteCredential(ctx, db.DeleteCredentialParams{ID: in.ID, UserID: viewer(ctx), Kind: "passkey"})
		if err == nil && n == 0 {
			return nil, huma.Error404NotFound("No such passkey.")
		}
		return nil, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "passkeySignInBegin", Method: http.MethodPost, Path: "/api/v1/auth/passkey/begin", Tags: []string{"Auth"},
		Summary: "Start signing in with a passkey", Description: "Options for navigator.credentials.get(); any passkey for this address will do.",
	}, func(ctx context.Context, in *originInput) (*passkeyOptionsOutput, error) {
		rp, err := s.relyingParty(in.Origin)
		if err != nil {
			return nil, err
		}
		assertion, flow, err := auth.BeginPasskeySignIn(ctx, s.Store.Q, rp)
		if err != nil {
			return nil, err
		}
		out := &passkeyOptionsOutput{}
		out.Body.Flow = flow
		out.Body.Options, err = json.Marshal(assertion)
		return out, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "passkeySignInFinish", Method: http.MethodPost, Path: "/api/v1/auth/passkey/finish", Tags: []string{"Auth"},
		Summary: "Sign in with the passkey the browser signed with", Description: "Starts a session. A passkey is a second step in itself: no code follows.",
	}, func(ctx context.Context, in *passkeyFinishInput) (*loginOutput, error) {
		rp, err := s.relyingParty(in.Origin)
		if err != nil {
			return nil, err
		}
		u, err := auth.FinishPasskeySignIn(ctx, s.Store.Q, rp, in.Body.Flow, in.Body.Credential)
		if errors.Is(err, auth.ErrPasskey) {
			return nil, huma.Error401Unauthorized("That passkey didn't sign you in. It may have been removed, or be for another address.")
		}
		if err != nil {
			return nil, err
		}
		return s.signedIn(ctx, u)
	})

	// Single sign-on.
	huma.Register(s.api, huma.Operation{
		OperationID: "signOnStart", Method: http.MethodGet, Path: "/api/v1/auth/oidc/start", Tags: []string{"Auth"},
		Summary: "Sign in with single sign-on", Description: "Sends the browser to the provider; it comes back to /oidc/callback.",
	}, func(ctx context.Context, in *oidcStartInput) (*redirectOutput, error) {
		if !s.signOnOn() {
			return nil, huma.Error404NotFound("Single sign-on isn't set up on this server.")
		}
		to, token, err := s.OIDC.Start(ctx, s.Store.Q, s.signOnReturn(in.base), uuid.NullUUID{})
		if err != nil {
			return &redirectOutput{Status: http.StatusFound, Location: "/?signin=" + url.QueryEscape(err.Error())}, nil
		}
		return &redirectOutput{Status: http.StatusFound, Location: to, SetCookie: []http.Cookie{s.flowCookie(token)}}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "signOnLink", Method: http.MethodPost, Path: "/api/v1/auth/oidc/link", Tags: []string{"Auth"},
		Summary: "Link a single sign-on account to yours", Description: "Gives the provider's address to send the browser to.", Security: sessionAuth,
	}, func(ctx context.Context, in *oidcLinkInput) (*oidcLinkOutput, error) {
		if !s.signOnOn() {
			return nil, huma.Error404NotFound("Single sign-on isn't set up on this server.")
		}
		if err := s.confirm(ctx, in.Body.Password); err != nil {
			return nil, err
		}
		to, token, err := s.OIDC.Start(ctx, s.Store.Q, s.signOnReturn(in.base), uuid.NullUUID{UUID: viewer(ctx), Valid: true})
		if err != nil {
			return nil, huma.Error502BadGateway(err.Error())
		}
		out := &oidcLinkOutput{SetCookie: s.flowCookie(token)}
		out.Body.URL = to
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "signOnCallback", Method: http.MethodGet, Path: "/api/v1/auth/oidc/callback", Tags: []string{"Auth"},
		Summary: "Where the provider sends the browser back to", Description: "Signs in (or links) and goes on to the app.",
	}, func(ctx context.Context, in *oidcCallbackInput) (*redirectOutput, error) {
		back := func(msg string) *redirectOutput {
			return &redirectOutput{Status: http.StatusFound, Location: "/?signin=" + url.QueryEscape(msg), SetCookie: []http.Cookie{s.flowCookie("")}}
		}
		if !s.signOnOn() {
			return back("Single sign-on isn't set up on this server."), nil
		}
		if in.Error != "" || in.Code == "" {
			return back("The sign-in was cancelled."), nil
		}
		if in.Cookie == "" || in.Cookie != in.State { // only the browser that set off can come back
			return back(auth.ErrSignOn.Error()), nil
		}
		u, err := s.OIDC.Finish(ctx, s.Store.Q, in.State, in.Code)
		if err != nil {
			msg := auth.ErrSignOn.Error()
			if errors.Is(err, auth.ErrNoAccount) || errors.Is(err, auth.ErrTaken) {
				msg = err.Error()
			}
			return back(msg), nil
		}
		token, err := auth.StartSession(ctx, s.Store, u)
		if err != nil {
			return nil, err
		}
		return &redirectOutput{Status: http.StatusFound, Location: "/", SetCookie: []http.Cookie{s.flowCookie(""), s.cookie(ctx, token, auth.SessionTTL)}}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "signOnUnlink", Method: http.MethodDelete, Path: "/api/v1/auth/oidc/{id}", Tags: []string{"Auth"},
		Summary: "Unlink a single sign-on account", Security: sessionAuth, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		if err := s.keepAWayIn(ctx, "oidc"); err != nil {
			return nil, err
		}
		n, err := s.Store.Q.DeleteCredential(ctx, db.DeleteCredentialParams{ID: in.ID, UserID: viewer(ctx), Kind: "oidc"})
		if err == nil && n == 0 {
			return nil, huma.Error404NotFound("No such link.")
		}
		return nil, err
	})
}

// keepAWayIn refuses to remove someone's last way to sign in (one of kind, when they have nothing
// else).
func (s *server) keepAWayIn(ctx context.Context, kind string) error {
	kinds, err := s.Store.Q.CredentialKinds(ctx, viewer(ctx))
	if err != nil {
		return err
	}
	others := 0
	for _, k := range kinds {
		switch {
		case k.Kind == "totp":
		case k.Kind == kind:
			others += int(k.N) - 1
		default:
			others += int(k.N)
		}
	}
	if others == 0 {
		return huma.Error409Conflict("That's your only way to sign in. Add another first.")
	}
	return nil
}

// flowCookie holds a single sign-on trip's token in the browser that started it (empty: clear it).
func (s *server) flowCookie(token string) http.Cookie {
	c := http.Cookie{Name: oidcCookie, Value: token, Path: "/api/v1/auth/oidc", HttpOnly: true, Secure: s.SecureCookies,
		SameSite: http.SameSiteLaxMode, MaxAge: 600}
	if token == "" {
		c.MaxAge = -1
	}
	return c
}
