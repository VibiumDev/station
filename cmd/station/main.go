// Command station is Vibium Station for one owner: enroll your rigs, sign
// in with a password or your tailnet identity, and open any device's
// viewer. Everything lives in one directory: config.json, station.key,
// station.db, and a certificate.
package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	ossignal "os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"fiatjaf.com/nostr"
	"golang.org/x/term"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/enroll"
	"github.com/VibiumDev/station/hold"
	"github.com/VibiumDev/station/relay"
	"github.com/VibiumDev/station/signal"
	"github.com/VibiumDev/station/sqlite"
	"github.com/VibiumDev/station/token"
	"github.com/VibiumDev/station/tunnel"
	"github.com/VibiumDev/station/turn"
)

func main() {
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		os.Exit(runCommand(os.Args[1], os.Args[2:]))
	}
	os.Exit(serve(os.Args[1:]))
}

func usage() {
	fmt.Println(`usage: station [flags]                 run the station (station -h for the flags)
       station passwd                  set the owner's password
       station enroll                  make a one-time enrollment code for a rig
       station token create NAME [--read-only]
       station token list | revoke NAME
       station key                     show the station's key`)
}

// openDB opens the station's database in the data directory.
func openDB(dir string) (*sqlite.DB, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return sqlite.Open(filepath.Join(dir, "station.db"))
}

