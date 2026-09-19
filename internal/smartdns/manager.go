package smartdns

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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

func (m Manager) ValidateWrittenConfig() error {
	return m.ValidateWrittenConfigContext(context.Background())
}

func (m Manager) ValidateWrittenConfigContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	binary, err := resolveBinary("smartdns")
	if err != nil {
		return err
	}
	data, err := os.ReadFile(m.paths.SmartDNSConf)
	if err != nil {
		return fmt.Errorf("read smartdns config for validation: %w", err)
	}
	port, err := validationLoopbackPort()
	if err != nil {
		return fmt.Errorf("reserve smartdns validation port: %w", err)
	}
	validationPath := filepath.Join(filepath.Dir(m.paths.SmartDNSConf), "smartdns.validate.conf")
	validationLog := filepath.Join(filepath.Dir(m.paths.SmartDNSConf), "smartdns.validate.log")
	defer os.Remove(validationPath)
	defer os.Remove(validationLog)
	if err := os.WriteFile(validationPath, []byte(rewriteValidationBinds(string(data), port)), 0o600); err != nil {
		return fmt.Errorf("write smartdns validation config: %w", err)
	}
	if err := os.WriteFile(validationLog, nil, 0o600); err != nil {
		return fmt.Errorf("prepare smartdns validation log: %w", err)
	}
	pid, err := process.StartDetachedWithLog(validationLog, binary, "-c", validationPath, "-f", "-x", "-p", "-")
	if err != nil {
		return fmt.Errorf("start smartdns validation instance: %w", err)
	}
	if err := process.RequireAlive(pid, 350*time.Millisecond); err != nil {
		_ = process.StopPID(pid, 0)
		return smartDNSValidationError(err, validationLog)
	}
	stopErr := process.StopPID(pid, time.Second)
	if err := ctx.Err(); err != nil {
		return err
	}
	if stopErr != nil {
		return fmt.Errorf("stop smartdns validation instance: %w", stopErr)
	}
	return nil
}

func validationLoopbackPort() (int, error) {
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := tcp.Addr().(*net.TCPAddr).Port
	udp, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)))
	if err != nil {
		_ = tcp.Close()
		return 0, err
	}
	_ = udp.Close()
	_ = tcp.Close()
	return port, nil
}

func rewriteValidationBinds(content string, port int) string {
	target := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port))
	lines := strings.Split(content, "\n")
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		fields := strings.Fields(trimmed)
		if len(fields) < 2 || (fields[0] != "bind" && fields[0] != "bind-tcp") {
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, fields[0]+" "+fields[1]))
		lines[index] = indent + fields[0] + " " + target
		if rest != "" {
			lines[index] += " " + rest
		}
	}
	return strings.Join(lines, "\n")
}

func smartDNSValidationError(cause error, logPath string) error {
	data, _ := os.ReadFile(logPath)
	detail := strings.TrimSpace(string(data))
	if len(detail) > 2000 {
		detail = detail[len(detail)-2000:]
	}
	if detail == "" {
		return fmt.Errorf("smartdns rejected candidate config: %w", cause)
	}
	return fmt.Errorf("smartdns rejected candidate config: %w: %s", cause, detail)
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
