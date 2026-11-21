// Package provenance reads how an AI image or video was made from evidence in its own metadata
// (ComfyUI and Automatic1111 settings, Midjourney job text, Grok's download signature, China's AIGC
// label, Content Credentials, IPTC's digital source type). It never guesses: no evidence, no
// generation, and the AI badge depends on it.
package provenance

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Version is the parser's version. Raising it reads every item's metadata again.
const Version = 1

type Lora struct {
	Name    string   // normalized (the catalog key)
	Display string   // as written
	Weight  *float64 // strength, when known
}

// Generation is what the evidence says about how an item was made.
type Generation struct {
	Kind        string // generated, or edited (a real photo changed by a generative model)
	Source      string // comfyui, a1111, midjourney, grok, aigc, c2pa, iptc
	Evidence    string // where: png:prompt, exif:usercomment, mp4:comment, c2pa, xmp:digitalsourcetype…
	Tool        string
	ToolVersion string
	Model       string // display name
	Loras       []Lora
	Prompt      string
	Negative    string
	Seed        string
	Sampler     string
	Scheduler   string
	Steps       int
	CFG         float64
	Width       int
	Height      int
	Settings    map[string]any // anything else read
	Graph       string         // ComfyUI API graph, exactly as found
	Workflow    string         // ComfyUI editor workflow, exactly as found
	NeedsFile   bool           // some of it was cut short in the index; reading the file gets the rest
}

// Tags are exiftool's tags for one file ("Group:Tag" -> value), either straight from exiftool or
// from the index, where values over 8 KB were replaced by {"truncated": true, ...}.
type Tags = map[string]any

// tags are indexed by name, without the group. Groups whose fields reuse common names for other
// things (C2PA's CBOR "Description" is an ingredient label, track handlers, color profiles) are
// left out; C2PA is read by its own full names.
type index struct {
	all    Tags
	byName map[string][]any
}

var skipGroups = []string{"CBOR", "JUMBF", "ICC", "Track", "Jpeg2000", "Composite", "System", "ExifTool", "File"}

func newIndex(t Tags) index {
	ix := index{all: t, byName: map[string][]any{}}
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	sort.Strings(keys) // the same order every time
	for _, k := range keys {
		group, name, ok := strings.Cut(k, ":")
		if !ok {
			continue
		}
		skip := false
		for _, g := range skipGroups {
			if strings.HasPrefix(group, g) {
				skip = true
			}
		}
		// A JPEG's own comment is File:Comment; keep that one, not the rest of File.
		if skip && k != "File:Comment" {
			continue
		}
		ix.byName[strings.ToLower(name)] = append(ix.byName[strings.ToLower(name)], t[k])
	}
	return ix
}

// cut says whether a value was shortened in the index.
func cut(v any) bool {
	m, ok := v.(map[string]any)
	return ok && m["truncated"] == true
}

func text(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case []any:
		var parts []string
		for _, e := range x {
			if s, ok := text(e); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n"), len(parts) > 0
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case json.Number:
		return x.String(), true
	}
	return "", false
}

