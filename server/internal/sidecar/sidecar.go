// Package sidecar writes what people add in Photostead to XMP sidecars, so other apps (Lightroom,
// digiKam, darktable, Bridge) and a future without Photostead keep it (decision 1.4). Originals are
// never written to. A sidecar is <file name>.xmp: beside the original in a writable library, or, for
// a read-only one, in the data folder under sidecars/<library id>/, mirroring the library's paths.
//
// Photostead owns a fixed set of tags and writes all of them each time (empty ones are removed), so a
// sidecar another app made keeps everything else. exiftool does the writing.
package sidecar

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

//go:embed photostead.config
var exiftoolConfig []byte

// The IPTC Digital Source Types for AI-made and AI-changed items.
const (
	trainedMedia   = "http://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia"
	compositeMedia = "http://cv.iptc.org/newscodes/digitalsourcetype/compositeWithTrainedAlgorithmicMedia"
)

// emptyPacket starts a sidecar that doesn't exist yet.
const emptyPacket = `<?xpacket begin="` + "\ufeff" + `" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
 <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
 </rdf:RDF>
</x:xmpmeta>
<?xpacket end="w"?>
`

// Tags is what Photostead writes for an item, as exiftool assignments: every tag it owns, empty when
// there's nothing (which removes an earlier value). worth is false when the owner has added nothing,
// and there's then no need for a sidecar.
func Tags(r db.SidecarItemsRow) (tags []string, worth bool) {
	set := func(tag, value string) { tags = append(tags, "-"+tag+"="+value) }
	list := func(tag string, values []string) {
		if len(values) == 0 {
			set(tag, "")
		}
		for _, v := range values {
			set(tag, v)
		}
	}

	// Marks: Lightroom's and Bridge's conventions (a reject is -1 stars), plus Photostead's own.
	switch {
	case r.Flag.String == "reject":
		set("XMP-xmp:Rating", "-1")
	case r.Rating > 0:
		set("XMP-xmp:Rating", fmt.Sprint(r.Rating))
	default:
		set("XMP-xmp:Rating", "")
	}
	label := ""
	if r.ColorLabel.Valid && r.ColorLabel.String != "" {
		label = strings.ToUpper(r.ColorLabel.String[:1]) + r.ColorLabel.String[1:]
	}
	set("XMP-xmp:Label", label)
	set("XMP-photostead:Favorite", map[bool]string{true: "True", false: ""}[r.Favorite])
	set("XMP-photostead:Flag", r.Flag.String)

	// Keywords: the last part of a nested one ("travel/japan") in dc:subject, and the whole
	// path in Lightroom's hierarchical keywords.
	var subjects, paths []string
	for _, k := range r.Keywords {
		parts := strings.Split(k, "/")
		subjects = append(subjects, parts[len(parts)-1])
		if len(parts) > 1 {
			paths = append(paths, strings.Join(parts, "|"))
		}
	}
	list("XMP-dc:Subject", subjects)
	list("XMP-lr:HierarchicalSubject", paths)
	set("XMP-dc:Description", r.Description.String)

	// A date the owner set; one read from the file is in the file already.
	if r.DateSource == "user" {
		t := r.TakenAt.UTC()
		zone := "Z"
		if r.TakenOffsetMin.Valid {
			off := int(r.TakenOffsetMin.Int16)
			t = t.Add(time.Duration(off) * time.Minute)
			sign := "+"
			if off < 0 {
				sign, off = "-", -off
			}
			zone = fmt.Sprintf("%s%02d:%02d", sign, off/60, off%60)
		}
		stamp := t.Format("2006:01:02 15:04:05") + zone
		set("XMP-photoshop:DateCreated", stamp)
		set("XMP-exif:DateTimeOriginal", stamp)
	} else {
		set("XMP-photoshop:DateCreated", "")
		set("XMP-exif:DateTimeOriginal", "")
	}

	list("XMP-iptcExt:PersonInImage", r.People)

	// How an AI item was made: IPTC 2025.1's fields, and the model in Photostead's namespace.
	source := ""
	switch r.SourceType {
	case "ai_generated":
		source = trainedMedia
	case "ai_edited":
		source = compositeMedia
	}
	prompt := r.Prompt.String
	if prompt == "" {
		prompt = r.GivenPrompt.String
	}
	set("XMP-iptcExt:DigitalSourceType", source)
	set("XMP-iptcExt:AISystemUsed", r.Tool.String)
	set("XMP-iptcExt:AIPromptInformation", prompt)
	set("XMP-photostead:AIModel", r.Model.String)

	set("XMP-photostead:EditRecipe", string(r.Edit))

	worth = r.Favorite || r.Rating > 0 || r.Flag.Valid || label != "" || len(r.Keywords) > 0 || r.Description.String != "" ||
		r.DateSource == "user" || len(r.People) > 0 || len(r.Edit) > 0 || r.GivenPrompt.String != ""
	return tags, worth
}

