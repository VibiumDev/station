// Package sdnotify tells systemd how a service is doing, over the socket
// systemd names in NOTIFY_SOCKET: that it is ready, that it is still
// alive (the watchdog) and that it is stopping. Outside systemd, or in a
// unit without Type=notify, every call does nothing and reports no error.
package sdnotify

import (
	"context"
	"net"
	"os"
	"strconv"
	"time"
)

// Ready says the service is up and serving.
func Ready() error { return send("READY=1") }

// Stopping says the service has begun to shut down.
func Stopping() error { return send("STOPPING=1") }

// Watchdog sends one keep-alive. With WatchdogSec= in the unit, systemd
// restarts a service that stays quiet for that long.
func Watchdog() error { return send("WATCHDOG=1") }

// Status sets the one-line status that systemctl shows.
func Status(s string) error { return send("STATUS=" + s) }

// Interval is how often Watchdog should be called: half of what the
// unit's WatchdogSec= allows, or zero when systemd expects no keep-alives.
func Interval() time.Duration {
	if pid := os.Getenv("WATCHDOG_PID"); pid != "" && pid != strconv.Itoa(os.Getpid()) {
		return 0
	}
	usec, err := strconv.ParseInt(os.Getenv("WATCHDOG_USEC"), 10, 64)
	if err != nil || usec <= 0 {
		return 0
	}
	return time.Duration(usec) * time.Microsecond / 2
}

// Keep sends keep-alives until ctx ends, each one only while alive says
// the service still works, so a wedged service is restarted rather than
// kept. It returns at once when no watchdog is set.
func Keep(ctx context.Context, alive func() bool) {
	every := Interval()
	if every == 0 {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if alive == nil || alive() {
				Watchdog()
			}
		}
	}
}

func send(state string) error {
	path := os.Getenv("NOTIFY_SOCKET")
	if path == "" {
		return nil
	}
	if path[0] == '@' {
		path = "\x00" + path[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write([]byte(state))
	return err
}
