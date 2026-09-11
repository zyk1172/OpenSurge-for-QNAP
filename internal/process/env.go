package process

import (
	"os"
	"os/exec"
)

// StartDetachedWithLogEnv is the environment-aware variant used when a child
// needs a narrowly scoped runtime switch. It preserves the parent environment
// and appends the requested overrides without mutating process-global state.
func StartDetachedWithLogEnv(logPath string, env []string, name string, args ...string) (int, error) {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	defer logFile.Close()

	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	return startDetached(cmd)
}
