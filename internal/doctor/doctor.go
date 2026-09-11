package doctor

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/mihomo"
	"open-mihomo-gateway/internal/runtime"
)

type Check struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

type Report struct {
	Checks []Check `json:"checks"`
}

var validateMihomoConfig = validateMihomoConfigWithEngine

func Run(cfg config.Config) Report {
	checks := []Check{
		checkRoot(),
		checkPath("dnsmasq", cfg.DHCP.Binary),
		checkPath("mihomo", cfg.Mihomo.Binary),
		checkMihomoConfigRender(cfg),
	}

	if goruntime.GOOS == "linux" {
		// QNAP/Linux must not inherit the upstream macOS pfctl health model.
		// same_lan uses ingress-interface policy routing and therefore requires
		// iproute2 + TUN but not nftables. Isolated/NAT topologies additionally
		// require nftables.
		checks = append(checks,
			checkCommand("iproute2", "ip"),
			checkTUNDevice("/dev/net/tun"),
			checkWritableDirectory("persistent runtime storage", cfg.Runtime.Dir),
		)
		if !cfg.Gateway.SameLAN() {
			checks = append(checks, checkCommand("nftables", "nft"))
		}
	} else {
		checks = append(checks, checkCommand("pfctl", "pfctl"))
	}

	checks = append(checks,
		checkInterface(cfg.Gateway.Interface),
		checkInterface(cfg.Gateway.UpstreamInterface),
		checkGatewayInterfaceTopology(cfg.Gateway),
		checkIPv4("LAN IP", cfg.Gateway.LANIP),
		checkInterfaceIPv4(cfg.Gateway.Interface, cfg.Gateway.LANIP),
	)

	if goruntime.GOOS == "linux" && cfg.Transparent.TUNIPv6 != config.TUNIPv6Off {
		checks = append(checks,
			checkIPv6Forwarding(),
			checkInterfaceIPv6(cfg.Gateway.Interface, config.DownstreamIPv6Gateway),
		)
	}
	return Report{Checks: checks}
}

func checkMihomoConfigRender(cfg config.Config) Check {
	if err := validateMihomoConfig(cfg); err != nil {
		return Check{Name: "mihomo config validation", OK: false, Message: err.Error()}
	}
	return Check{Name: "mihomo config validation", OK: true}
}

func validateMihomoConfigWithEngine(cfg config.Config) error {
	dir, err := os.MkdirTemp("", "open-surge-doctor-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	copy := cfg
	copy.Runtime.Dir = dir
	copy.Mihomo.Config = filepath.Join(dir, "mihomo.yaml")
	paths := runtime.NewPaths(copy)
	return mihomo.New(copy, paths).ValidateConfig()
}

func (r Report) Healthy() bool {
	for _, check := range r.Checks {
		if !check.OK {
			return false
		}
	}
	return true
}

func (r Report) Format() string {
	var out strings.Builder
	for _, check := range r.Checks {
		status := "FAIL"
		if check.OK {
			status = "OK"
		}
		fmt.Fprintf(&out, "[%s] %s", status, check.Name)
		if check.Message != "" {
			fmt.Fprintf(&out, " - %s", check.Message)
		}
		out.WriteByte('\n')
	}
	if r.Healthy() {
		out.WriteString("Doctor: healthy\n")
	} else {
		out.WriteString("Doctor: issues found\n")
	}
	return out.String()
}

func checkRoot() Check {
	if os.Geteuid() == 0 {
		return Check{Name: "root privileges", OK: true}
	}
	return Check{Name: "root privileges", OK: false, Message: "start/stop require elevated network privileges"}
}

func checkCommand(name, command string) Check {
	path, err := exec.LookPath(command)
	if err != nil {
		return Check{Name: name, OK: false, Message: "not found in PATH"}
	}
	return Check{Name: name, OK: true, Message: path}
}

func checkPath(name, path string) Check {
	if strings.ContainsRune(path, os.PathSeparator) {
		info, err := os.Stat(path)
		if err != nil {
			return Check{Name: name, OK: false, Message: err.Error()}
		}
		if info.IsDir() {
			return Check{Name: name, OK: false, Message: "path is a directory"}
		}
		return Check{Name: name, OK: true, Message: path}
	}
	return checkCommand(name, path)
}

func checkTUNDevice(path string) Check {
	info, err := os.Stat(path)
	if err != nil {
		return Check{Name: "TUN device", OK: false, Message: err.Error()}
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return Check{Name: "TUN device", OK: false, Message: path + " is not a character device"}
	}
	return Check{Name: "TUN device", OK: true, Message: path}
}

func checkIPv6Forwarding() Check {
	const path = "/proc/sys/net/ipv6/conf/all/forwarding"
	data, err := os.ReadFile(path)
	if err != nil {
		return Check{Name: "IPv6 forwarding", OK: false, Message: err.Error()}
	}
	if strings.TrimSpace(string(data)) != "1" {
		return Check{Name: "IPv6 forwarding", OK: false, Message: "set net.ipv6.conf.all.forwarding=1 in the OpenSurge container namespace"}
	}
	return Check{Name: "IPv6 forwarding", OK: true, Message: "enabled"}
}

func checkInterfaceIPv6(interfaceName, ipValue string) Check {
	name := "IPv6 gateway bound to " + interfaceName
	target := net.ParseIP(ipValue)
	if target == nil || target.To4() != nil {
		return Check{Name: name, OK: false, Message: "invalid IPv6 address"}
	}
	iface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return Check{Name: name, OK: false, Message: err.Error()}
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return Check{Name: name, OK: false, Message: err.Error()}
	}
	for _, addr := range addrs {
		switch value := addr.(type) {
		case *net.IPNet:
			if value.IP.Equal(target) {
				return Check{Name: name, OK: true, Message: ipValue}
			}
		case *net.IPAddr:
			if value.IP.Equal(target) {
				return Check{Name: name, OK: true, Message: ipValue}
			}
		}
	}
	return Check{Name: name, OK: false, Message: "not configured on interface"}
}

