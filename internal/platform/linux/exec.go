//go:build linux

package linux

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strings"

	"open-mihomo-gateway/internal/platform"
)

// interfaceNamePattern is deliberately strict. Anything that reaches an
// exec argv must first pass through here, because interface names, table names
// and marks come from configuration or the Web UI and must never be allowed to
// smuggle shell metacharacters or argument switches into a command line.
var interfaceNamePattern = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,15}$`)

// validateInterfaceName rejects anything that is not already a canonical Linux
// interface name.
func validateInterfaceName(name string) error {
	if name == "" {
		return platform.NewError(platform.CodeInvalidArgument, "interface name is empty")
	}
	if !interfaceNamePattern.MatchString(name) {
		return platform.NewError(platform.CodeInvalidArgument, "invalid interface name").
			WithDetail("interface", name)
	}
	return nil
}

func validateTableName(name string) error {
	if !regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`).MatchString(name) {
		return platform.NewError(platform.CodeInvalidArgument, "invalid nftables table name").
			WithDetail("table", name)
	}
	return nil
}

func validateIPv4(value string) (net.IP, error) {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil || ip.To4() == nil {
		return nil, platform.NewError(platform.CodeInvalidArgument, "value is not an IPv4 address").
			WithDetail("value", value)
	}
	return ip.To4(), nil
}

func validateCIDR(value string) (*net.IPNet, error) {
	ip, network, err := net.ParseCIDR(strings.TrimSpace(value))
	if err != nil || ip.To4() == nil {
		return nil, platform.NewError(platform.CodeSubnetInvalid, "value is not an IPv4 CIDR").
			WithDetail("value", value)
	}
	return network, nil
}

// runner executes host commands. Every invocation passes arguments as a slice
// to exec.CommandContext; there is no shell, ever.
type runner struct {
	nftPath string
	ipPath  string
}

func newRunner() *runner {
	nftPath, _ := exec.LookPath("nft")
	ipPath, _ := exec.LookPath("ip")
	if ipPath == "" {
		if fallback, err := exec.LookPath("/sbin/ip"); err == nil {
			ipPath = fallback
		}
	}
	return &runner{nftPath: nftPath, ipPath: ipPath}
}

func (r *runner) output(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name == "" {
		return nil, platform.NewError(platform.CodeInvalidArgument, "command name is empty")
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		return stdout.Bytes(), commandError(ctx, name, args, err, message)
	}
	return stdout.Bytes(), nil
}

func (r *runner) run(ctx context.Context, name string, args ...string) error {
	_, err := r.output(ctx, name, args...)
	return err
}

func commandError(ctx context.Context, name string, args []string, err error, stderr string) error {
	detail := map[string]string{
		"command": name,
		"args":    strings.Join(args, " "),
	}
	if stderr != "" {
		detail["stderr"] = stderr
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		detail["context"] = ctxErr.Error()
	}
	message := fmt.Sprintf("%s %s failed: %v", name, strings.Join(args, " "), err)
	if stderr != "" {
		message = fmt.Sprintf("%s: %s", message, stderr)
	}
	coded := platform.NewError(platform.CodeCommandFailed, message).
		WithDetails(detail).
		Wrap(err)
	switch {
	case strings.Contains(stderr, "Operation not permitted"), strings.Contains(stderr, "Permission denied"):
		coded.Code = platform.CodePermissionDenied
	case strings.Contains(stderr, "Cannot find device"), strings.Contains(stderr, "No such device"):
		coded.Code = platform.CodeInterfaceNotFound
	}
	return coded
}

// isNotExist recognises the common idempotent-delete responses emitted by nft
// and iproute2 across kernel/iproute2 versions.
func isNotExist(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	for _, needle := range []string{
		"No such file or directory",
		"No such device",
		"No such process",
		"Cannot find device",
		"FIB table does not exist",
		"does not exist",
		"not found",
	} {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
