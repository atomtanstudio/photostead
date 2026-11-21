//go:build darwin

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

const exeSuffix = ""

func defaultPaths() Paths {
	home, _ := os.UserHomeDir()
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	// Photostead.app/Contents/MacOS/Photostead → Photostead.app/Contents/Resources/runtime
	return Paths{
		Runtime: filepath.Join(filepath.Dir(exe), "..", "Resources", "runtime"),
		Data:    filepath.Join(home, "Library", "Application Support", "Photostead"),
	}
}

func defaultLibrary() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Pictures", "Photostead Library")
}

func pythonPath(runtime string) string { return filepath.Join(runtime, "python", "bin", "python3") }

func systemPath() string { return "/usr/bin:/bin:/usr/sbin:/sbin" }

// The database's socket: a short path (socket paths are short on macOS), one per data folder.
func socketDir(r *Runtime) string {
	sum := sha256.Sum256([]byte(r.paths.Data))
	return fmt.Sprintf("/tmp/photostead-%d-%s", os.Getuid(), hex.EncodeToString(sum[:4]))
}

func (r *Runtime) initdb(pgdata string) error {
	cmd := exec.Command(r.pgBin("initdb"), "-D", pgdata, "-U", "postgres", "--encoding=UTF8", "--locale=C",
		"--auth-local=trust", "--auth-host=reject")
	out, err := r.run(cmd)
	if err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}
	return nil
}

// pgListen: no network port at all, only the socket.
func pgListen(r *Runtime) string {
	dir := socketDir(r)
	_ = os.MkdirAll(dir, 0o700)
	return fmt.Sprintf(" -c listen_addresses='' -c unix_socket_directories='%s'", dir)
}

// afterPostgres removes the socket's folder once Postgres has stopped.
func afterPostgres(r *Runtime) { _ = os.Remove(socketDir(r)) }

func pgConnArgs(r *Runtime) []string { return []string{"-h", socketDir(r), "-U", "postgres"} }
func pgEnv(r *Runtime) []string      { return nil }
func pgURL(r *Runtime) string {
	return "postgres://photostead@/photostead?host=" + socketDir(r)
}

// platformEnv: libvips finds its HEIC module under the runtime.
func platformEnv(r *Runtime) []string {
	return []string{"VIPSHOME=" + r.paths.Runtime}
}

func hidden() *syscall.SysProcAttr { return nil }

func openURL(u string)        { _ = exec.Command("/usr/bin/open", u).Start() }
func revealFolder(p string)   { _ = exec.Command("/usr/bin/open", p).Start() }
func fileManagerName() string { return "Finder" }

// pickFolder asks for a folder (macOS's own dialog, through AppleScript).
func pickFolder(prompt string) (string, bool) {
	script := fmt.Sprintf(`POSIX path of (choose folder with prompt %q)`, prompt)
	out, err := exec.Command("/usr/bin/osascript", "-e", script).Output()
	if err != nil {
		return "", false
	}
	p := strings.TrimRight(strings.TrimSpace(string(out)), "/")
	return p, p != ""
}

// Keeping the Mac awake: macOS's own caffeinate, while this launcher lives (only on power, as -s).
var awake struct {
	sync.Mutex
	cmd *exec.Cmd
}

func keepAwake(on bool) {
	awake.Lock()
	defer awake.Unlock()
	if awake.cmd != nil {
		_ = awake.cmd.Process.Kill()
		_ = awake.cmd.Wait()
		awake.cmd = nil
	}
	if on {
		c := exec.Command("/usr/bin/caffeinate", "-s", "-w", fmt.Sprint(os.Getpid()))
		if c.Start() == nil {
			awake.cmd = c
		}
	}
}

// Opening at login: a LaunchAgent that opens the app.
func agentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", "com.photostead.desktop.plist")
}

func appPath() string {
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	return filepath.Clean(filepath.Join(filepath.Dir(exe), "..", ".."))
}

func loginItemEnabled() bool {
	_, err := os.Stat(agentPath())
	return err == nil
}

func setLoginItem(on bool) error {
	if !on {
		return os.Remove(agentPath())
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.photostead.desktop</string>
  <key>ProgramArguments</key><array><string>/usr/bin/open</string><string>-a</string><string>%s</string></array>
  <key>RunAtLoad</key><true/>
</dict>
</plist>
`, appPath())
	if err := os.MkdirAll(filepath.Dir(agentPath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(agentPath(), []byte(plist), 0o644)
}