// checkWritableDirectory verifies the filesystem operations OpenSurge depends
// on for durable QNAP state: create, write, fsync, rename, parent-directory
// fsync and cleanup. This is intentionally stronger than os.Access or a simple
// writable-bit check because QTS/QuTS shared-folder ACLs can make those checks
// misleading for bind mounts.
func checkWritableDirectory(name, path string) Check {
	if strings.TrimSpace(path) == "" {
		return Check{Name: name, OK: false, Message: "path is empty"}
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return Check{Name: name, OK: false, Message: "mkdir: " + err.Error()}
	}
	file, err := os.CreateTemp(path, ".opensurge-doctor-*.tmp")
	if err != nil {
		return Check{Name: name, OK: false, Message: "create: " + err.Error()}
	}
	tmp := file.Name()
	final := strings.TrimSuffix(tmp, ".tmp") + ".state"
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(tmp)
		_ = os.Remove(final)
	}
	defer cleanup()

	if err := file.Chmod(0o600); err != nil {
		return Check{Name: name, OK: false, Message: "chmod: " + err.Error()}
	}
	if _, err := file.WriteString("opensurge-storage-probe\n"); err != nil {
		return Check{Name: name, OK: false, Message: "write: " + err.Error()}
	}
	if err := file.Sync(); err != nil {
		return Check{Name: name, OK: false, Message: "file fsync: " + err.Error()}
	}
	if err := file.Close(); err != nil {
		return Check{Name: name, OK: false, Message: "close: " + err.Error()}
	}
	if err := os.Rename(tmp, final); err != nil {
		return Check{Name: name, OK: false, Message: "rename: " + err.Error()}
	}
	dir, err := os.Open(path)
	if err != nil {
		return Check{Name: name, OK: false, Message: "open parent: " + err.Error()}
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return Check{Name: name, OK: false, Message: "parent fsync: " + err.Error()}
	}
	if err := dir.Close(); err != nil {
		return Check{Name: name, OK: false, Message: "close parent: " + err.Error()}
	}
	if err := os.Remove(final); err != nil {
		return Check{Name: name, OK: false, Message: "cleanup: " + err.Error()}
	}
	return Check{Name: name, OK: true, Message: path + " supports durable create/rename/fsync"}
}

func checkInterface(name string) Check {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return Check{Name: "interface " + name, OK: false, Message: err.Error()}
	}
	return Check{Name: "interface " + name, OK: true, Message: iface.HardwareAddr.String()}
}

func checkGatewayInterfaceTopology(cfg config.GatewayConfig) Check {
	name := "gateway interface topology"
	sameInterface := strings.TrimSpace(cfg.Interface) == strings.TrimSpace(cfg.UpstreamInterface)
	if cfg.SameLAN() {
		if !sameInterface {
			return Check{Name: name, OK: false, Message: fmt.Sprintf("%s requires gateway and upstream interfaces to match", cfg.Mode)}
		}
		return Check{Name: name, OK: true, Message: cfg.Mode + " uses one LAN interface"}
	}
	if sameInterface {
		return Check{Name: name, OK: false, Message: "isolated_lan requires separate downstream and upstream interfaces"}
	}
	return Check{Name: name, OK: true}
}

func checkInterfaceIPv4(interfaceName, ipValue string) Check {
	name := "LAN IP bound to " + interfaceName
	target := net.ParseIP(ipValue).To4()
	if target == nil {
		return Check{Name: name, OK: false, Message: "invalid IPv4 address"}
	}
	iface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return Check{Name: name, OK: false, Message: err.Error()}
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return Check{Name: name, OK: false, Message: err.Error()}
	}
	for _, addr := range addrs {
		switch value := addr.(type) {
		case *net.IPNet:
			if value.IP.To4() != nil && value.IP.Equal(target) {
				return Check{Name: name, OK: true, Message: ipValue}
			}
		case *net.IPAddr:
			if value.IP.To4() != nil && value.IP.Equal(target) {
				return Check{Name: name, OK: true, Message: ipValue}
			}
		}
	}
	return Check{Name: name, OK: false, Message: "not configured on interface"}
}

func checkIPv4(name, value string) Check {
	if net.ParseIP(value).To4() == nil {
		return Check{Name: name, OK: false, Message: "invalid IPv4 address"}
	}
	return Check{Name: name, OK: true, Message: value}
}
