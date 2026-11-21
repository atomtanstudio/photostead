package api

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/store/db"
	"photostead.com/server/internal/trash"
)

// The vault: a place for private items, locked with a PIN. Its items are left out of everything
// else (the timeline, collections, search, the trash, sharing); their owner sees them only in a
// signed-in session that unlocked it, and only for a while after its last use.

// vaultOpen is how long an unlocked vault stays open after it was last used.
const vaultOpen = 10 * time.Minute

func sessionOf(ctx context.Context) (session, bool) {
	s, ok := ctx.Value(sessionKey{}).(session)
	return s, ok
}

// vaultViewer is the viewer while their vault is unlocked in this session, and no one otherwise:
// the queries that read the vault are given this, so a locked vault reads as empty.
func vaultViewer(ctx context.Context) uuid.UUID {
	if s, ok := sessionOf(ctx); ok && time.Now().Before(s.vaultUntil) {
		return viewer(ctx)
	}
	return uuid.Nil
}

var errVaultLocked = huma.Error403Forbidden("The vault is locked. Unlock it with its PIN.")

func unlockedVault(ctx context.Context) (uuid.UUID, error) {
	if v := vaultViewer(ctx); v != uuid.Nil {
		return v, nil
	}
	return uuid.Nil, errVaultLocked
}

// cacheFor is how long a browser may keep an item's file: nothing kept from the vault.
func cacheFor(visibility, usual string) string {
	if visibility == "vault" {
		return "private, no-store"
	}
	return usual
}

// VaultState is what the vault looks like from this session.
type VaultState struct {
	HasPIN        bool       `json:"hasPin" doc:"A PIN has been set (until then the vault is empty and can't be opened)"`
	Unlocked      bool       `json:"unlocked"`
	UnlockedUntil *time.Time `json:"unlockedUntil,omitempty" doc:"It locks itself then, unless used again"`
	WaitUntil     *time.Time `json:"waitUntil,omitempty" doc:"After too many wrong PINs, no tries until then"`
	Items         *int32     `json:"items,omitempty" doc:"Items in the vault (only while unlocked)"`
	Deleted       *int32     `json:"deleted,omitempty" doc:"Items in the vault's trash (only while unlocked)"`
}

type vaultOutput struct{ Body VaultState }

type pinInput struct {
	Body struct {
		PIN string `json:"pin" pattern:"^[0-9]{4,12}$" doc:"4 to 12 digits"`
	}
}

type newPinInput struct {
	Body struct {
		PIN        string `json:"pin" pattern:"^[0-9]{4,12}$" doc:"The new PIN: 4 to 12 digits"`
		CurrentPIN string `json:"currentPin,omitempty" doc:"To change the PIN: the one it has now"`
		Password   string `json:"password,omitempty" doc:"Or, with the PIN forgotten, the account's password"`
	}
}

func (s *server) vaultState(ctx context.Context, sess session, hasSess bool) (VaultState, error) {
	u, err := s.Store.Q.VaultPin(ctx, viewer(ctx))
	if err != nil {
		return VaultState{}, err
	}
	st := VaultState{HasPIN: u.VaultPin.Valid}
	if u.VaultBlockedUntil != nil && time.Now().Before(*u.VaultBlockedUntil) {
		st.WaitUntil = u.VaultBlockedUntil
	}
	if hasSess && time.Now().Before(sess.vaultUntil) {
		st.Unlocked, st.UnlockedUntil = true, &sess.vaultUntil
		c, err := s.Store.Q.VaultCount(ctx, viewer(ctx))
		if err != nil {
			return VaultState{}, err
		}
		st.Items, st.Deleted = &c.Items, &c.Deleted
	}
	return st, nil
}

// tooSoon is the answer while wrong PINs make the vault wait.
func tooSoon(until time.Time) error {
	mins := int(math.Ceil(time.Until(until).Minutes()))
	return huma.NewError(http.StatusTooManyRequests, fmt.Sprintf("Too many wrong PINs. Try again in %d %s.", mins, map[bool]string{true: "minute", false: "minutes"}[mins == 1]))
}

// checkPIN says whether pin is the user's vault PIN, counting a wrong one (which, from the fifth in
// a row, makes the next try wait).
func (s *server) checkPIN(ctx context.Context, pin string) error {
	u, err := s.Store.Q.VaultPin(ctx, viewer(ctx))
	if err != nil {
		return err
	}
	if !u.VaultPin.Valid {
		return huma.Error409Conflict("The vault has no PIN yet.")
	}
	if u.VaultBlockedUntil != nil && time.Now().Before(*u.VaultBlockedUntil) {
		return tooSoon(*u.VaultBlockedUntil)
	}
	if ok, err := auth.CheckPassword(pin, u.VaultPin.String); err == nil && ok {
		return nil
	}
	return s.wrongPIN(ctx, "That isn’t the vault’s PIN.")
}

