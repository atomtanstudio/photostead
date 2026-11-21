package provenance

import (
	"path"
	"regexp"
	"strings"
)

// Reference images: the image files a ComfyUI graph feeds into its generation, such as the start
// (and end) frame of image-to-video or the reference set of reference-to-video. Only loaders
// upstream of a generating node count, so a LoadImage left unwired elsewhere is ignored. A video
// made from an image of a character shows that character too (lineage).

var (
	imageFile   = regexp.MustCompile(`(?i)\.(png|jpe?g|webp|bmp|gif|tiff?|avif|heic)$`)
	genNode     = regexp.MustCompile(`(?i)sampler|imagetovideo|referencetovideo|i2v|fl2v|ref2v|firstlast|startend|textencode|guider|generate|wanvideo|vace|phantom|ipadapter|pulid|instantid|reactor|faceswap`)
	notRefInput = regexp.MustCompile(`(?i)prefix|output|save|audio|video|mask`)
	fileTag     = regexp.MustCompile(`(?i)\s*\[(input|output|temp)\]\s*$`)
	notRefNode  = regexp.MustCompile(`(?i)mask|save|preview|combine`)

	// Text-to-video models ignore a wired start image; image-to-video ones use it.
	textToVideo  = regexp.MustCompile(`(?i)t2v`)
	imageToVideo = regexp.MustCompile(`(?i)i2v|ti2v|fl2v|flf2v|ref2v|r2v|s2v|vace`)
)

// UsesStartImage says whether a video model takes its look from reference images (an
// image-to-video model), so a video made with it shows what its references show.
func UsesStartImage(model string) bool {
	return !textToVideo.MatchString(model) || imageToVideo.MatchString(model)
}

func imageNames(v any) []string {
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return nil
	}
	var parts []string
	var list []any
	if strings.HasPrefix(strings.TrimSpace(s), "[") && parseJSON(s, &list) {
		for _, x := range list {
			if t, ok := x.(string); ok {
				parts = append(parts, t)
			}
		}
	} else {
		parts = strings.Split(s, "\n")
	}
	var out []string
	for _, p := range parts {
		b := path.Base(strings.ReplaceAll(fileTag.ReplaceAllString(strings.TrimSpace(p), ""), `\`, "/"))
		if imageFile.MatchString(b) {
			out = append(out, b)
		}
	}
	return out
}

// Refs lists the reference images' file names from a ComfyUI API graph, or failing that its
// editor workflow, in graph order without repeats.
func Refs(graphJSON, workflowJSON string) []string {
	if g, ok := asGraph(graphJSON); ok {
		return graphRefs(g)
	}
	if w, ok := asWorkflowLinks(workflowJSON); ok {
		return w.refs()
	}
	return nil
}

func graphRefs(g graph) []string {
	upstream := map[string]bool{}
	var todo []string
	for id, n := range g {
		if genNode.MatchString(n.ClassType) {
			todo = append(todo, id)
		}
	}
	for len(todo) > 0 {
		id := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		n, ok := g[id]
		if upstream[id] || !ok {
			continue
		}
		upstream[id] = true
		for _, v := range n.Inputs {
			if isLink(v) {
				todo = append(todo, linkID(v))
			}
		}
	}
	ids := make([]string, 0, len(upstream))
	for id := range upstream {
		ids = append(ids, id)
	}
	sortNatural(ids)
	var refs []string
	for _, id := range ids {
		n := g[id]
		if strings.Contains(strings.ToLower(n.ClassType), "mask") {
			continue
		}
		keys := make([]string, 0, len(n.Inputs))
		for k := range n.Inputs {
			keys = append(keys, k)
		}
		sortNatural(keys)
		for _, k := range keys {
			if !notRefInput.MatchString(k) {
				refs = append(refs, imageNames(n.Inputs[k])...)
			}
		}
	}
	return unique(refs)
}

// An editor workflow with its links: [id, from node, from slot, to node, to slot, type].
type workflowLinks struct {
	Nodes []struct {
		ID      any    `json:"id"`
		Type    string `json:"type"`
		Mode    int    `json:"mode"`
		Widgets []any  `json:"widgets_values"`
	} `json:"nodes"`
	Links [][]any `json:"links"`
}

func asWorkflowLinks(s string) (workflowLinks, bool) {
	var w workflowLinks
	if !parseJSON(s, &w) || len(w.Nodes) == 0 {
		return w, false
	}
	return w, true
}

func key(v any) string {
	if f, ok := toFloat(v); ok {
		return strings.TrimSuffix(strings.TrimRight(strings.TrimRight(formatFloat(f), "0"), "."), ".")
	}
	s, _ := v.(string)
	return s
}

func (w workflowLinks) refs() []string {
	from := map[string][]string{} // node -> nodes feeding it
	for _, l := range w.Links {
		if len(l) >= 4 {
			from[key(l[3])] = append(from[key(l[3])], key(l[1]))
		}
	}
	live := map[string]int{}
	var todo []string
	for i, n := range w.Nodes {
		if n.Mode == 2 || n.Mode == 4 {
			continue
		}
		live[key(n.ID)] = i
		if genNode.MatchString(n.Type) {
			todo = append(todo, key(n.ID))
		}
	}
	upstream := map[string]bool{}
	for len(todo) > 0 {
		id := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		if _, ok := live[id]; upstream[id] || !ok {
			continue
		}
		upstream[id] = true
		todo = append(todo, from[id]...)
	}
	var refs []string
	for _, n := range w.Nodes {
		if !upstream[key(n.ID)] || notRefNode.MatchString(n.Type) {
			continue
		}
		for _, v := range n.Widgets {
			refs = append(refs, imageNames(v)...)
		}
	}
	return unique(refs)
}

func unique(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
