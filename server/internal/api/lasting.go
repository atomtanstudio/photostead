package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/store/db"
)

// Pairing codes that last: for someone who can't come to the home network to pair while a
// 10-minute code is on screen, such as Apple's App Review (pairing with a demo server days after
// the app was sent in) or family far away. The admin makes one for an account, good for a number
// of days and devices; each device that pairs with it counts, and it stops when its days or
// devices run out, or when it's revoked. Revoking doesn't sign out the devices it paired: their
// sessions are the account's, ended from its list of where it's signed in. Only the code's hash
// is kept; the code is shown once, as it's made. It looks like a 10-minute code (two fours), so
// the phone app takes either, and wrong ones count toward the same limit (tryAllowed). A short note
// tells codes apart in the list ("App Review", "Mum's iPad").

const (
	lastingDays    = 14
	lastingDevices = 5
	lastingNote    = 60 // characters
)

// ErrGuestPairing: a lasting code pairs into a household member's account, never a guest's (guests
// come in by their personal link).
var ErrGuestPairing = errors.New("a guest's account can't have devices paired with it; choose a household member")

// LastingCode is a pairing code that lasts, as the admin sees it (the code itself only once, when
// it's made).
type LastingCode struct {
	ID         uuid.UUID `json:"id"`
	UserID     uuid.UUID `json:"userId" doc:"The account its devices pair into"`
	Name       string    `json:"name" doc:"That account's name"`
	Email      string    `json:"email"`
	Devices    int32     `json:"devices" doc:"Devices paired with it so far"`
	MaxDevices int32     `json:"maxDevices"`
	Note       string    `json:"note" doc:"What it's for, to tell it apart (empty: no note)"`
	CreatedAt  time.Time `json:"createdAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

type lastingInput struct {
	Body struct {
		UserID  *uuid.UUID `json:"userId,omitempty" doc:"The account its devices pair into (default: yours); a household member's, not a guest's"`
		Days    int32      `json:"days,omitempty" minimum:"1" maximum:"90" default:"14" doc:"How many days it works"`
		Devices int32      `json:"devices,omitempty" minimum:"1" maximum:"50" default:"5" doc:"How many devices may pair with it"`
		Note    string     `json:"note,omitempty" maxLength:"60" doc:"A short note to tell it apart in the list (\"App Review\", \"Mum's iPad\")"`
	}
}

type lastingOutput struct {
	Body struct {
		Code      string      `json:"code" doc:"Type it into the device, or scan the QR code. Shown this once"`
		ExpiresAt time.Time   `json:"expiresAt"`
		Link      string      `json:"link" doc:"photostead://pair?… with the code, the server's id and its addresses"`
		QR        string      `json:"qr" doc:"The link as a QR code (PNG data URL)"`
		Lasting   LastingCode `json:"lasting"`
	}
}

type lastingListOutput struct {
	Body struct {
		Codes []LastingCode `json:"codes"`
	}
}

// MakeLastingCode makes a pairing code that pairs up to devices devices into the account, for
// days days, with a short note (or none). It returns the code (shown once: only its hash is kept)
// and the code as listed.
func MakeLastingCode(ctx context.Context, q *db.Queries, userID uuid.UUID, days, devices int, note string) (string, LastingCode, error) {
	if days < 1 || days > 90 {
		return "", LastingCode{}, errors.New("a code lasts 1 to 90 days")
	}
	if devices < 1 || devices > 50 {
		return "", LastingCode{}, errors.New("a code pairs 1 to 50 devices")
	}
	note = strings.Join(strings.Fields(note), " ") // one line
	if utf8.RuneCountInString(note) > lastingNote {
		return "", LastingCode{}, fmt.Errorf("a note is at most %d characters", lastingNote)
	}
	u, err := q.GetUser(ctx, userID)
	if err != nil {
		return "", LastingCode{}, err
	}
	if u.Role == "guest" {
		return "", LastingCode{}, ErrGuestPairing
	}
	code, err := newPairCode()
	if err != nil {
		return "", LastingCode{}, err
	}
	row, err := q.CreatePairingCode(ctx, db.CreatePairingCodeParams{CodeHash: auth.TokenHash(normalCode(code)), UserID: u.ID,
		MaxDevices: int32(devices), Note: note, Days: int32(days)})
	if err != nil {
		return "", LastingCode{}, err
	}
	return code, LastingCode{ID: row.ID, UserID: u.ID, Name: u.DisplayName, Email: u.Email, MaxDevices: int32(devices), Note: note,
		CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt}, nil
}

// LastingCodes are the pairing codes that last, while they do, newest first.
func LastingCodes(ctx context.Context, q *db.Queries) ([]LastingCode, error) {
	rows, err := q.PairingCodes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]LastingCode, len(rows))
	for i, r := range rows {
		out[i] = LastingCode{ID: r.ID, UserID: r.UserID, Name: r.DisplayName, Email: r.Email, Devices: r.Devices, MaxDevices: r.MaxDevices,
			Note: r.Note, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt}
	}
	return out, nil
}

// PairingLink is a code's link as the command line can make it: with the public address and the
// home one, as only the running server knows its tailnet address.
func PairingLink(ctx context.Context, q *db.Queries, code, publicURL string) (string, error) {
	info, err := readServerInfo(ctx, q)
	if err != nil {
		return "", err
	}
	return pairLink(code, info.ID, Addresses{Public: publicURL, Home: readHome(ctx, q)}), nil
}

func (s *server) registerLastingCodes() {
	huma.Register(s.api, huma.Operation{
		OperationID: "lastingCodes", Method: http.MethodGet, Path: "/api/v1/devices/pairing/lasting", Tags: []string{"Devices"},
		Summary:     "Pairing codes that last, while they do",
		Description: "Whose account each pairs into, how many devices have paired with it and may, and until when. The codes themselves aren't kept. Admin only.",
		Security:    sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*lastingListOutput, error) {
		if err := s.admin(ctx); err != nil {
			return nil, err
		}
		codes, err := LastingCodes(ctx, s.Store.Q)
		if err != nil {
			return nil, err
		}
		out := &lastingListOutput{}
		out.Body.Codes = codes
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "makeLastingCode", Method: http.MethodPost, Path: "/api/v1/devices/pairing/lasting", Tags: []string{"Devices"},
		Summary: "A pairing code that lasts, for App Review or someone who can't come to your home network",
		Description: "Pairs up to a number of devices (default 5) into an account (default yours) for a number of days (default 14), " +
			"with a short note to tell it apart if you like. The code, its link and QR code are shown this once. Admin only.",
		Security: sessionAuth,
	}, func(ctx context.Context, in *lastingInput) (*lastingOutput, error) {
		if err := s.admin(ctx); err != nil {
			return nil, err
		}
		userID, days, devices := viewer(ctx), int(in.Body.Days), int(in.Body.Devices)
		if in.Body.UserID != nil {
			userID = *in.Body.UserID
		}
		if days == 0 {
			days = lastingDays
		}
		if devices == 0 {
			devices = lastingDevices
		}
		code, lc, err := MakeLastingCode(ctx, s.Store.Q, userID, days, devices, in.Body.Note)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil, huma.Error404NotFound("No such account.")
		case errors.Is(err, ErrGuestPairing):
			return nil, huma.Error422UnprocessableEntity("A guest can’t have devices paired with their account. Choose a household member.")
		case err != nil:
			return nil, err
		}
		info, err := s.serverInfo(ctx)
		if err != nil {
			return nil, err
		}
		out := &lastingOutput{}
		out.Body.Code, out.Body.ExpiresAt, out.Body.Lasting = code, lc.ExpiresAt, lc
		out.Body.Link = pairLink(code, info.ID, s.addresses(ctx))
		out.Body.QR, err = qrPNG(out.Body.Link)
		return out, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "revokeLastingCode", Method: http.MethodDelete, Path: "/api/v1/devices/pairing/lasting/{id}", Tags: []string{"Devices"},
		Summary:     "Stop a pairing code that lasts",
		Description: "No more devices pair with it. Those it paired stay signed in until they're signed out from their account's sessions. Admin only.",
		Security:    sessionAuth, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		if err := s.admin(ctx); err != nil {
			return nil, err
		}
		n, err := s.Store.Q.RevokePairingCode(ctx, in.ID)
		if err == nil && n == 0 {
			return nil, huma.Error404NotFound("No such code.")
		}
		return nil, err
	})
}
