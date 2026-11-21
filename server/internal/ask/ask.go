// Package ask turns a plain-language question about the library into a plan of filters, and
// writes the answer. It's deterministic (the planner from the Lightbox prototype, ported): dates,
// media type, favorites, people, AI characters, models and LoRAs, where an item was made, words in
// its prompt, text in the picture, file names and utility kinds (screenshots, receipts…). Whatever
// is left over describes what the items look like, and is matched by meaning.
//
// The plan is plain data, so the app can send it back with a follow-up ("only videos") or with
// filters to drop, and an LLM planner could produce the same shape later.
package ask

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Named names a person or character.
type Named struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// Plan is what a question asks for. Dates are local days (YYYY-MM-DD); Before is exclusive.
type Plan struct {
	Intent    string      `json:"intent" enum:"list,count,first,last"`
	Type      string      `json:"type,omitempty" enum:"image,video"`
	People    []Named     `json:"people,omitempty"`
	Character *Named      `json:"character,omitempty"`
	Model     string      `json:"model,omitempty" doc:"A model or LoRA, as the question named it"`
	ModelIDs  []uuid.UUID `json:"modelIds,omitempty" doc:"The models and LoRAs whose names contain it"`
	Source    string      `json:"source,omitempty" doc:"Where it was made: comfyui, a1111, midjourney, grok, openai, google"`
	Prompt    string      `json:"prompt,omitempty" doc:"Words in its prompt"`
	OCR       string      `json:"ocr,omitempty" doc:"Text in the picture"`
	Filename  string      `json:"filename,omitempty"`
	Label     string      `json:"label,omitempty" doc:"A utility kind: screenshot, document, receipt, id_card, qr_code"`
	Favorite  bool        `json:"favorite,omitempty"`
	After     string      `json:"after,omitempty"`
	Before    string      `json:"before,omitempty"`
	When      string      `json:"when,omitempty" doc:"The dates, as they'd be said"`
	AIOnly    bool        `json:"aiOnly,omitempty"`
	Text      string      `json:"text,omitempty" doc:"What the items look like (matched by meaning)"`
}

// Model is a model or LoRA a question can name.
type Model struct {
	ID   uuid.UUID
	Kind string // model or lora
	Name string // normalized, lower case
}

// Vocabulary is what the planner recognizes by name.
type Vocabulary struct {
	People     []Named
	Characters []Named
	Models     []Model
}

var months = []string{"january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december"}

var stop = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`show me find all my the of from with a an please any some that which i did do have has when time times how many
		were was made make created generated take took taken in on at during for to and or is are what where who this these those there their our we
		you it its by containing contain contains using used ever only just look looks like see get give list can could would me also about last first
		use uses most recent latest earliest text words word says saying named called file filename prompt prompts lora loras model models checkpoint
		checkpoints kind type stuff things ones one something anything every each library collection items item say said read reads
		files shot shots`) {
		stop[w] = true
	}
}

// Model families worth recognizing even before the exact model is known.
var families = []string{"flux", "qwen", "wan", "sdxl", "sd15", "pony", "illustrious", "hunyuan", "ltx", "minimax", "krea", "chroma", "hidream",
	"kolors", "lustify", "juggernaut", "realvis", "epicrealism", "cyberrealistic", "zimage", "seedream", "dmd2", "lightx2v", "fusionx"}

// Where things are made: the words people use, the value stored (a generation's source, or part
// of its tool's name), and how to say it.
var sources = map[string]string{"comfyui": "comfyui", "comfy": "comfyui", "a1111": "a1111", "automatic1111": "a1111", "forge": "a1111",
	"midjourney": "midjourney", "grok": "grok", "chatgpt": "openai", "openai": "openai", "dall-e": "openai", "dalle": "openai", "sora": "openai",
	"google": "google", "gemini": "google", "imagen": "google", "veo": "google"}

// SourceLabel says a source's name.
var SourceLabel = map[string]string{"comfyui": "ComfyUI", "a1111": "A1111 or Forge", "midjourney": "Midjourney", "grok": "Grok",
	"openai": "OpenAI", "google": "Google AI"}

