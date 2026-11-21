package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"photostead.com/server/internal/search"
	"photostead.com/server/internal/store/db"
)

const searchLimit = 2000

type searchInput struct {
	Q       string `query:"q" maxLength:"200" doc:"What to look for"`
	Type    string `query:"type" enum:"image,video"`
	Mode    string `query:"mode" enum:"auto,meaning,names" default:"auto" doc:"meaning: what's in the picture; names: words in file and folder names; auto: meaning when it's ready"`
	Similar string `query:"similar" doc:"Instead of q: items that look like this one (its id)"`
}

type MeaningStatus struct {
	Available   bool  `json:"available" doc:"Search by meaning can run now"`
	Embedded    int32 `json:"embedded" doc:"Items ready for it"`
	Waiting     int32 `json:"waiting" doc:"Items still to be embedded"`
	TextRead    int32 `json:"textRead" doc:"Items whose text has been read (OCR)"`
	TextWaiting int32 `json:"textWaiting" doc:"Items still to have their text read"`
}

// Found says how many items each way of searching found, before they were merged.
type Found struct {
	Meaning int `json:"meaning"`
	Prompts int `json:"prompts"`
	Text    int `json:"text" doc:"Items with the words in the text read from them"`
	Details int `json:"details" doc:"Items with the words in their description or keywords"`
	Names   int `json:"names"`
}

type searchOutput struct {
	Body struct {
		Query     string          `json:"query"`
		Found     *Found          `json:"found,omitempty" doc:"For a search by meaning: what each way of searching found"`
		Mode      string          `json:"mode" enum:"meaning,names,similar" doc:"How the results were found"`
		Order     string          `json:"order" enum:"relevance,date" doc:"Best match first, or newest first"`
		Assets    []TimelineAsset `json:"assets"`
		Truncated bool            `json:"truncated" doc:"True when more items match than were returned"`
		Meaning   MeaningStatus   `json:"meaning"`
	}
}

func (s *server) meaningStatus(ctx context.Context) MeaningStatus {
	st := MeaningStatus{}
	if p, err := s.Store.Q.EmbeddingProgress(ctx, search.Model); err == nil {
		st.Embedded, st.Waiting = p.Embedded, p.Waiting
	}
	if p, err := s.Store.Q.OCRProgress(ctx, search.OCREngine); err == nil {
		st.TextRead, st.TextWaiting = p.Done, p.Waiting
	}
	st.Available = st.Embedded > 0 && s.Embedder.Available(ctx)
	return st
}

func (s *server) registerSearch() {
	huma.Register(s.api, huma.Operation{
		OperationID: "search", Method: http.MethodGet, Path: "/api/v1/search", Tags: []string{"Search"},
		Summary: "Find items by what's in them, by file and folder names, or by likeness to an item",
		Description: "By meaning (SigLIP 2), the best matches come first; by names, every word must appear in the item's file path " +
			"(any copy), newest first. similar={id} finds items that look like that one.",
		Security: sessionAuth,
	}, func(ctx context.Context, in *searchInput) (*searchOutput, error) {
		out := &searchOutput{}
		out.Body.Query = in.Q
		out.Body.Meaning = s.meaningStatus(ctx)
		out.Body.Assets = []TimelineAsset{}
		if in.Similar != "" {
			id, err := uuid.Parse(in.Similar)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("similar must be an item's id")
			}
			out.Body.Mode, out.Body.Order = "similar", "relevance"
			vec, err := s.itemVector(ctx, id)
			if err != nil || vec == "" {
				return out, err // not embedded yet: nothing to compare with
			}
			if out.Body.Assets, err = s.nearest(ctx, vec, in.Type, id); err != nil {
				return nil, err
			}
			out.Body.Assets, err = s.sift(ctx, out.Body.Assets)
			return out, err
		}
		if strings.TrimSpace(in.Q) == "" {
			return nil, huma.Error422UnprocessableEntity("Type something to search for.")
		}
		var patterns []string
		for _, w := range strings.Fields(in.Q) {
			patterns = append(patterns, "%"+likeEscape(w)+"%")
		}
		if s.Embedder != nil && (in.Mode == "meaning" || (in.Mode == "auto" && out.Body.Meaning.Available)) {
			v, err := s.Embedder.Text(ctx, in.Q)
			if err == nil {
				// Hybrid: what's in the picture, what its prompt asked for, the text in it, its description and
				// keywords, what its file is called.
				meaning, err := s.nearest(ctx, search.Literal(v), in.Type, uuid.Nil)
				if err != nil {
					return nil, err
				}
				prompts, err := s.Store.Q.SearchByPrompt(ctx, db.SearchByPromptParams{Viewer: viewer(ctx), Type: in.Type, Q: in.Q, Lim: meaningLimit})
				if err != nil {
					return nil, err
				}
				text, err := s.Store.Q.SearchByText(ctx, db.SearchByTextParams{Viewer: viewer(ctx), Type: in.Type, Q: in.Q, Lim: meaningLimit})
				if err != nil {
					return nil, err
				}
				details, err := s.Store.Q.SearchByDetails(ctx, db.SearchByDetailsParams{Viewer: viewer(ctx), Type: in.Type, Q: in.Q, Lim: meaningLimit})
				if err != nil {
					return nil, err
				}
				names, err := s.Store.Q.SearchByName(ctx, db.SearchByNameParams{Viewer: viewer(ctx), Type: in.Type, Patterns: patterns, Lim: meaningLimit})
				if err != nil {
					return nil, err
				}
				byPrompt := make([]db.SearchByNameRow, len(prompts))
				for i, r := range prompts {
					byPrompt[i] = db.SearchByNameRow(r)
				}
				byText := make([]db.SearchByNameRow, len(text))
				for i, r := range text {
					byText[i] = db.SearchByNameRow(r)
				}
				byDetails := make([]db.SearchByNameRow, len(details))
				for i, r := range details {
					byDetails[i] = db.SearchByNameRow(r)
				}
				out.Body.Mode, out.Body.Order = "meaning", "relevance"
				out.Body.Found = &Found{Meaning: len(meaning), Prompts: len(prompts), Text: len(text), Details: len(details), Names: len(names)}
				out.Body.Assets = fuse(meaningLimit, meaning, listed(byPrompt), listed(byText), listed(byDetails), listed(names))
				out.Body.Assets, err = s.sift(ctx, out.Body.Assets)
				return out, err
			}
			if in.Mode == "meaning" {
				return nil, huma.Error503ServiceUnavailable("Search by meaning isn't available right now.")
			}
		}
		if in.Mode == "meaning" {
			return nil, huma.Error503ServiceUnavailable("Search by meaning isn't set up on this server.")
		}
		out.Body.Mode, out.Body.Order = "names", "date"
		rows, err := s.Store.Q.SearchByName(ctx, db.SearchByNameParams{Viewer: viewer(ctx), Type: in.Type, Patterns: patterns, Lim: searchLimit + 1})
		if err != nil {
			return nil, err
		}
		if len(rows) > searchLimit {
			rows, out.Body.Truncated = rows[:searchLimit], true
		}
		out.Body.Assets = listed(rows)
		out.Body.Assets, err = s.sift(ctx, out.Body.Assets)
		return out, err
	})
}

// likeEscape makes a word literal inside ILIKE (where % and _ are wildcards).
func likeEscape(w string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(w)
}
