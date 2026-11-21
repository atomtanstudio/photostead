package labels

import (
	"reflect"
	"testing"
)

// sims makes similarities where the prompt matching text is best, at sim; every other prompt is far.
func sims(t *testing.T, text string, sim float32) []float32 {
	t.Helper()
	out := make([]float32, len(prompts))
	found := false
	for i, p := range prompts {
		out[i] = 0.02
		if p.text == text {
			out[i], found = sim, true
		}
	}
	if !found {
		t.Fatalf("no prompt %q", text)
	}
	return out
}

func names(ls []Label) []string {
	out := []string{}
	for _, l := range ls {
		out = append(out, l.Name+"/"+l.Basis)
	}
	return out
}

func TestClassify(t *testing.T) {
	for _, c := range []struct {
		name string
		it   Item
		want []string
	}{
		{"named screenshot that looks like a photo", Item{Type: "image", Path: "phone/Screenshot_20240101-101010.png", Sims: sims(t, "a photo of a person", 0.15)},
			[]string{"screenshot/metadata"}},
		{"looks like a screenshot", Item{Type: "image", Path: "x/IMG_1.png", Sims: sims(t, "a screenshot of a chat conversation", 0.13)},
			[]string{"screenshot/visual"}},
		{"too faint to be a screenshot", Item{Type: "image", Path: "x/1.jpg", Sims: sims(t, "a screenshot of an app", 0.09)}, []string{}},
		{"faint, but its text confirms it", Item{Type: "image", Text: "9:41 Messages Delivered", Sims: sims(t, "a screenshot of an app", 0.11)},
			[]string{"screenshot/visual"}},
		{"faint and no text", Item{Type: "image", Sims: sims(t, "a screenshot of an app", 0.11)}, []string{}},
		{"named and looks like one: one label", Item{Type: "image", Path: "Screen Shot 2020.png", Sims: sims(t, "a screenshot of a website", 0.15)},
			[]string{"screenshot/metadata"}},
		{"AI image of a screen", Item{Type: "image", SourceType: "ai_generated", Path: "Screenshot.png", Sims: sims(t, "a screenshot of an app", 0.2)}, []string{}},
		{"video", Item{Type: "video", Path: "Screen Recording.mp4", Sims: sims(t, "a screenshot of an app", 0.2)}, []string{}},
		{"document with text", Item{Type: "image", Text: "Dear Sir, thank you for your letter of the 5th regarding the lease.", Sims: sims(t, "a photo of a letter", 0.12)},
			[]string{"document/visual"}},
		{"document-like but no text", Item{Type: "image", Text: "", Sims: sims(t, "a scanned document", 0.13)}, []string{}},
		{"page with receipt words is a receipt", Item{Type: "image", Text: "Lorem Ipsum Cafe\nSUBTOTAL 12.00\nTAX 1.02\nTOTAL 13.02\nVISA ****1234",
			Sims: sims(t, "a scanned document", 0.12)}, []string{"document/visual", "receipt/visual"}},
		{"receipt by look", Item{Type: "image", Sims: sims(t, "a printed shopping receipt", 0.11)}, []string{"receipt/visual"}},
		{"faint receipt saved by its words", Item{Type: "image", Text: "Total 5.00 Cash 10.00 Change 5.00", Sims: sims(t, "a restaurant bill", 0.08)},
			[]string{"receipt/visual"}},
		{"portrait that resembles a passport page", Item{Type: "image", Sims: sims(t, "a passport photo page", 0.12)}, []string{}},
		{"ID card with its words", Item{Type: "image", Text: "DRIVER LICENSE\nDOB 01/02/1990\nCLASS C", Sims: sims(t, "a driver's license", 0.11)},
			[]string{"id_card/visual"}},
		{"ID card by a strong look", Item{Type: "image", Sims: sims(t, "an identity card", 0.15)}, []string{"id_card/visual"}},
		{"QR code", Item{Type: "image", Sims: sims(t, "a QR code", 0.12)}, []string{"qr_code/visual"}},
		{"everyday photo", Item{Type: "image", Text: "total", Sims: sims(t, "a photo of food", 0.16)}, []string{}},
	} {
		got := names(Classify(c.it))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPromptsCoverEveryLabel(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range prompts {
		seen[p.label] = true
	}
	for _, l := range All {
		if !seen[l] {
			t.Errorf("no prompt for %s", l)
		}
		if _, ok := minSim[l]; !ok {
			t.Errorf("no threshold for %s", l)
		}
	}
}
