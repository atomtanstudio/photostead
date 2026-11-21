package libraries

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagedNeedsAWritableShare(t *testing.T) {
	// No share mounted, or no name: nothing is set up (and nothing touches the database).
	if ok, err := Managed(t.Context(), nil, filepath.Join(t.TempDir(), "missing"), [16]byte{1}, "rich"); ok || err != nil {
		t.Errorf("a missing share: %v %v", ok, err)
	}
	if ok, err := Managed(t.Context(), nil, t.TempDir(), [16]byte{1}, ""); ok || err != nil {
		t.Errorf("no username: %v %v", ok, err)
	}
	ro := t.TempDir()
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(ro, 0o755)
	if os.Getuid() != 0 { // root writes anywhere
		if ok, err := Managed(t.Context(), nil, ro, [16]byte{1}, "rich"); ok || err != nil {
			t.Errorf("a read-only share: %v %v", ok, err)
		}
	}
}
