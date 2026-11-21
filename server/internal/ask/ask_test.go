package ask

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	la, _ = time.LoadLocation("America/Los_Angeles")
	now   = time.Date(2026, 10, 17, 9, 30, 0, 0, la) // a Saturday
	ada   = Named{uuid.MustParse("00000000-0000-0000-0000-00000000000a"), "Ada Lovelace"}
	emma  = Named{uuid.MustParse("00000000-0000-0000-0000-00000000000e"), "Emma"}
	nova  = Named{uuid.MustParse("00000000-0000-0000-0000-00000000000c"), "Nova"}
	flux  = Model{uuid.MustParse("00000000-0000-0000-0000-0000000000f1"), "model", "flux1-dev-fp8"}
	lora  = Model{uuid.MustParse("00000000-0000-0000-0000-0000000000f2"), "lora", "nova_v3_flux"}
	vocab = Vocabulary{People: []Named{emma, {uuid.New(), "Ada"}}, Characters: []Named{ada, nova}, Models: []Model{flux, lora}}
)

func TestParse(t *testing.T) {
	for _, c := range []struct {
		q    string
		want Plan
	}{
		{"when did I last make videos of Emma?", Plan{Intent: "last", Type: "video", People: []Named{emma}}},
		{"how many ComfyUI renders of Nova from last summer", Plan{Intent: "count", Type: "image", Character: &nova, Source: "comfyui", AIOnly: true,
			After: "2026-06-01", Before: "2026-09-01", When: "summer 2026"}},
		{"beach photos in may 2024", Plan{Intent: "list", Type: "image", After: "2024-05-01", Before: "2024-06-01", When: "May 2024", Text: "beach"}},
		{"what may look like a cat", Plan{Intent: "list", Text: "may cat"}},
		{"screenshots with the text order confirmed from 2025", Plan{Intent: "list", Label: "screenshot", OCR: "order confirmed",
			After: "2025-01-01", Before: "2026-01-01", When: "2025"}},
		{`receipts that say "costco"`, Plan{Intent: "list", Label: "receipt", OCR: "costco"}},
		{"images made with flux1 models", Plan{Intent: "list", Type: "image", Model: "flux1", ModelIDs: []uuid.UUID{flux.ID}, AIOnly: true}},
		{"my favorite AI videos this year", Plan{Intent: "list", Type: "video", Favorite: true, AIOnly: true, After: "2026-01-01", Before: "2027-01-01", When: "2026"}},
		{"prompts about red dresses in october", Plan{Intent: "list", Prompt: "red dresses", AIOnly: true, After: "2026-10-01", Before: "2026-11-01", When: "October 2026"}},
		{"Ada in the rain", Plan{Intent: "list", People: []Named{vocab.People[1]}, Text: "rain"}}, // a person by default…
		{"Ada lora renders", Plan{Intent: "list", Type: "image", Character: &ada, AIOnly: true}},  // …a character when it's about AI work
		{"earliest QR codes", Plan{Intent: "first", Label: "qr_code"}},
		{"files named \"IMG_2041\"", Plan{Intent: "list", Filename: "img_2041"}},
		{"sunset", Plan{Intent: "list", Text: "sunset"}},
		{"photos from yesterday", Plan{Intent: "list", Type: "image", After: "2026-10-16", Before: "2026-10-17", When: "yesterday"}},
		{"videos from the last 3 weeks", Plan{Intent: "list", Type: "video", After: "2026-09-26", When: "the last 3 weeks"}},
		{"last month", Plan{Intent: "list", After: "2026-09-01", Before: "2026-10-01", When: "September 2026"}},
		{"between 2019 and 2021", Plan{Intent: "list", After: "2019-01-01", Before: "2022-01-01", When: "2019 to 2021"}},
		{"december photos", Plan{Intent: "list", Type: "image", After: "2025-12-01", Before: "2026-01-01", When: "December 2025"}}, // still to come: last year's
		{"made with sdxl", Plan{Intent: "list", Model: "sdxl", AIOnly: true}},
		{"anything with the flux lora", Plan{Intent: "list", Model: "flux", ModelIDs: []uuid.UUID{flux.ID, lora.ID}, AIOnly: true}},
	} {
		got := Parse(c.q, now, vocab)
		if !same(got, c.want) {
			t.Errorf("%q\n got %+v\nwant %+v", c.q, got, c.want)
		}
	}
}

