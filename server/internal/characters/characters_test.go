package characters

import (
	"testing"

	"github.com/google/uuid"
)

func TestName(t *testing.T) {
	for _, c := range []struct {
		patterns []string
		want     string
	}{
		{[]string{"^daniela_ohwxd"}, "Daniela"},
		{[]string{"minjicosta", "min_ji_costa", "ohwxminji"}, "Min Ji Costa"},
		{[]string{"emma", "^3mm4", "ohwxei"}, "Emma"},
		{[]string{"^mina_v1"}, "Mina"},
	} {
		if got := Name(c.patterns); got != c.want {
			t.Errorf("Name(%v) = %q, want %q", c.patterns, got, c.want)
		}
	}
}

func TestMatching(t *testing.T) {
	res, err := Compile([]string{"^daniela_ohwxd", "min_ji_costa", " "})
	if err != nil || len(res) != 2 {
		t.Fatalf("compile: %v %d", err, len(res))
	}
	for name, want := range map[string]bool{
		"daniela_ohwxd_sdxl":                          true,
		"Daniela_OHWXD_v2.safetensors":                true, // names are normalized before matching
		"my_daniela_ohwxd":                            false,
		"loras/chars/Min_Ji_Costa-000004.safetensors": true,
		"detail_tweaker":                              false,
	} {
		if got := Matches(res, name); got != want {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
	if _, err := Compile([]string{"(unclosed"}); err == nil {
		t.Error("a bad pattern compiled")
	}
	if p := Exactly("Ada_v2.safetensors"); p != `^ada_v2$` {
		t.Errorf("Exactly = %q", p)
	}
}

func TestStartImage(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	unique := func(id uuid.UUID) bool { return id == a }
	notUnique := func(uuid.UUID) bool { return false }
	for _, tc := range []struct {
		name  string
		cands []uuid.UUID
		look  map[uuid.UUID]float32
		uniq  func(uuid.UUID) bool
		want  uuid.UUID
	}{
		{"lone candidate, close", []uuid.UUID{a}, map[uuid.UUID]float32{a: 0.93}, notUnique, a},
		{"lone candidate, not alike", []uuid.UUID{a}, map[uuid.UUID]float32{a: 0.6}, unique, uuid.Nil},
		{"several, one clearly closest", []uuid.UUID{a, b, c}, map[uuid.UUID]float32{a: 0.55, b: 0.94, c: 0.71}, notUnique, b},
		{"several, too close to call", []uuid.UUID{a, b}, map[uuid.UUID]float32{a: 0.92, b: 0.90}, notUnique, uuid.Nil},
		{"no looks yet, unique name", []uuid.UUID{a}, nil, unique, a},
		{"no looks yet, shared name", []uuid.UUID{b}, nil, unique, uuid.Nil},
		{"no looks yet, several", []uuid.UUID{a, b}, nil, unique, uuid.Nil},
	} {
		if got := StartImage(tc.cands, tc.look, tc.uniq); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
