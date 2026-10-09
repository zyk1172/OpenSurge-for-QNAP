package process

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDetachedChildIsReapedAndLogRemainsUsable(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "child.log")
	pid, err := StartDetachedWithLog(logPath, "/bin/sh", "-c", "printf child-finished; exit 7")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for IsAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if IsAlive(pid) {
		// Reap our own already-exited child if this regression is reintroduced.
		_, _ = syscall.Wait4(pid, nil, syscall.WNOHANG, nil)
		t.Fatal("detached child was not reaped by its long-lived parent")
	}
	data, err := os.ReadFile(logPath)
	if err != nil || !strings.Contains(string(data), "child-finished") {
		t.Fatalf("child log = %q, %v", data, err)
	}
}

func TestStopDetachedChildDoesNotConsumeFullGracePeriod(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "30")
	pid, err := startDetached(cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	started := time.Now()
	if err := StopPID(pid, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("stopping a cooperative child took %s; expected prompt reap", elapsed)
	}
	if IsAlive(pid) {
		t.Fatal("stopped child is still observable as alive")
	}
}

func TestSignalErrMeansAlive(t *testing.T) {
	for _, err := range []error{
		nil,
		syscall.EPERM,
		os.ErrPermission,
		&os.SyscallError{Syscall: "kill", Err: syscall.EPERM},
	} {
		if !signalErrMeansAlive(err) {
			t.Fatalf("signalErrMeansAlive(%v) = false", err)
		}
	}

	for _, err := range []error{
		os.ErrProcessDone,
		syscall.ESRCH,
		errors.New("missing"),
	} {
		if signalErrMeansAlive(err) {
			t.Fatalf("signalErrMeansAlive(%v) = true", err)
		}
	}
}

func TestFingerprintMatchesCurrentProcessAndRejectsDifferentIdentity(t *testing.T) {
	fingerprint, err := Fingerprint(os.Getpid())
	if errors.Is(err, os.ErrPermission) {
		t.Skipf("process inspection is blocked by the test sandbox: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint == "" {
		t.Fatal("current process fingerprint is empty")
	}
	matches, err := MatchesFingerprint(os.Getpid(), fingerprint)
	if err != nil || !matches {
		t.Fatalf("matching fingerprint = %v, %v", matches, err)
	}
	matches, err = MatchesFingerprint(os.Getpid(), fingerprint+" changed")
	if err != nil || matches {
		t.Fatalf("different fingerprint = %v, %v", matches, err)
	}
}

func TestFingerprintTreatsMissingProcessAsAbsent(t *testing.T) {
	const impossiblePID = 2_000_000_000
	fingerprint, err := Fingerprint(impossiblePID)
	if err != nil || fingerprint != "" {
		t.Fatalf("missing process fingerprint = %q, %v", fingerprint, err)
	}
}

func TestLinuxProcessIdentityUsesProcfsWithoutPS(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("procfs identity is Linux-only")
	}
	identity, err := linuxProcessIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"linux-proc-v2|pid=", "|start="} {
		if !strings.Contains(identity, token) {
			t.Fatalf("procfs identity %q does not contain %q", identity, token)
		}
	}
}

func TestLinuxProcessIdentityIsStableAcrossCommChanges(t *testing.T) {
	const pid = 73
	stat := func(command string) []byte {
		statFields := make([]string, 20)
		for i := range statFields {
			statFields[i] = "0"
		}
		statFields[0] = "S"
		statFields[19] = "12345"
		return []byte(fmt.Sprintf("%d (%s) %s", pid, command, strings.Join(statFields, " ")))
	}
	currentStat := stat("dnsmasq")
	readProcFile := func(path string) ([]byte, error) {
		if path != "/proc/73/stat" {
			t.Fatalf("process identity read mutable procfs metadata at %s", path)
		}
		return currentStat, nil
	}
	isAlive := func(candidate int) bool { return candidate == pid }
	firstIdentity, err := linuxProcessIdentityWithProc(pid, readProcFile, isAlive)
	if err != nil {
		t.Fatal(err)
	}
	currentStat = stat("dnsmasq (nobody)")
	secondIdentity, err := linuxProcessIdentityWithProc(pid, readProcFile, isAlive)
	if err != nil {
		t.Fatal(err)
	}
	if firstIdentity != secondIdentity {
		t.Fatalf("process identity changed with comm metadata: first=%q second=%q", firstIdentity, secondIdentity)
	}
	if want := "linux-proc-v2|pid=73|start=12345"; firstIdentity != want {
		t.Fatalf("linuxProcessIdentityWithProc() = %q, want %q", firstIdentity, want)
	}

	currentStat = stat("dnsmasq (nobody)")
	changedStartStat := []byte(strings.Replace(string(currentStat), " 12345", " 12346", 1))
	readProcFile = func(path string) ([]byte, error) {
		if path != "/proc/73/stat" {
			t.Fatalf("process identity read mutable procfs metadata at %s", path)
		}
		return changedStartStat, nil
	}
	changedIdentity, err := linuxProcessIdentityWithProc(pid, readProcFile, isAlive)
	if err != nil {
		t.Fatal(err)
	}
	if changedIdentity == firstIdentity {
		t.Fatal("process identity did not change when kernel start time changed")
	}
}

func TestLinuxProcessIdentityTreatsMissingProcessAsAbsent(t *testing.T) {
	const pid = 73
	identity, err := linuxProcessIdentityWithProc(pid, func(string) ([]byte, error) {
		return nil, os.ErrNotExist
	}, func(int) bool { return false })
	if err != nil || identity != "" {
		t.Fatalf("missing process identity = %q, %v", identity, err)
	}
}

func TestLinuxProcessIdentityRejectsOtherProcStatErrors(t *testing.T) {
	const pid = 73
	_, err := linuxProcessIdentityWithProc(pid, func(string) ([]byte, error) {
		return nil, syscall.EIO
	}, func(int) bool { return true })
	if !errors.Is(err, syscall.EIO) {
		t.Fatalf("linuxProcessIdentityWithProc() error = %v, want EIO", err)
	}
}

func TestLinuxProcessIdentityIncludesPID(t *testing.T) {
	stat := func(pid int) []byte {
		statFields := make([]string, 20)
		for i := range statFields {
			statFields[i] = "0"
		}
		statFields[0] = "S"
		statFields[19] = "12345"
		return []byte(fmt.Sprintf("%d (daemon) %s", pid, strings.Join(statFields, " ")))
	}
	identities := make([]string, 2)
	for index, pid := range []int{73, 74} {
		identity, err := linuxProcessIdentityWithProc(pid, func(path string) ([]byte, error) {
			if path != fmt.Sprintf("/proc/%d/stat", pid) {
				return nil, os.ErrNotExist
			}
			return stat(pid), nil
		}, func(candidate int) bool { return candidate == pid })
		if err != nil {
			t.Fatal(err)
		}
		identities[index] = identity
	}
	if identities[0] == identities[1] {
		t.Fatalf("different PIDs have identical process identities: %q", identities[0])
	}
}