func (s *server) wrongPIN(ctx context.Context, msg string) error {
	until, err := s.Store.Q.VaultPinFailed(ctx, viewer(ctx))
	if err != nil {
		return err
	}
	if until != nil {
		return tooSoon(*until)
	}
	return huma.Error403Forbidden(msg)
}

func (s *server) unlock(ctx context.Context, sess session) (session, error) {
	if err := s.Store.Q.UnlockVault(ctx, db.UnlockVaultParams{UserID: viewer(ctx), SessionID: sess.id, Minutes: int32(vaultOpen / time.Minute)}); err != nil {
		return sess, err
	}
	sess.vaultUntil = time.Now().Add(vaultOpen)
	return sess, nil
}

func signedInSession(ctx context.Context) (session, error) {
	sess, ok := sessionOf(ctx)
	if !ok {
		return sess, huma.Error403Forbidden("The vault opens only in a signed-in session.")
	}
	return sess, nil
}

func (s *server) registerVault() {
	huma.Register(s.api, huma.Operation{
		OperationID: "vault", Method: http.MethodGet, Path: "/api/v1/vault", Tags: []string{"Vault"},
		Summary: "Whether the vault has a PIN and is unlocked here", Security: sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*vaultOutput, error) {
		sess, ok := sessionOf(ctx)
		st, err := s.vaultState(ctx, sess, ok)
		return &vaultOutput{Body: st}, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "setVaultPin", Method: http.MethodPost, Path: "/api/v1/vault/pin", Tags: []string{"Vault"},
		Summary:     "Set the vault's PIN, or change it",
		Description: "The first PIN needs nothing more. Changing it needs the current PIN, or the account's password if it's forgotten. The vault is then unlocked here and locked everywhere else.",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *newPinInput) (*vaultOutput, error) {
		sess, err := signedInSession(ctx)
		if err != nil {
			return nil, err
		}
		u, err := s.Store.Q.VaultPin(ctx, viewer(ctx))
		if err != nil {
			return nil, err
		}
		if u.VaultPin.Valid {
			switch {
			case u.VaultBlockedUntil != nil && time.Now().Before(*u.VaultBlockedUntil):
				return nil, tooSoon(*u.VaultBlockedUntil)
			case in.Body.CurrentPIN != "":
				if err := s.checkPIN(ctx, in.Body.CurrentPIN); err != nil {
					return nil, err
				}
			case in.Body.Password != "":
				if !auth.CheckUserPassword(ctx, s.Store.Q, viewer(ctx), in.Body.Password) {
					return nil, s.wrongPIN(ctx, "That isn’t your password.")
				}
			default:
				return nil, huma.Error403Forbidden("Give the current PIN, or your password if you've forgotten it.")
			}
		}
		hash, err := auth.HashPassword(in.Body.PIN)
		if err != nil {
			return nil, err
		}
		err = s.Store.Tx(ctx, func(q *db.Queries) error {
			if err := q.SetVaultPin(ctx, db.SetVaultPinParams{UserID: viewer(ctx), PinHash: pgtype.Text{String: hash, Valid: true}}); err != nil {
				return err
			}
			return q.LockVaultEverywhere(ctx, db.LockVaultEverywhereParams{UserID: viewer(ctx), SessionID: sess.id})
		})
		if err != nil {
			return nil, err
		}
		if sess, err = s.unlock(ctx, sess); err != nil {
			return nil, err
		}
		st, err := s.vaultState(ctx, sess, true)
		return &vaultOutput{Body: st}, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "unlockVault", Method: http.MethodPost, Path: "/api/v1/vault/unlock", Tags: []string{"Vault"},
		Summary:     "Unlock the vault in this session",
		Description: "It stays open for 10 minutes after it was last used. From the fifth wrong PIN in a row, each makes the next try wait (a minute, doubling, up to an hour).",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *pinInput) (*vaultOutput, error) {
		sess, err := signedInSession(ctx)
		if err != nil {
			return nil, err
		}
		if err := s.checkPIN(ctx, in.Body.PIN); err != nil {
			return nil, err
		}
		if sess, err = s.unlock(ctx, sess); err != nil {
			return nil, err
		}
		st, err := s.vaultState(ctx, sess, true)
		return &vaultOutput{Body: st}, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "lockVault", Method: http.MethodPost, Path: "/api/v1/vault/lock", Tags: []string{"Vault"},
		Summary: "Lock the vault in this session", Security: sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*vaultOutput, error) {
		sess, ok := sessionOf(ctx)
		if ok {
			if err := s.Store.Q.LockVault(ctx, sess.id); err != nil {
				return nil, err
			}
			sess.vaultUntil = time.Time{}
		}
		st, err := s.vaultState(ctx, sess, ok)
		return &vaultOutput{Body: st}, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "moveToVault", Method: http.MethodPost, Path: "/api/v1/vault/add", Tags: []string{"Vault"},
		Summary:     "Move items into the vault",
		Description: "With their Live Photo clips and a RAW+JPEG pair's RAW. They leave other stacks, and no one's cover on People comes from them. Albums keep them, out of sight, for when they come back out. Needs a PIN set, not the vault unlocked.",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *itemsInput) (*changedOutput, error) {
		u, err := s.Store.Q.VaultPin(ctx, viewer(ctx))
		if err != nil {
			return nil, err
		}
		if !u.VaultPin.Valid {
			return nil, huma.Error409Conflict("Set a PIN for the vault first.")
		}
		var moved []uuid.UUID
		err = s.Store.Tx(ctx, func(q *db.Queries) error {
			var err error
			if moved, err = q.MoveToVault(ctx, db.MoveToVaultParams{Ids: in.Body.AssetIDs, Owner: viewer(ctx)}); err != nil || len(moved) == 0 {
				return err
			}
			if err := leaveStacks(ctx, q, viewer(ctx), moved); err != nil {
				return err
			}
			if err := q.LeaveSpaces(ctx, moved); err != nil { // no longer shared, and not shared again by a rule
				return err
			}
			return q.SetCovers(ctx, viewer(ctx)) // nobody's face on People comes from the vault
		})
		if err != nil {
			return nil, err
		}
		out := &changedOutput{}
		out.Body.Changed = int64(len(moved))
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "moveOutOfVault", Method: http.MethodPost, Path: "/api/v1/vault/remove", Tags: []string{"Vault"},
		Summary: "Move items out of the vault, back to the timeline", Security: sessionAuth,
	}, func(ctx context.Context, in *itemsInput) (*changedOutput, error) {
		vv, err := unlockedVault(ctx)
		if err != nil {
			return nil, err
		}
		ids, err := s.Store.Q.MoveOutOfVault(ctx, db.MoveOutOfVaultParams{Ids: in.Body.AssetIDs, Owner: vv})
		if err != nil {
			return nil, err
		}
		out := &changedOutput{}
		out.Body.Changed = int64(len(ids))
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "emptyVaultTrash", Method: http.MethodPost, Path: "/api/v1/vault/trash/empty", Tags: []string{"Vault"},
		Summary:     "Delete items in the vault's trash for good",
		Description: "Like emptying the trash, for the vault's own: the ones given, or all of it.",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *emptyInput) (*emptyOutput, error) {
		vv, err := unlockedVault(ctx)
		if err != nil {
			return nil, err
		}
		st, err := trash.Empty(ctx, s.Store, s.Render, vv, in.Body.AssetIDs, true, slog.Default())
		if err != nil {
			return nil, err
		}
		out := &emptyOutput{}
		out.Body.Deleted, out.Body.Kept = st.Deleted, st.Kept
		return out, nil
	})
}