// get is the first text value under any of the names (in order of the names).
func (ix index) get(names ...string) string {
	for _, n := range names {
		for _, v := range ix.byName[n] {
			if s, ok := text(v); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return ""
}

// anyCut says whether any value under the names was shortened in the index.
func (ix index) anyCut(names ...string) bool {
	for _, n := range names {
		for _, v := range ix.byName[n] {
			if cut(v) {
				return true
			}
		}
	}
	return false
}

func (ix index) full(key string) string {
	s, _ := text(ix.all[key])
	return s
}

var (
	descriptionNames = []string{"comment", "description", "synopsis", "information", "imagedescription", "usercomment"}
	a1111Text        = regexp.MustCompile(`(?s)\bSteps: \d+.*\bSampler: `)
	mjJob            = regexp.MustCompile(`(?is)^(.*?)\s*Job ID:\s*([0-9a-f]{8}-[0-9a-f-]{27})`)
	mjParams         = regexp.MustCompile(`\s--(v|ar|niji|style|stylize|chaos|profile|sref|cref|q|seed|motion|raw|p)\b`)
	grokSignature    = regexp.MustCompile(`^Signature: [A-Za-z0-9+/=]{100,}\s*$`)
	aiSource         = regexp.MustCompile(`(?i)(compositeWith)?trainedAlgorithmicMedia`)
)

// Parse reads the evidence in an item's tags. It returns nil when there's none. needsFile says
// whether something that might be evidence was cut short in the index, so the file should be
// read again (even when there's no generation yet).
func Parse(t Tags, video bool) (g *Generation, needsFile bool) {
	ix := newIndex(t)
	ev := func(png, mp4 string) string {
		if video {
			return mp4
		}
		return png
	}

	// 1. ComfyUI: "prompt"/"workflow" text chunks (PNG) or keys (core SaveVideo), or one JSON
	//    comment {prompt, workflow} (VideoHelperSuite).
	var gr graph
	var graphJSON, wfJSON, where string
	if s := ix.get("prompt"); s != "" {
		if x, ok := asGraph(s); ok {
			gr, graphJSON, where = x, s, ev("png:prompt", "video:prompt")
		}
	}
	if s := ix.get("workflow"); s != "" {
		if _, ok := asWorkflow(s); ok {
			wfJSON = s
		}
	}
	for _, name := range descriptionNames {
		s := ix.get(name)
		if s == "" {
			continue
		}
		if x, ok := asGraph(s); ok && gr == nil {
			gr, graphJSON, where = x, s, ev("png:"+name, "video:"+name)
			continue
		}
		var j struct {
			Prompt   any `json:"prompt"`
			Workflow any `json:"workflow"`
		}
		if !parseJSON(s, &j) {
			continue
		}
		if p, ok := j.Prompt.(string); ok && gr == nil {
			if x, ok := asGraph(p); ok {
				gr, graphJSON, where = x, p, ev("png:"+name, "video:"+name)
			}
		}
		if w, ok := j.Workflow.(string); ok && wfJSON == "" {
			if _, ok := asWorkflow(w); ok {
				wfJSON = w
			}
		}
	}
	comfyCut := ix.anyCut("prompt", "workflow")
	commentCut := ix.anyCut(descriptionNames...)
	needsFile = comfyCut || commentCut || ix.anyCut("parameters")
	if gr != nil || wfJSON != "" {
		var f found
		if gr != nil {
			f = parseGraph(gr)
		} else {
			w, _ := asWorkflow(wfJSON)
			f = parseWorkflow(w)
			where = ev("png:workflow", "video:workflow")
		}
		if !f.generative {
			// ComfyUI only post-processed this file (upscale, interpolation, re-encode): not
			// evidence it was generated.
			return nil, needsFile
		}
		g := &Generation{Kind: "generated", Source: "comfyui", Evidence: where, Tool: "ComfyUI", Graph: graphJSON, Workflow: wfJSON,
			NeedsFile: comfyCut, Prompt: f.prompt, Negative: f.negative, Model: f.model, Settings: map[string]any{}}
		g.loras(f.loras)
		g.settingsFrom(f.settings)
		return g, needsFile
	}
	if comfyCut {
		// The graph is in the file but too big for the index: it's ComfyUI's, details to come.
		return &Generation{Kind: "generated", Source: "comfyui", Evidence: ev("png:prompt", "video:prompt"), Tool: "ComfyUI",
			NeedsFile: true, Settings: map[string]any{}}, true
	}

	// 2. Automatic1111 / Forge / Civitai parameters: their own chunk, or a description-style tag.
	if s := ix.get("parameters"); strings.TrimSpace(s) != "" {
		return parseA1111(s, ev("png:parameters", "video:parameters")), needsFile
	}
	for _, name := range []string{"usercomment", "comment", "description", "imagedescription", "xpcomment", "synopsis"} {
		if s := ix.get(name); a1111Text.MatchString(s) {
			return parseA1111(s, evidenceFor(name, video)), needsFile
		}
	}

	// 3. Midjourney: the prompt with its --parameters, then the job's id.
	for _, name := range []string{"description", "imagedescription", "comment", "title", "caption-abstract"} {
		if m := mjJob.FindStringSubmatch(ix.get(name)); m != nil && mjParams.MatchString(m[1]) {
			return &Generation{Kind: "generated", Source: "midjourney", Evidence: evidenceFor(name, video), Tool: "Midjourney",
				Model: "Midjourney", Prompt: strings.TrimSpace(m[1]), Settings: map[string]any{"job id": m[2]}}, needsFile
		}
	}

	// 4. Grok Imagine downloads: "Signature: <base64>" in the description or comment.
	for _, name := range []string{"imagedescription", "usercomment", "comment", "description"} {
		if grokSignature.MatchString(strings.TrimSpace(ix.get(name))) {
			return &Generation{Kind: "generated", Source: "grok", Evidence: evidenceFor(name, video), Tool: "Grok Imagine",
				Model: "Grok Imagine", Settings: map[string]any{}}, needsFile
		}
	}

	// 5. China's mandatory AIGC label (GB 45438-2025): Label "1" means AI-generated.
	if g := aigc(ix, video); g != nil {
		return g, needsFile
	}

	// 6. Content Credentials (C2PA) that say a generative model made or edited it.
	if src := ix.full("CBOR:ActionsDigitalSourceType"); aiSource.MatchString(src) {
		return c2pa(ix, src), needsFile
	}

	// 7. IPTC's digital source type in XMP, or Google's "Made with Google AI" credit.
	src := ix.get("digitalsourcetype")
	credit := ix.get("credit")
	if aiSource.MatchString(src) || strings.Contains(credit, "with Google AI") {
		kind := "generated"
		if strings.Contains(strings.ToLower(src), "composite") || strings.HasPrefix(credit, "Edited with") {
			kind = "edited"
		}
		g := &Generation{Kind: kind, Source: "iptc", Evidence: "xmp:digitalsourcetype", Prompt: describedPrompt(ix), Settings: map[string]any{}}
		if strings.Contains(credit, "Google AI") {
			g.Tool, g.Evidence = "Google AI", "xmp:credit"
		}
		return g, needsFile
	}
	return nil, needsFile
}

func evidenceFor(name string, video bool) string {
	if video {
		return "video:" + name
	}
	switch name {
	case "usercomment":
		return "exif:usercomment"
	case "imagedescription":
		return "exif:imagedescription"
	}
	return "metadata:" + name
}

func (g *Generation) loras(m map[string]Lora) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		g.Loras = append(g.Loras, m[k])
	}
}