// Utility kinds, as said, and their plural for answers.
var labelWords = []struct {
	re            *regexp.Regexp
	label, plural string
}{
	{regexp.MustCompile(`\b(screen ?shots?)\b`), "screenshot", "screenshots"},
	{regexp.MustCompile(`\b(receipts?)\b`), "receipt", "receipts"},
	{regexp.MustCompile(`\b(documents?|scans|scanned (?:pages?|documents?))\b`), "document", "documents"},
	{regexp.MustCompile(`\b(id cards?|identity cards?|driver'?s? licen[cs]es?|passports?)\b`), "id_card", "IDs"},
	{regexp.MustCompile(`\b(qr ?codes?)\b`), "qr_code", "QR codes"},
}

var (
	reCount   = regexp.MustCompile(`\bhow many\b|\bcount\b|\bnumber of\b`)
	reFirst   = regexp.MustCompile(`\bfirst time\b|\bearliest\b|\boldest\b|\bwhen did i (?:first|start)\b`)
	reLast    = regexp.MustCompile(`\blast time\b|\bmost recent\b|\blatest\b|\bnewest\b|\bwhen did i last\b|\bwhen was the last\b`)
	rePromptQ = regexp.MustCompile(`\bprompt(?:ed)?(?: with| containing| that says)?\s+"([^"]+)"`)
	reFileQ   = regexp.MustCompile(`\b(?:file(?:name)?|named|called)\s+"([^"]+)"`)
	reQuoted  = regexp.MustCompile(`"([^"]+)"`)
	reSays    = regexp.MustCompile(`\b(?:with the (?:text|words?)|that (?:says|say|reads|read)|saying)\s+([a-z0-9][a-z0-9 '-]{0,60})`)
	rePrompt  = regexp.MustCompile(`\bprompts?\s+(?:with|containing|about|mentioning)\s+([a-z0-9][a-z0-9 ,'-]{0,80})`)
	reFav     = regexp.MustCompile(`\b(favou?rites?|favou?rited|starred|liked)\b`)
	reVideo   = regexp.MustCompile(`\b(videos?|clips?|movies?|footage)\b`)
	rePhoto   = regexp.MustCompile(`\b(photos?|pictures?|pics?|images?|stills?|renders?)\b`)
	reToday   = regexp.MustCompile(`\btoday\b`)
	reYest    = regexp.MustCompile(`\byesterday\b`)
	reRel     = regexp.MustCompile(`\b(this|last|past)\s+(week|month|year)\b`)
	reLastN   = regexp.MustCompile(`\b(?:in the )?(?:last|past)\s+(\d+)\s+(days?|weeks?|months?|years?)\b`)
	reBetween = regexp.MustCompile(`\bbetween\s+((?:19|20)\d{2})\s+and\s+((?:19|20)\d{2})\b`)
	reSince   = regexp.MustCompile(`\bsince\s+((?:19|20)\d{2})\b`)
	reBefore  = regexp.MustCompile(`\bbefore\s+((?:19|20)\d{2})\b`)
	reMonth   = regexp.MustCompile(`\b(in\s+)?(january|february|march|april|may|june|july|august|september|october|november|december)(?:\s+((?:19|20)\d{2}))?\b`)
	reSeason  = regexp.MustCompile(`\b(?:(last|this)\s+)?(summer|winter|spring|autumn|fall)(?:\s+((?:19|20)\d{2}))?\b`)
	reYear    = regexp.MustCompile(`\b(?:in\s+)?((?:19|20)\d{2})\b`)
	reSource  = regexp.MustCompile(`\b(comfyui|comfy|a1111|automatic1111|forge|midjourney|grok|chatgpt|openai|dall-e|dalle|sora|google|gemini|imagen|veo)\b`)
	reAI      = regexp.MustCompile(`\b(ai[- ]generated|ai[- ]made|generated|ai)\b`)
	reModelK  = regexp.MustCompile(`\b([a-z0-9][a-z0-9._-]{2,40})\s+(?:loras?|models?|checkpoints?)\b`)
	reModelW  = regexp.MustCompile(`\b(?:with|using|in|on|via)\s+([a-z0-9][a-z0-9._-]{2,40})\b`)
	reFamily  = regexp.MustCompile(`\b(` + strings.Join(families, "|") + `)\b`)
	reGen     = regexp.MustCompile(`\b(lora|model|prompt|render)`)
	reSpaces  = regexp.MustCompile(`\s+`)
	rePunct   = regexp.MustCompile(`[?!.,;]+(\s|$)`)
)