// Path is where an item's sidecar goes, or "" when its library keeps none.
func Path(r db.SidecarItemsRow, dataDir string) string {
	if !r.RelPath.Valid || !r.RootPath.Valid || !r.LibraryID.Valid || strings.ContainsAny(r.RelPath.String, "\r\n") {
		return ""
	}
	rel := filepath.Clean(filepath.FromSlash(r.RelPath.String))
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	switch r.SidecarMode.String {
	case "beside":
		if !r.ReadOnly.Bool { // never write into a read-only library, whatever its mode says
			return filepath.Join(r.RootPath.String, rel) + ".xmp"
		}
		fallthrough
	case "shadow":
		return filepath.Join(dataDir, "sidecars", r.LibraryID.UUID.String(), rel) + ".xmp"
	}
	return ""
}

// Stats is what a pass did.
type Stats struct{ Written, Unchanged, Skipped, Failed int }

// Write writes the sidecars of up to limit queued items.
func Write(ctx context.Context, s *store.Store, dataDir string, limit int) (Stats, error) {
	var st Stats
	rows, err := s.Q.SidecarItems(ctx, int32(limit))
	if err != nil || len(rows) == 0 {
		return st, err
	}
	type job struct {
		r    db.SidecarItemsRow
		path string
		tags []string
		hash []byte
	}
	var jobs []job
	done := make([]uuid.UUID, 0, len(rows))
	queued := map[uuid.UUID]time.Time{}
	for _, r := range rows {
		queued[r.ID] = r.QueuedAt
		tags, worth := Tags(r)
		path := Path(r, dataDir)
		switch {
		case r.Purged || r.Visibility == "vault" || path == "":
			done = append(done, r.ID) // gone, private, or a library that keeps no sidecars
			st.Skipped++
			continue
		case !worth && r.LastHash == nil:
			done = append(done, r.ID) // nothing to write, and nothing written before
			st.Skipped++
			continue
		}
		sum := sha256.Sum256([]byte(path + "\n" + strings.Join(tags, "\n")))
		if bytes.Equal(sum[:], r.LastHash) {
			done = append(done, r.ID)
			st.Unchanged++
			continue
		}
		jobs = append(jobs, job{r, path, tags, sum[:]})
	}
	if len(jobs) > 0 {
		w, err := newWriter(dataDir)
		if err != nil {
			return st, err
		}
		defer w.close()
		for _, j := range jobs {
			if ctx.Err() != nil {
				break
			}
			if err := prepare(j.path, j.r); err != nil {
				st.Failed++
				done = append(done, j.r.ID) // tried; it's queued again by its next change
				continue
			}
			if err := w.write(j.path, j.tags); err != nil {
				st.Failed++
				done = append(done, j.r.ID)
				continue
			}
			if err := s.Q.SidecarWritten(ctx, db.SidecarWrittenParams{AssetID: j.r.ID, Path: j.path, Hash: j.hash}); err != nil {
				return st, err
			}
			st.Written++
			done = append(done, j.r.ID)
		}
	}
	at := make([]time.Time, len(done))
	for i, id := range done {
		at[i] = queued[id]
	}
	return st, s.Q.SidecarsDone(ctx, db.SidecarsDoneParams{Ids: done, Queued: at})
}

// prepare makes sure a sidecar exists to write into: for a read-only library, starting from the
// sidecar another app left beside the original, if any.
func prepare(path string, r db.SidecarItemsRow) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	start := []byte(emptyPacket)
	beside := filepath.Join(r.RootPath.String, filepath.FromSlash(r.RelPath.String)) + ".xmp"
	if beside != path {
		if b, err := os.ReadFile(beside); err == nil && len(b) < 32<<20 {
			start = b
		}
	}
	return os.WriteFile(path, start, 0o644)
}

// writer is one exiftool kept open for a pass (-stay_open), with Photostead's namespace loaded.
type writer struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *bytes.Buffer
	seq    int
}

func newWriter(dataDir string) (*writer, error) {
	cfg := filepath.Join(dataDir, "config", "photostead.exiftool.config")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(cfg, exiftoolConfig, 0o644); err != nil {
		return nil, err
	}
	cmd := exec.Command("exiftool", "-config", cfg, "-stay_open", "True", "-@", "-")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("exiftool: %w", err)
	}
	return &writer{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), stderr: &stderr}, nil
}

// escape keeps a value on one argument line (exiftool's -ec turns \n back into a line break).
func escape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`, "\r", "").Replace(s)
}

func (w *writer) write(path string, tags []string) error {
	w.seq++
	w.stderr.Reset()
	var b strings.Builder
	b.WriteString("-overwrite_original\n-m\n-ec\n")
	for _, t := range tags {
		b.WriteString(escape(t))
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "%s\n-execute%d\n", path, w.seq)
	if _, err := io.WriteString(w.stdin, b.String()); err != nil {
		return err
	}
	ready := fmt.Sprintf("{ready%d}", w.seq)
	var out strings.Builder
	for {
		line, err := w.stdout.ReadString('\n')
		if err != nil {
			return fmt.Errorf("exiftool stopped: %w", err)
		}
		if strings.TrimSpace(line) == ready {
			break
		}
		out.WriteString(line)
	}
	if s := out.String(); strings.Contains(s, "1 image files updated") || strings.Contains(s, "1 image files unchanged") {
		return nil
	}
	return errors.New(strings.TrimSpace(firstLine(w.stderr.String() + out.String())))
}

func (w *writer) close() {
	_, _ = io.WriteString(w.stdin, "-stay_open\nFalse\n")
	_ = w.stdin.Close()
	done := make(chan struct{})
	go func() { _ = w.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = w.cmd.Process.Kill()
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
