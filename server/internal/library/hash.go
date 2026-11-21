package library

import (
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// ErrChanged means the file changed while it was being read; try again later.
var ErrChanged = errors.New("file changed while reading")

type Hashes struct {
	SHA256 []byte
	SHA1   []byte // Immich's checksum, kept only to match assets during import
	Size   int64
}

// HashFile streams a file once, computing SHA-256 and SHA-1. Used for large files (videos).
func HashFile(path string, wantSize int64, wantMtime time.Time) (Hashes, error) {
	h, _, err := read(path, wantSize, wantMtime, false)
	return h, err
}

// ReadFile reads a whole file into memory in one sequential pass and hashes it. Photos are read
// this way so that decoding and metadata reading don't go back to a slow network share.
func ReadFile(path string, wantSize int64, wantMtime time.Time) (Hashes, []byte, error) {
	return read(path, wantSize, wantMtime, true)
}

// read opens the file read-only; its size and modification time must match before and after.
func read(path string, wantSize int64, wantMtime time.Time, keep bool) (Hashes, []byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return Hashes{}, nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return Hashes{}, nil, err
	}
	if before.Size() != wantSize || !before.ModTime().Equal(wantMtime) {
		return Hashes{}, nil, ErrChanged
	}
	s256, s1 := sha256.New(), sha1.New()
	var data []byte
	var n int64
	if keep {
		data = make([]byte, wantSize)
		var m int
		m, err = io.ReadFull(f, data)
		n = int64(m)
		if err == nil {
			s256.Write(data)
			s1.Write(data)
		}
	} else {
		n, err = io.CopyBuffer(io.MultiWriter(s256, s1), f, make([]byte, 4<<20))
	}
	if err != nil {
		return Hashes{}, nil, fmt.Errorf("read: %w", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		return Hashes{}, nil, err
	}
	if n != wantSize || after.Size() != wantSize || !after.ModTime().Equal(wantMtime) {
		return Hashes{}, nil, ErrChanged
	}
	return Hashes{SHA256: s256.Sum(nil), SHA1: s1.Sum(nil), Size: n}, data, nil
}
