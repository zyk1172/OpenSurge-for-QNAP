package process

import (
	"errors"
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
	for _, token := range []string{"linux-proc-v1|", "start=", "|exe=", "|command="} {
		if !strings.Contains(identity, token) {
			t.Fatalf("procfs identity %q does not contain %q", identity, token)
		}
	}
}

func TestNormalizeProcCmdline(t *testing.T) {
	got := normalizeProcCmdline([]byte("/usr/local/bin/mihomo\x00-d\x00/data/mihomo\x00\x00"))
	if want := "/usr/local/bin/mihomo -d /data/mihomo"; got != want {
		t.Fatalf("normalizeProcCmdline() = %q, want %q", got, want)
	}
}
