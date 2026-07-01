// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestMainGracefulShutdown runs main() end-to-end: it starts the server on an
// ephemeral port, waits for it to accept connections, then delivers SIGTERM so
// main() takes its graceful-shutdown path and returns.
func TestMainGracefulShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	t.Setenv("SERVER_PORT", itoa(port))
	t.Setenv("LOG_LEVEL", "error")

	done := make(chan struct{})
	go func() {
		main()
		close(done)
	}()

	addr := net.JoinHostPort("127.0.0.1", itoa(port))
	var up bool
	for i := 0; i < 200; i++ {
		conn, derr := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if derr == nil {
			conn.Close()
			up = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !up {
		t.Fatalf("server did not come up on %s", addr)
	}

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("failed to send SIGTERM: %v", err)
	}

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatalf("main did not return after SIGTERM")
	}
}

// TestMainInvalidConfig ensures LoadConfig failure exits with a non-zero code.
// It re-executes the test binary as a subprocess so os.Exit(1) is observable.
func TestMainInvalidConfig(t *testing.T) {
	if os.Getenv("RUN_MAIN_INVALID") == "1" {
		main()
		return
	}
	cmd := helperCommand(t, "TestMainInvalidConfig")
	cmd.Env = append(os.Environ(), "RUN_MAIN_INVALID=1", "SERVER_PORT=not-a-port")
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected non-zero exit for invalid config")
	}
}

func helperCommand(t *testing.T, run string) *exec.Cmd {
	t.Helper()
	return exec.Command(os.Args[0], "-test.run=^"+run+"$")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
