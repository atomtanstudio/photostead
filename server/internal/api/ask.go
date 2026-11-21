package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/ask"
	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/search"
	"photostead.com/server/internal/store/db"
)

const (
	askLimit   = 2000  // items listed for a question answered by date
	askRanking = 20000 // items a description is ranked among
)

type askInput struct {
	Body struct {
		Q       string    `json:"q" maxLength:"300" doc:"The question, e.g. when did I last make videos of Ada?"`
		Prev    *ask.Plan `json:"prev,omitempty" doc:"The previous answer's plan, so a follow-up (only videos) refines it"`
		Exclude []string  `json:"exclude,omitempty" maxItems:"20" doc:"Chip keys to drop from the plan"`
	}
}

type askOutput struct {
	Body struct {
		Plan      ask.Plan        `json:"plan"`
		Chips     []ask.Chip      `json:"chips" doc:"The plan's filters, each removable"`
		Answer    string          `json:"answer"`
		Describe  string          `json:"describe" doc:"What was looked for, as a phrase"`
		Assets    []TimelineAsset `json:"assets"`
		Total     int             `json:"total"`
		Exact     bool            `json:"exact" doc:"False when ranked by likeness"`
		Order     string          `json:"order" enum:"date,relevance"`
		Truncated bool            `json:"truncated" doc:"More items match than were listed"`
		Notes     []string        `json:"notes"`
		Alt       *ask.Alt        `json:"alt,omitempty" doc:"When nothing matches: the filter to drop to find something"`
	}
}

func (s *server) registerAsk() {
	huma.Register(s.api, huma.Operation{
		OperationID: "ask", Method: http.MethodPost, Path: "/api/v1/ask", Tags: []string{"Search"},
		Summary: "Ask a question about the library",
		Description: "The question is planned into filters (dates, media type, favorites, people, characters, models and LoRAs, where it was made, " +
			"prompt words, text in the picture, file names, utility kinds); what's left describes what the items look like and is matched by meaning.",
		Security: sessionAuth,
	}, func(ctx context.Context, in *askInput) (*askOutput, error) {
		q := strings.TrimSpace(in.Body.Q)
		if q == "" && in.Body.Prev == nil {
			return nil, huma.Error422UnprocessableEntity("Ask something.")
		}
		u, err := s.Store.Q.GetUser(ctx, viewer(ctx))
		if err != nil {
			return nil, err
		}
		loc, err := time.LoadLocation(auth.EffectiveZone(u, s.ServerZone))
		if err != nil {
			loc = time.UTC
		}
		vocab, err := s.askVocabulary(ctx)
		if err != nil {
			return nil, err
		}
		var plan ask.Plan
		switch {
		case q == "":
			plan = *in.Body.Prev // only dropping filters
		case in.Body.Prev != nil:
			plan = ask.FollowUp(*in.Body.Prev, ask.Parse(q, time.Now().In(loc), vocab), q)
		default:
			plan = ask.Parse(q, time.Now().In(loc), vocab)
		}
		plan = plan.Without(in.Body.Exclude...)

		out := &askOutput{}
		out.Body.Notes = []string{}
		r, assets, truncated, notes, err := s.runPlan(ctx, plan)
		if err != nil {
			return nil, err
		}
		if r.Total == 0 {
			if r.Alt, err = s.relax(ctx, plan); err != nil {
				return nil, err
			}
		}
		out.Body.Plan, out.Body.Chips, out.Body.Describe = plan, plan.Chips(), plan.Describe()
		out.Body.Answer = ask.Answer(plan, r)
		out.Body.Assets, out.Body.Total, out.Body.Exact, out.Body.Order = assets, r.Total, r.Exact, r.Order
		out.Body.Truncated, out.Body.Alt = truncated, r.Alt
		out.Body.Notes = append(out.Body.Notes, notes...)
		out.Body.Assets, err = s.sift(ctx, out.Body.Assets)
		return out, err
	})
}

func (s *server) askVocabulary(ctx context.Context) (ask.Vocabulary, error) {
	rows, err := s.Store.Q.AskVocabulary(ctx, viewer(ctx))
	var v ask.Vocabulary
	for _, r := range rows {
		switch r.Kind {
		case "person":
			v.People = append(v.People, ask.Named{ID: r.ID, Name: r.Name})
		case "character":
			v.Characters = append(v.Characters, ask.Named{ID: r.ID, Name: r.Name})
		default:
			v.Models = append(v.Models, ask.Model{ID: r.ID, Kind: r.Kind, Name: r.Name})
		}
	}
	return v, err
}

