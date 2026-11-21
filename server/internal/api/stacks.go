package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"photostead.com/server/internal/store/db"
)

// Stacks: RAW+JPEG pairs, AI batches, bursts and look-alikes shown as one item with the rest
// tucked under it, and the owner's say over them.

type Stack struct {
	ID      uuid.UUID       `json:"id"`
	Kind    string          `json:"kind" enum:"raw_jpeg,burst,near_duplicate,ai_batch,manual"`
	State   string          `json:"state" enum:"suggested,confirmed" doc:"suggested: a look-alike group waiting for a yes or no"`
	Cover   uuid.UUID       `json:"cover" doc:"The item shown for it"`
	Members []TimelineAsset `json:"members" doc:"In order"`
}

type stackOutput struct{ Body Stack }

type suggestedInput struct {
	Limit  int32 `query:"limit" minimum:"1" maximum:"200" default:"40"`
	Offset int32 `query:"offset" minimum:"0" default:"0"`
}

type suggestedOutput struct {
	Body struct {
		Total  int32   `json:"total"`
		Stacks []Stack `json:"stacks" doc:"Biggest first"`
	}
}

type stackItemsInput struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		AssetIDs []uuid.UUID `json:"assetIds" minItems:"1" maxItems:"500"`
	}
}

type stackIDsInput struct {
	Body struct {
		StackIDs []uuid.UUID `json:"stackIds" minItems:"1" maxItems:"100000"`
	}
}

type confirmAllOutput struct {
	Body struct {
		Changed  int64       `json:"changed" doc:"Groups stacked"`
		StackIDs []uuid.UUID `json:"stackIds" doc:"Which (to undo with /stacks/unconfirm)"`
	}
}

type coverInput struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		AssetID uuid.UUID `json:"assetId"`
	}
}

type newStackInput struct {
	Body struct {
		AssetIDs []uuid.UUID `json:"assetIds" minItems:"2" maxItems:"500"`
	}
}

// stack reads one stack the viewer can see, with its members.
func (s *server) stack(ctx context.Context, id uuid.UUID, kind, state string, cover uuid.UUID) (Stack, error) {
	rows, err := s.Store.Q.StackMembers(ctx, db.StackMembersParams{Viewer: viewer(ctx), StackID: id})
	if err != nil {
		return Stack{}, err
	}
	st := Stack{ID: id, Kind: kind, State: state, Cover: cover, Members: make([]TimelineAsset, len(rows))}
	for i, r := range rows {
		st.Members[i] = TimelineAsset{ID: r.ID, Type: r.Type, TakenAt: r.TakenAt, LocalDate: r.LocalDate.Time.Format("2006-01-02"),
			Width: r.Width.Int32, Height: r.Height.Int32, DurationMs: r.DurationMs.Int32, ThumbHash: r.Thumbhash, Render: r.RenderState, AI: aiKind(r.SourceType)}
	}
	members, err := s.markAdult(ctx, st.Members, false)
	if err != nil {
		return st, err
	}
	st.Members = members
	if err := s.markCopies(ctx, st.Members); err != nil {
		return st, err
	}
	return st, s.addMarks(ctx, st.Members)
}

// owned is one of the viewer's own stacks.
func (s *server) owned(ctx context.Context, id uuid.UUID) (db.StackOwnedRow, error) {
	st, err := s.Store.Q.StackOwned(ctx, db.StackOwnedParams{StackID: id, OwnerID: viewer(ctx)})
	if errors.Is(err, pgx.ErrNoRows) {
		return st, huma.Error404NotFound("No such stack.")
	}
	return st, err
}

func (s *server) stackReply(ctx context.Context, id uuid.UUID) (*stackOutput, error) {
	st, err := s.owned(ctx, id)
	if err != nil {
		return nil, err
	}
	out, err := s.stack(ctx, st.ID, st.Kind, st.State, st.CoverAssetID)
	return &stackOutput{Body: out}, err
}