// leaveStacks takes items going into the vault out of their stacks (other than RAW+JPEG pairs,
// which went in whole): a stack left with one item goes, and one whose top left gets a new one.
func leaveStacks(ctx context.Context, q *db.Queries, owner uuid.UUID, ids []uuid.UUID) error {
	rows, err := q.StacksLeavingForVault(ctx, db.StacksLeavingForVaultParams{Owner: owner, Ids: ids})
	if err != nil {
		return err
	}
	leaving := map[uuid.UUID][]uuid.UUID{}
	cover := map[uuid.UUID]uuid.UUID{}
	for _, r := range rows {
		leaving[r.ID] = append(leaving[r.ID], r.AssetID)
		cover[r.ID] = r.CoverAssetID
	}
	for stack, gone := range leaving {
		if _, err := q.RemoveStackMembers(ctx, db.RemoveStackMembersParams{StackID: stack, OwnerID: owner, Ids: gone}); err != nil {
			return err
		}
		left, err := q.StackMemberIDs(ctx, stack)
		if err != nil {
			return err
		}
		if len(left) < 2 {
			if err := q.DeleteStack(ctx, db.DeleteStackParams{StackID: stack, OwnerID: owner}); err != nil {
				return err
			}
			continue
		}
		for _, id := range gone {
			if id == cover[stack] {
				if _, err := q.SetStackCover(ctx, db.SetStackCoverParams{StackID: stack, OwnerID: owner, AssetID: left[0]}); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}
