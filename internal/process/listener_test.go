package process

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestOwnsTCPListenerRealProcess(t *testing.T) {
	if os.Getenv("OPENSURGE_LISTENER_TEST_CHILD") == "1" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		fmt.Println(listener.Addr())
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if owned, err := OwnsTCPListener(os.Getpid(), listener.Addr().String()); err != nil || !owned {
		t.Fatalf("live listener not owned by current process: %v %v", owned, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOwnsTCPListenerRealProcess$")
	child.Env = append(os.Environ(), "OPENSURGE_LISTENER_TEST_CHILD=1")
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = child.Wait() })
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatalf("child listener missing: %v", scanner.Err())
	}
	address := scanner.Text()
	if owned, err := OwnsTCPListener(child.Process.Pid, address); err != nil || !owned {
		t.Fatalf("child socket not owned: %v %v", owned, err)
	}
	if owned, err := OwnsTCPListener(os.Getpid(), address); err != nil || owned {
		t.Fatalf("foreign child socket accepted: %v %v", owned, err)
	}
	if owned, err := OwnsTCPListener(child.Process.Pid, listener.Addr().String()); err != nil || owned {
		t.Fatalf("foreign parent socket accepted: %v %v", owned, err)
	}
}
