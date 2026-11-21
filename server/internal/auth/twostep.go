package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image/png"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"photostead.com/server/internal/store/db"
)

// Two-step sign-in: after the password, a six-digit code from an authenticator app (TOTP:
// SHA-1, 30 seconds, as every app expects), or one of ten recovery codes, each good once.

const recoveryCodes = 10

// TOTPData is a user's authenticator-app credential.
type TOTPData struct {
	Secret    string   `json:"secret"`
	Confirmed bool     `json:"confirmed"`          // a code from the app was given back: it's on
	LastStep  int64    `json:"lastStep,omitempty"` // the newest 30-second step a code was used for (no code twice)
	Recovery  []string `json:"recovery,omitempty"` // SHA-256 of the recovery codes not used yet
}

var (
	ErrNoTwoStep = errors.New("two-step sign-in isn't on")
	ErrBadCode   = errors.New("that code isn't right, or it was used already")
)

// TwoStep reads a user's authenticator-app credential, if they have one.
func TwoStep(ctx context.Context, q *db.Queries, userID uuid.UUID) (id uuid.UUID, d TOTPData, ok bool, err error) {
	rows, err := q.Credentials(ctx, db.CredentialsParams{UserID: userID, Kind: "totp"})
	if err != nil || len(rows) == 0 {
		return uuid.Nil, d, false, err
	}
	err = json.Unmarshal(rows[0].Data, &d)
	return rows[0].ID, d, err == nil, err
}

// TwoStepOn says whether a user signs in with a code after their password.
func TwoStepOn(ctx context.Context, q *db.Queries, userID uuid.UUID) (bool, error) {
	_, d, ok, err := TwoStep(ctx, q, userID)
	return ok && d.Confirmed, err
}

// NewTwoStep makes a new secret for a user (replacing any not confirmed yet), for their app to
// scan: the otpauth:// address and a QR code of it (a PNG data URL).
func NewTwoStep(ctx context.Context, q *db.Queries, u db.User) (secret, uri, qr string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Photostead", AccountName: u.Email})
	if err != nil {
		return "", "", "", err
	}
	data, _ := json.Marshal(TOTPData{Secret: key.Secret()})
	if err := q.SetTOTP(ctx, db.SetTOTPParams{UserID: u.ID, Data: data}); err != nil {
		return "", "", "", err
	}
	img, err := key.Image(240, 240)
	if err != nil {
		return "", "", "", err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", "", "", err
	}
	return key.Secret(), key.URL(), "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// step is the 30-second time step a code is valid for, if it's one of now's (a step either side
// allows for a clock a little off).
func step(secret, code string, now time.Time) (int64, bool) {
	code = strings.TrimSpace(code)
	for _, d := range []int64{0, -1, 1} {
		t := now.Add(time.Duration(d) * 30 * time.Second)
		want, err := totp.GenerateCodeCustom(secret, t, totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return t.Unix() / 30, true
		}
	}
	return 0, false
}

// ConfirmTwoStep turns two-step sign-in on with a first code from the app, and returns the
// recovery codes (shown once; only their hashes are kept).
func ConfirmTwoStep(ctx context.Context, q *db.Queries, userID uuid.UUID, code string) ([]string, error) {
	id, d, ok, err := TwoStep(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	if !ok || d.Confirmed {
		return nil, ErrNoTwoStep
	}
	st, good := step(d.Secret, code, time.Now())
	if !good {
		return nil, ErrBadCode
	}
	codes, hashes, err := newRecovery()
	if err != nil {
		return nil, err
	}
	d.Confirmed, d.LastStep, d.Recovery = true, st, hashes
	data, _ := json.Marshal(d)
	return codes, q.UpdateCredential(ctx, db.UpdateCredentialParams{ID: id, Data: data})
}

// NewRecovery replaces a user's recovery codes.
func NewRecovery(ctx context.Context, q *db.Queries, userID uuid.UUID) ([]string, error) {
	id, d, ok, err := TwoStep(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	if !ok || !d.Confirmed {
		return nil, ErrNoTwoStep
	}
	codes, hashes, err := newRecovery()
	if err != nil {
		return nil, err
	}
	d.Recovery = hashes
	data, _ := json.Marshal(d)
	return codes, q.UpdateCredential(ctx, db.UpdateCredentialParams{ID: id, Data: data})
}

// CheckSecondStep takes a code from the app (once only) or a recovery code (spent).
func CheckSecondStep(ctx context.Context, q *db.Queries, userID uuid.UUID, code string) error {
	id, d, ok, err := TwoStep(ctx, q, userID)
	if err != nil {
		return err
	}
	if !ok || !d.Confirmed {
		return ErrNoTwoStep
	}
	if st, good := step(d.Secret, code, time.Now()); good && st > d.LastStep {
		d.LastStep = st
	} else if i := recoveryIndex(d.Recovery, code); i >= 0 {
		d.Recovery = append(d.Recovery[:i:i], d.Recovery[i+1:]...)
	} else {
		return ErrBadCode
	}
	data, _ := json.Marshal(d)
	return q.UpdateCredential(ctx, db.UpdateCredentialParams{ID: id, Data: data})
}

func newRecovery() (codes, hashes []string, err error) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789" // nothing that reads as another letter
	for range recoveryCodes {
		b := make([]byte, 10)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, err
		}
		for i := range b {
			b[i] = alphabet[int(b[i])%len(alphabet)]
		}
		c := string(b[:5]) + "-" + string(b[5:])
		codes = append(codes, c)
		hashes = append(hashes, recoveryHash(c))
	}
	return codes, hashes, nil
}

func recoveryHash(code string) string {
	c := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(code)), "-", "")
	h := sha256.Sum256([]byte(c))
	return hex.EncodeToString(h[:])
}

func recoveryIndex(hashes []string, code string) int {
	h := recoveryHash(code)
	for i, x := range hashes {
		if subtle.ConstantTimeCompare([]byte(x), []byte(h)) == 1 {
			return i
		}
	}
	return -1
}

// Flow tokens: a sign-in under way is found by a random token the browser holds; its hash is kept.

// StartFlow begins a sign-in step that waits for the browser (a code, a passkey, the sign-on
// provider), returning the token for it.
func StartFlow(ctx context.Context, q *db.Queries, kind string, userID uuid.NullUUID, data any, ttl time.Duration) (string, error) {
	token, hash, err := NewToken()
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	_, err = q.StartFlow(ctx, db.StartFlowParams{TokenHash: hash, Kind: kind, UserID: userID, Data: raw, Seconds: int32(ttl / time.Second)})
	return token, err
}