// settingsFrom moves the well-known settings into their fields and keeps the rest.
func (g *Generation) settingsFrom(s map[string]any) {
	for k, v := range s {
		switch k {
		case "seed":
			g.Seed, _ = text(v)
		case "sampler_name":
			g.Sampler, _ = v.(string)
		case "scheduler":
			g.Scheduler, _ = v.(string)
		case "steps":
			if f, ok := v.(float64); ok {
				g.Steps = int(f)
			}
		case "cfg":
			if f, ok := v.(float64); ok {
				g.CFG = f
			}
		case "width":
			if f, ok := v.(float64); ok {
				g.Width = int(f)
			}
		case "height":
			if f, ok := v.(float64); ok {
				g.Height = int(f)
			}
		default:
			g.Settings[k] = v
		}
	}
}

var (
	a1111Pair  = regexp.MustCompile(`([A-Za-z][\w /]*?): ("[^"]*"|[^,]*)(?:,|$)`)
	loraHashes = regexp.MustCompile(`Lora hashes: "([^"]*)"`)
	size       = regexp.MustCompile(`^(\d+)x(\d+)$`)
	stepsLine  = regexp.MustCompile(`^\s*Steps: \d+`)
)

// parseA1111 reads Automatic1111-style parameters: the prompt, "Negative prompt: …", then a
// "Steps: …, Sampler: …" line. Forge, SD.Next and Civitai write the same format.
func parseA1111(s, evidence string) *Generation {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	pi := -1
	for i, l := range lines {
		if stepsLine.MatchString(l) {
			pi = i
		}
	}
	body, params := strings.Join(lines, "\n"), ""
	if pi >= 0 {
		body, params = strings.Join(lines[:pi], "\n"), strings.Join(lines[pi:], " ")
	}
	prompt, negative := body, ""
	if i := strings.Index(body, "Negative prompt:"); i >= 0 {
		prompt, negative = body[:i], strings.TrimSpace(body[i+len("Negative prompt:"):])
	}
	g := &Generation{Kind: "generated", Source: "a1111", Evidence: evidence, Tool: "Automatic1111", Negative: negative, Settings: map[string]any{}}
	loras := map[string]Lora{}
	add := func(name string, w *float64) {
		if k := Normalize(name); k != "" {
			if _, ok := loras[k]; !ok || w != nil {
				loras[k] = Lora{Name: k, Display: display(name), Weight: w}
			}
		}
	}
	for _, m := range loraTag.FindAllStringSubmatch(prompt, -1) {
		var w *float64
		if x, err := strconv.ParseFloat(strings.TrimSpace(m[2]), 64); err == nil {
			w = &x
		}
		add(m[1], w)
	}
	g.Prompt = strings.TrimSpace(loraTag.ReplaceAllString(prompt, ""))
	for _, m := range a1111Pair.FindAllStringSubmatch(params, -1) {
		k, v := strings.TrimSpace(m[1]), strings.Trim(strings.TrimSpace(m[2]), `"`)
		switch strings.ToLower(k) {
		case "steps":
			g.Steps, _ = strconv.Atoi(v)
		case "sampler":
			g.Sampler = v
		case "schedule type":
			g.Scheduler = v
		case "cfg scale":
			g.CFG, _ = strconv.ParseFloat(v, 64)
		case "seed":
			g.Seed = v
		case "size":
			if s := size.FindStringSubmatch(v); s != nil {
				g.Width, _ = strconv.Atoi(s[1])
				g.Height, _ = strconv.Atoi(s[2])
			}
		case "model":
			g.Model = display(v)
		case "version":
			g.ToolVersion = v
			if strings.HasPrefix(v, "f") { // Forge versions read like f2.0.1v1.10.1
				g.Tool = "Forge"
			}
		case "lora hashes":
		default:
			g.Settings[strings.ToLower(k)] = v
		}
	}
	for _, m := range loraHashes.FindAllStringSubmatch(params, -1) {
		for _, p := range strings.Split(m[1], ",") {
			if name, _, _ := strings.Cut(strings.TrimSpace(p), ":"); name != "" {
				add(name, nil)
			}
		}
	}
	if strings.Contains(s, "Civitai resources") || strings.Contains(s, "Civitai metadata") {
		g.Tool = "Civitai"
		delete(g.Settings, "civitai resources")
		delete(g.Settings, "civitai metadata")
	}
	g.loras(loras)
	return g
}

