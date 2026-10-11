package smartdns

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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
	if err := os.MkdirAll(m.cacheDir(), 0o700); err != nil {
		return fmt.Errorf("prepare SmartDNS cache directory: %w", err)
	}
	return os.WriteFile(m.paths.SmartDNSConf, []byte(rendered), 0o640)
}

func (m Manager) cacheDir() string { return filepath.Join(m.paths.Dir, "smartdns-cache") }

var ownedCacheName = regexp.MustCompile(`^resolver-[a-f0-9]{64}\.cache(?:\.tmp)?$`)

// Prune only our regular cache files. Leave unrelated files, directories and
// symlinks alone. Removing old policies also bounds storage across edits.
func (m Manager) pruneResolverCaches(content string) error {
	keep := ""
	for _, line := range strings.Split(content, "\n") {
		if value, ok := strings.CutPrefix(line, "cache-file "); ok {
			path, err := strconv.Unquote(strings.TrimSpace(value))
			if err != nil || filepath.Dir(path) != m.cacheDir() || !ownedCacheName.MatchString(filepath.Base(path)) {
				return fmt.Errorf("SmartDNS cache path is outside the managed cache directory")
			}
			keep = filepath.Base(path)
		}
	}
	entries, err := os.ReadDir(m.cacheDir())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !ownedCacheName.MatchString(entry.Name()) || entry.Name() == keep {
			continue
		}
		if err := os.Remove(filepath.Join(m.cacheDir(), entry.Name())); err != nil {
			return fmt.Errorf("remove obsolete SmartDNS cache: %w", err)
		}
	}
	return nil
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
	validationCache := filepath.Join(filepath.Dir(m.paths.SmartDNSConf), "smartdns.validate.cache")
	defer os.Remove(validationPath)
	defer os.Remove(validationLog)
	defer os.Remove(validationCache)
	// Validation must neither load the live cache nor checkpoint it, and must
	// not prefetch cached domains through a proxy that may not be running yet.
	validation := strings.ReplaceAll(string(data), "cache-persist yes", "cache-persist no")
	validation = strings.ReplaceAll(validation, "prefetch-domain yes", "prefetch-domain no")
	lines := strings.Split(validation, "\n")
	for index, line := range lines {
		if strings.HasPrefix(line, "cache-file ") {
			lines[index] = fmt.Sprintf("cache-file %q", validationCache)
		}
	}
	validation = strings.Join(lines, "\n")
	if err := os.WriteFile(validationPath, []byte(rewriteValidationBinds(validation, port)), 0o600); err != nil {
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
	data, err := os.ReadFile(m.paths.SmartDNSConf)
	if err != nil {
		return 0, fmt.Errorf("smartdns config is unavailable: %w", err)
	}
	if err := m.pruneResolverCaches(string(data)); err != nil {
		return 0, err
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