// askParams turns a plan's filters into the query's parameters (its description aside).
func askParams(ctx context.Context, p ask.Plan, oldestFirst bool, lim int32) db.AskItemsParams {
	ap := db.AskItemsParams{Viewer: viewer(ctx), Type: p.Type, AiOnly: p.AIOnly, Favorite: p.Favorite, People: []uuid.UUID{}, Models: []uuid.UUID{},
		Source: p.Source, Prompt: p.Prompt, Ocr: p.OCR, Label: p.Label, OldestFirst: oldestFirst, Lim: lim}
	if p.Filename != "" {
		ap.Filename = "%" + likeEscape(p.Filename) + "%"
	}
	if t, err := time.Parse("2006-01-02", p.After); err == nil {
		ap.After = pgtype.Date{Time: t, Valid: true}
	}
	if t, err := time.Parse("2006-01-02", p.Before); err == nil {
		ap.Before = pgtype.Date{Time: t, Valid: true}
	}
	for _, r := range p.People {
		ap.People = append(ap.People, r.ID)
	}
	if p.Character != nil {
		ap.Character = p.Character.ID
	}
	if p.Model != "" {
		ap.Models = append(ap.Models, p.ModelIDs...)
		if len(ap.Models) == 0 {
			ap.Models = []uuid.UUID{uuid.Nil} // named, but in nothing here: matches nothing
		}
	}
	return ap
}

func askAssets(rows []db.AskItemsRow) []TimelineAsset {
	out := make([]TimelineAsset, len(rows))
	for i, r := range rows {
		out[i] = TimelineAsset{ID: r.ID, Type: r.Type, TakenAt: r.TakenAt, LocalDate: r.LocalDate.Time.Format("2006-01-02"),
			Width: r.Width.Int32, Height: r.Height.Int32, DurationMs: r.DurationMs.Int32, ThumbHash: r.Thumbhash, Render: r.RenderState,
			Favorite: r.Favorite, AI: aiKind(r.SourceType)}
		if r.LiveMotionID.Valid {
			id := r.LiveMotionID.UUID
			out[i].MotionID = &id
		}
	}
	return out
}

// runPlan answers a plan: by date when it's all filters; ranked by likeness when it describes
// what the items look like.
func (s *server) runPlan(ctx context.Context, p ask.Plan) (ask.Result, []TimelineAsset, bool, []string, error) {
	var notes []string
	r := ask.Result{Exact: true, Order: "date"}
	if p.Text != "" {
		var vec []float32
		var err error
		if s.Embedder != nil && s.Embedder.Available(ctx) {
			vec, err = s.Embedder.Text(ctx, p.Text)
		}
		if vec == nil || err != nil {
			notes = append(notes, "Search by meaning isn’t available right now, so “"+p.Text+"” was set aside.")
			p.Text = ""
		} else {
			r.Exact, r.Order = false, "relevance"
			var assets []TimelineAsset
			if !p.Structured() {
				if assets, err = s.nearest(ctx, search.Literal(vec), "", uuid.Nil); err != nil {
					return r, nil, false, nil, err
				}
			} else {
				rows, err := s.Store.Q.AskItems(ctx, askParams(ctx, p, false, askRanking))
				if err != nil {
					return r, nil, false, nil, err
				}
				byID := map[uuid.UUID]db.AskItemsRow{}
				ids := make([]uuid.UUID, len(rows))
				for i, row := range rows {
					ids[i], byID[row.ID] = row.ID, row
				}
				ranked, err := s.Store.Q.AskRank(ctx, db.AskRankParams{Model: search.Model, Ids: ids, Vec: search.Literal(vec), Lim: meaningLimit})
				if err != nil {
					return r, nil, false, nil, err
				}
				picked := make([]db.AskItemsRow, len(ranked))
				for i, id := range ranked {
					picked[i] = byID[id]
				}
				assets = askAssets(picked)
			}
			r.Total = len(assets)
			for _, a := range assets {
				if r.Newest == "" || a.LocalDate > r.Newest {
					r.Newest = a.LocalDate
				}
				if r.Oldest == "" || a.LocalDate < r.Oldest {
					r.Oldest = a.LocalDate
				}
			}
			return r, assets, false, notes, nil
		}
	}
	rows, err := s.Store.Q.AskItems(ctx, askParams(ctx, p, p.Intent == "first", askLimit))
	if err != nil {
		return r, nil, false, nil, err
	}
	if len(rows) == 0 {
		return r, []TimelineAsset{}, false, notes, nil
	}
	r.Total = int(rows[0].Total)
	truncated := r.Total > len(rows)
	first, last := rows[0].LocalDate.Time.Format("2006-01-02"), rows[len(rows)-1].LocalDate.Time.Format("2006-01-02")
	if p.Intent == "first" {
		r.Oldest = first
		if !truncated {
			r.Newest = last
		}
	} else {
		r.Newest = first
		if !truncated {
			r.Oldest = last
		}
	}
	return r, askAssets(rows), truncated, notes, nil
}

// relax finds the one filter whose removal brings back the most, so an empty answer can offer it.
func (s *server) relax(ctx context.Context, p ask.Plan) (*ask.Alt, error) {
	var best *ask.Alt
	for _, k := range p.Relaxable() {
		q := p.Without(k)
		if k != "text" {
			q.Text = "" // a description ranks rather than filters; count what the filters find
		}
		if !q.Structured() {
			continue
		}
		rows, err := s.Store.Q.AskItems(ctx, askParams(ctx, q, false, 1))
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 && (best == nil || int(rows[0].Total) > best.Count) {
			best = &ask.Alt{Remove: k, Count: int(rows[0].Total), Describe: q.DescribeN(int(rows[0].Total))}
		}
	}
	return best, nil
}
