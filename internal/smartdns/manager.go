package smartdns

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/process"
	"open-mihomo-gateway/internal/runtime"
)

type Manager struct {
	cfg   config.Config
	paths runtime.Paths
}

func New(cfg config.Config, paths runtime.Paths) Manager {
	return Manager{cfg: cfg, paths: paths}
}

func (m Manager) WriteConfig() error {
	rendered, err := RenderConfig(m.cfg, m.paths)
	if err != nil {
		return err
	}
	return os.WriteFile(m.paths.SmartDNSConf, []byte(rendered), 0o640)
}

func (m Manager) Check() error {
	_, err := resolveBinary("smartdns")
	return err
}

func (m Manager) Start() (int, error) {
	binary, err := resolveBinary("smartdns")
	if err != nil {
		return 0, err
	}
	if _, err := os.Stat(m.paths.SmartDNSConf); err != nil {
		return 0, fmt.Errorf("smartdns config is unavailable: %w", err)
	}
	if err := os.WriteFile(m.paths.SmartDNSLog, nil, 0o640); err != nil {
		return 0, err
	}
	pid, err := process.StartDetachedWithLog(m.paths.SmartDNSLog, binary, "-c", m.paths.SmartDNSConf, "-f", "-x")
	if err != nil {
		return 0, err
	}
	if err := process.RequireAlive(pid, 500*time.Millisecond); err != nil {
		_ = process.StopPID(pid, 0)
		return 0, err
	}
	return pid, nil
}

func (m Manager) Stop(pid int) error {
	return process.StopPID(pid, 3*time.Second)
}

func (m Manager) Running(pid int) bool {
	return process.IsAlive(pid)
}

func resolveBinary(path string) (string, error) {
	if strings.ContainsRune(path, os.PathSeparator) {
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if info.IsDir() {
			return "", fmt.Errorf("%s is a directory", path)
		}
		return path, nil
	}
	binary, err := exec.LookPath(path)
	if err != nil {
		return "", fmt.Errorf("smartdns not found in PATH")
	}
	return binary, nil
}
