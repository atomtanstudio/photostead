package labels

import "slices"

// Adult content: a worker's model (Freepik's EVA-based NSFW classifier, MIT) rates each picture with
// four probabilities, which Freepik calls neutral, low, medium and high. Looked at on a real library
// of 90,000 photos, low is swimwear, tight clothes and cleavage; medium is lingerie, bikinis and
// revealing outfits, rarely nudity; high is nudity and sex. So an item is adult on high, and
// suggestive on low or medium. The labels are kept beside the utility labels, so corrections work
// the same way.
const (
	Adult      = "adult"      // nudity or sex
	Suggestive = "suggestive" // swimwear, lingerie, revealing clothes, sexy poses: neither nude nor sex
)

// AdultEngine names the model, how its input is prepared and what a worker sends back (each
// picture's probabilities); a new one scans everything again.
const AdultEngine = "freepik-eva02-nsfw-v2"

// AdultRule is the version of how probabilities become labels (AdultLabel, AdultOf). A new one
// relabels every item from the probabilities kept, without scanning anything again.
const AdultRule = 2

// AdultLabel decides one picture from its four probabilities (neutral, low, medium, high); score
// is how strongly. Adult needs the top grade: nearly certain on its own, or some of it beside a
// real share of medium, which is how nudity rates. (A close-up of skin, an eye or a hand can rate
// high with nothing in medium: torn between neutral and high, it's neither.) Suggestive is low and
// medium together, at even odds.
func AdultLabel(p [4]float32) (label string, score float32) {
	low, medium, high := p[1], p[2], p[3]
	if high >= 0.85 || (high >= 0.05 && medium >= 0.15 && medium+high >= 0.5) {
		return Adult, medium + high
	}
	if low+medium >= 0.15 && low+medium+high >= 0.5 {
		return Suggestive, low + medium + high
	}
	return "", 0
}

// AdultOf decides an item from all its pictures' probabilities, four for each: a photo by its one
// picture, a video by its poster and frames. In a video two pictures must agree (one, when only
// two were looked at), so a single odd frame of a music video doesn't decide the whole of it; the
// score is that of the weaker of the two.
func AdultOf(scores []float32) (label string, score float32) {
	n := len(scores) / 4
	need := 1
	if n > 2 {
		need = 2
	}
	var adult, either []float32
	for i := range n {
		l, s := AdultLabel([4]float32(scores[4*i : 4*i+4]))
		if l == Adult {
			adult = append(adult, s)
		}
		if l != "" {
			either = append(either, s)
		}
	}
	if len(adult) >= need {
		return Adult, nth(adult, need)
	}
	if len(either) >= need {
		return Suggestive, nth(either, need)
	}
	return "", 0
}

// nth is the n-th largest of xs (1 is the largest).
func nth(xs []float32, n int) float32 {
	s := slices.Clone(xs)
	slices.Sort(s)
	return s[len(s)-n]
}
