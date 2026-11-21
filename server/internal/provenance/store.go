package provenance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

// Save records what Parse found for an item: its generation (model, LoRAs and prompt in the
// catalogs), its source type, and which parser read it. With no generation, an earlier one is
// removed. needsFile marks the item for a reading of the file itself.
func Save(ctx context.Context, s *store.Store, asset, owner uuid.UUID, g *Generation, needsFile bool) error {
	return s.Tx(ctx, func(q *db.Queries) error {
		ai := ""
		if g == nil {
			if err := q.DeleteGeneration(ctx, asset); err != nil {
				return err
			}
		} else {
			ai = "ai_" + g.Kind
			if err := save(ctx, q, asset, owner, g); err != nil {
				return err
			}
		}
		return q.SetProvenance(ctx, db.SetProvenanceParams{ID: asset, Version: pgtype.Int2{Int16: Version, Valid: true},
			NeedsFile: needsFile, Ai: ai})
	})
}

func save(ctx context.Context, q *db.Queries, asset, owner uuid.UUID, g *Generation) error {
	var model, prompt uuid.NullUUID
	if strings.TrimSpace(g.Prompt) == "" {
		// The file shows it's AI but not what it was asked for: an importer may have said.
		if given, err := q.GivenPrompt(ctx, asset); err == nil {
			g.Prompt = given
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	if g.Model != "" {
		id, err := q.UpsertAIModel(ctx, db.UpsertAIModelParams{Kind: "model", Name: Normalize(g.Model), Display: g.Model})
		if err != nil {
			return err
		}
		model = uuid.NullUUID{UUID: id, Valid: true}
	}
	if p := strings.TrimSpace(g.Prompt); p != "" {
		id, err := q.UpsertPrompt(ctx, db.UpsertPromptParams{OwnerID: owner, Text: p, Hash: promptHash(p)})
		if err != nil {
			return err
		}
		prompt = uuid.NullUUID{UUID: id, Valid: true}
	}
	settings, err := json.Marshal(g.Settings)
	if err != nil || g.Settings == nil {
		settings = []byte("{}")
	}
	settings = bytes.ReplaceAll(settings, []byte(`\u0000`), nil) // jsonb can't hold NUL either
	err = q.SaveGeneration(ctx, db.SaveGenerationParams{
		AssetID: asset, Kind: g.Kind, Source: g.Source, Evidence: g.Evidence,
		Tool: txt(g.Tool), ToolVersion: txt(g.ToolVersion), ModelID: model, PromptID: prompt,
		Negative: txt(g.Negative), Seed: txt(g.Seed), Sampler: txt(g.Sampler), Scheduler: txt(g.Scheduler),
		Steps: i4(g.Steps), Cfg: pgtype.Float4{Float32: float32(g.CFG), Valid: g.CFG != 0},
		GenWidth: i4(g.Width), GenHeight: i4(g.Height), Settings: settings,
		Graph: txt(g.Graph), Workflow: txt(g.Workflow), NeedsFile: g.NeedsFile, ParserVersion: Version,
	})
	if err != nil {
		return err
	}
	if err := q.ClearGenerationLoras(ctx, asset); err != nil {
		return err
	}
	for _, l := range g.Loras {
		id, err := q.UpsertAIModel(ctx, db.UpsertAIModelParams{Kind: "lora", Name: l.Name, Display: l.Display})
		if err != nil {
			return err
		}
		w := pgtype.Float4{}
		if l.Weight != nil {
			w = pgtype.Float4{Float32: float32(*l.Weight), Valid: true}
		}
		if err := q.AddGenerationLora(ctx, db.AddGenerationLoraParams{AssetID: asset, LoraID: id, Weight: w}); err != nil {
			return err
		}
	}
	return nil
}

// Give keeps the prompt that came with an item's upload, and makes it the item's prompt when its
// own evidence shows it's AI and gives none (or gave the prompt an earlier upload did). An item
// not read yet gets it when it is (Save).
func Give(ctx context.Context, q *db.Queries, asset, owner uuid.UUID, text string) error {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\x00", "")) // Postgres text can't hold NUL
	if text == "" {
		return nil
	}
	was, err := q.GivenPrompt(ctx, asset)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err := q.GivePrompt(ctx, db.GivePromptParams{AssetID: asset, Text: text}); err != nil {
		return err
	}
	g, err := q.GenerationPrompt(ctx, asset)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // not AI, as far as its file says
	} else if err != nil {
		return err
	}
	if g.PromptID.Valid && (was == "" || !bytes.Equal(g.NormalizedHash, promptHash(was))) {
		return nil // the file's own prompt
	}
	id, err := q.UpsertPrompt(ctx, db.UpsertPromptParams{OwnerID: owner, Text: text, Hash: promptHash(text)})
	if err != nil {
		return err
	}
	return q.SetGenerationPrompt(ctx, db.SetGenerationPromptParams{AssetID: asset, PromptID: uuid.NullUUID{UUID: id, Valid: true}})
}

// Repeats says whether a description only repeats an item's prompt, or the start of it: a copy an
// importer cut short, or wrote with its line breaks spelled out ("\n").
func Repeats(description, prompt string) bool {
	flat := func(s string) string {
		s = strings.NewReplacer(`\n`, " ", `\"`, `"`).Replace(s)
		return strings.Join(strings.Fields(strings.ToLower(s)), " ")
	}
	d := flat(description)
	return d != "" && strings.HasPrefix(flat(prompt), d)
}

// promptHash identifies a prompt regardless of case and spacing, so the same prompt typed twice
// is one entry in the prompt library.
func promptHash(p string) []byte {
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(strings.ToLower(p)), " ")))
	return sum[:]
}

func txt(s string) pgtype.Text {
	s = strings.ReplaceAll(s, "\x00", "") // Postgres text can't hold NUL
	return pgtype.Text{String: s, Valid: s != ""}
}

func i4(n int) pgtype.Int4 { return pgtype.Int4{Int32: int32(n), Valid: n != 0} }