// tailStops end a free-text phrase ("that says open late in june" → "open late").
var tailStops = map[string]bool{"in": true, "from": true, "on": true, "during": true, "since": true, "before": true, "after": true, "with": true}

// cutTail keeps a phrase up to the first word that starts a new part of the question, and returns
// the rest so it can still be read.
func cutTail(phrase string) (kept, rest string) {
	words := strings.Fields(phrase)
	for i, w := range words {
		if i > 0 && tailStops[w] {
			return strings.Join(words[:i], " "), strings.Join(words[i:], " ")
		}
	}
	return strings.Join(words, " "), ""
}

type parser struct{ s string }

// take replaces each match of re with what fn returns (fn gets the submatches; returning m[0]
// keeps the text as it was).
func (p *parser) take(re *regexp.Regexp, fn func(m []string) string) {
	var b strings.Builder
	last := 0
	for _, ix := range re.FindAllStringSubmatchIndex(p.s, -1) {
		m := make([]string, len(ix)/2)
		for i := range m {
			if ix[2*i] >= 0 {
				m[i] = p.s[ix[2*i]:ix[2*i+1]]
			}
		}
		b.WriteString(p.s[last:ix[0]])
		b.WriteString(fn(m))
		last = ix[1]
	}
	b.WriteString(p.s[last:])
	p.s = b.String()
}

func day(t time.Time) string { return t.Format("2006-01-02") }

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func wordRe(w string) *regexp.Regexp { return regexp.MustCompile(`\b` + regexp.QuoteMeta(w) + `\b`) }

