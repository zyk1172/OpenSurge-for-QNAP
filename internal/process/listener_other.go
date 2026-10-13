//go:build !linux

package process

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func OwnsTCPListener(pid int, address string) (bool, error) {
	host, port, err := net.SplitHostPort(address)
	portNumber, portErr := strconv.Atoi(port)
	if pid <= 0 {
		return false, nil
	}
	if err != nil || net.ParseIP(host) == nil || portErr != nil || portNumber < 1 || portNumber > 65535 {
		return false, fmt.Errorf("invalid TCP listener identity")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "lsof", "-nP", "-a", "-p", strconv.Itoa(pid), "-iTCP@"+host+":"+port, "-sTCP:LISTEN", "-Fn").Output()
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		return false, nil // no matching socket
	}
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(output), "\n") {
		if line == "n"+address {
			return true, nil
		}
	}
	return false, nil
}
