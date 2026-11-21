package faces

import (
	"math"
	"math/rand"
	"sort"
	"testing"
)

func unitNear(r *rand.Rand, center []float32, noise float64) []float32 {
	v := make([]float32, len(center))
	var n float64
	for i := range v {
		v[i] = center[i] + float32(r.NormFloat64()*noise)
		n += float64(v[i]) * float64(v[i])
	}
	for i := range v {
		v[i] /= float32(math.Sqrt(n))
	}
	return v
}

func randomUnit(r *rand.Rand, dims int) []float32 {
	return unitNear(r, make([]float32, dims), 1)
}

// partition makes a comparable form of a clustering: sorted groups of sorted members.
func partition(groups [][]int) [][]int {
	out := [][]int{}
	for _, g := range groups {
		c := append([]int(nil), g...)
		sort.Ints(c)
		out = append(out, c)
	}
	sort.Slice(out, func(a, b int) bool { return out[a][0] < out[b][0] })
	return out
}

// naiveAverageLinkage is UPGMA the textbook way: repeatedly merge the closest pair while it's
// within t.
func naiveAverageLinkage(faces []Face, idx []int, t float32) [][]int {
	groups := [][]int{}
	for _, i := range idx {
		groups = append(groups, []int{i})
	}
	avg := func(a, b []int) float32 {
		var s float32
		for _, x := range a {
			for _, y := range b {
				s += 1 - dot(faces[x].Vec, faces[y].Vec)
			}
		}
		return s / float32(len(a)*len(b))
	}
	for {
		ba, bb, bd := -1, -1, float32(math.Inf(1))
		for a := range groups {
			for b := a + 1; b < len(groups); b++ {
				if d := avg(groups[a], groups[b]); d < bd {
					ba, bb, bd = a, b, d
				}
			}
		}
		if ba < 0 || bd > t {
			return groups
		}
		groups[ba] = append(groups[ba], groups[bb]...)
		groups = append(groups[:bb], groups[bb+1:]...)
	}
}

func TestAverageLinkageMatchesTheTextbook(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for trial := 0; trial < 30; trial++ {
		var faces []Face
		centers := 1 + r.Intn(5)
		for c := 0; c < centers; c++ {
			center := randomUnit(r, 16)
			for j := 0; j < 2+r.Intn(8); j++ {
				faces = append(faces, Face{Vec: unitNear(r, center, 0.04+0.1*r.Float64()), Label: -1})
			}
		}
		idx := make([]int, len(faces))
		for i := range idx {
			idx[i] = i
		}
		thr := float32(0.2 + 0.5*r.Float64())
		got, want := partition(averageLinkage(faces, idx, thr)), partition(naiveAverageLinkage(faces, idx, thr))
		if len(got) != len(want) {
			t.Fatalf("trial %d: %d clusters, want %d", trial, len(got), len(want))
		}
		for i := range got {
			if len(got[i]) != len(want[i]) {
				t.Fatalf("trial %d: cluster %v, want %v", trial, got[i], want[i])
			}
			for j := range got[i] {
				if got[i][j] != want[i][j] {
					t.Fatalf("trial %d: cluster %v, want %v", trial, got[i], want[i])
				}
			}
		}
	}
}

func TestNearest(t *testing.T) {
	d := []float32{0.5, 0.1, 0.9, 0.1, 0.3, 0.7, 0.2}
	got := nearest(d, 3)
	if want := []int{1, 3, 6}; len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("nearest = %v, want %v", got, want)
	}
	if got := nearest(d, 20); len(got) != len(d) {
		t.Errorf("nearest of more than there are = %v", got)
	}
}

// Three people photographed many times, and strangers seen once: three people come out, the
// strangers stay unplaced, and nobody is merged.
func TestGroupFindsPeopleNotStrangers(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	const dims = 64
	people := [][]float32{randomUnit(r, dims), randomUnit(r, dims), randomUnit(r, dims)}
	var faces []Face
	var truth []int
	for i := 0; i < 600; i++ {
		if i%4 == 3 {
			faces = append(faces, Face{Vec: randomUnit(r, dims), Label: -1})
			truth = append(truth, -1)
			continue
		}
		p := r.Intn(3)
		faces = append(faces, Face{Vec: unitNear(r, people[p], 0.06), Label: -1})
		truth = append(truth, p)
	}
	n := Group(faces, 0, Threshold)
	if n != 3 {
		t.Fatalf("%d people, want 3", n)
	}
	who := map[int]map[int]int{} // label -> true person -> faces
	for i, f := range faces {
		if truth[i] < 0 {
			if f.Label >= 0 {
				t.Errorf("a stranger joined person %d", f.Label)
			}
			continue
		}
		if f.Label < 0 {
			t.Errorf("face %d of person %d left unplaced", i, truth[i])
			continue
		}
		if who[f.Label] == nil {
			who[f.Label] = map[int]int{}
		}
		who[f.Label][truth[i]]++
	}
	for l, m := range who {
		if len(m) != 1 {
			t.Errorf("person %d mixes %v", l, m)
		}
	}
}