func runCommand(name string, args []string) int {
	fs := flag.NewFlagSet("station "+name, flag.ContinueOnError)
	dirFlag := fs.String("data-dir", "", "where the station keeps its files (default $STATION_DIR or the user config dir)")
	readOnly := fs.Bool("read-only", false, "token create: the token may only view")
	// Flags may come before or after the words.
	var words []string
	for len(args) > 0 {
		if strings.HasPrefix(args[0], "-") {
			if err := fs.Parse(args); err != nil {
				return 2
			}
			args = fs.Args()
			continue
		}
		words = append(words, args[0])
		args = args[1:]
	}
	dir, err := dataDir(*dirFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	switch name {
	case "help", "-h", "--help":
		usage()
		return 0
	case "key":
		sk, err := loadKey(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("npub        %s\nhex         %s\nfingerprint %s\n", npub(sk.Public()), sk.Public().Hex(), enroll.Fingerprint(sk.Public()))
		return 0
	case "passwd":
		return passwd(dir)
	case "enroll":
		sk, err := loadKey(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		db, err := openDB(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer db.Close()
		code := enroll.NewCode(sk.Public())
		if err := db.SaveCode(context.Background(), code, time.Now().Add(enroll.CodeLifetime)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("Enrollment code: %s (valid 15 minutes, once)\n\nOn the rig:  roadie enroll https://<this station> --code %s\n", code, code)
		return 0
	case "token":
		db, err := openDB(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer db.Close()
		ctx := context.Background()
		switch {
		case len(words) == 2 && words[0] == "create":
			secret, err := db.CreateToken(ctx, words[1], *readOnly)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			fmt.Printf("%s\n", secret)
			return 0
		case len(words) == 1 && words[0] == "list":
			tokens, _ := db.Tokens(ctx)
			for _, t := range tokens {
				access := "control"
				if t.ReadOnly {
					access = "read-only"
				}
				used := "never used"
				if !t.UsedAt.IsZero() {
					used = "used " + t.UsedAt.Format(time.DateTime)
				}
				fmt.Printf("%-20s %-10s created %s, %s\n", t.Name, access, t.CreatedAt.Format(time.DateOnly), used)
			}
			return 0
		case len(words) == 2 && words[0] == "revoke":
			if err := db.RevokeToken(ctx, words[1]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return 0
		}
		fmt.Fprintln(os.Stderr, "usage: station token create NAME [--read-only] | list | revoke NAME")
		return 2
	}
	fmt.Fprintf(os.Stderr, "station: unknown command %q (try station help)\n", name)
	return 2
}

// passwd asks for the password twice and saves its hash.
func passwd(dir string) int {
	cfg, err := loadConfig(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	read := func(prompt string) (string, error) {
		fmt.Print(prompt)
		if term.IsTerminal(int(os.Stdin.Fd())) {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			return string(b), err
		}
		var s string
		_, err := fmt.Scanln(&s)
		return s, err
	}
	pw, err := read("New password: ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(pw) < 8 {
		fmt.Fprintln(os.Stderr, "a password needs at least 8 characters")
		return 1
	}
	again, _ := read("Again: ")
	if pw != again {
		fmt.Fprintln(os.Stderr, "the two differ")
		return 1
	}
	if cfg.PasswordHash, err = hashPassword(pw); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := saveConfig(dir, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("Password set.")
	return 0
}

func serve(args []string) int {
	fs := flag.NewFlagSet("station", flag.ExitOnError)
	dirFlag := fs.String("data-dir", "", "where the station keeps its files (default $STATION_DIR or the user config dir)")
	listen := fs.String("listen", "", "address to serve HTTPS on (LAN mode: :8443; with --acme: :443)")
	tailnetMode := fs.Bool("tailnet", false, "join the tailnet as a node with tsnet and serve there only")
	hostname := fs.String("hostname", "station", "the node's name on the tailnet")
	controlURL := fs.String("control-url", "", "a Headscale or other control server, with --tailnet")
	acmeHost := fs.String("acme", "", "public mode: HTTPS with a Let's Encrypt certificate for this host name, on :443, the HTTP-01 challenge on :80")
	httpAddr := fs.String("http", "", "public mode behind your own TLS proxy: plain HTTP on this address, such as :8080; needs --public-url")
	publicURL := fs.String("public-url", "", "how rigs and browsers reach this station (default: derived)")
	turnMode := fs.String("turn", "auto", "run the TURN server: on, off, or auto (on in public mode)")
	turnListen := fs.String("turn-listen", turn.DefaultListen, "TURN's UDP address; on Fly.io, fly-global-services:3478")
	turnPublicIP := fs.String("turn-public-ip", "", "the public address TURN is reached at (default: the --acme host's)")
	turnRelay := fs.String("turn-relay-addr", "", "the address relay sockets bind (default: the listen address's host); on Fly.io, fly-global-services")
	turnPorts := fs.String("turn-ports", fmt.Sprintf("%d-%d", turn.DefaultMinPort, turn.DefaultMaxPort), "the relay port range, reachable from outside")
	turnDeny := fs.String("turn-deny", "", "more destinations TURN refuses, comma-separated CIDRs, besides private, loopback, link-local and shared ranges")
	turnMTU := fs.Int("turn-mtu", 0, "TURN's inbound MTU (default: 1600)")
	name := fs.String("name", "", "the station's name (saved)")
	allow := fs.String("allow", "", "tailnet logins and tags that may sign in by identity, comma-separated (saved)")
	noMDNS := fs.Bool("no-mdns", false, "do not advertise on the LAN")
	fs.Usage = func() { usage(); fmt.Println(); fs.PrintDefaults() }
	fs.Parse(args)

	dir, err := dataDir(*dirFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	cfg, err := loadConfig(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	changed := false
	if *name != "" && *name != cfg.Name {
		cfg.Name, changed = *name, true
	}
	if *allow != "" {
		cfg.Allow, changed = strings.Split(*allow, ","), true
	}
	// A password from the environment, for a host where secrets are
	// environment variables, such as Fly.io.
	if pw := os.Getenv("STATION_PASSWORD"); pw != "" && !checkPassword(cfg.PasswordHash, pw) {
		if len(pw) < 8 {
			fmt.Fprintln(os.Stderr, "STATION_PASSWORD needs at least 8 characters")
			return 1
		}
		if cfg.PasswordHash, err = hashPassword(pw); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		changed = true
		fmt.Println("Password set from STATION_PASSWORD")
	}
	if changed {
		if err := saveConfig(dir, cfg); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	key, err := loadKey(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	db, err := openDB(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()

	ctx, stop := ossignal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var ln net.Listener
	var who identity
	var closeNet func()
	mode := "lan"
	base := *publicURL
	proxied := false
	switch {
	case *tailnetMode:
		mode = "tailnet"
		fmt.Println("Joining the tailnet (a login link appears below if this node is new)...")
		var tsURL string
		ln, tsURL, who, closeNet, err = tailnet(ctx, dir, *hostname, *controlURL)
		if err != nil {
			fmt.Fprintln(os.Stderr, "tailnet:", err)
			return 1
		}
		defer closeNet()
		if base == "" {
			base = tsURL
		}
	case *acmeHost != "":
		mode = "public"
		addr := *listen
		if addr == "" {
			addr = ":443"
		}
		ln, closeNet, err = acme(dir, *acmeHost, ":80", addr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer closeNet()
		if base == "" {
			base = "https://" + *acmeHost
		}
	case *httpAddr != "":
		mode = "public"
		proxied = true
		if base == "" {
			fmt.Fprintln(os.Stderr, "--http needs --public-url, the https address your proxy serves")
			return 1
		}
		ln, err = net.Listen("tcp", *httpAddr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	default:
		addr := *listen
		if addr == "" {
			addr = ":8443"
		}
		cert, created, err := selfSigned(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if created {
			fmt.Println("Made a self-signed certificate in", dir)
		}
		ln, err = tls.Listen("tcp", addr, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if base == "" {
			host := localIPv4()
			if host == "" {
				host = "localhost"
			}
			_, port, _ := net.SplitHostPort(ln.Addr().String())
			base = "https://" + net.JoinHostPort(host, port)
		}
	}
	base = strings.TrimRight(base, "/")
	tunnelURL := "wss" + strings.TrimPrefix(base, "https") + "/v1/tunnel"

	ts := &tunnel.Server{Key: key.Public(), Devices: db, Events: db}
	mgr := &hold.Manager{Holds: db, Devices: db, Events: db, Audit: db, Key: key,
		OnRevoke: func(_ context.Context, rigID string, ev nostr.Event) {
			if err := ts.Revoke(rigID, ev); err != nil && !errors.Is(err, tunnel.ErrOffline) {
				fmt.Fprintln(os.Stderr, "revoke:", err)
			}
		}}
	// TURN: on in public mode unless told otherwise, with credentials
	// derived from the station's key, so nothing more is kept.
	var turnSrv *turn.Server
	turnInfo := ""
	if *turnMode == "on" || (*turnMode == "auto" && mode == "public") {
		pub := net.ParseIP(*turnPublicIP)
		if pub == nil && *turnPublicIP != "" {
			fmt.Fprintf(os.Stderr, "--turn-public-ip %q is not an address\n", *turnPublicIP)
			return 1
		}
		if pub == nil && *acmeHost != "" {
			if ips, err := net.LookupIP(*acmeHost); err == nil {
				for _, ip := range ips {
					if ip.To4() != nil {
						pub = ip
						break
					}
				}
			}
		}
		if pub == nil {
			fmt.Fprintln(os.Stderr, "TURN needs --turn-public-ip, the address it is reached at from outside")
			return 1
		}
		var lo, hi uint16
		if _, err := fmt.Sscanf(*turnPorts, "%d-%d", &lo, &hi); err != nil || hi < lo {
			fmt.Fprintf(os.Stderr, "--turn-ports %q: want low-high\n", *turnPorts)
			return 1
		}
		var deny []netip.Prefix
		for _, c := range strings.Split(*turnDeny, ",") {
			if c = strings.TrimSpace(c); c != "" {
				p, err := netip.ParsePrefix(c)
				if err != nil {
					fmt.Fprintf(os.Stderr, "--turn-deny %q: %v\n", c, err)
					return 1
				}
				deny = append(deny, p)
			}
		}
		secret := sha256.Sum256([]byte("vibium station turn " + key.Hex()))
		turnSrv = &turn.Server{Listen: *turnListen, PublicIP: pub, RelayAddress: *turnRelay, MinPort: lo, MaxPort: hi,
			Secret: secret[:], Deny: deny, MTU: *turnMTU,
			Valid: func(holdID string) bool {
				l, err := db.Hold(context.Background(), holdID)
				return err == nil && l.Held(time.Now())
			},
			Logf: func(format string, args ...any) { fmt.Printf(format+"\n", args...) }}
		if err := turnSrv.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer turnSrv.Close()
		mgr.ICE = func(l station.Hold) []token.ICEServer { return turnSrv.Credentials(l.ID, l.ExpiresAt) }
		turnInfo = fmt.Sprintf("on: %s, relay ports %d-%d", strings.Join(turnSrv.URLs(), " "), lo, hi)
	}
	rl := &relay.Relay{Tunnel: ts, Logf: func(format string, args ...any) { fmt.Printf(format+"\n", args...) }}
	s := &server{
		cfg: cfg, dir: dir, db: db, key: key, mode: mode, publicURL: base,
		auth:     newAuth(cfg, db, who, strings.HasPrefix(base, "https"), proxied),
		holds:    mgr,
		tunnel:   ts,
		relay:    rl,
		turn:     turnSrv,
		turnInfo: turnInfo,
		enroll:   &enroll.Handler{Key: key, Name: cfg.Name, TunnelURL: tunnelURL, Devices: db, Events: db, Codes: db, Audit: db},
		signal: &signal.Handler{Relay: rl, Devices: db,
			Touch: func(ctx context.Context, deviceID string) {
				if l, err := db.Current(ctx, deviceID); err == nil {
					mgr.Touch(ctx, l.ID)
				}
			}},
	}
	if mode == "lan" && !*noMDNS {
		_, port, _ := net.SplitHostPort(ln.Addr().String())
		var p int
		fmt.Sscan(port, &p)
		if shutdown, err := enroll.Advertise(cfg.Name, p); err == nil {
			s.mdns = true
			defer shutdown()
		} else {
			fmt.Fprintln(os.Stderr, "mdns:", err)
		}
	}
	// Holds nobody refreshes end on their own.
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if ended, err := mgr.Expire(context.Background()); err == nil {
					for _, l := range ended {
						fmt.Printf("hold %s on %s ended\n", l.ID, l.DeviceID)
					}
				}
			}
		}
	}()

	srv := &http.Server{Handler: s.handler(), ReadHeaderTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()
	fmt.Printf("%s: %s mode at %s\n", cfg.Name, mode, base)
	fmt.Printf("   key %s, rigs enroll at %s\n", npub(key.Public()), tunnelURL)
	if turnInfo != "" {
		fmt.Println("   TURN " + turnInfo)
	}
	if cfg.PasswordHash == "" {
		fmt.Println("   no password yet: run `station passwd` to sign in by password")
	}
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
