package api

import (
	"testing"

	"github.com/google/uuid"
)

func TestFuse(t *testing.T) {
	ids := map[string]uuid.UUID{}
	item := func(n string) TimelineAsset {
		if _, ok := ids[n]; !ok {
			ids[n] = uuid.New()
		}
		return TimelineAsset{ID: ids[n]}
	}
	got := fuse(3,
		[]TimelineAsset{item("a"), item("b"), item("c")}, // by meaning
		[]TimelineAsset{item("c"), item("d")},            // by prompt words
		nil,                                              // by names: nothing
	)
	want := []string{"c", "a", "b"} // c is in two lists; a leads its list; b and d tie on rank, then by id
	if len(got) != 3 || got[0].ID != ids["c"] || got[1].ID != ids["a"] {
		t.Errorf("fuse = %v, want %v first", got, want[:2])
	}
}
