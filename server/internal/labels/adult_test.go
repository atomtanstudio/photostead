package labels

import "testing"

// The cases are real scores from a library, with what each picture showed.
func TestAdultLabel(t *testing.T) {
	for _, c := range []struct {
		p    [4]float32
		want string
		what string
	}{
		{[4]float32{0.9, 0.05, 0.03, 0.02}, "", "nothing"},
		{[4]float32{0.02, 0.97, 0.01, 0}, Suggestive, "a ski suit"},
		{[4]float32{0, 0.02, 0.98, 0}, Suggestive, "lingerie"},
		{[4]float32{0.1, 0.37, 0.53, 0}, Suggestive, "a bikini"},
		{[4]float32{0.01, 0.06, 0.885, 0.045}, Suggestive, "covered, topless"},
		{[4]float32{0, 0.04, 0.9, 0.06}, Adult, "topless"},
		{[4]float32{0.1, 0.1, 0.6, 0.2}, Adult, "nude from behind"},
		{[4]float32{0.05, 0.1, 0.36, 0.49}, Adult, "nude"},
		{[4]float32{0, 0, 0.02, 0.98}, Adult, "sex"},
		{[4]float32{0.4, 0, 0.06, 0.54}, "", "a close-up of an eye"},
		{[4]float32{0.2, 0.01, 0.06, 0.73}, "", "hands"},
		{[4]float32{0.49, 0, 0.08, 0.43}, "", "a film still"},
		{[4]float32{0.51, 0.25, 0.24, 0}, "", "0.49 above neutral"},
	} {
		if got, _ := AdultLabel(c.p); got != c.want {
			t.Errorf("%s: AdultLabel(%v) = %q, want %q", c.what, c.p, got, c.want)
		}
	}
}

func TestAdultOf(t *testing.T) {
	none := []float32{0.9, 0.1, 0, 0}
	bikini := []float32{0.1, 0.4, 0.5, 0}
	nude := []float32{0, 0, 0.3, 0.7}
	frames := func(fs ...[]float32) []float32 {
		var out []float32
		for _, f := range fs {
			out = append(out, f...)
		}
		return out
	}
	for _, c := range []struct {
		scores []float32
		want   string
		what   string
	}{
		{nude, Adult, "a photo"},
		{bikini, Suggestive, "a photo"},
		{frames(none, nude), Adult, "a short clip: either picture decides"},
		{frames(none, none, nude, none, none, none, none), "", "one odd frame"},
		{frames(none, none, bikini, none, none, none, none), "", "one frame in swimwear"},
		{frames(none, nude, none, nude, none, none, none), Adult, "two frames agree"},
		{frames(none, nude, bikini, none, none, none, none), Suggestive, "one nude, one suggestive"},
	} {
		if got, _ := AdultOf(c.scores); got != c.want {
			t.Errorf("%s: AdultOf = %q, want %q", c.what, got, c.want)
		}
	}
	// The score is the second strongest frame's: 0.8, 0.9 and 0.95 give 0.9.
	if _, s := AdultOf(frames(none, []float32{0, 0, 0.2, 0.6}, []float32{0, 0, 0.3, 0.6}, []float32{0, 0, 0.05, 0.9})); s < 0.899 || s > 0.901 {
		t.Errorf("score = %v, want 0.9", s)
	}
}

func TestNth(t *testing.T) {
	if got := nth([]float32{0.2, 0.9, 0.5, 0.7}, 2); got != 0.7 {
		t.Errorf("nth 2 = %v, want 0.7", got)
	}
	if got := nth([]float32{0.4}, 1); got != 0.4 {
		t.Errorf("nth 1 = %v", got)
	}
}
