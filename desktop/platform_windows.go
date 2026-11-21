//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const exeSuffix = ".exe"

func defaultPaths() Paths {
	exe, _ := os.Executable()
	data := os.Getenv("LOCALAPPDATA")
	if data == "" {
		home, _ := os.UserHomeDir()
		data = filepath.Join(home, "AppData", "Local")
	}
	// Photostead\Photostead.exe → Photostead\runtime
	return Paths{
		Runtime: filepath.Join(filepath.Dir(exe), "runtime"),
		Data:    filepath.Join(data, "Photostead"),
	}
}

func defaultLibrary() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Pictures", "Photostead Library")
}

func pythonPath(runtime string) string { return filepath.Join(runtime, "python", "python.exe") }

func systemPath() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return strings.Join([]string{filepath.Join(root, "System32"), root, filepath.Join(root, "System32", "Wbem")}, ";")
}

// On Windows Postgres listens on localhost (no Unix sockets for it here), so it has passwords.
func (r *Runtime) initdb(pgdata string) error {
	pw := filepath.Join(r.paths.Data, "initdb-pw.tmp")
	if err := os.WriteFile(pw, []byte(r.cfg.SuperPassword+"\n"), 0o600); err != nil {
		return err
	}
	defer os.Remove(pw)
	cmd := exec.Command(r.pgBin("initdb"), "-D", pgdata, "-U", "postgres", "--encoding=UTF8", "--locale=C",
		"--auth-local=scram-sha-256", "--auth-host=scram-sha-256", "--pwfile="+pw)
	out, err := r.run(cmd)
	if err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}
	return nil
}

func pgListen(r *Runtime) string {
	return fmt.Sprintf(" -c listen_addresses=127.0.0.1 -c port=%d", r.cfg.DBPort)
}

func afterPostgres(r *Runtime) {}

func pgConnArgs(r *Runtime) []string {
	return []string{"-h", "127.0.0.1", "-p", fmt.Sprint(r.cfg.DBPort), "-U", "postgres"}
}

func pgEnv(r *Runtime) []string { return []string{"PGPASSWORD=" + r.cfg.SuperPassword} }

func pgURL(r *Runtime) string {
	return fmt.Sprintf("postgres://photostead:%s@127.0.0.1:%d/photostead?sslmode=disable", r.cfg.DBPassword, r.cfg.DBPort)
}

// platformEnv: libvips and libheif find their modules and plugins under the runtime.
func platformEnv(r *Runtime) []string {
	return []string{
		"VIPSHOME=" + r.paths.Runtime,
		"LIBHEIF_PLUGIN_PATH=" + filepath.Join(r.paths.Runtime, "lib", "libheif"),
	}
}

// hidden: no console window for anything the launcher runs.
func hidden() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}

func openURL(u string) {
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
}

func revealFolder(p string)   { _ = exec.Command("explorer", p).Start() }
func fileManagerName() string { return "Explorer" }

// pickFolder asks for a folder (Windows's own dialog, through PowerShell).
func pickFolder(prompt string) (string, bool) {
	ps := fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms; $d = New-Object System.Windows.Forms.FolderBrowserDialog; $d.Description = '%s'; if ($d.ShowDialog() -eq 'OK') { $d.SelectedPath }`,
		strings.ReplaceAll(prompt, "'", "''"))
	cmd := exec.Command("powershell", "-NoProfile", "-STA", "-Command", ps)
	cmd.SysProcAttr = hidden()
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	p := strings.TrimSpace(string(out))
	return p, p != ""
}

// Keeping the PC awake: a thread that holds ES_SYSTEM_REQUIRED while asked to.
var awake struct {
	sync.Mutex
	stop chan struct{}
}

const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

var setThreadExecutionState = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")

func keepAwake(on bool) {
	awake.Lock()
	defer awake.Unlock()
	if awake.stop != nil {
		close(awake.stop)
		awake.stop = nil
	}
	if !on {
		return
	}
	stop := make(chan struct{})
	awake.stop = stop
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		setThreadExecutionState.Call(esContinuous | esSystemRequired)
		<-stop
		setThreadExecutionState.Call(esContinuous)
	}()
}

// Opening at login: the current user's Run key.
const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func loginItemEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue("Photostead")
	return err == nil
}

func setLoginItem(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		return k.DeleteValue("Photostead")
	}
	exe, _ := os.Executable()
	return k.SetStringValue("Photostead", `"`+exe+`"`)
}