// Parse plans a question. now is the viewer's current time, in their time zone.
func Parse(q string, now time.Time, v Vocabulary) Plan {
	plan := Plan{Intent: "list"}
	lower := strings.NewReplacer("“", `"`, "”", `"`, "‘", "'", "’", "'").Replace(strings.ToLower(q))
	p := &parser{s: " " + rePunct.ReplaceAllString(lower, " ") + " "}
	y, mo, d := now.Date()
	loc := now.Location()
	date := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, loc) }
	rng := func(a, b time.Time, label string) {
		plan.After, plan.Before, plan.When = "", "", label
		if !a.IsZero() {
			plan.After = day(a)
		}
		if !b.IsZero() {
			plan.Before = day(b)
		}
	}

	switch {
	case reCount.MatchString(p.s):
		plan.Intent = "count"
	case reFirst.MatchString(p.s):
		plan.Intent = "first"
	case reLast.MatchString(p.s):
		plan.Intent = "last"
	}

	p.take(rePromptQ, func(m []string) string { plan.Prompt, plan.AIOnly = m[1], true; return " " })
	p.take(reFileQ, func(m []string) string { plan.Filename = m[1]; return " " })
	p.take(reQuoted, func(m []string) string { plan.OCR = m[1]; return " " })
	p.take(reSays, func(m []string) string {
		kept, rest := cutTail(m[1])
		plan.OCR = kept
		return " " + rest + " "
	})
	p.take(rePrompt, func(m []string) string {
		kept, rest := cutTail(m[1])
		plan.Prompt, plan.AIOnly = strings.Trim(kept, " ,"), true
		return " " + rest + " "
	})
	p.take(reFav, func([]string) string { plan.Favorite = true; return " " })
	for _, l := range labelWords {
		p.take(l.re, func([]string) string { plan.Label = l.label; return " " })
	}
	p.take(reVideo, func([]string) string { plan.Type = "video"; return " " })
	p.take(rePhoto, func(m []string) string {
		if plan.Type == "" {
			plan.Type = "image"
		}
		if strings.HasPrefix(m[1], "render") {
			plan.AIOnly = true
		}
		return " "
	})

	// Dates, most specific first.
	p.take(reToday, func([]string) string { rng(date(y, mo, d), time.Time{}, "today"); return " " })
	p.take(reYest, func([]string) string { rng(date(y, mo, d-1), date(y, mo, d), "yesterday"); return " " })
	p.take(reRel, func(m []string) string {
		which, unit := m[1], m[2]
		switch unit {
		case "week":
			back := int(now.Weekday())
			if which != "this" {
				back += 7
			}
			a := date(y, mo, d-back)
			if which == "this" {
				rng(a, time.Time{}, "this week")
			} else {
				rng(a, a.AddDate(0, 0, 7), "last week")
			}
		case "month":
			if which == "this" {
				rng(date(y, mo, 1), time.Time{}, titleCase(months[mo-1]))
			} else {
				a := date(y, mo-1, 1)
				rng(a, date(y, mo, 1), titleCase(months[a.Month()-1])+" "+strconv.Itoa(a.Year()))
			}
		default:
			yy := y
			if which != "this" {
				yy--
			}
			rng(date(yy, 1, 1), date(yy+1, 1, 1), strconv.Itoa(yy))
		}
		return " "
	})
	p.take(reLastN, func(m []string) string {
		n, _ := strconv.Atoi(m[1])
		a := now
		switch {
		case strings.HasPrefix(m[2], "day"):
			a = now.AddDate(0, 0, -n)
		case strings.HasPrefix(m[2], "week"):
			a = now.AddDate(0, 0, -7*n)
		case strings.HasPrefix(m[2], "month"):
			a = now.AddDate(0, -n, 0)
		default:
			a = now.AddDate(-n, 0, 0)
		}
		rng(date(a.Year(), a.Month(), a.Day()), time.Time{}, fmt.Sprintf("the last %d %s", n, m[2]))
		return " "
	})
	p.take(reBetween, func(m []string) string {
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		rng(date(a, 1, 1), date(b+1, 1, 1), m[1]+" to "+m[2])
		return " "
	})
	p.take(reSince, func(m []string) string {
		a, _ := strconv.Atoi(m[1])
		rng(date(a, 1, 1), time.Time{}, "since "+m[1])
		return " "
	})
	p.take(reBefore, func(m []string) string {
		a, _ := strconv.Atoi(m[1])
		rng(time.Time{}, date(a, 1, 1), "before "+m[1])
		return " "
	})
	p.take(reMonth, func(m []string) string {
		// "may" is a month only as "in may" or "may 2024".
		if m[2] == "may" && m[1] == "" && m[3] == "" {
			return m[0]
		}
		mi := time.Month(1)
		for i, name := range months {
			if name == m[2] {
				mi = time.Month(i + 1)
			}
		}
		yy := y
		if m[3] != "" {
			yy, _ = strconv.Atoi(m[3])
		} else if mi > mo {
			yy-- // a month still to come this year means last year's
		}
		rng(date(yy, mi, 1), date(yy, mi+1, 1), fmt.Sprintf("%s %d", titleCase(m[2]), yy))
		return " "
	})
	p.take(reSeason, func(m []string) string {
		start := map[string]time.Month{"spring": 3, "summer": 6, "autumn": 9, "fall": 9, "winter": 12}[m[2]]
		yy := y
		if m[3] != "" {
			yy, _ = strconv.Atoi(m[3])
		} else if date(yy, start+3, 1).After(now) {
			yy-- // not over yet this year: the last one
		}
		rng(date(yy, start, 1), date(yy, start+3, 1), fmt.Sprintf("%s %d", m[2], yy))
		return " "
	})
	p.take(reYear, func(m []string) string {
		a, _ := strconv.Atoi(m[1])
		rng(date(a, 1, 1), date(a+1, 1, 1), m[1])
		return " "
	})

	// Where and how it was made.
	p.take(reSource, func(m []string) string { plan.Source, plan.AIOnly = sources[m[1]], true; return " " })
	p.take(reAI, func([]string) string { plan.AIOnly = true; return " " })
	models := func(t string) []uuid.UUID {
		var ids []uuid.UUID
		for _, m := range v.Models {
			if strings.Contains(m.Name, t) {
				ids = append(ids, m.ID)
			}
		}
		return ids
	}
	isFamily := func(t string) bool {
		for _, f := range families {
			if f == t {
				return true
			}
		}
		return false
	}
	known := func(t string) bool { return isFamily(t) || (len(t) >= 4 && len(models(t)) > 0) }
	setModel := func(t string) {
		plan.Model, plan.ModelIDs, plan.AIOnly = t, models(t), true
	}
	p.take(reModelK, func(m []string) string {
		if !known(m[1]) {
			return m[0]
		}
		setModel(m[1])
		return " "
	})
	p.take(reModelW, func(m []string) string {
		if plan.Model != "" || !known(m[1]) {
			return m[0]
		}
		setModel(m[1])
		return " "
	})
	p.take(reFamily, func(m []string) string {
		if plan.Model == "" {
			setModel(m[1])
		}
		return " "
	})

	// Characters and people, by name: the longest names first, the full name or a first name of
	// three letters or more. A name that is both goes to the character when the question is about
	// AI work (or there's no such person).
	genContext := plan.AIOnly || reGen.MatchString(lower)
	type hit struct {
		ref  Named
		said string
	}
	find := func(refs []Named) []hit {
		sorted := append([]Named(nil), refs...)
		sort.SliceStable(sorted, func(i, j int) bool { return len(sorted[i].Name) > len(sorted[j].Name) })
		var hits []hit
		for _, r := range sorted {
			full := strings.ToLower(strings.TrimSpace(r.Name))
			first := strings.Fields(full)
			for _, n := range []string{full, firstOf(first)} {
				if len(n) >= 3 && wordRe(n).MatchString(p.s) {
					hits = append(hits, hit{r, n})
					break
				}
			}
		}
		return hits
	}
	chars, people := find(v.Characters), find(v.People)
	if len(chars) > 0 && (genContext || len(people) == 0) {
		c := chars[0]
		plan.Character, plan.AIOnly = &Named{c.ref.ID, c.ref.Name}, true
		p.take(wordRe(c.said), func([]string) string { return " " })
		if parts := strings.Fields(strings.ToLower(c.ref.Name)); len(parts) > 1 {
			p.take(wordRe(strings.Join(parts[1:], " ")), func([]string) string { return " " })
		}
	} else {
		for _, h := range people {
			plan.People = append(plan.People, h.ref)
			p.take(wordRe(h.said), func([]string) string { return " " })
		}
	}

	var words []string
	for _, w := range strings.Fields(p.s) {
		if w != "" && !stop[w] && len(w) > 1 {
			if _, err := strconv.Atoi(w); err == nil {
				continue
			}
			words = append(words, w)
		}
	}
	plan.Text = strings.Join(words, " ")
	if !plan.anything() && plan.Text == "" {
		plan.Text = strings.TrimSpace(q)
	}
	return plan
}

