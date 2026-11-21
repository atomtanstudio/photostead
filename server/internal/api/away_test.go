package api

import (
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

// What a paired device can't do from away names real operations: a renamed one would quietly
// become reachable from the internet.
func TestNotAwayOperationsExist(t *testing.T) {
	_, a := Handler(Options{})
	ops := map[string]bool{}
	for _, item := range a.OpenAPI().Paths {
		for _, op := range []*huma.Operation{item.Get, item.Put, item.Post, item.Patch, item.Delete} {
			if op != nil {
				ops[op.OperationID] = true
			}
		}
	}
	for id := range notAway {
		if !ops[id] {
			t.Errorf("notAway names %q, which isn't an operation", id)
		}
	}
}
