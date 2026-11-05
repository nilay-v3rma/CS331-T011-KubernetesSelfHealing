package agent

import (
	"io"
	"net"
	"testing"
	"time"
)

// TestProbeTCPAckSuccess starts a listener that writes a 1-byte ack then
// drains.  probeTCP should succeed with RTT > 0.
func TestProbeTCPAckSuccess(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = conn.Write([]byte{0x01})
		_, _ = io.Copy(io.Discard, conn)
	}()

	rtt, _, err := probeTCP(ln.Addr().String(), 2*time.Second, 64)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if rtt <= 0 {
		t.Errorf("expected RTT > 0, got %v", rtt)
	}
}

// TestProbeTCPAcceptCloseFailure starts a listener that accepts then
// immediately closes WITHOUT writing an ack.  probeTCP should return an error
// because io.ReadFull will fail.
func TestProbeTCPAcceptCloseFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		// Close immediately without writing ack.
		conn.Close()
	}()

	_, _, err = probeTCP(ln.Addr().String(), 2*time.Second, 64)
	if err == nil {
		t.Fatal("expected an error when ack is not sent, got nil")
	}
}

// TestProbeTCPDeadlineHonored starts a listener that never writes anything.
// probeTCP with a 100 ms timeout should return an error within ~200 ms.
func TestProbeTCPDeadlineHonored(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	// Accept but do nothing (no ack, no drain).
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		time.Sleep(10 * time.Second) // hold connection open but never write
	}()

	const timeout = 100 * time.Millisecond
	start := time.Now()
	_, _, err = probeTCP(ln.Addr().String(), timeout, 0)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("probe took too long (%v); expected < 200ms", elapsed)
	}
}
