//go:build linux

package process

import (
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// OwnsTCPListener verifies both the exact bind address and the socket inode in
// this process's fd table. A successful dial alone can reach a foreign process.
// QNAP does not need procps, ss, netstat or lsof for this check.
func OwnsTCPListener(pid int, address string) (bool, error) {
	return ownsTCPListenerAt("/proc", pid, address)
}

func ownsTCPListenerAt(procRoot string, pid int, address string) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	host, portText, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	port, portErr := strconv.Atoi(portText)
	if err != nil || ip == nil || portErr != nil || port < 1 || port > 65535 {
		return false, fmt.Errorf("invalid TCP listener address %q", address)
	}
	root := filepath.Join(procRoot, strconv.Itoa(pid))
	fds, err := os.ReadDir(filepath.Join(root, "fd"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	owned := make(map[string]bool)
	for _, fd := range fds {
		link, err := os.Readlink(filepath.Join(root, "fd", fd.Name()))
		if err == nil && strings.HasPrefix(link, "socket:[") && strings.HasSuffix(link, "]") {
			owned[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] = true
		}
	}
	for _, name := range []string{"tcp", "tcp6"} {
		data, err := os.ReadFile(filepath.Join(root, "net", name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, err
		}
		for _, line := range strings.Split(string(data), "\n") {
			f := strings.Fields(line)
			if len(f) < 10 || f[3] != "0A" || !owned[f[9]] {
				continue
			}
			rawIP, rawPort, ok := strings.Cut(f[1], ":")
			p, err := strconv.ParseUint(rawPort, 16, 16)
			bytes, decodeErr := hex.DecodeString(rawIP)
			if !ok || err != nil || int(p) != port || decodeErr != nil || len(bytes) != 4 && len(bytes) != 16 {
				continue
			}
			// procfs encodes native little-endian 32-bit words on our supported
			// Linux amd64/arm64 NAS architectures (including IPv4-mapped IPv6).
			for i := 0; i < len(bytes); i += 4 {
				bytes[i], bytes[i+3] = bytes[i+3], bytes[i]
				bytes[i+1], bytes[i+2] = bytes[i+2], bytes[i+1]
			}
			if net.IP(bytes).Equal(ip) {
				return true, nil
			}
		}
	}
	return false, nil
}