func aigc(ix index, video bool) *Generation {
	for _, s := range []string{ix.get("aigc"), ix.get("comment")} {
		var j map[string]any
		if !parseJSON(s, &j) {
			continue
		}
		if inner, ok := j["AIGC"].(map[string]any); ok {
			j = inner
		}
		label, _ := text(j["Label"])
		if label == "" {
			label, _ = text(j["label"])
		}
		if label != "1" {
			continue
		}
		g := &Generation{Kind: "generated", Source: "aigc", Evidence: evidenceFor("aigc", video), Settings: map[string]any{}}
		if id, _ := text(j["ProduceID"]); id != "" {
			g.Settings["produce id"] = id
		}
		if p, _ := text(j["ContentProducer"]); p != "" {
			g.Settings["content producer"] = p
			g.Tool = aigcProducers[p]
		}
		return g
	}
	return nil
}

// Registered AIGC content producers, by the code in the label.
var aigcProducers = map[string]string{
	"001191110108335469089C10100": "Kling", // Kuaishou; its labels' ids read KLingMuse_… and SGP_PROD_ai_web_…
}

// Content Credentials claim generators, as people know the products.
var claimTools = map[string]string{
	"Google C2PA Core Generator Library": "Google AI",
	"OpenAI Media Service API":           "OpenAI",
	"ChatGPT":                            "ChatGPT",
	"Grok Imagine":                       "Grok Imagine",
	"Sora":                               "Sora",
	"recraft.ai":                         "Recraft",
	"Black Forest Labs API":              "Black Forest Labs",
}

