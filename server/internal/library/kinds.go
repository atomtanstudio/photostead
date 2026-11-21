// Package library finds files in a library folder and hashes them. It only ever opens files for reading.
package library

import (
	"path/filepath"
	"strings"
)

var imageExt = set("jpg", "jpeg", "jpe", "png", "gif", "webp", "heic", "heif", "hif", "avif", "tif", "tiff", "bmp", "jxl", "svg",
	"dng", "cr2", "cr3", "crw", "nef", "nrw", "arw", "srf", "sr2", "orf", "rw2", "raf", "pef", "srw", "3fr", "erf", "kdc", "mrw", "x3f", "iiq", "raw")

var videoExt = set("mp4", "m4v", "mov", "qt", "mkv", "webm", "avi", "wmv", "asf", "3gp", "3g2", "mts", "m2ts", "m2t", "ts", "mpg", "mpeg", "mpe", "flv", "ogv", "vob", "mxf", "insv")

// Kind classifies a file by extension: "image", "video", "sidecar" or "other".
func Kind(name string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	switch {
	case imageExt[ext]:
		return "image"
	case videoExt[ext]:
		return "video"
	case ext == "xmp":
		return "sidecar"
	}
	return "other"
}

// skipDir reports folders that never hold library content: hidden folders, NAS and OS housekeeping.
func skipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch strings.ToLower(name) {
	case "@eadir", "#recycle", "#snapshot", "$recycle.bin", "system volume information", "lost+found":
		return true
	}
	return false
}

// skipFile reports OS clutter that isn't worth recording at all.
func skipFile(name string) bool {
	if strings.HasPrefix(name, "._") || name == ".DS_Store" {
		return true
	}
	switch strings.ToLower(name) {
	case "thumbs.db", "desktop.ini", ".picasa.ini", "picasa.ini":
		return true
	}
	return false
}

func set(xs ...string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}
