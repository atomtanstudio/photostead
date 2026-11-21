package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"photostead.com/server/internal/labels"
	"photostead.com/server/internal/store/db"
)

// Utility collections (screenshots, documents, receipts, ID cards, QR codes), and adult and
// suggestive items, are listed with the timeline endpoints and collection={label}; these endpoints
// correct them by hand.

type labelInput struct {
	Label string `path:"label" enum:"screenshot,document,receipt,id_card,qr_code,adult,suggestive"`
	Body  struct {
		AssetIDs []uuid.UUID `json:"assetIds" minItems:"1" maxItems:"5000"`
	}
}

type labelOutput struct {
	Body struct {
		Changed int64 `json:"changed" doc:"Items whose label changed (only your own items can be changed)"`
	}
}

func (s *server) registerLabels() {
	for _, op := range []struct{ id, path, basis, summary string }{
		{"addLabel", "/api/v1/labels/{label}/add", "confirmed", "Say these items are screenshots, documents, receipts, ID cards, QR codes, adult or suggestive"},
		{"removeLabel", "/api/v1/labels/{label}/remove", "rejected", "Say these items aren't (they stay out when items are sorted again)"},
	} {
		basis := op.basis
		huma.Register(s.api, huma.Operation{
			OperationID: op.id, Method: http.MethodPost, Path: op.path, Tags: []string{"Collections"}, Summary: op.summary, Security: sessionAuth,
		}, func(ctx context.Context, in *labelInput) (*labelOutput, error) {
			n, err := s.Store.Q.CorrectLabel(ctx, db.CorrectLabelParams{Label: in.Label, Basis: basis, Ids: in.Body.AssetIDs, Owner: viewer(ctx)})
			if err != nil {
				return nil, err
			}
			if in.Label == labels.Adult && basis == "confirmed" { // adult, so not merely suggestive
				if err := s.Store.Q.ClearSuggestive(ctx, db.ClearSuggestiveParams{Ids: in.Body.AssetIDs, Owner: viewer(ctx)}); err != nil {
					return nil, err
				}
			}
			out := &labelOutput{}
			out.Body.Changed = n
			return out, nil
		})
	}
}