func c2pa(ix index, src string) *Generation {
	g := &Generation{Kind: "generated", Source: "c2pa", Evidence: "c2pa", Settings: map[string]any{}}
	actions := ix.full("CBOR:ActionsDescription")
	if strings.Contains(src, "compositeWith") || (strings.Contains(actions, "Edited by") && !strings.Contains(actions, "Created by")) {
		g.Kind = "edited"
	}
	claim := ix.full("CBOR:Claim_Generator_InfoName")
	g.Tool = claimTools[claim]
	agent := firstLine(ix.full("CBOR:ActionsSoftwareAgentName"))
	if agent == "" {
		agent = firstLine(ix.full("CBOR:ActionsSoftwareAgent"))
	}
	if g.Tool == "" {
		switch {
		case agent != "" && agent != "API":
			g.Tool = strings.ReplaceAll(agent, "_", " ")
		case claim != "" && claim != "c2pa-rs":
			g.Tool = strings.TrimPrefix(claim, "fal-ai/")
		}
	}
	if v := ix.full("CBOR:Claim_Generator_InfoVersion"); v != "" && !strings.Contains(v, ":") {
		g.ToolVersion = v
	}
	model := firstLine(ix.full("CBOR:ActionsParametersModel_Name"))
	if model == "" {
		model = firstLine(ix.full("CBOR:ModelName"))
	}
	if model == "" && agent != "" && agent != g.Tool && agent != "API" {
		model = agent
	}
	g.Model = model
	if strings.Contains(actions, "SynthID") {
		g.Settings["watermark"] = "SynthID"
	}
	g.Prompt = describedPrompt(ix)
	return g
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(s)
}

// describedPrompt is a description that reads like a prompt, for items already shown to be AI
// by Content Credentials or IPTC. It's never evidence on its own.
func describedPrompt(ix index) string {
	for _, name := range []string{"imagedescription", "description"} {
		s := strings.TrimSpace(ix.get(name))
		if len(s) > 15 && !strings.HasPrefix(s, "Signature:") && !strings.HasPrefix(s, "<") && !strings.HasPrefix(s, "{") &&
			!strings.HasPrefix(s, "Input ingredient") {
			return s
		}
	}
	return ""
}

// Summary is a one-line description, for logs and tests.
func (g *Generation) Summary() string {
	if g == nil {
		return "none"
	}
	cfg := ""
	if g.CFG != 0 && !math.IsNaN(g.CFG) {
		cfg = fmt.Sprintf(" cfg=%g", g.CFG)
	}
	return fmt.Sprintf("%s/%s tool=%q model=%q loras=%d steps=%d%s seed=%q prompt=%d chars needsFile=%v",
		g.Kind, g.Source, g.Tool, g.Model, len(g.Loras), g.Steps, cfg, g.Seed, len(g.Prompt), g.NeedsFile)
}
