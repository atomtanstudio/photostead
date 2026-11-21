package provenance

import (
	"encoding/json"
	"fmt"
	"math"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// A ComfyUI API graph (the "prompt" JSON): node id -> {class_type, inputs}. An input is a
// literal or a link [node id, output index].
type graph map[string]struct {
	ClassType string         `json:"class_type"`
	Inputs    map[string]any `json:"inputs"`
}

// ComfyUI writes JSON with Python's json.dumps, which allows bare NaN and Infinity.
var pyNonJSON = regexp.MustCompile(`([:\[,]\s*)(-?Infinity|NaN)(\s*[,\]}])`)

// parseJSON decodes with numbers kept exact (json.Number): seeds go past what a float64 holds.
func parseJSON(s string, v any) bool {
	s = strings.TrimSpace(s)
	if s == "" || (s[0] != '{' && s[0] != '[') {
		return false
	}
	decode := func(s string) bool {
		d := json.NewDecoder(strings.NewReader(s))
		d.UseNumber()
		return d.Decode(v) == nil
	}
	return decode(s) || decode(pyNonJSON.ReplaceAllString(s, "${1}null${3}"))
}

// toFloat reads a number however it was decoded.
func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}

// asGraph reads s as an API graph: at least one node with a class_type.
func asGraph(s string) (graph, bool) {
	var g graph
	if !parseJSON(s, &g) {
		return nil, false
	}
	for _, n := range g {
		if n.ClassType != "" {
			return g, true
		}
	}
	return nil, false
}

// A ComfyUI editor workflow (the "workflow" JSON).
type workflow struct {
	Nodes []struct {
		ID      any    `json:"id"`
		Type    string `json:"type"`
		Title   string `json:"title"`
		Mode    int    `json:"mode"` // 2 muted, 4 bypassed
		Widgets []any  `json:"widgets_values"`
	} `json:"nodes"`
}

func asWorkflow(s string) (workflow, bool) {
	var w workflow
	if !parseJSON(s, &w) || len(w.Nodes) == 0 {
		return w, false
	}
	return w, true
}

func isLink(v any) bool {
	a, ok := v.([]any)
	if !ok || len(a) != 2 {
		return false
	}
	switch a[0].(type) {
	case string, float64, json.Number:
		return true
	}
	return false
}

func linkID(v any) string {
	switch x := v.([]any)[0].(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatInt(int64(x), 10)
	}
	return ""
}

func linkOut(v any) int {
	f, _ := toFloat(v.([]any)[1])
	return int(f)
}

// numOf follows a number input to its value (exactly as written): a literal, or a primitive or
// seed node's value.
func (g graph) numOf(ref any, depth int) (json.Number, bool) {
	switch x := ref.(type) {
	case json.Number:
		return x, true
	case float64:
		return json.Number(strconv.FormatFloat(x, 'f', -1, 64)), true
	}
	if !isLink(ref) || depth > 4 {
		return "", false
	}
	n, ok := g[linkID(ref)]
	if !ok {
		return "", false
	}
	for _, k := range []string{"value", "int", "number", "seed", "noise_seed", "Xi", "index"} {
		if v, ok := n.Inputs[k]; ok {
			if x, ok := g.numOf(v, depth+1); ok {
				return x, true
			}
		}
	}
	return "", false
}

var textKeys = []string{"text", "string", "text_g", "prompt", "value", "positive", "positive_prompt", "manual_prompt", "user_prompt", "Text", "STRING"}
var partKey = regexp.MustCompile(`(?i)^(prompt|string|text)_?(\d+|[a-d])$`)

