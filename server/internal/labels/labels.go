// Package labels sorts items into utility collections: screenshots, documents, receipts, ID cards
// and QR codes. It reads three kinds of evidence, all already at hand: the file name, the item's
// search embedding compared with a short description of each kind (zero-shot, the same SigLIP 2
// model search uses), and the text read from it. AI-generated items and videos are never labeled.
//
// Corrections by hand (confirmed, rejected) are kept apart from what the rules decide, so changing
// the rules (Version) recomputes only the automatic labels.
package labels

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"photostead.com/server/internal/search"
	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

// Version of the rules and prompts below. Bump it to look at every item again.
const Version = 2

// The labels, as stored.
const (
	Screenshot = "screenshot"
	Document   = "document"
	Receipt    = "receipt"
	IDCard     = "id_card"
	QRCode     = "qr_code"
	other      = "other"
)

// All lists the labels in the order the app shows them.
var All = []string{Screenshot, Document, Receipt, IDCard, QRCode}

type prompt struct{ label, text string }

// Each item is compared with every prompt; the best match decides what it looks like. The "other"
// prompts give everyday pictures somewhere to go, so a portrait never wins as an ID card just
// because nothing else was on offer.
var prompts = []prompt{
	{Screenshot, "a screenshot of a phone screen"},
	{Screenshot, "a screenshot of a computer screen"},
	{Screenshot, "a screenshot of a website"},
	{Screenshot, "a screenshot of a social media post"},
	{Screenshot, "a screenshot of a chat conversation"},
	{Screenshot, "a screenshot of an app"},
	{Document, "a scanned document"},
	{Document, "a photo of a printed page of text"},
	{Document, "a photo of a letter"},
	{Document, "a page of a form with text fields"},
	{Document, "a photo of a contract"},
	{Receipt, "a photo of a receipt"},
	{Receipt, "a printed shopping receipt"},
	{Receipt, "a restaurant bill"},
	{Receipt, "an invoice"},
	{IDCard, "a photo of an ID card"},
	{IDCard, "a driver's license"},
	{IDCard, "a passport photo page"},
	{IDCard, "an identity card"},
	{QRCode, "a QR code"},
	{QRCode, "a photo of a QR code"},
	{other, "a photo of a person"},
	{other, "a portrait of a woman"},
	{other, "a portrait of a man"},
	{other, "a photo of people"},
	{other, "a landscape photo"},
	{other, "a photo of food"},
	{other, "a photo of an animal"},
	{other, "a digital illustration"},
	{other, "an anime drawing"},
	{other, "a meme with text"},
	{other, "a poster with text"},
	{other, "a book cover"},
	{other, "a product photo"},
	{other, "a photo of a building"},
	{other, "a photo of a car"},
	{other, "a logo"},
	{other, "a selfie"},
	{other, "a fashion photo"},
	{other, "a 3d render"},
	{other, "a painting"},
	{other, "a photo of a room"},
	{other, "a close-up photo"},
	{other, "a video game scene"},
	{other, "a comic page"},
	{other, "a chart or diagram"},
	{other, "a map"},
}

// How close the best match must be (cosine similarity) before it counts. SigLIP's similarities
// are small numbers. These were set against the owner's library (2026-10-17, 24k items with their
// text read), using text as the check, since real screens and pages nearly always have some:
// items labeled screenshots had text 99-100% of the time against 26% for the rest; between 0.10
// and 0.12 a screenshot-like item still had text 95% of the time, below 0.10 only 76%. ID-like
// items below 0.14 often had none (portraits resemble passport pages), so IDs need their words.
var minSim = map[string]float32{
	Screenshot: 0.12, // or 0.10 with some text
	Document:   0.10, // with text
	Receipt:    0.10,
	IDCard:     0.14, // or any best match, with words from an ID
	QRCode:     0.10,
}

const screenshotWithText = 0.10

var (
	screenshotName = regexp.MustCompile(`(?i)(screen ?shot|screenshot|bildschirmfoto|capture d.écran|captura de pantalla|スクリーンショット)`)
	receiptWords   = regexp.MustCompile(`(?i)\b(sub-?total|total|tax|change|cash|visa|mastercard|amex|debit|credit|receipt|qty|balance due|tip|gratuity|amount)\b`)
	idWords        = regexp.MustCompile(`(?i)\b(driver'?s? licen[cs]e|licen[cs]e (no|number)|passport|identification|identity card|id card|date of birth|dob|nationality|class [a-z]\b|organ donor)`)
)

