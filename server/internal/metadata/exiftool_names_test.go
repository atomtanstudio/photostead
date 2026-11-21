package metadata

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A file name with line breaks is read as a file, not as more of exiftool's arguments.
func TestReadOddName(t *testing.T) {
	dir := t.TempDir()
	// Read as arguments, "-if 0" would skip the file and nothing would come back.
	name := filepath.Join(dir, "x\n-if\n0\nclip.png")
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde\x00\x00\x00\x0cIDATx\x9cc\xf8\xcf\xc0\x00\x00\x03\x01\x01\x00\xc9\xfe\x92\xef\x00\x00\x00\x00IEND\xaeB`\x82")
	if err := os.WriteFile(name, png, 0o644); err != nil {
		t.Skip("this filesystem doesn't allow line breaks in names")
	}
	r, err := NewReader(1)
	if err != nil {
		t.Skip("no exiftool:", err)
	}
	defer r.Close()
	tags, err := r.Read(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) == 0 {
		t.Error("no tags read through the link")
	}
}