func same(a, b Plan) bool {
	ref := func(r *Named) string {
		if r == nil {
			return ""
		}
		return r.ID.String()
	}
	ids := func(rs []Named) string {
		var s []string
		for _, r := range rs {
			s = append(s, r.ID.String())
		}
		return strings.Join(s, ",")
	}
	mids := func(xs []uuid.UUID) string {
		var s []string
		for _, x := range xs {
			s = append(s, x.String())
		}
		return strings.Join(s, ",")
	}
	return a.Intent == b.Intent && a.Type == b.Type && ids(a.People) == ids(b.People) && ref(a.Character) == ref(b.Character) &&
		a.Model == b.Model && mids(a.ModelIDs) == mids(b.ModelIDs) && a.Source == b.Source && a.Prompt == b.Prompt && a.OCR == b.OCR &&
		a.Filename == b.Filename && a.Label == b.Label && a.Favorite == b.Favorite && a.After == b.After && a.Before == b.Before &&
		a.When == b.When && a.AIOnly == b.AIOnly && a.Text == b.Text
}

func TestFollowUpAndWithout(t *testing.T) {
	first := Parse("videos of Emma from 2025", now, vocab)
	only := FollowUp(first, Parse("only favorites", now, vocab), "only favorites")
	if !only.Favorite || only.Type != "video" || len(only.People) != 1 || only.When != "2025" {
		t.Errorf("refined = %+v", only)
	}
	whole := FollowUp(only, Parse("when did I last make AI images", now, vocab), "when did I last make AI images")
	if whole.Favorite || len(whole.People) != 0 || whole.Intent != "last" {
		t.Errorf("a whole new question should start over: %+v", whole)
	}
	fresh := FollowUp(first, Parse("sunsets", now, vocab), "sunsets")
	if fresh.Type != "" || len(fresh.People) != 0 || fresh.Text != "sunsets" {
		t.Errorf("a new subject should start over: %+v", fresh)
	}
	dropped := only.Without("when", "people")
	if dropped.When != "" || dropped.After != "" || len(dropped.People) != 0 || !dropped.Favorite {
		t.Errorf("without = %+v", dropped)
	}
	ai := Parse("ComfyUI renders", now, vocab).Without("source")
	if !ai.AIOnly { // renders are AI work even without the source
		t.Errorf("AI-only should stay with renders: %+v", ai)
	}
}

func TestAnswer(t *testing.T) {
	p := Parse("how many videos of Emma", now, vocab)
	if a := Answer(p, Result{Total: 1234, Exact: true, Order: "date"}); a != "You have 1,234 videos with Emma." {
		t.Error(a)
	}
	p = Parse("when did I last make videos of Emma", now, vocab)
	if a := Answer(p, Result{Total: 3, Exact: true, Order: "date", Newest: "2026-10-05"}); a != "The most recent was on Monday 5 October 2026. There are 3 videos with Emma in all." {
		t.Error(a)
	}
	if a := Answer(Parse("how many videos from 2025", now, vocab), Result{Total: 1, Exact: true}); a != "You have 1 video from 2025." {
		t.Error(a)
	}
	p = Parse("receipts from 2019", now, vocab)
	if a := Answer(p, Result{Alt: &Alt{Remove: "when", Count: 1, Describe: p.Without("when").DescribeN(1)}}); a != "I couldn’t find any receipts from 2019, but there is 1 receipt." {
		t.Error(a)
	}
	alt := &Alt{Remove: "when", Count: 12, Describe: p.Without("when").Describe()}
	if a := Answer(p, Result{Alt: alt}); a != "I couldn’t find any receipts from 2019, but there are 12 receipts." {
		t.Error(a)
	}
	p = Parse("beach photos", now, vocab)
	if a := Answer(p, Result{Total: 300, Order: "relevance", Oldest: "2020-01-01", Newest: "2026-01-01"}); !strings.HasPrefix(a, "Here are the 300 best matches for photos that look like “beach”") {
		t.Error(a)
	}
	if c := Parse("favorite screenshots from 2025 that say hello", now, vocab).Chips(); len(c) != 4 || c[0].Key != "label" {
		t.Errorf("chips = %+v", c)
	}
}