// text follows a text or conditioning input back to the words: through switches (index +
// value0…), concatenations (text_a, string1…) and nodes that output positive and negative.
func (g graph) text(ref any, depth int) string {
	if depth > 12 {
		return ""
	}
	if s, ok := ref.(string); ok {
		return s
	}
	if !isLink(ref) {
		return ""
	}
	n, ok := g[linkID(ref)]
	if !ok {
		return ""
	}
	in := n.Inputs
	if linkOut(ref) == 1 && isLink(in["positive"]) && isLink(in["negative"]) {
		return g.text(in["negative"], depth+1)
	}
	for _, k := range []string{"index", "select", "selected"} {
		if v, ok := in[k]; ok {
			if n, ok := g.numOf(v, 0); ok {
				sel, _ := n.Float64()
				for _, p := range []string{"value", "input", "text", "string"} {
					if v, ok := in[fmt.Sprintf("%s%d", p, int(sel))]; ok {
						return g.text(v, depth+1)
					}
				}
			}
			break
		}
	}
	for _, k := range textKeys {
		if v, ok := in[k]; ok {
			if t := g.text(v, depth+1); t != "" {
				return t
			}
		}
	}
	var parts []string
	for k := range in {
		if partKey.MatchString(k) {
			parts = append(parts, k)
		}
	}
	sort.Slice(parts, func(i, j int) bool { return naturalLess(parts[i], parts[j]) })
	var texts []string
	for _, k := range parts {
		if t := g.text(in[k], depth+1); t != "" {
			texts = append(texts, t)
		}
	}
	if len(texts) > 0 {
		sep := " "
		if s, ok := in["separator"].(string); ok {
			sep = s
		} else if s, ok := in["delimiter"].(string); ok {
			sep = s
		}
		return strings.Join(texts, sep)
	}
	for _, k := range []string{"conditioning", "conditioning_1", "conditioning_to", "text_embeds"} {
		if isLink(in[k]) {
			return g.text(in[k], depth+1)
		}
	}
	return ""
}

