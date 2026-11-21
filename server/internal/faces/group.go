// Package faces groups the faces in an owner's real photos into people: the streaming
// average-of-3 rule that Photostead's face benchmark chose over single-nearest-face chaining (on AI
// faces especially, chaining merged many different people).
//
//   - Faces are taken in the order they were captured. A face joins a person when its 3 nearest
//     faces there are, on average, within the threshold: several witnesses, so a chain of
//     look-alikes can't pull two people together the way following the single nearest face does.
//   - Faces that don't qualify wait in a pool. Every 500 faces (and at the end) the pool is grouped
//     with average linkage at the threshold, and groups of 3 or more become new people.
//   - Faces the quality gate holds back (small, unsure, turned away, blurry) never found a person;
//     at the end they may join one, by the same 3-nearest rule.
//   - Corrections are hard constraints: a face placed by hand never moves, and a face is never put
//     back in a person it was taken out of.
package faces

import (
	"math"
	"runtime"
	"sort"
	"sync"
)

// The settings the benchmark chose for AuraFace with mirror averaging.
const (
	Engine     = "yunet-auraface-v1"
	Threshold  = 0.45 // cosine distance: the People threshold (no two people merged on a family album or children growing up)
	K          = 3    // witnesses
	MinSize    = 3    // faces to found a person
	FlushEvery = 500
	Shortlist  = 64 // nearest placed faces looked at for a new face

	// Pets: open-noodle's pet recognition (base), on whole-animal crops. At 0.5 its published
	// clustering keeps groups 98% (cats) and 99% (dogs) pure; a cat is now and then split in two,
	// which an owner merges. Two photos are enough to suggest a pet: there are few, and the owner
	// confirms each by naming it.
	PetEngine    = "rfdetr-petrec-base-v1"
	PetThreshold = 0.5
	PetMinSize   = 2
)

// Params are the grouping's settings for one kind of face.
type Params struct {
	Threshold float32 // cosine distance
	K         int     // witnesses
	MinSize   int     // faces to found a group
}

var (
	PeopleParams = Params{Threshold: Threshold, K: K, MinSize: MinSize}
	PetParams    = Params{Threshold: PetThreshold, K: K, MinSize: PetMinSize}
)

// Face is one face as the grouping sees it.
type Face struct {
	Vec    []float32 // unit length
	Label  int       // its person (an index), or -1
	Fixed  bool      // placed by the owner: never moves
	Gated  bool      // held back from founding a person
	Seen   bool      // grouped before and left waiting: it's in the pool from the start
	Reject []int     // people it must never join
}

func (f *Face) rejects(label int) bool {
	for _, r := range f.Reject {
		if r == label {
			return true
		}
	}
	return false
}

// dot is the inner product of two unit vectors; the cosine distance is 1 minus it.
func dot(a, b []float32) float32 {
	var s0, s1, s2, s3 float32
	n := len(a) &^ 3
	for i := 0; i < n; i += 4 {
		s0 += a[i] * b[i]
		s1 += a[i+1] * b[i+1]
		s2 += a[i+2] * b[i+2]
		s3 += a[i+3] * b[i+3]
	}
	for i := n; i < len(a); i++ {
		s0 += a[i] * b[i]
	}
	return s0 + s1 + s2 + s3
}

// distances fills out[j] with the cosine distance from v to each of faces[idx[j]], on all cores.
func distances(faces []Face, v []float32, idx []int, out []float32) {
	const chunk = 2048
	if len(idx) < chunk {
		for j, i := range idx {
			out[j] = 1 - dot(v, faces[i].Vec)
		}
		return
	}
	var wg sync.WaitGroup
	work := make(chan int, runtime.NumCPU())
	for w := 0; w < runtime.NumCPU(); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for lo := range work {
				for j := lo; j < min(lo+chunk, len(idx)); j++ {
					out[j] = 1 - dot(v, faces[idx[j]].Vec)
				}
			}
		}()
	}
	for lo := 0; lo < len(idx); lo += chunk {
		work <- lo
	}
	close(work)
	wg.Wait()
}

// Group places the faces that have no person yet. labels is the number of people already known
// (faces' Label values are below it); new people get labels from there up. It returns the new
// number of labels.
//
// A run only does new work. Faces left waiting by an earlier run (Seen) start in the pool and never
// try to join a person again (in the rule, a waiting face only leaves the pool by founding one);
// the pool's "within t" links are worked out only for faces a new face reaches. Gated faces left
// unplaced try again only when this run placed something.
func Group(faces []Face, labels int, t float32) int {
	p := PeopleParams
	p.Threshold = t
	return GroupWith(faces, labels, p)
}