func firstOf(words []string) string {
	if len(words) == 0 {
		return ""
	}
	return words[0]
}

func (p Plan) anything() bool {
	return p.Type != "" || len(p.People) > 0 || p.Character != nil || p.Model != "" || p.Source != "" || p.Prompt != "" || p.OCR != "" ||
		p.Filename != "" || p.Label != "" || p.Favorite || p.When != "" || p.AIOnly
}

// Structured says whether the plan has filters beyond a description.
func (p Plan) Structured() bool { return p.anything() }

// refining says a follow-up narrows the last question rather than asking a new one.
var refining = regexp.MustCompile(`^(and|only|just|but|what about|how about|now)\b`)

// FollowUp merges a follow-up into the previous plan: "only videos", "and last year" or "in 2024"
// keep what was asked before; a new subject, or a whole new question, starts over.
func FollowUp(prev Plan, next Plan, q string) Plan {
	hasSubject := len(next.People) > 0 || next.Character != nil || next.Model != "" || next.Prompt != "" || next.OCR != "" || next.Text != "" ||
		next.Filename != "" || next.Label != ""
	short := len(strings.Fields(q)) <= 4
	if !refining.MatchString(strings.ToLower(strings.TrimSpace(q))) && (hasSubject || !short) {
		return next
	}
	m := prev
	m.People = append([]Named(nil), prev.People...)
	if next.Type != "" {
		m.Type = next.Type
	}
	if next.Character != nil {
		m.Character = next.Character
	}
	if next.Model != "" {
		m.Model, m.ModelIDs = next.Model, next.ModelIDs
	}
	for _, f := range []struct {
		dst *string
		src string
	}{{&m.Source, next.Source}, {&m.Prompt, next.Prompt}, {&m.OCR, next.OCR},
		{&m.Filename, next.Filename}, {&m.Label, next.Label}, {&m.Text, next.Text}} {
		if f.src != "" {
			*f.dst = f.src
		}
	}
	if len(next.People) > 0 {
		m.People = next.People
	}
	if next.When != "" {
		m.After, m.Before, m.When = next.After, next.Before, next.When
	}
	m.Favorite = m.Favorite || next.Favorite
	m.AIOnly = m.AIOnly || next.AIOnly
	m.Intent = next.Intent
	return m
}

