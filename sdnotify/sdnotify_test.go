package sdnotify

import (
	"context"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// listen stands in for systemd: a datagram socket the package writes to.
func listen(t *testing.T) *net.UnixConn {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notify")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	t.Setenv("NOTIFY_SOCKET", path)
	return conn
}

func read(t *testing.T, conn *net.UnixConn, within time.Duration) string {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(within))
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil {
		return ""
	}
	return string(buf[:n])
}

func TestReadyAndStopping(t *testing.T) {
	conn := listen(t)
	if err := Ready(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, conn, time.Second); got != "READY=1" {
		t.Fatalf("got %q", got)
	}
	Stopping()
	if got := read(t, conn, time.Second); got != "STOPPING=1" {
		t.Fatalf("got %q", got)
	}
}

func TestWithoutSystemd(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	if err := Ready(); err != nil {
		t.Fatalf("no socket should be a no-op, got %v", err)
	}
	t.Setenv("WATCHDOG_USEC", "")
	if Interval() != 0 {
		t.Fatal("no WATCHDOG_USEC should mean no interval")
	}
}

func TestInterval(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "30000000")
	t.Setenv("WATCHDOG_PID", "")
	if got := Interval(); got != 15*time.Second {
		t.Fatalf("got %v, want half of 30s", got)
	}
	t.Setenv("WATCHDOG_PID", "1")
	if Interval() != 0 {
		t.Fatal("a WATCHDOG_PID for another process should mean no interval")
	}
	t.Setenv("WATCHDOG_PID", strconv.Itoa(pid()))
	if Interval() != 15*time.Second {
		t.Fatal("our own WATCHDOG_PID should count")
	}
}

func TestKeep(t *testing.T) {
	conn := listen(t)
	t.Setenv("WATCHDOG_USEC", "20000") // ping every 10 ms
	t.Setenv("WATCHDOG_PID", "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { Keep(ctx, func() bool { return true }); close(done) }()
	if got := read(t, conn, time.Second); got != "WATCHDOG=1" {
		t.Fatalf("got %q", got)
	}
	cancel()
	<-done

	// A service that says it is not alive sends nothing.
	ctx, cancel = context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	Keep(ctx, func() bool { return false })
	if got := read(t, conn, 50*time.Millisecond); got != "" {
		t.Fatalf("got %q, want silence", got)
	}
}
