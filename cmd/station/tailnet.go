package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"tailscale.com/tsnet"
)

// tailnet joins the station to the tailnet as its own node with tsnet and
// listens with Tailscale's certificate. The returned identity asks
// tailscaled who each connection belongs to.
func tailnet(ctx context.Context, dir, hostname, controlURL string) (net.Listener, string, identity, func(), error) {
	srv := &tsnet.Server{
		Dir:        filepath.Join(dir, "tsnet"),
		Hostname:   hostname,
		ControlURL: controlURL,
		AuthKey:    os.Getenv("TS_AUTHKEY"),
		UserLogf:   func(format string, args ...any) { fmt.Fprintf(os.Stderr, "tailscale: "+format+"\n", args...) },
		Logf:       func(string, ...any) {},
	}
	status, err := srv.Up(ctx)
	if err != nil {
		return nil, "", nil, nil, err
	}
	ln, err := srv.ListenTLS("tcp", ":443")
	if err != nil {
		srv.Close()
		return nil, "", nil, nil, err
	}
	lc, err := srv.LocalClient()
	if err != nil {
		ln.Close()
		srv.Close()
		return nil, "", nil, nil, err
	}
	who := func(ctx context.Context, remoteAddr string) (string, []string, error) {
		resp, err := lc.WhoIs(ctx, remoteAddr)
		if err != nil {
			return "", nil, err
		}
		login := ""
		if resp.UserProfile != nil {
			login = resp.UserProfile.LoginName
		}
		var tags []string
		if resp.Node != nil {
			tags = resp.Node.Tags
		}
		return login, tags, nil
	}
	name := strings.TrimSuffix(status.Self.DNSName, ".")
	return ln, "https://" + name, who, func() { srv.Close() }, nil
}
