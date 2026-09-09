package process

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func StartDetached(name string, args ...string) (int, error) {
	cmd := exec.Command(name, args...)
	return startDetached(cmd)
}

func StartDetachedWithLog(logPath string, name string, args ...string) (int, error) {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	defer logFile.Close()

	cmd := exec.Command(name, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	return startDetached(cmd)
}

func startDetached(cmd *exec.Cmd) (int, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	// The privileged Helper is long-lived. Release only discards the Go handle;
	// it does not reap an exited Unix child, which leaves zombies that signal(0)
	// still considers alive and makes every stop consume its full grace period.
	// Keep exactly one waiter without tying the child to the request's lifetime.
	go func() { _ = cmd.Wait() }()
	return pid, nil
}

func IsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return signalErrMeansAlive(proc.Signal(syscall.Signal(0)))
}

// Fingerprint hashes kernel-recorded process identity for a PID. A PID alone is
// not an identity: it can be reused after a child exits or after the host
// reboots. On Linux we read procfs directly so the QNAP runtime does not depend
// on procps/ps being installed in the deliberately small container image. Other
// Unix targets retain the ps-based identity used by the original macOS path.
func Fingerprint(pid int) (string, error) {
	if pid <= 0 {
		return "", nil
	}

	var (
		identity string
		err      error
	)
	if runtime.GOOS == "linux" {
		identity, err = linuxProcessIdentity(pid)
	} else {
		identity, err = psProcessIdentity(pid)
	}
	if err != nil {
		return "", err
	}
	if identity == "" {
		return "", nil
	}
	digest := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(digest[:]), nil
}

func linuxProcessIdentity(pid int) (string, error) {
	statPath := fmt.Sprintf("/proc/%d/stat", pid)
	statData, err := os.ReadFile(statPath)
	if err != nil {
		if !IsAlive(pid) {
			return "", nil
		}
		return "", fmt.Errorf("inspect pid %d identity from procfs stat: %w", pid, err)
	}

	// /proc/<pid>/stat field 2 (comm) is parenthesized and may contain spaces or
	// ')' characters. The final ')' terminates comm. Fields after it start at
	// field 3 (state), so field 22 (starttime) is index 19 in this slice.
	statText := string(statData)
	closeComm := strings.LastIndex(statText, ")")
	if closeComm < 0 {
		return "", fmt.Errorf("inspect pid %d identity from procfs stat: malformed comm", pid)
	}
	statFields := strings.Fields(statText[closeComm+1:])
	if len(statFields) <= 19 {
		return "", fmt.Errorf("inspect pid %d identity from procfs stat: missing start time", pid)
	}
	startTime := statFields[19]

	cmdlineData, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		if !IsAlive(pid) {
			return "", nil
		}
		return "", fmt.Errorf("inspect pid %d identity from procfs cmdline: %w", pid, err)
	}
	cmdline := normalizeProcCmdline(cmdlineData)
	if cmdline == "" {
		// Userspace children managed by OpenSurge should have a cmdline. Fall back
		// to comm rather than depending on ps if a process intentionally clears it.
		openComm := strings.Index(statText, "(")
		if openComm >= 0 && openComm < closeComm {
			cmdline = strings.TrimSpace(statText[openComm+1 : closeComm])
		}
	}
	if cmdline == "" {
		if !IsAlive(pid) {
			return "", nil
		}
		return "", fmt.Errorf("inspect pid %d identity from procfs: empty command", pid)
	}

	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		if !IsAlive(pid) {
			return "", nil
		}
		return "", fmt.Errorf("inspect pid %d identity from procfs exe: %w", pid, err)
	}

	return fmt.Sprintf("linux-proc-v1|start=%s|exe=%s|command=%s", startTime, exe, cmdline), nil
}

func normalizeProcCmdline(value []byte) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(string(value), "\x00", " ")), " ")
}

func psProcessIdentity(pid int) (string, error) {
	cmd := exec.Command("/bin/ps", "-ww", "-p", strconv.Itoa(pid), "-o", "lstart=", "-o", "command=")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		if !IsAlive(pid) {
			return "", nil
		}
		return "", fmt.Errorf("inspect pid %d identity: %w", pid, err)
	}
	identity := strings.Join(strings.Fields(string(out)), " ")
	if identity == "" {
		if !IsAlive(pid) {
			return "", nil
		}
		return "", fmt.Errorf("inspect pid %d identity: empty output", pid)
	}
	return identity, nil
}

func MatchesFingerprint(pid int, expected string) (bool, error) {
	if strings.TrimSpace(expected) == "" {
		return IsAlive(pid), nil
	}
	actual, err := Fingerprint(pid)
	if err != nil {
		return false, err
	}
	return actual != "" && actual == expected, nil
}

func signalErrMeansAlive(err error) bool {
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM) || errors.Is(err, os.ErrPermission)
}

func StopPID(pid int, timeout time.Duration) error {
	if pid <= 0 || !IsAlive(pid) {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !IsAlive(pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	if err := proc.Signal(syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("force stop pid %d: %w", pid, err)
	}
	return nil
}

func RequireAlive(pid int, startupWindow time.Duration) error {
	if pid <= 0 {
		return nil
	}
	deadline := time.Now().Add(startupWindow)
	for time.Now().Before(deadline) {
		if !IsAlive(pid) {
			return fmt.Errorf("pid %d exited during startup", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !IsAlive(pid) {
		return fmt.Errorf("pid %d exited during startup", pid)
	}
	return nil
}