// Without drops filters by their chip keys.
func (p Plan) Without(keys ...string) Plan {
	for _, k := range keys {
		switch k {
		case "type":
			p.Type = ""
		case "people":
			p.People = nil
		case "character":
			p.Character = nil
		case "model":
			p.Model, p.ModelIDs = "", nil
		case "source":
			p.Source = ""
		case "prompt":
			p.Prompt = ""
		case "ocr":
			p.OCR = ""
		case "filename":
			p.Filename = ""
		case "label":
			p.Label = ""
		case "favorite":
			p.Favorite = false
		case "when":
			p.After, p.Before, p.When = "", "", ""
		case "aiOnly":
			p.AIOnly = false
		case "text":
			p.Text = ""
		}
	}
	// AI-only goes with what implied it.
	if p.Character == nil && p.Model == "" && p.Source == "" && p.Prompt == "" {
		for _, k := range keys {
			if k == "aiOnly" {
				p.AIOnly = false
			}
		}
	}
	return p
}

// Chip is one filter of a plan, shown so it can be removed.
type Chip struct {
	Key   string `json:"key"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

func labelPlural(l string) string {
	for _, w := range labelWords {
		if w.label == l {
			return w.plural
		}
	}
	return l
}

// Chips lists a plan's filters.
func (p Plan) Chips() []Chip {
	c := []Chip{}
	add := func(key, kind, label string) { c = append(c, Chip{key, kind, label}) }
	if p.Label != "" {
		add("label", "Kind", titleCase(labelPlural(p.Label)))
	}
	if p.Type != "" {
		add("type", "Type", map[string]string{"video": "Videos", "image": "Photos and images"}[p.Type])
	}
	if p.Character != nil {
		add("character", "Character", p.Character.Name)
	}
	for _, r := range p.People {
		add("people", "Person", r.Name)
	}
	if p.Model != "" {
		add("model", "Model or LoRA", p.Model)
	}
	if p.Source != "" {
		add("source", "Made in", SourceLabel[p.Source])
	}
	if p.AIOnly && p.Character == nil && p.Model == "" && p.Source == "" && p.Prompt == "" {
		add("aiOnly", "Kind", "AI-generated")
	}
	if p.Prompt != "" {
		add("prompt", "Prompt mentions", p.Prompt)
	}
	if p.OCR != "" {
		add("ocr", "Text in the picture", p.OCR)
	}
	if p.Filename != "" {
		add("filename", "File name", p.Filename)
	}
	if p.When != "" {
		add("when", "When", p.When)
	}
	if p.Favorite {
		add("favorite", "Only", "Favorites")
	}
	if p.Text != "" {
		add("text", "Looks like", p.Text)
	}
	return c
}

// Describe says what a plan asks for, as a noun phrase: "favorite videos of Ada from 2024".
func (p Plan) Describe() string { return p.DescribeN(2) }

// DescribeN says it for n items ("1 video", "3 videos").
func (p Plan) DescribeN(n int) string {
	plainAI := p.AIOnly && p.Character == nil && p.Model == "" && p.Source == "" && p.Prompt == ""
	one := n == 1
	pick := func(single, many string) string {
		if one {
			return single
		}
		return many
	}
	noun := pick("item", "items")
	switch {
	case p.Label != "":
		noun = labelPlural(p.Label)
		if one {
			noun = strings.TrimSuffix(noun, "s")
		}
	case p.Type == "video" && plainAI:
		noun = pick("AI video", "AI videos")
	case p.Type == "video":
		noun = pick("video", "videos")
	case p.Type == "image" && plainAI:
		noun = pick("AI image", "AI images")
	case p.Type == "image" && p.AIOnly:
		noun = pick("image", "images")
	case p.Type == "image":
		noun = pick("photo", "photos")
	case p.AIOnly:
		noun = pick("AI image or video", "AI images and videos")
	}
	if p.Favorite {
		noun = "favorite " + noun
	}
	parts := []string{noun}
	if p.Character != nil {
		parts = append(parts, "of "+p.Character.Name)
	}
	if len(p.People) > 0 {
		names := make([]string, len(p.People))
		for i, r := range p.People {
			names[i] = r.Name
		}
		parts = append(parts, "with "+strings.Join(names, " and "))
	}
	if p.Model != "" {
		parts = append(parts, "made with "+p.Model)
	}
	if p.Source != "" {
		parts = append(parts, "from "+SourceLabel[p.Source])
	}
	if p.Prompt != "" {
		parts = append(parts, "whose prompt mentions “"+p.Prompt+"”")
	}
	if p.OCR != "" {
		parts = append(parts, "with the text “"+p.OCR+"”")
	}
	if p.Filename != "" {
		parts = append(parts, "named like “"+p.Filename+"”")
	}
	switch p.When {
	case "":
	case "today", "yesterday":
		parts = append(parts, p.When)
	default:
		parts = append(parts, "from "+p.When)
	}
	if p.Text != "" {
		parts = append(parts, pick("that looks like “", "that look like “")+p.Text+"”")
	}
	return strings.Join(parts, " ")
}

// Result is what running a plan found.
type Result struct {
	Total          int
	Exact          bool   // false when ranked by likeness (a description was matched by meaning)
	Order          string // date or relevance
	Newest, Oldest string // local days, YYYY-MM-DD
	Alt            *Alt
}

// Alt is the nearest question that does have answers, when one doesn't.
type Alt struct {
	Remove   string `json:"remove" doc:"The chip key to drop"`
	Count    int    `json:"count"`
	Describe string `json:"describe"`
}

func longDay(s string) string {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return "an unknown day"
	}
	return t.Format("Monday 2 January 2006")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func count(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// Answer writes the reply to a question.
func Answer(p Plan, r Result) string {
	what := p.DescribeN(r.Total)
	if r.Total == 0 {
		what = p.Describe()
		if r.Alt != nil {
			return fmt.Sprintf("I couldn’t find any %s, but there %s %s %s.", what, plural(r.Alt.Count, "is", "are"), count(r.Alt.Count), r.Alt.Describe)
		}
		return fmt.Sprintf("I couldn’t find any %s. Try a broader question.", what)
	}
	n := count(r.Total)
	switch {
	case p.Intent == "count" && r.Exact:
		return fmt.Sprintf("You have %s %s.", n, what)
	case p.Intent == "count":
		return fmt.Sprintf("I found %s close matches for %s. Matching by likeness ranks rather than counts, so this isn’t an exact number.", n, what)
	case p.Intent == "last":
		return fmt.Sprintf("The most recent was on %s. There %s %s %s in all.", longDay(r.Newest), plural(r.Total, "is", "are"), n, what)
	case p.Intent == "first":
		return fmt.Sprintf("The earliest was on %s. There %s %s %s in all.", longDay(r.Oldest), plural(r.Total, "is", "are"), n, what)
	case r.Order == "relevance" && r.Total == 1:
		return fmt.Sprintf("Here is the one match for %s, from %s.", p.Describe(), longDay(r.Newest))
	case r.Order == "relevance":
		span := ""
		if r.Oldest != "" && r.Oldest != r.Newest {
			span = fmt.Sprintf(" They range from %s to %s.", longDay(r.Oldest), longDay(r.Newest))
		}
		return fmt.Sprintf("Here are the %s best matches for %s, closest first.%s", n, what, span)
	}
	span := ""
	if r.Oldest != "" && r.Oldest != r.Newest {
		span = ", going back to " + longDay(r.Oldest)
	}
	return fmt.Sprintf("Here %s %s %s. The newest is from %s%s.", plural(r.Total, "is", "are"), n, what, longDay(r.Newest), span)
}

// Relaxable lists the chip keys worth trying without, when a question finds nothing.
func (p Plan) Relaxable() []string {
	var keys []string
	on := map[string]bool{"type": p.Type != "", "when": p.When != "", "model": p.Model != "", "source": p.Source != "", "prompt": p.Prompt != "",
		"favorite": p.Favorite, "people": len(p.People) > 0, "ocr": p.OCR != "", "filename": p.Filename != "", "label": p.Label != "",
		"character": p.Character != nil, "text": p.Text != ""}
	for _, k := range []string{"type", "when", "favorite", "label", "model", "source", "prompt", "ocr", "filename", "people", "character", "text"} {
		if on[k] {
			keys = append(keys, k)
		}
	}
	return keys
}
