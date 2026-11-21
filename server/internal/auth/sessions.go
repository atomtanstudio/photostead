package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

const SessionTTL = 30 * 24 * time.Hour

var (
	ErrBadLogin     = errors.New("email or password is incorrect")
	ErrAlreadySetUp = errors.New("this server already has an account; sign in instead")
	errWeakPassword = errors.New("passwords need at least 12 characters")
)

type passwordData struct {
	Hash string `json:"hash"`
}

// NewToken returns a random session token and the hash that is stored (the token itself never is).
func NewToken() (token string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, TokenHash(token), nil
}

func TokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// Login checks a password and starts a session.
func Login(ctx context.Context, s *store.Store, email, password string) (db.User, string, error) {
	u, err := Password(ctx, s, email, password)
	if err != nil {
		return db.User{}, "", err
	}
	token, err := StartSession(ctx, s, u)
	if err != nil {
		return db.User{}, "", err
	}
	return u, token, nil
}

// Password checks an email and password, without starting a session (a second step may follow).
func Password(ctx context.Context, s *store.Store, email, password string) (db.User, error) {
	u, err := s.Q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		_, _ = HashPassword(password) // same cost either way, so timing doesn't reveal accounts
		return db.User{}, ErrBadLogin
	}
	if err != nil {
		return db.User{}, err
	}
	raw, err := s.Q.GetPassword(ctx, u.ID)
	if err != nil {
		return db.User{}, ErrBadLogin
	}
	var pd passwordData
	if err := json.Unmarshal(raw, &pd); err != nil {
		return db.User{}, err
	}
	if ok, err := CheckPassword(password, pd.Hash); err != nil || !ok {
		return db.User{}, ErrBadLogin
	}
	return u, nil
}

// CheckUserPassword reports whether pw is the user's password: to confirm who's there before a
// sensitive change, such as a new vault PIN when the old one is forgotten.
func CheckUserPassword(ctx context.Context, q *db.Queries, userID uuid.UUID, pw string) bool {
	raw, err := q.GetPassword(ctx, userID)
	if err != nil {
		return false
	}
	var pd passwordData
	if json.Unmarshal(raw, &pd) != nil {
		return false
	}
	ok, err := CheckPassword(pw, pd.Hash)
	return err == nil && ok
}

// SetPassword stores a new password hash for a user.
func SetPassword(ctx context.Context, q *db.Queries, u db.User, password string) error {
	if len(password) < 12 {
		return errWeakPassword
	}
	h, err := HashPassword(password)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(passwordData{Hash: h})
	return q.SetPassword(ctx, db.SetPasswordParams{UserID: u.ID, Data: data})
}

var nonUsername = regexp.MustCompile(`[^a-z0-9_-]+`)

// UsernameFrom derives a stable short name (used in folder paths) from an email address.
func UsernameFrom(email string) string {
	local, _, _ := strings.Cut(strings.ToLower(email), "@")
	u := strings.Trim(nonUsername.ReplaceAllString(local, "-"), "-_")
	if u == "" {
		u = "user"
	}
	if len(u) > 32 {
		u = u[:32]
	}
	return u
}

// Bootstrap creates the first admin from configuration (for scripted installs) when the server
// has no users yet and both settings are present. Otherwise the web app's first-run setup does it.
// The time zone is left unchosen so the app asks.
func Bootstrap(ctx context.Context, s *store.Store, email, password string) (created bool, err error) {
	if email == "" || password == "" {
		return false, nil
	}
	n, err := s.Q.CountUsers(ctx)
	if err != nil || n > 0 {
		return false, err
	}
	err = s.Tx(ctx, func(q *db.Queries) error {
		u, err := q.CreateUser(ctx, db.CreateUserParams{Email: email, Username: UsernameFrom(email), DisplayName: UsernameFrom(email), Role: "admin"})
		if err != nil {
			return err
		}
		return SetPassword(ctx, q, u, password)
	})
	return err == nil, err
}

// Setup creates the first account from the web app's first-run screen, with the time zone the
// person chose there. It fails with ErrAlreadySetUp once any account exists.
func Setup(ctx context.Context, s *store.Store, displayName, email, password, zone string) (db.User, error) {
	if _, err := Zone(zone); err != nil {
		return db.User{}, err
	}
	if len(password) < 12 {
		return db.User{}, errWeakPassword
	}
	var u db.User
	err := s.Tx(ctx, func(q *db.Queries) error {
		var err error
		u, err = q.CreateFirstUser(ctx, db.CreateFirstUserParams{
			Email: email, Username: UsernameFrom(email), DisplayName: strings.TrimSpace(displayName),
			TimeZone: pgtype.Text{String: zone, Valid: true},
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAlreadySetUp
		}
		if err != nil {
			return err
		}
		return SetPassword(ctx, q, u, password)
	})
	return u, err
}

// StartSession creates a session for a user and returns its token.
func StartSession(ctx context.Context, s *store.Store, u db.User) (string, error) {
	token, hash, err := NewToken()
	if err != nil {
		return "", err
	}
	_, err = s.Q.CreateSession(ctx, db.CreateSessionParams{TokenHash: hash, UserID: u.ID, ExpiresAt: time.Now().Add(SessionTTL)})
	return token, err
}