// GroupWith is Group with other settings (pets').
func GroupWith(faces []Face, labels int, p Params) int {
	g := &grouper{faces: faces, next: labels, t: p.Threshold, k: p.K, minSize: p.MinSize, size: map[int]int{}, near: map[int][]int{},
		inPool: map[int]bool{}, expanded: map[int]bool{}}
	for i := range faces {
		f := &faces[i]
		switch {
		case f.Label >= 0 && !f.Gated:
			g.placed = append(g.placed, i)
			g.size[f.Label]++
		case f.Label < 0 && !f.Gated && f.Seen:
			g.pending = append(g.pending, i)
			g.inPool[i] = true
		}
	}
	placedBefore := len(g.placed)
	step := 0
	for i := range faces {
		f := &faces[i]
		if f.Label >= 0 || f.Gated || f.Seen {
			continue
		}
		if best := g.join(i, true); best >= 0 {
			f.Label = best
			g.placed = append(g.placed, i)
			g.size[best]++
		} else {
			g.wait(i)
		}
		step++
		if step%FlushEvery == 0 {
			g.flush()
		}
	}
	g.flush()
	// The gated faces may now join a person (never found one).
	changed := len(g.placed) > placedBefore
	for i := range faces {
		f := &faces[i]
		if f.Label < 0 && f.Gated && (changed || !f.Seen) {
			if best := g.join(i, false); best >= 0 {
				f.Label = best
			}
		}
	}
	return g.next
}

type grouper struct {
	faces    []Face
	t        float32
	k        int // witnesses
	minSize  int // faces to found a group
	next     int
	placed   []int       // placed faces that aren't gated: the people new faces may join
	size     map[int]int // placed (ungated) faces per person
	pending  []int       // waiting to found a person
	inPool   map[int]bool
	near     map[int][]int // pending face -> pending faces within t (complete once expanded)
	expanded map[int]bool  // faces whose links to the whole pool are known
	fresh    []int         // pooled since the last flush: the only places a new group can start
	buf      []float32
}

// join finds the person whose K nearest faces are on average within t of face i. When streaming
// (the main pass), only the Shortlist nearest placed faces are looked at, and a person needs K of
// them there (or all of theirs, if they have fewer); the gated pass looks at every placed face.
func (g *grouper) join(i int, streaming bool) int {
	if len(g.placed) == 0 {
		return -1
	}
	f := &g.faces[i]
	if cap(g.buf) < len(g.placed) {
		g.buf = make([]float32, len(g.placed), len(g.placed)*2)
	}
	d := g.buf[:len(g.placed)]
	distances(g.faces, f.Vec, g.placed, d)
	type cand struct {
		d     float32
		label int
	}
	var near []cand
	if streaming {
		for _, j := range nearest(d, Shortlist) {
			near = append(near, cand{d[j], g.faces[g.placed[j]].Label})
		}
	} else {
		for j, p := range g.placed {
			near = append(near, cand{d[j], g.faces[p].Label})
		}
		sort.Slice(near, func(a, b int) bool { return near[a].d < near[b].d })
	}
	// Per person, the K smallest distances (near is sorted, so the first K seen).
	sums := map[int]float32{}
	counts := map[int]int{}
	for _, c := range near {
		if counts[c.label] < g.k {
			sums[c.label] += c.d
			counts[c.label]++
		}
	}
	best, bestD := -1, g.t
	labels := make([]int, 0, len(counts))
	for l := range counts {
		labels = append(labels, l)
	}
	sort.Ints(labels) // the same answer every run
	for _, l := range labels {
		if f.rejects(l) {
			continue
		}
		n := counts[l]
		if streaming && n < min(g.k, g.size[l]) {
			continue
		}
		if mean := sums[l] / float32(n); mean < bestD {
			best, bestD = l, mean
		}
	}
	return best
}

// nearest returns the indices of the m smallest values of d, smallest first (ties by index).
func nearest(d []float32, m int) []int {
	m = min(m, len(d))
	top := make([]int, 0, m) // the m best so far; worst tracked so most values are rejected at once
	worst := -1
	for j, v := range d {
		if len(top) < m {
			top = append(top, j)
			if worst < 0 || v > d[top[worst]] {
				worst = len(top) - 1
			}
			continue
		}
		if v >= d[top[worst]] {
			continue
		}
		top[worst] = j
		worst = 0
		for k := range top {
			if d[top[k]] > d[top[worst]] || (d[top[k]] == d[top[worst]] && top[k] > top[worst]) {
				worst = k
			}
		}
	}
	sort.Slice(top, func(a, b int) bool {
		if d[top[a]] != d[top[b]] {
			return d[top[a]] < d[top[b]]
		}
		return top[a] < top[b]
	})
	return top
}

// wait puts a new face in the pool, with its links to every pooled face.
func (g *grouper) wait(i int) {
	g.expand(i)
	g.pending = append(g.pending, i)
	g.inPool[i] = true
	g.fresh = append(g.fresh, i)
}

