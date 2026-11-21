package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"photostead.com/server/internal/store/db"
)

// Passkeys (WebAuthn): sign in with the phone, computer or security key that holds one, no password
// or code (the device already asked for a face, fingerprint or PIN). They belong to the address
// Photostead is reached at (its host name), and browsers offer them only over HTTPS (or localhost).

const flowTTL = 5 * time.Minute

var ErrPasskey = errors.New("that passkey couldn't be used here")

// PasskeyData is a passkey credential: what the library keeps of it.
type PasskeyData struct {
	Credential webauthn.Credential `json:"credential"`
}

// passkeyUser is a user as WebAuthn sees them: their id (the user handle a passkey carries), names
// and passkeys.
type passkeyUser struct {
	u     db.User
	creds []webauthn.Credential
	ids   []uuid.UUID // the credentials' rows, in the same order
}

func (p *passkeyUser) WebAuthnID() []byte                         { return p.u.ID[:] }
func (p *passkeyUser) WebAuthnName() string                       { return p.u.Email }
func (p *passkeyUser) WebAuthnDisplayName() string                { return firstOf(p.u.DisplayName, p.u.Email) }
func (p *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return p.creds }

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func loadPasskeyUser(ctx context.Context, q *db.Queries, u db.User) (*passkeyUser, error) {
	rows, err := q.Credentials(ctx, db.CredentialsParams{UserID: u.ID, Kind: "passkey"})
	if err != nil {
		return nil, err
	}
	p := &passkeyUser{u: u}
	for _, r := range rows {
		var d PasskeyData
		if json.Unmarshal(r.Data, &d) == nil {
			p.creds, p.ids = append(p.creds, d.Credential), append(p.ids, r.ID)
		}
	}
	return p, nil
}

// RelyingParty is WebAuthn for the address the browser is at (its Origin header): passkeys are
// made for, and only work at, that host name.
func RelyingParty(origin string) (*webauthn.WebAuthn, error) {
	o, err := url.Parse(origin)
	if err != nil || o.Hostname() == "" || (o.Scheme != "https" && o.Scheme != "http") {
		return nil, errors.New("passkeys need the address Photostead is reached at")
	}
	return webauthn.New(&webauthn.Config{RPID: o.Hostname(), RPDisplayName: "Photostead", RPOrigins: []string{o.Scheme + "://" + o.Host}})
}

// BeginPasskey starts making a passkey for a signed-in user: the options for the browser, and the
// flow token to finish with.
func BeginPasskey(ctx context.Context, q *db.Queries, rp *webauthn.WebAuthn, u db.User) (*protocol.CredentialCreation, string, error) {
	p, err := loadPasskeyUser(ctx, q, u)
	if err != nil {
		return nil, "", err
	}
	creation, session, err := rp.BeginRegistration(p,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired), // found without an email: sign in with nothing typed
		webauthn.WithExclusions(webauthn.Credentials(p.creds).CredentialDescriptors()))
	if err != nil {
		return nil, "", err
	}
	token, err := StartFlow(ctx, q, "passkey_add", uuid.NullUUID{UUID: u.ID, Valid: true}, session, flowTTL)
	return creation, token, err
}

// FinishPasskey keeps the passkey the browser made, under a name the user knows it by.
func FinishPasskey(ctx context.Context, q *db.Queries, rp *webauthn.WebAuthn, u db.User, token, name string, response []byte) (uuid.UUID, error) {
	session, err := takeSession(ctx, q, "passkey_add", token, &u.ID)
	if err != nil {
		return uuid.Nil, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(response)
	if err != nil {
		return uuid.Nil, ErrPasskey
	}
	p, err := loadPasskeyUser(ctx, q, u)
	if err != nil {
		return uuid.Nil, err
	}
	cred, err := rp.CreateCredential(p, session, parsed)
	if err != nil {
		return uuid.Nil, ErrPasskey
	}
	data, _ := json.Marshal(PasskeyData{Credential: *cred})
	row, err := q.AddCredential(ctx, db.AddCredentialParams{UserID: u.ID, Kind: "passkey", Name: firstOf(name, "Passkey"), Data: data})
	return row.ID, err
}

// BeginPasskeySignIn starts a sign-in with any passkey for this address (the browser offers the
// ones it has).
func BeginPasskeySignIn(ctx context.Context, q *db.Queries, rp *webauthn.WebAuthn) (*protocol.CredentialAssertion, string, error) {
	assertion, session, err := rp.BeginDiscoverableLogin()
	if err != nil {
		return nil, "", err
	}
	token, err := StartFlow(ctx, q, "passkey_sign_in", uuid.NullUUID{}, session, flowTTL)
	return assertion, token, err
}

// FinishPasskeySignIn checks the passkey the browser signed with and says whose it is.
func FinishPasskeySignIn(ctx context.Context, q *db.Queries, rp *webauthn.WebAuthn, token string, response []byte) (db.User, error) {
	session, err := takeSession(ctx, q, "passkey_sign_in", token, nil)
	if err != nil {
		return db.User{}, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(response)
	if err != nil {
		return db.User{}, ErrPasskey
	}
	var found *passkeyUser
	_, cred, err := rp.ValidatePasskeyLogin(func(_, handle []byte) (webauthn.User, error) {
		id, err := uuid.FromBytes(handle)
		if err != nil {
			return nil, ErrPasskey
		}
		u, err := q.GetUser(ctx, id)
		if err != nil {
			return nil, ErrPasskey
		}
		if found, err = loadPasskeyUser(ctx, q, u); err != nil {
			return nil, err
		}
		return found, nil
	}, session, parsed)
	if err != nil || found == nil {
		return db.User{}, ErrPasskey
	}
	for i, c := range found.creds { // keep its counter (a cloned key shows up as going backwards)
		if string(c.ID) == string(cred.ID) {
			data, _ := json.Marshal(PasskeyData{Credential: *cred})
			if err := q.UpdateCredential(ctx, db.UpdateCredentialParams{ID: found.ids[i], Data: data}); err != nil {
				return db.User{}, err
			}
		}
	}
	return found.u, nil
}

// takeSession reads (and ends) a passkey flow, for the user it was started for if any.
func takeSession(ctx context.Context, q *db.Queries, kind, token string, user *uuid.UUID) (webauthn.SessionData, error) {
	var session webauthn.SessionData
	f, err := q.TakeFlow(ctx, db.TakeFlowParams{TokenHash: TokenHash(token), Kind: kind})
	if err != nil {
		return session, ErrPasskey
	}
	_ = q.EndFlow(ctx, f.ID) // one try each
	if user != nil && (!f.UserID.Valid || f.UserID.UUID != *user) {
		return session, ErrPasskey
	}
	if err := json.Unmarshal(f.Data, &session); err != nil {
		return session, ErrPasskey
	}
	return session, nil
}
