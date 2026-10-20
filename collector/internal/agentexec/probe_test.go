package agentexec

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestProbeOutputBoundsAndCancellation(t *testing.T) {
	for _, size := range []int{16, 8192} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProbeOutputHelper$")
			command.Env = append(os.Environ(), "COSLASH_TEST_PROBE_SIZE="+strconv.Itoa(size))
			output, err := ProbeOutput(command, 1024)
			if len(output) != min(size, 1024) || (err != nil) != (size > 1024) {
				t.Fatalf("output bytes=%d error=%v", len(output), err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ProbeOutput(exec.CommandContext(ctx, os.Args[0]), 1024); err == nil {
		t.Fatal("canceled probe ran")
	}
}
func TestProbeOutputCancelsRunningProcess(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProbeOutputHelper$")
	command.Env = append(os.Environ(), "COSLASH_TEST_PROBE_READY="+listener.Addr().String())
	done := make(chan error, 1)
	go func() { _, err := ProbeOutput(command, 1024); done <- err }()
	ready := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			_, err = io.ReadFull(conn, make([]byte, 1))
			conn.Close()
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case err := <-done:
		t.Fatalf("probe exited before readiness: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("probe did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled running probe succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled probe did not terminate")
	}
}

func TestProbeOutputHelper(t *testing.T) {
	if address := os.Getenv("COSLASH_TEST_PROBE_READY"); address != "" {
		conn, err := net.Dial("tcp", address)
		if err != nil {
			os.Exit(2)
		}
		_, _ = conn.Write([]byte("x"))
		conn.Close()
		time.Sleep(time.Minute)
		os.Exit(3)
	}

	if size, err := strconv.Atoi(os.Getenv("COSLASH_TEST_PROBE_SIZE")); err == nil {
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), size))
		os.Exit(0)
	}
}
