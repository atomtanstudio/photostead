package immich

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/google/uuid"
)

func TestMatcher(t *testing.T) {
	images, uploads, other := uuid.New(), uuid.New(), uuid.New()
	ids := map[string]uuid.UUID{}
	file := func(lib uuid.UUID, rel, content string) File {
		id := uuid.New()
		ids[rel] = id
		sum := sha1.Sum([]byte(content))
		return File{Library: lib, RelPath: rel, SHA1: hex.EncodeToString(sum[:]), Asset: id}
	}
	m := newMatcher([]File{
		file(images, "2024/a.jpg", "a"),
		file(images, "2024/b.jpg", "b"),
		file(images, "trips/c.jpg", "c"),
		file(uploads, "admin/2025/x.jpg", "x"),
		file(other, "IMG_1.jpg", "one"),
	})
	external := func(path string) Asset { // Immich hashes an external file's path, not its bytes
		sum := sha1.Sum([]byte("path:" + path))
		return Asset{ID: path, OriginalPath: path, Checksum: base64.StdEncoding.EncodeToString(sum[:])}
	}
	uploaded := func(path, content string) Asset {
		sum := sha1.Sum([]byte(content))
		return Asset{ID: path, OriginalPath: path, Checksum: base64.StdEncoding.EncodeToString(sum[:])}
	}
	assets := []Asset{
		external("/mnt/external/images/2024/a.jpg"),
		external("/mnt/external/images/2024/b.jpg"),
		external("/mnt/external/images/trips/c.jpg"),
		external("/mnt/external/images/excluded/d.jpg"),    // not in Photostead
		external("/mnt/external/somewhere/else/IMG_1.jpg"), // shares only a file name with another library
		uploaded("/data/upload/admin/ff/x.jpg", "x"),       // Immich's upload folder, laid out differently
	}
	m.learn(assets)
	if len(m.folders) != 1 || m.folders[0].Prefix != "/mnt/external/images/" || m.folders[0].Library != images {
		t.Fatalf("folders = %+v, want only /mnt/external/images/ -> images", m.folders)
	}
	cases := []struct {
		asset Asset
		want  string // relative path of the expected item, or "" for none
		how   string
	}{
		{assets[0], "2024/a.jpg", "path"},
		{assets[2], "trips/c.jpg", "path"},
		{assets[3], "", ""},
		{assets[4], "", ""},
		{assets[5], "admin/2025/x.jpg", "content"},
	}
	for _, c := range cases {
		id, how, ok := m.match(c.asset)
		if c.want == "" {
			if ok {
				t.Errorf("%s matched %v, want no match", c.asset.OriginalPath, id)
			}
			continue
		}
		if !ok || id != ids[c.want] || how != c.how {
			t.Errorf("%s = %v by %q (%v), want %s by %q", c.asset.OriginalPath, id, how, ok, c.want, c.how)
		}
	}
}

func TestMissingPermission(t *testing.T) {
	err := &Error{Status: 403, Message: "Missing required permission: album.read"}
	if p := MissingPermission(err); p != "album.read" {
		t.Errorf("got %q", p)
	}
	if p := MissingPermission(&Error{Status: 401, Message: "Invalid API key"}); p != "" {
		t.Errorf("got %q for a bad key", p)
	}
}