func naturalLess(a, b string) bool {
	na, ea := strconv.Atoi(strings.TrimLeft(a, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_"))
	nb, eb := strconv.Atoi(strings.TrimLeft(b, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_"))
	if ea == nil && eb == nil && na != nb {
		return na < nb
	}
	return a < b
}

var (
	loraTag     = regexp.MustCompile(`(?i)<lora:([^:>]+)(?::([^>]*))?>`)
	modelFile   = regexp.MustCompile(`(?i)\.(safetensors|ckpt|gguf)$`)
	notModel    = regexp.MustCompile(`(?i)t5|clip|vae|text.?enc|upscal|esrgan|rife|controlnet|ipadapter|florence|siglip|llava|gemma`)
	generative  = regexp.MustCompile(`(?i)sampler|sample|textencode|generate|wanvideo|hunyuan|ltxv|cosmos|mochi|denoise|guider`)
	samplerNode = regexp.MustCompile(`(?i)sampler`)
	modelExt    = regexp.MustCompile(`(?i)\.(safetensors|ckpt|pt|pth|bin|gguf)$`)
	modelInput  = regexp.MustCompile(`(?i)ckpt|unet|model`)
	auxInput    = regexp.MustCompile(`(?i)clip|vae|text|t5|upscale|control`)
	guiderNode  = regexp.MustCompile(`(?i)guider`)
	wfGenerates = regexp.MustCompile(`(?i)sampler|textencode|wanvideo|generate`)
	wfPrompt    = regexp.MustCompile(`(?i)textencode|prompt`)
)

// display is a model file's name as shown: no folders, no extension.
func display(name string) string {
	return strings.TrimSpace(modelExt.ReplaceAllString(path.Base(strings.ReplaceAll(name, `\`, "/")), ""))
}

// Normalize is the catalog's key for a model or LoRA name.
func Normalize(name string) string { return strings.ToLower(display(name)) }

type found struct {
	prompt, negative string
	model            string
	loras            map[string]Lora // by normalized name
	settings         map[string]any
	generative       bool
}

func (f *found) lora(name string, weight *float64) {
	key := Normalize(name)
	if key == "" || key == "none" {
		return
	}
	if l, ok := f.loras[key]; ok && l.Weight != nil {
		return
	}
	f.loras[key] = Lora{Name: key, Display: display(name), Weight: weight}
}

func num(v any) *float64 {
	if f, ok := toFloat(v); ok && !math.IsNaN(f) {
		return &f
	}
	return nil
}

// parseGraph reads an API graph: the prompt (following the sampler's positive input), the
// negative, the model, LoRAs and sampler settings. A graph with no generating node (an upscale
// or re-encode) isn't evidence that its output was generated.
func parseGraph(g graph) found {
	f := found{loras: map[string]Lora{}, settings: map[string]any{}}
	ids := make([]string, 0, len(g))
	for id := range g {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return naturalLess(ids[i], ids[j]) })
	models, aux := map[string]string{}, map[string]string{}
	var pos, neg []string
	// Sampler settings first, so a sampler's seed wins over a stray seed elsewhere.
	slices.SortStableFunc(ids, func(a, b string) int {
		sa, sb := samplerNode.MatchString(g[a].ClassType), samplerNode.MatchString(g[b].ClassType)
		switch {
		case sa && !sb:
			return -1
		case sb && !sa:
			return 1
		}
		return 0
	})
	for _, id := range ids {
		n := g[id]
		in := n.Inputs
		if generative.MatchString(n.ClassType) {
			f.generative = true
		}
		for k, v := range in {
			if s, ok := v.(string); ok && modelFile.MatchString(s) {
				switch {
				case strings.Contains(strings.ToLower(k), "lora") || strings.Contains(strings.ToLower(n.ClassType), "lora"):
					var w *float64
					for _, wk := range []string{"strength_model", "strength", "lora_strength", "weight"} {
						if w = num(in[wk]); w != nil {
							break
						}
					}
					f.lora(s, w)
				case modelInput.MatchString(k):
					if auxInput.MatchString(k) || notModel.MatchString(s) {
						aux[Normalize(s)] = display(s)
					} else {
						models[Normalize(s)] = display(s)
					}
				}
			}
			// rgthree's Power Lora Loader: {on, lora, strength}
			if m, ok := v.(map[string]any); ok {
				if l, ok := m["lora"].(string); ok && l != "" && m["on"] != false {
					f.lora(l, num(m["strength"]))
				}
			}
		}
		for _, k := range []string{"seed", "noise_seed"} {
			if v, ok := in[k]; ok {
				if s, ok := g.numOf(v, 0); ok {
					if _, have := f.settings["seed"]; !have {
						seed, _, _ := strings.Cut(s.String(), ".") // a seed is a whole number
						f.settings["seed"] = seed
					}
				}
			}
		}
		for _, k := range []string{"steps", "cfg", "guidance", "sampler_name", "scheduler", "denoise", "width", "height"} {
			if v, ok := in[k]; ok && !isLink(v) {
				if _, have := f.settings[k]; !have {
					switch x := v.(type) {
					case string, bool:
						f.settings[k] = v
					case json.Number:
						if n, err := x.Float64(); err == nil {
							f.settings[k] = n
						}
					}
				}
			}
		}
		for _, k := range []string{"positive", "text_embeds"} {
			if isLink(in[k]) {
				pos = append(pos, g.text(in[k], 0))
			}
		}
		if guiderNode.MatchString(n.ClassType) && !isLink(in["positive"]) && isLink(in["conditioning"]) {
			pos = append(pos, g.text(in["conditioning"], 0))
		}
		if isLink(in["negative"]) {
			neg = append(neg, g.text(in["negative"], 0))
		}
		if s, ok := in["positive_prompt"].(string); ok {
			pos = append(pos, s)
		}
		if s, ok := in["negative_prompt"].(string); ok {
			neg = append(neg, s)
		}
	}
	f.prompt = longest(pos)
	if f.prompt == "" {
		// No sampler wiring to follow: the longest literal text, never one feeding a negative.
		negNodes := map[string]bool{}
		for _, n := range g {
			for _, k := range []string{"negative", "negative_prompt"} {
				if isLink(n.Inputs[k]) {
					negNodes[linkID(n.Inputs[k])] = true
				}
			}
		}
		var texts []string
		for id, n := range g {
			if negNodes[id] {
				continue
			}
			for _, k := range []string{"text", "prompt"} {
				if s, ok := n.Inputs[k].(string); ok && !slices.Contains(neg, s) {
					texts = append(texts, s)
				}
			}
		}
		f.prompt = longest(texts)
	}
	for _, m := range loraTag.FindAllStringSubmatch(f.prompt, -1) {
		var w *float64
		if x, err := strconv.ParseFloat(strings.TrimSpace(m[2]), 64); err == nil {
			w = &x
		}
		f.lora(m[1], w)
	}
	f.prompt = strings.TrimSpace(loraTag.ReplaceAllString(f.prompt, ""))
	if n := longest(neg); n != f.prompt {
		f.negative = strings.TrimSpace(n)
	}
	// The model is the one feeding a sampler, followed back through LoRA loaders and patches;
	// when there's no such wiring, the first by name.
	for _, id := range ids {
		n := g[id]
		if !samplerNode.MatchString(n.ClassType) && !guiderNode.MatchString(n.ClassType) {
			continue
		}
		for _, k := range modelLinks {
			if m := g.modelFor(n.Inputs[k], 0, map[string]bool{}); m != "" {
				f.model = m
				break
			}
		}
		if f.model != "" {
			break
		}
	}
	if f.model == "" {
		f.model = first(models)
	}
	if f.model == "" {
		f.model = first(aux)
	}
	if f.prompt != "" {
		f.generative = true
	}
	return f
}

var modelLinks = []string{"model", "unet", "diffusion_model", "base_model", "MODEL", "guider"}

// modelFor follows a model input upstream to the loader that names the model file.
func (g graph) modelFor(ref any, depth int, seen map[string]bool) string {
	if !isLink(ref) || depth > 20 {
		return ""
	}
	id := linkID(ref)
	if seen[id] {
		return ""
	}
	seen[id] = true
	n, ok := g[id]
	if !ok {
		return ""
	}
	if !strings.Contains(strings.ToLower(n.ClassType), "lora") {
		keys := make([]string, 0, len(n.Inputs))
		for k := range n.Inputs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if s, ok := n.Inputs[k].(string); ok && modelFile.MatchString(s) && modelInput.MatchString(k) && !auxInput.MatchString(k) && !notModel.MatchString(s) {
				return display(s)
			}
		}
	}
	for _, k := range modelLinks {
		if m := g.modelFor(n.Inputs[k], depth+1, seen); m != "" {
			return m
		}
	}
	return ""
}

// parseWorkflow reads an editor workflow when no API graph was saved: the prompt from text
// encoders' widgets, models and LoRAs from loaders. Muted and bypassed nodes don't count.
func parseWorkflow(w workflow) found {
	f := found{loras: map[string]Lora{}, settings: map[string]any{}}
	models := map[string]string{}
	var texts []string
	for _, n := range w.Nodes {
		if n.Mode == 2 || n.Mode == 4 {
			continue
		}
		if wfGenerates.MatchString(n.Type) {
			f.generative = true
		}
		firstText := true
		for _, v := range n.Widgets {
			s, ok := v.(string)
			if !ok {
				continue
			}
			switch {
			case modelFile.MatchString(s):
				if strings.Contains(strings.ToLower(n.Type), "lora") {
					f.lora(s, nil)
				} else if !notModel.MatchString(s) {
					models[Normalize(s)] = display(s)
				}
			case firstText && wfPrompt.MatchString(n.Type) &&
				!strings.Contains(strings.ToLower(n.Title), "negative") && len(strings.TrimSpace(s)) > 3:
				texts = append(texts, s)
				firstText = false
			}
		}
	}
	f.prompt = strings.TrimSpace(longest(texts))
	f.model = first(models)
	if f.prompt != "" {
		f.generative = true
	}
	return f
}

func longest(xs []string) string {
	best := ""
	for _, x := range xs {
		if len(strings.TrimSpace(x)) > len(best) {
			best = strings.TrimSpace(x)
		}
	}
	return best
}

// first is the alphabetically first model's display name (stable from run to run).
func first(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	return m[keys[0]]
}

func sortNatural(xs []string) {
	sort.Slice(xs, func(i, j int) bool { return naturalLess(xs[i], xs[j]) })
}

func formatFloat(f float64) string { return strconv.FormatFloat(f, 'f', 6, 64) }
