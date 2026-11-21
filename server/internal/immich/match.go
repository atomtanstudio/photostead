package immich

import (
	"encoding/base64"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// File is one of Photostead's files, as the matcher sees it.
type File struct {
	Library uuid.UUID
	RelPath string
	SHA1    string // hex
	Asset   uuid.UUID
}

// matcher finds the Photostead item for an Immich item. Immich's paths are its container's
// (/mnt/external/images/…, /data/library/…), so the folders are learned from the files
// themselves: an Immich path that ends in a library's relative path votes for that folder
// being that library. Items in Immich's own upload folder also match by the SHA-1 of their
// bytes; items in Immich external libraries can't, because Immich hashes their path instead.
type matcher struct {
	byLib   map[uuid.UUID]map[string]uuid.UUID // library -> relative path -> item
	byRel   map[string][]uuid.UUID             // relative path -> libraries holding it
	bySHA1  map[string]uuid.UUID
	folders []folder // learned, longest first
}

type folder struct {
	Prefix  string    // an Immich path prefix, ending in /
	Library uuid.UUID // the Photostead library it is
	Votes   int
}

// minVotes is how many items must agree before a folder is trusted, so one file that happens
// to share a name with another library's file can't map a whole folder.
const minVotes = 2

func newMatcher(files []File) *matcher {
	m := &matcher{byLib: map[uuid.UUID]map[string]uuid.UUID{}, byRel: map[string][]uuid.UUID{}, bySHA1: map[string]uuid.UUID{}}
	for _, f := range files {
		lib := m.byLib[f.Library]
		if lib == nil {
			lib = map[string]uuid.UUID{}
			m.byLib[f.Library] = lib
		}
		lib[f.RelPath] = f.Asset
		m.byRel[f.RelPath] = append(m.byRel[f.RelPath], f.Library)
		if f.SHA1 != "" {
			m.bySHA1[f.SHA1] = f.Asset
		}
	}
	return m
}

// learn works out which Immich folder is which library from a set of Immich items.
func (m *matcher) learn(assets []Asset) {
	votes := map[folder]int{}
	for _, a := range assets {
		p := a.OriginalPath
		// Try the longest relative path first: the more of the path that agrees, the surer.
		for i := 0; i < len(p); i++ {
			if p[i] != '/' {
				continue
			}
			rel := p[i+1:]
			if libs := m.byRel[rel]; len(libs) == 1 {
				votes[folder{Prefix: p[:i+1], Library: libs[0]}]++
				break
			} else if len(libs) > 1 {
				break // the same path in two libraries says nothing
			}
		}
	}
	m.folders = m.folders[:0]
	for f, n := range votes {
		if n >= minVotes {
			f.Votes = n
			m.folders = append(m.folders, f)
		}
	}
	sort.Slice(m.folders, func(i, j int) bool {
		if len(m.folders[i].Prefix) != len(m.folders[j].Prefix) {
			return len(m.folders[i].Prefix) > len(m.folders[j].Prefix)
		}
		return m.folders[i].Prefix < m.folders[j].Prefix
	})
}

// match returns the Photostead item for an Immich item, and how it was found ("path" or
// "content").
func (m *matcher) match(a Asset) (uuid.UUID, string, bool) {
	for _, f := range m.folders {
		if rel, ok := strings.CutPrefix(a.OriginalPath, f.Prefix); ok {
			if id, ok := m.byLib[f.Library][rel]; ok {
				return id, "path", true
			}
		}
	}
	if sum, err := base64.StdEncoding.DecodeString(a.Checksum); err == nil && len(sum) == 20 {
		if id, ok := m.bySHA1[hex.EncodeToString(sum)]; ok {
			return id, "content", true
		}
	}
	return uuid.Nil, "", false
}
