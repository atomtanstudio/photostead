package provenance

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// TestRealSample parses a sample of a real library's indexed metadata and reports what it finds.
// It only runs when PHOTOSTEAD_RAW_SAMPLE names a file of rows "id<TAB>type<TAB>raw json"
// (Postgres COPY text format).
func TestRealSample(t *testing.T) {
	p := os.Getenv("PHOTOSTEAD_RAW_SAMPLE")
	if p == "" {
		t.Skip("PHOTOSTEAD_RAW_SAMPLE not set")
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<20), 64<<20)
	unescape := strings.NewReplacer(`\\`, `\`, `\n`, "\n", `\t`, "\t", `\r`, "\r")
	count := map[string]int{}
	examples := map[string][]string{}
	var rows, found, needs, cutOnly int
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "\t", 3)
		if len(parts) != 3 {
			continue
		}
		var tags Tags
		if err := json.Unmarshal([]byte(unescape.Replace(parts[2])), &tags); err != nil {
			t.Fatalf("row %d: %v", rows, err)
		}
		rows++
		g, nf := Parse(tags, parts[1] == "video")
		if nf {
			needs++
		}
		if g == nil {
			if nf {
				cutOnly++
			}
			continue
		}
		found++
		key := fmt.Sprintf("%s %s / %s", parts[1], g.Source, g.Tool)
		count[key]++
		count["kind "+g.Kind]++
		count["evidence "+g.Evidence]++
		if g.Model != "" {
			count["model "+g.Model]++
		}
		if g.Prompt == "" {
			count["no prompt: "+g.Source]++
		}
		if len(examples[key]) < 2 {
			ex := g.Summary()
			if g.Prompt != "" {
				ex += "\n      prompt: " + trim(g.Prompt, 160)
			}
			if g.Negative != "" {
				ex += "\n      negative: " + trim(g.Negative, 80)
			}
			for _, l := range g.Loras {
				w := ""
				if l.Weight != nil {
					w = fmt.Sprintf(" %.2f", *l.Weight)
				}
				ex += "\n      lora: " + l.Display + w
			}
			if len(g.Settings) > 0 {
				b, _ := json.Marshal(g.Settings)
				ex += "\n      settings: " + trim(string(b), 160)
			}
			examples[key] = append(examples[key], ex)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d rows: %d with a generation, %d with evidence cut short in the index (%d of those with no generation yet)", rows, found, needs, cutOnly)
	keys := make([]string, 0, len(count))
	for k := range count {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return count[keys[i]] > count[keys[j]] })
	for _, k := range keys {
		if strings.HasPrefix(k, "model ") && count[k] < 15 {
			continue
		}
		t.Logf("%6d  %s", count[k], k)
	}
	ek := make([]string, 0, len(examples))
	for k := range examples {
		ek = append(ek, k)
	}
	sort.Strings(ek)
	for _, k := range ek {
		for _, ex := range examples[k] {
			t.Logf("[%s] %s", k, ex)
		}
	}
}

func trim(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