// expand works out face i's links to the whole pool, once.
func (g *grouper) expand(i int) {
	if g.expanded[i] {
		return
	}
	g.expanded[i] = true
	if len(g.pending) == 0 {
		return
	}
	d := make([]float32, len(g.pending))
	distances(g.faces, g.faces[i].Vec, g.pending, d)
	for j, p := range g.pending {
		if p != i && d[j] <= g.t && !g.linked(i, p) {
			g.near[i] = append(g.near[i], p)
			g.near[p] = append(g.near[p], i)
		}
	}
}

func (g *grouper) linked(a, b int) bool {
	for _, x := range g.near[a] {
		if x == b {
			return true
		}
	}
	return false
}

// flush groups the pool with average linkage at t; groups of MinSize or more become people. Average
// linkage only ever joins clusters with some pair within t, so it runs on each connected group of
// the "within t" graph separately, with the same result as on the whole pool.
func (g *grouper) flush() {
	fresh := g.fresh
	g.fresh = nil
	if len(g.pending) < g.minSize {
		return
	}
	// Only groups a new face reached can have changed; the rest were tried before.
	seen := map[int]bool{}
	var founded []int
	for _, start := range fresh {
		if seen[start] || !g.inPool[start] {
			continue
		}
		comp := []int{start}
		seen[start] = true
		for q := 0; q < len(comp); q++ {
			g.expand(comp[q])
			for _, n := range g.near[comp[q]] {
				if g.inPool[n] && !seen[n] {
					seen[n] = true
					comp = append(comp, n)
				}
			}
		}
		if len(comp) < g.minSize {
			continue
		}
		sort.Ints(comp)
		for _, cluster := range averageLinkage(g.faces, comp, g.t) {
			if len(cluster) < g.minSize {
				continue
			}
			label := g.next
			g.next++
			for _, i := range cluster {
				g.faces[i].Label = label
				g.placed = append(g.placed, i)
				delete(g.inPool, i)
				founded = append(founded, i)
			}
			g.size[label] = len(cluster)
		}
	}
	if len(founded) == 0 {
		return
	}
	left := g.pending[:0]
	for _, i := range g.pending {
		if g.inPool[i] {
			left = append(left, i)
		}
	}
	g.pending = left
	for _, i := range founded {
		delete(g.near, i)
	}
}

// averageLinkage clusters the faces at idx with average linkage (UPGMA), making only merges at
// distance <= t: the flat clusters scipy's fcluster(linkage(method="average"), t, "distance")
// gives. It uses the nearest-neighbour chain, O(n²).
func averageLinkage(faces []Face, idx []int, t float32) [][]int {
	n := len(idx)
	D := make([]float32, n*n)
	for a := 0; a < n; a++ {
		for b := a + 1; b < n; b++ {
			d := 1 - dot(faces[idx[a]].Vec, faces[idx[b]].Vec)
			D[a*n+b], D[b*n+a] = d, d
		}
	}
	active := make([]bool, n)
	size := make([]int, n)
	members := make([][]int, n)
	for a := range active {
		active[a], size[a], members[a] = true, 1, []int{idx[a]}
	}
	alive := n
	var done [][]int
	var chain []int
	for alive > 1 {
		if len(chain) == 0 {
			for a := 0; a < n; a++ {
				if active[a] {
					chain = append(chain, a)
					break
				}
			}
		}
		a := chain[len(chain)-1]
		prev := -1
		if len(chain) > 1 {
			prev = chain[len(chain)-2]
		}
		b, bd := -1, float32(math.Inf(1))
		if prev >= 0 {
			b, bd = prev, D[a*n+prev] // ties go to the previous link, so the chain ends
		}
		for c := 0; c < n; c++ {
			if active[c] && c != a && D[a*n+c] < bd {
				b, bd = c, D[a*n+c]
			}
		}
		if b == -1 {
			break
		}
		if b != prev {
			chain = append(chain, b)
			continue
		}
		// a and b are each other's nearest.
		chain = chain[:len(chain)-2]
		if bd > t {
			// Neither can merge with anything within t any more: they're final.
			for _, x := range []int{a, b} {
				active[x] = false
				alive--
				done = append(done, members[x])
			}
			continue
		}
		for c := 0; c < n; c++ {
			if active[c] && c != a && c != b {
				d := (float32(size[a])*D[a*n+c] + float32(size[b])*D[b*n+c]) / float32(size[a]+size[b])
				D[a*n+c], D[c*n+a] = d, d
			}
		}
		size[a] += size[b]
		members[a] = append(members[a], members[b]...)
		active[b] = false
		alive--
		// The rest of the chain stays valid: average linkage is reducible, so merging can't bring
		// a cluster closer than the nearer of its two parts.
	}
	for a := 0; a < n; a++ {
		if active[a] {
			done = append(done, members[a])
		}
	}
	return done
}
