package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

// Zone loads a named IANA time zone such as "America/Los_Angeles". A zone has to be named
// explicitly; "Local" and "" are refused so nothing silently follows the server's clock.
func Zone(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "Local" {
		return nil, errors.New("choose a time zone, for example America/Los_Angeles")
	}
	z, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("%q isn't a time zone Photostead knows", name)
	}
	return z, nil
}

// EffectiveZone is the user's chosen zone, or the server's zone until they choose one.
func EffectiveZone(u db.User, serverZone string) string {
	if u.TimeZone.Valid && u.TimeZone.String != "" {
		return u.TimeZone.String
	}
	return serverZone
}

type ZoneChange struct {
	CameraClocksMoved int64 // items whose camera time now reads in the new zone
	DaysChanged       int64 // items that moved to a different day
}

// SetTimeZone records a user's time zone and re-dates their library to match: camera clocks
// keep their wall time in the new zone, and moments (video clocks, file dates) move to the day
// they fall on there. Times recorded with their own offset stay as they are.
func SetTimeZone(ctx context.Context, s *store.Store, u db.User, name, serverZone string) (ZoneChange, error) {
	if _, err := Zone(name); err != nil {
		return ZoneChange{}, err
	}
	old := EffectiveZone(u, serverZone)
	var ch ZoneChange
	err := s.Tx(ctx, func(q *db.Queries) error {
		var err error
		if old != name {
			if ch.CameraClocksMoved, err = q.ShiftCameraClockAssets(ctx, db.ShiftCameraClockAssetsParams{OldZone: old, NewZone: name, OwnerID: u.ID}); err != nil {
				return err
			}
		}
		if ch.DaysChanged, err = q.RedateInstantAssets(ctx, db.RedateInstantAssetsParams{NewZone: name, OwnerID: u.ID}); err != nil {
			return err
		}
		return q.SetUserTimeZone(ctx, db.SetUserTimeZoneParams{TimeZone: pgtype.Text{String: name, Valid: true}, ID: u.ID})
	})
	return ch, err
}
