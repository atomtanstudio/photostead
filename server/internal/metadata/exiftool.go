// Package metadata reads embedded metadata with exiftool and normalizes the parts Photostead indexes.
package metadata

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Tags is exiftool's JSON for one file: "Group:Tag" -> value, with numeric values unformatted (-n).
type Tags map[string]any

// Reader keeps a pool of long-running exiftool processes (-stay_open), so files don't pay
// exiftool's start-up cost. It only ever passes read arguments: it never writes to a file.
type Reader struct {
	pool chan *proc
}

type proc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *bytes.Buffer
	seq    int
}

func NewReader(size int) (*Reader, error) {
	r := &Reader{pool: make(chan *proc, size)}
	for i := 0; i < size; i++ {
		p, err := startProc()
		if err != nil {
			r.Close()
			return nil, err
		}
		r.pool <- p
	}
	return r, nil
}

func startProc() (*proc, error) {
	cmd := exec.Command("exiftool", "-stay_open", "True", "-@", "-")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &syncWriter{w: &stderr}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start exiftool: %w", err)
	}
	return &proc{cmd: cmd, stdin: stdin, stdout: bufio.NewReaderSize(stdout, 1<<20), stderr: &stderr}, nil
}

// Read returns the tags of one file.
func (r *Reader) Read(ctx context.Context, path string) (Tags, error) {
	// exiftool takes its arguments a line at a time: a name with a line break (or starting with a
	// dash) would be read as more arguments, and some of those run code. Such a file is read
	// through a link with a plain name.
	if strings.ContainsAny(path, "\r\n") || strings.HasPrefix(path, "-") {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		dir, err := os.MkdirTemp("", "photostead-exif-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		ext := strings.Map(func(c rune) rune {
			if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
				return c
			}
			return -1
		}, strings.ToLower(filepath.Ext(abs)))
		link := filepath.Join(dir, "file."+ext)
		if err := os.Symlink(abs, link); err != nil {
			return nil, err
		}
		path = link
	}
	var p *proc
	select {
	case p = <-r.pool:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	tags, err := p.read(path)
	if err != nil && !errors.Is(err, errNoTags) {
		// The process may be wedged; replace it.
		p.kill()
		if np, startErr := startProc(); startErr == nil {
			p = np
		}
	}
	r.pool <- p
	return tags, err
}

var errNoTags = errors.New("exiftool returned no metadata")

func (p *proc) read(path string) (Tags, error) {
	p.seq++
	args := []string{
		"-json", "-G1", "-n", "-fast", // groups in keys, raw numbers, don't scan to the end of JPEGs
		"-api", "LargeFileSupport=1",
		"-charset", "filename=utf8",
		path,
		"-execute" + strconv.Itoa(p.seq),
	}
	if _, err := io.WriteString(p.stdin, strings.Join(args, "\n")+"\n"); err != nil {
		return nil, err
	}
	ready := "{ready" + strconv.Itoa(p.seq) + "}"
	var out bytes.Buffer
	for {
		line, err := p.stdout.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("exiftool: %w", err)
		}
		if strings.TrimSpace(line) == ready {
			break
		}
		out.WriteString(line)
	}
	if out.Len() == 0 {
		return nil, errNoTags
	}
	var list []Tags
	if err := json.Unmarshal(out.Bytes(), &list); err != nil {
		return nil, fmt.Errorf("exiftool output: %w", err)
	}
	if len(list) == 0 {
		return nil, errNoTags
	}
	return list[0], nil
}

func (p *proc) kill() {
	_ = p.stdin.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	_ = p.cmd.Wait()
}

func (r *Reader) Close() {
	for {
		select {
		case p := <-r.pool:
			_, _ = io.WriteString(p.stdin, "-stay_open\nFalse\n")
			_ = p.stdin.Close()
			_ = p.cmd.Wait()
		default:
			return
		}
	}
}

type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if bb, ok := s.w.(*bytes.Buffer); ok && bb.Len() > 64<<10 {
		bb.Reset() // keep only recent warnings
	}
	return s.w.Write(b)
}
