package jobs

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/metadata"
	"photostead.com/server/internal/store/db"
)

// Redate re-dates items with no date inside their file from all their present copies (file
// times and dates in names, decision 1.7). With no ids it covers the whole index. It returns how
// many items changed.
func Redate(ctx context.Context, q *db.Queries, ids []uuid.UUID, serverZone *time.Location) (int64, error) {
	rows, err := q.DerivedDateCopies(ctx, db.DerivedDateCopiesParams{Everything: ids == nil, Ids: ids})
	if err != nil {
		return 0, err
	}
	zones := map[string]*time.Location{}
	zoneOf := func(name pgtype.Text) *time.Location {
		if !name.Valid || name.String == "" {
			return serverZone
		}
		if z, ok := zones[name.String]; ok {
			return z
		}
		z, err := time.LoadLocation(name.String)
		if err != nil {
			z = serverZone
		}
		zones[name.String] = z
		return z
	}
	now := time.Now()
	var changed int64
	flush := func(id uuid.UUID, zone *time.Location, copies []metadata.CopyTime) error {
		if len(copies) == 0 {
			return nil
		}
		d := metadata.DateFromCopies(copies, zone, now)
		n, err := q.SetDerivedDate(ctx, db.SetDerivedDateParams{ID: id, TakenAt: d.TakenAt,
			LocalDate: pgtype.Date{Time: d.LocalDate, Valid: true}, DateSource: d.Source, DateBasis: d.Basis})
		changed += n
		return err
	}
	var cur uuid.UUID
	var curZone *time.Location
	var copies []metadata.CopyTime
	for _, r := range rows {
		if r.AssetID != cur {
			if err := flush(cur, curZone, copies); err != nil {
				return changed, err
			}
			cur, curZone, copies = r.AssetID, zoneOf(r.TimeZone), copies[:0]
		}
		copies = append(copies, metadata.CopyTime{Rel: r.RelPath, Mtime: r.Mtime})
	}
	return changed, flush(cur, curZone, copies)
}