// Item is what the rules look at.
type Item struct {
	Type       string    // image or video
	SourceType string    // ai_generated and ai_edited are never labeled
	Path       string    // the file's path in its library
	Text       string    // text read from it ('' when none)
	Sims       []float32 // similarity to each prompt, in order
}

// Label is one conclusion, with the evidence it rests on.
type Label struct {
	Name  string
	Basis string // metadata (its file name) or visual (how it looks, with its text)
	Score float32
}

// Classify decides an item's labels.
func Classify(it Item) []Label {
	if it.Type != "image" || it.SourceType == "ai_generated" || it.SourceType == "ai_edited" || len(it.Sims) != len(prompts) {
		return nil
	}
	var out []Label
	if screenshotName.MatchString(path.Base(it.Path)) {
		out = append(out, Label{Screenshot, "metadata", 1})
	}
	best := 0
	for i, s := range it.Sims {
		if s > it.Sims[best] {
			best = i
		}
	}
	look, sim := prompts[best].label, it.Sims[best]
	text := strings.TrimSpace(it.Text)
	add := func(name string) {
		for _, l := range out {
			if l.Name == name {
				return
			}
		}
		out = append(out, Label{name, "visual", sim})
	}
	switch look {
	case Screenshot:
		if sim >= minSim[Screenshot] || (sim >= screenshotWithText && len(text) >= 20) {
			add(Screenshot)
		}
	case Document:
		if sim >= minSim[Document] && len(text) >= 40 {
			add(Document)
		}
		if distinct(receiptWords, text) >= 2 {
			add(Receipt)
		}
	case Receipt:
		if sim >= minSim[Receipt] || distinct(receiptWords, text) >= 2 {
			add(Receipt)
		}
	case IDCard:
		if sim >= minSim[IDCard] || idWords.MatchString(text) {
			add(IDCard)
		}
	case QRCode:
		if sim >= minSim[QRCode] {
			add(QRCode)
		}
	}
	return out
}

// distinct counts the different words of a kind in a text.
func distinct(re *regexp.Regexp, text string) int {
	seen := map[string]bool{}
	for _, m := range re.FindAllString(text, -1) {
		seen[strings.ToLower(m)] = true
	}
	return len(seen)
}

// Stats says what a refresh did.
type Stats struct{ Checked, Labeled int }

// Refresh labels every item the current rules haven't looked at yet, in batches. It needs the
// text service (to embed the prompts) and does nothing until it's up.
func Refresh(ctx context.Context, s *store.Store, e *search.Embedder) (Stats, error) {
	var st Stats
	if !e.Available(ctx) {
		return st, nil
	}
	labels := make([]string, len(prompts))
	vectors := make([]string, len(prompts))
	for i, p := range prompts {
		v, err := e.Text(ctx, p.text)
		if err != nil {
			return st, fmt.Errorf("embed %q: %w", p.text, err)
		}
		labels[i], vectors[i] = p.label, search.Literal(v)
	}
	for {
		rows, err := s.Q.LabelCandidates(ctx, db.LabelCandidatesParams{Prompts: labels, Vectors: vectors, Model: search.Model, Version: Version, Lim: 2000})
		if err != nil || len(rows) == 0 {
			return st, err
		}
		ids := make([]uuid.UUID, len(rows))
		hadText := make([]bool, len(rows))
		var lIDs []uuid.UUID
		var lNames, lBases []string
		var lScores []float32
		for i, r := range rows {
			ids[i], hadText[i] = r.ID, r.HadText
			for _, l := range Classify(Item{Type: r.Type, SourceType: r.SourceType, Path: r.RelPath, Text: r.Ocr, Sims: r.Sims}) {
				lIDs, lNames, lBases, lScores = append(lIDs, r.ID), append(lNames, l.Name), append(lBases, l.Basis), append(lScores, l.Score)
			}
		}
		err = s.Tx(ctx, func(q *db.Queries) error {
			if err := q.ClearAutoLabels(ctx, ids); err != nil {
				return err
			}
			if len(lIDs) > 0 {
				if err := q.AddAutoLabels(ctx, db.AddAutoLabelsParams{Ids: lIDs, Labels: lNames, Bases: lBases, Scores: lScores}); err != nil {
					return err
				}
			}
			return q.MarkLabelsChecked(ctx, db.MarkLabelsCheckedParams{Ids: ids, Version: Version, HadText: hadText})
		})
		if err != nil {
			return st, err
		}
		st.Checked += len(rows)
		st.Labeled += len(lIDs)
	}
}