func (s *server) registerStacks() {
	huma.Register(s.api, huma.Operation{
		OperationID: "assetStack", Method: http.MethodGet, Path: "/api/v1/assets/{id}/stack", Tags: []string{"Stacks"},
		Summary: "The stack an item is in, with the rest of it", Security: sessionAuth,
	}, func(ctx context.Context, in *idInput) (*stackOutput, error) {
		st, err := s.Store.Q.StackOf(ctx, db.StackOfParams{Viewer: viewer(ctx), AssetID: in.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("It isn't in a stack.")
		}
		if err != nil {
			return nil, err
		}
		out, err := s.stack(ctx, st.ID, st.Kind, st.State, st.CoverAssetID)
		return &stackOutput{Body: out}, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "suggestedStacks", Method: http.MethodGet, Path: "/api/v1/stacks/suggested", Tags: []string{"Stacks"},
		Summary: "Look-alikes: groups of near-duplicates waiting for a yes or no", Security: sessionAuth,
	}, func(ctx context.Context, in *suggestedInput) (*suggestedOutput, error) {
		total, err := s.Store.Q.SuggestedStackCount(ctx, viewer(ctx))
		if err != nil {
			return nil, err
		}
		rows, err := s.Store.Q.SuggestedStacks(ctx, db.SuggestedStacksParams{Viewer: viewer(ctx), Lim: in.Limit, Off: in.Offset})
		if err != nil {
			return nil, err
		}
		out := &suggestedOutput{}
		out.Body.Total, out.Body.Stacks = total, make([]Stack, 0, len(rows))
		for _, r := range rows {
			st, err := s.stack(ctx, r.ID, "near_duplicate", "suggested", r.CoverAssetID)
			if err != nil {
				return nil, err
			}
			out.Body.Stacks = append(out.Body.Stacks, st)
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "makeStack", Method: http.MethodPost, Path: "/api/v1/stacks", Tags: []string{"Stacks"},
		Summary: "Stack items by hand", Description: "Only your own items, none already in a stack; the earliest goes on top.",
		Security: sessionAuth, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *newStackInput) (*stackOutput, error) {
		ids, err := s.Store.Q.OwnedVisible(ctx, db.OwnedVisibleParams{Ids: in.Body.AssetIDs, OwnerID: viewer(ctx)})
		if err != nil {
			return nil, err
		}
		if len(ids) < 2 {
			return nil, huma.Error422UnprocessableEntity("Pick at least two of your own items.")
		}
		if in, err := s.Store.Q.StackedAlready(ctx, ids); err != nil {
			return nil, err
		} else if len(in) > 0 {
			return nil, huma.Error409Conflict("Some of them are in a stack already: unstack those first.")
		}
		var id uuid.UUID
		err = s.Store.Tx(ctx, func(q *db.Queries) error {
			var err error
			if id, err = q.NewStack(ctx, db.NewStackParams{OwnerID: viewer(ctx), Kind: "manual", Cover: ids[0], State: "confirmed"}); err != nil {
				return err
			}
			return q.AddStackMembers(ctx, db.AddStackMembersParams{StackID: id, AssetIds: ids})
		})
		if err != nil {
			return nil, err
		}
		return s.stackReply(ctx, id)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "stackCover", Method: http.MethodPost, Path: "/api/v1/stacks/{id}/cover", Tags: []string{"Stacks"},
		Summary: "Put another of a stack's items on top", Security: sessionAuth,
	}, func(ctx context.Context, in *coverInput) (*stackOutput, error) {
		n, err := s.Store.Q.SetStackCover(ctx, db.SetStackCoverParams{StackID: in.ID, OwnerID: viewer(ctx), AssetID: in.Body.AssetID})
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, huma.Error404NotFound("That isn't in this stack.")
		}
		return s.stackReply(ctx, in.ID)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "confirmStack", Method: http.MethodPost, Path: "/api/v1/stacks/{id}/confirm", Tags: []string{"Stacks"},
		Summary: "Yes, stack these look-alikes", Security: sessionAuth,
	}, func(ctx context.Context, in *idInput) (*stackOutput, error) {
		if n, err := s.Store.Q.ConfirmStack(ctx, db.ConfirmStackParams{StackID: in.ID, OwnerID: viewer(ctx)}); err != nil {
			return nil, err
		} else if n == 0 {
			return nil, huma.Error404NotFound("No such stack.")
		}
		return s.stackReply(ctx, in.ID)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "confirmAllStacks", Method: http.MethodPost, Path: "/api/v1/stacks/suggested/confirm", Tags: []string{"Stacks"},
		Summary:     "Yes to every look-alike group",
		Description: "Each group is stacked, the best on top. Nothing is trashed. Undo with /stacks/unconfirm and the ids this returns.",
		Security:    sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*confirmAllOutput, error) {
		ids, err := s.Store.Q.ConfirmAllSuggested(ctx, viewer(ctx))
		if err != nil {
			return nil, err
		}
		out := &confirmAllOutput{}
		out.Body.Changed, out.Body.StackIDs = int64(len(ids)), ids
		if out.Body.StackIDs == nil {
			out.Body.StackIDs = []uuid.UUID{}
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "unconfirmStacks", Method: http.MethodPost, Path: "/api/v1/stacks/unconfirm", Tags: []string{"Stacks"},
		Summary: "Put look-alike groups back to waiting for an answer", Security: sessionAuth,
	}, func(ctx context.Context, in *stackIDsInput) (*changedOutput, error) {
		n, err := s.Store.Q.UnconfirmStacks(ctx, db.UnconfirmStacksParams{OwnerID: viewer(ctx), Ids: in.Body.StackIDs})
		if err != nil {
			return nil, err
		}
		out := &changedOutput{}
		out.Body.Changed = n
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "removeFromStack", Method: http.MethodPost, Path: "/api/v1/stacks/{id}/remove", Tags: []string{"Stacks"},
		Summary:     "Take items out of a stack",
		Description: "They stay out: stacking never puts them back in one of this kind. A stack left with one item goes.",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *stackItemsInput) (*changedOutput, error) {
		st, err := s.owned(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		var n int64
		err = s.Store.Tx(ctx, func(q *db.Queries) error {
			if err := q.ExcludeFromStacks(ctx, db.ExcludeFromStacksParams{Kind: st.Kind, Ids: in.Body.AssetIDs, OwnerID: viewer(ctx)}); err != nil {
				return err
			}
			var err error
			if n, err = q.RemoveStackMembers(ctx, db.RemoveStackMembersParams{StackID: in.ID, OwnerID: viewer(ctx), Ids: in.Body.AssetIDs}); err != nil {
				return err
			}
			left, err := q.StackMemberIDs(ctx, in.ID)
			if err != nil {
				return err
			}
			if len(left) < 2 {
				return q.DeleteStack(ctx, db.DeleteStackParams{StackID: in.ID, OwnerID: viewer(ctx)})
			}
			for _, id := range in.Body.AssetIDs {
				if id == st.CoverAssetID { // the top went: the first of the rest goes on top
					_, err := q.SetStackCover(ctx, db.SetStackCoverParams{StackID: in.ID, OwnerID: viewer(ctx), AssetID: left[0]})
					return err
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		out := &changedOutput{}
		out.Body.Changed = n
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "unstack", Method: http.MethodDelete, Path: "/api/v1/stacks/{id}", Tags: []string{"Stacks"},
		Summary:     "Unstack (or, for suggested look-alikes, keep them apart)",
		Description: "Every item shows on its own again, and stacking never puts them back in one of this kind.",
		Security:    sessionAuth, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		st, err := s.owned(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return nil, s.Store.Tx(ctx, func(q *db.Queries) error {
			ids, err := q.StackMemberIDs(ctx, in.ID)
			if err != nil {
				return err
			}
			if err := q.ExcludeFromStacks(ctx, db.ExcludeFromStacksParams{Kind: st.Kind, Ids: ids, OwnerID: viewer(ctx)}); err != nil {
				return err
			}
			return q.DeleteStack(ctx, db.DeleteStackParams{StackID: in.ID, OwnerID: viewer(ctx)})
		})
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "keepTopTrashRest", Method: http.MethodPost, Path: "/api/v1/stacks/{id}/trash-rest", Tags: []string{"Stacks"},
		Summary:     "Keep the top item, move the rest to the trash",
		Description: "For look-alikes and duplicates. The stack goes; the trashed items can be restored for 30 days.",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *idInput) (*changedOutput, error) {
		st, err := s.owned(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		ids, err := s.Store.Q.StackMemberIDs(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		var rest []uuid.UUID
		for _, id := range ids {
			if id != st.CoverAssetID {
				rest = append(rest, id)
			}
		}
		var trashed []uuid.UUID
		err = s.Store.Tx(ctx, func(q *db.Queries) error {
			var err error
			if trashed, err = q.TrashAssets(ctx, db.TrashAssetsParams{Ids: rest, Owner: viewer(ctx)}); err != nil {
				return err
			}
			return q.DeleteStack(ctx, db.DeleteStackParams{StackID: in.ID, OwnerID: viewer(ctx)})
		})
		if err != nil {
			return nil, err
		}
		out := &changedOutput{}
		out.Body.Changed = int64(len(trashed))
		return out, nil
	})
}