// Corrections hold: a fixed face stays put, and a face is never put in a person it was taken out
// of; gated faces join people but never found one.
func TestGroupRespectsCorrectionsAndTheGate(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	const dims = 32
	a, b := randomUnit(r, dims), randomUnit(r, dims)
	var faces []Face
	for i := 0; i < 10; i++ {
		faces = append(faces, Face{Vec: unitNear(r, a, 0.05), Label: 0})
	}
	faces = append(faces,
		Face{Vec: unitNear(r, a, 0.05), Label: -1, Reject: []int{0}}, // taken out of person 0
		Face{Vec: unitNear(r, a, 0.05), Label: -1, Gated: true},      // gated, but joins person 0
		Face{Vec: unitNear(r, b, 0.05), Label: -1, Gated: true},      // gated look-alikes of b...
		Face{Vec: unitNear(r, b, 0.05), Label: -1, Gated: true},
		Face{Vec: unitNear(r, b, 0.05), Label: -1, Gated: true}, // ...never found a person
		Face{Vec: unitNear(r, b, 0.05), Label: 1, Fixed: true},  // placed by hand
	)
	n := Group(faces, 2, Threshold)
	if faces[10].Label == 0 {
		t.Error("a rejected face went back to its person")
	}
	if faces[11].Label != 0 {
		t.Errorf("a gated face that matches person 0 = %d", faces[11].Label)
	}
	if n != 2 || faces[15].Label != 1 {
		t.Errorf("labels = %d, fixed face = %d", n, faces[15].Label)
	}
	for i := 12; i <= 14; i++ {
		if faces[i].Label != 1 {
			t.Errorf("gated face %d = %d, want to join the fixed person 1 (and found nothing new)", i, faces[i].Label)
		}
	}
}

// Grouping in two runs (the second with faces added later) places the same people as one run
// would, without redoing the first run's work.
func TestGroupIncrementally(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	const dims = 48
	people := [][]float32{randomUnit(r, dims), randomUnit(r, dims), randomUnit(r, dims), randomUnit(r, dims)}
	var all []Face
	var truth []int
	for i := 0; i < 400; i++ {
		if i%3 == 2 {
			all = append(all, Face{Vec: randomUnit(r, dims), Label: -1})
			truth = append(truth, -1)
			continue
		}
		p := r.Intn(4)
		if i < 200 && p == 3 {
			p = 0 // the fourth person only shows up later
		}
		all = append(all, Face{Vec: unitNear(r, people[p], 0.06), Label: -1})
		truth = append(truth, p)
	}
	first := append([]Face(nil), all[:200]...)
	n := Group(first, 0, Threshold)
	for i := range first {
		if first[i].Label < 0 {
			first[i].Seen = true
		}
	}
	second := append(first, all[200:]...)
	n = Group(second, n, Threshold)
	if n != 4 {
		t.Fatalf("%d people after two runs, want 4", n)
	}
	mix := map[int]map[int]bool{}
	for i, f := range second {
		if truth[i] < 0 {
			if f.Label >= 0 {
				t.Errorf("a stranger was placed")
			}
			continue
		}
		if f.Label < 0 {
			t.Errorf("face %d (person %d) unplaced", i, truth[i])
			continue
		}
		if mix[f.Label] == nil {
			mix[f.Label] = map[int]bool{}
		}
		mix[f.Label][truth[i]] = true
	}
	for l, m := range mix {
		if len(m) != 1 {
			t.Errorf("person %d mixes %v", l, m)
		}
	}
}

func TestHalfAndPercentile(t *testing.T) {
	for _, c := range []struct {
		h    uint16
		want float32
	}{{0x3c00, 1}, {0xc000, -2}, {0x3555, 0.33325195}, {0x0001, 5.9604645e-08}, {0x0000, 0}} {
		if got := half(c.h); got != c.want {
			t.Errorf("half(%#x) = %v, want %v", c.h, got, c.want)
		}
	}
	if got := percentile([]float32{4, 1, 3, 2, 5}, 10); math.Abs(float64(got)-1.4) > 1e-6 {
		t.Errorf("percentile = %v, want 1.4 (numpy's linear rule)", got)
	}
	v, err := Vector([]byte{0, 2, 0, 0, 0x3c, 0x00, 0x3c, 0x00})
	if err != nil || math.Abs(float64(v[0])-1/math.Sqrt2) > 1e-6 {
		t.Errorf("Vector = %v %v", v, err)
	}
}
