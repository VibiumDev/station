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
	"github.com/VibiumDev/station/blob"
	"github.com/VibiumDev/station/enroll"
	"github.com/VibiumDev/station/hold"
	"github.com/VibiumDev/station/jam"
	"github.com/VibiumDev/station/records"
	"github.com/VibiumDev/station/relay"
	"github.com/VibiumDev/station/sdnotify"
	"github.com/VibiumDev/station/signal"
	"github.com/VibiumDev/station/sqlite"
	"github.com/VibiumDev/station/token"
	"github.com/VibiumDev/station/tunnel"
	"github.com/VibiumDev/station/turn"
)

// version is stamped by the release build (-X main.version=...).
var version = "dev"

func main() {
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		os.Exit(runCommand(os.Args[1], os.Args[2:]))
	}
	os.Exit(serve(os.Args[1:]))
}

func usage() {
	fmt.Println(`usage: station [flags]                 run the station (station -h for the flags)
       station passwd [--reset] [login]  set a user's password, the owner's by default;
                                       --reset also forgets their authenticator and backup codes
       station enroll                  make a one-time enrollment code for a rig
       station token create NAME [--level watch|drive|admin]
       station token list | revoke NAME
       station key                     show the station's key
       station version                 show the build's version`)
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
	readOnly := fs.Bool("read-only", false, "token create: the token may only watch (the same as --level watch)")
	levelFlag := fs.String("level", "drive", "token create: watch, drive or admin")
	reset := fs.Bool("reset", false, "passwd: also forget the user's authenticator and backup codes")
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
	if name == "version" {
		fmt.Println(version)
		return 0
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
		return passwd(dir, words, *reset)
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
			level, err := station.ParseTokenLevel(*levelFlag)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 2
			}
			if *readOnly {
				level = station.TokenWatch
			}
			secret, err := db.CreateToken(ctx, words[1], level, "")
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			fmt.Printf("%s\n", secret)
			return 0
		case len(words) == 1 && words[0] == "list":
			tokens, _ := db.Tokens(ctx)
			for _, t := range tokens {
				used := "never used"
				if !t.UsedAt.IsZero() {
					used = "used " + t.UsedAt.Format(time.DateTime)
				}
				fmt.Printf("%-20s %-6s created %s, %s\n", t.Name, t.Level, t.CreatedAt.Format(time.DateOnly), used)
			}
			return 0
		case len(words) == 2 && words[0] == "revoke":
			if err := db.RevokeToken(ctx, words[1]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return 0
		}
		fmt.Fprintln(os.Stderr, "usage: station token create NAME [--level watch|drive|admin] | list | revoke NAME")
		return 2
	}
	fmt.Fprintf(os.Stderr, "station: unknown command %q (try station help)\n", name)
	return 2
}

// ensureOwner finds the owner user, seeding them from the config file's
// password hash when the station comes from before users, or making
// them with no password yet.
func ensureOwner(ctx context.Context, db *sqlite.DB, cfg *Config) (station.User, error) {
	if _, err := db.SeedOwner(ctx, cfg.PasswordHash); err != nil {
		return station.User{}, err
	}
	u, err := db.UserByLogin(ctx, station.OwnerLogin)
	if errors.Is(err, station.ErrNotFound) {
		u = station.User{ID: station.NewID("usr"), Login: station.OwnerLogin, Name: "Owner", Admin: true}
		err = db.PutUser(ctx, u)
	}
	return u, err
}

// ownerHasPassword says whether the owner can sign in by password: the
// database holds the hash, wherever it came from, and the config file's
// own field is only what a station from before users is seeded from.
func ownerHasPassword(ctx context.Context, db *sqlite.DB) bool {
	u, err := db.UserByLogin(ctx, station.OwnerLogin)
	return err == nil && u.HasPassword
}

// passwd asks for a password twice and stores its hash for a user: the
// owner, or the login given. With --reset the user's authenticator and
// backup codes are forgotten too, so their next sign-in sets up a new
// one: the way back in for an owner who lost both phone and codes.
func passwd(dir string, words []string, reset bool) int {
	cfg, err := loadConfig(dir)
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
	ctx := context.Background()
	login := station.OwnerLogin
	if len(words) > 0 {
		login = words[0]
	}
	var user station.User
	if login == station.OwnerLogin {
		user, err = ensureOwner(ctx, db, cfg)
	} else {
		user, err = db.UserByLogin(ctx, login)
		if errors.Is(err, station.ErrNotFound) {
			err = fmt.Errorf("no user %q: an admin adds users on Settings", login)
		}
	}
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
	if err := checkPasswordRules(pw, user.Login); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	again, _ := read("Again: ")
	if pw != again {
		fmt.Fprintln(os.Stderr, "the two differ")
		return 1
	}
	hash, err := hashPassword(pw)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := db.SetPassword(ctx, user.ID, hash); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if reset {
		if err := db.SetSecondFactor(ctx, user.ID, "", time.Time{}); err == nil {
			err = db.SetBackupCodes(ctx, user.ID, nil)
		} else {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("Password set for %s; their authenticator and backup codes are forgotten, and their next sign-in sets up a new one.\n", user.Login)
		return 0
	}
	fmt.Printf("Password set for %s.\n", user.Login)
	return 0
}

func serve(args []string) int {
	fs := flag.NewFlagSet("station", flag.ExitOnError)
	dirFlag := fs.String("data-dir", "", "where the station keeps its files (default $STATION_DIR or the user config dir)")
	listen := fs.String("listen", "", "address to serve HTTPS on (LAN mode: :8443; with --public: :443)")
	tailnetMode := fs.Bool("tailnet", false, "join the tailnet as a node with tsnet and serve there only")
	hostname := fs.String("hostname", "station", "the node's name on the tailnet")
	controlURL := fs.String("control-url", "", "a Headscale or other control server, with --tailnet")
	publicHost := fs.String("public", "", "public mode: serve HTTPS on :443 with a Let's Encrypt certificate for this host name, answering its challenge on :80")
	httpAddr := fs.String("http", "", "public mode behind your own TLS proxy: plain HTTP on this address, such as :8080; needs --public-url")
	publicURL := fs.String("public-url", "", "how rigs and browsers reach this station (default: derived)")
	turnMode := fs.String("turn", "auto", "run the TURN server: on, off, or auto (on in public mode)")
	turnListen := fs.String("turn-listen", turn.DefaultListen, "TURN's UDP address; on Fly.io, fly-global-services:3478")
	turnPublicIP := fs.String("turn-public-ip", "", "the public address TURN is reached at (default: the --public host's)")
	turnRelay := fs.String("turn-relay-addr", "", "the address relay sockets bind (default: the listen address's host); on Fly.io, fly-global-services")
	turnPorts := fs.String("turn-ports", fmt.Sprintf("%d-%d", turn.DefaultMinPort, turn.DefaultMaxPort), "the relay port range, reachable from outside")
	turnDeny := fs.String("turn-deny", "", "more destinations TURN refuses, comma-separated CIDRs, besides private, loopback, link-local and shared ranges")
	turnMTU := fs.Int("turn-mtu", 0, "TURN's inbound MTU (default: 1600)")
	name := fs.String("name", "", "the station's name (saved)")
	allow := fs.String("allow", "", "tailnet logins and tags that may sign in by identity, comma-separated (saved)")
	noMDNS := fs.Bool("no-mdns", false, "do not advertise on the LAN")
	recordsDir := fs.String("records-dir", "", "where the Record Store keeps its files (default: blobs/ in the data directory)")
	recordsMaxFile := fs.String("records-max-file", "256M", "the largest file the Record Store takes (bytes, or K, M, G)")
	recordsMaxTotal := fs.String("records-max-total", "2G", "how much the Record Store keeps before the oldest files go (0: no limit)")
	recordsMaxAge := fs.Duration("records-max-age", 30*24*time.Hour, "how long the Record Store keeps a file (0: for ever)")
	vibiumPath := fs.String("vibium", "", "the Vibium binary jams drive devices with (default: $STATION_VIBIUM, or vibium on PATH)")
	piperPath := fs.String("piper", "", "the Python that has piper-tts[http], whose server the station runs for jams to speak (default: $STATION_PIPER, piper/venv/bin/python in the data directory, or piper on PATH)")
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
	// The owner: seeded from the config file's password hash the first
	// time, and given the password from the environment when there is
	// one, for a host where secrets are environment variables, such as
	// Fly.io.
	if pw := os.Getenv("STATION_PASSWORD"); pw != "" {
		if len(pw) < 8 {
			fmt.Fprintln(os.Stderr, "STATION_PASSWORD needs at least 8 characters")
			return 1
		}
		if err := checkPasswordRules(pw, station.OwnerLogin); err != nil {
			fmt.Fprintf(os.Stderr, "STATION_PASSWORD: %v; set a stronger one\n", err)
		}
		owner, err := ensureOwner(context.Background(), db, cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if hash, _ := db.PasswordHash(context.Background(), owner.ID); !checkPassword(hash, pw) {
			h, err := hashPassword(pw)
			if err == nil {
				err = db.SetPassword(context.Background(), owner.ID, h)
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			fmt.Println("Owner's password set from STATION_PASSWORD")
		}
	} else if _, err := db.SeedOwner(context.Background(), cfg.PasswordHash); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

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
	case *publicHost != "":
		mode = "public"
		addr := *listen
		if addr == "" {
			addr = ":443"
		}
		ln, closeNet, err = acme(dir, *publicHost, ":80", addr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer closeNet()
		if base == "" {
			base = "https://" + *publicHost
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
		if pub == nil && *publicHost != "" {
			if ips, err := net.LookupIP(*publicHost); err == nil {
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
	// A hold's end closes the connections relayed on it, and a hold with
	// connections open is in use, not idle.
	mgr.OnEnd = func(h station.Hold) { rl.End(h.ID) }
	mgr.InUse = func(holdID string) bool { return rl.Open(holdID) > 0 }
	s := &server{
		cfg: cfg, dir: dir, db: db, key: key, mode: mode, publicURL: base,
		auth:     newAuth(cfg, db, who, strings.HasPrefix(base, "https"), proxied).withDeviceKey(key[:]),
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
	// The Record Store: files by hash on disk, indexed in the database,
	// uploads signed by the station key on the account's behalf.
	maxFile, err := parseSize(*recordsMaxFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "--records-max-file:", err)
		return 1
	}
	maxTotal, err := parseSize(*recordsMaxTotal)
	if err != nil {
		fmt.Fprintln(os.Stderr, "--records-max-total:", err)
		return 1
	}
	blobDir := *recordsDir
	if blobDir == "" {
		blobDir = filepath.Join(dir, "blobs")
	}
	s.records = &records.Store{Blobs: blob.Disk{Dir: blobDir}, Index: db, Events: db, Holds: db, Devices: db, Signer: records.Key(key),
		HoldsOf: db.HoldsOf, BaseURL: base, UnpackDir: filepath.Join(blobDir, "unpacked"), MaxFile: maxFile, MaxTotal: maxTotal, MaxAge: *recordsMaxAge, Audit: db}
	s.recordsDir = blobDir
	// Whoever may watch a device may see its records.
	s.records.Access = func(ctx context.Context, p station.Principal, deviceID string) bool {
		return s.auth.level(ctx, p, deviceID).AtLeast(station.LevelWatch)
	}
	// Jams: the models the owner set up, under ai/ in the data directory,
	// and the Vibium binary whose MCP server drives the devices.
	s.jams = &jams{s: s, models: &jam.Store{Dir: filepath.Join(dir, "ai")}, dir: filepath.Join(dir, "jams"), live: map[string]*liveJam{}}
	if *vibiumPath == "" {
		*vibiumPath = os.Getenv("STATION_VIBIUM")
	}
	if path, err := jam.LookPath(*vibiumPath); err != nil {
		s.jams.missing = err.Error()
	} else {
		s.jams.vibium = path
	}
	s.jams.closeLeftovers(context.Background())
	// The station's own Piper, when it is installed: a say voice kept
	// loaded, listed on Settings without adding it.
	s.piper = &piperRunner{voicesDir: filepath.Join(dir, "piper"), logPath: filepath.Join(dir, "piper", "server.log"), ready: 2 * time.Minute,
		logf: func(f string, a ...any) { fmt.Printf(f+"\n", a...) }, done: make(chan struct{})}
	s.piper.preferred = func() string {
		if c, err := s.jams.models.Get(piperModel); err == nil && c.OwnPiper() {
			return c.Model
		}
		return ""
	}
	if python, err := findPiper(*piperPath, dir); err != nil {
		s.piper.missing = err.Error()
		close(s.piper.done)
	} else {
		s.piper.python = python
		s.jams.models.Builtin = s.piper.builtin
		go s.piper.run(ctx)
	}
	if mode == "lan" && !*noMDNS {
		_, port, _ := net.SplitHostPort(ln.Addr().String())
		var p int
		fmt.Sscan(port, &p)
		if shutdown, err := enroll.Advertise(cfg.Name, p); err == nil {
			s.mdns, s.mdnsStop, s.mdnsPort = true, shutdown, p
			defer func() {
				s.nameMu.Lock()
				defer s.nameMu.Unlock()
				if s.mdnsStop != nil {
					s.mdnsStop()
				}
			}()
		} else {
			fmt.Fprintln(os.Stderr, "mdns:", err)
		}
	}
	// Retention: aged and surplus recordings go, once at start and
	// then hourly; their file events stay.
	go func() {
		sweep := func() {
			gone, err := s.records.Sweep(context.Background())
			if err != nil {
				fmt.Println("records: retention:", err)
			}
			for _, b := range gone {
				fmt.Printf("records: retention removed %s %s (%d bytes)\n", b.Name, b.SHA256[:12], b.Size)
			}
		}
		if n, err := s.records.UnpackAll(context.Background()); err != nil {
			fmt.Println("records: unpacking:", err)
		} else if n > 0 {
			fmt.Printf("records: unpacked %d recording(s)\n", n)
		}
		sweep()
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				sweep()
			}
		}
	}()
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
	// A jam's Vibium reaches /session on a loopback listener without
	// TLS, since the station's own certificate may be one it made
	// itself; every request there still needs a token.
	if lb, err := net.Listen("tcp", "127.0.0.1:0"); err == nil {
		s.jams.loopback = "ws://" + lb.Addr().String()
		go srv.Serve(lb)
	} else {
		fmt.Fprintln(os.Stderr, "jams: loopback listener:", err)
	}
	go func() {
		<-ctx.Done()
		sdnotify.Stopping()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()
	// Under systemd with a watchdog: alive for as long as the handler
	// still answers, so a wedged station is restarted rather than kept.
	go sdnotify.Keep(ctx, func() bool { return answers(srv.Handler) })
	fmt.Printf("%s: %s mode at %s\n", cfg.Name, mode, base)
	fmt.Printf("   key %s, rigs enroll at %s\n", npub(key.Public()), tunnelURL)
	if turnInfo != "" {
		fmt.Println("   TURN " + turnInfo)
	}
	if !ownerHasPassword(context.Background(), db) {
		fmt.Println("   no password yet: run `station passwd` to sign in by password")
	}
	sdnotify.Ready()
	err = srv.Serve(ln)
	stop() // a station that could not serve ends Piper's server too
	<-s.piper.done
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// answers reports whether the handler still replies to a request, with any
// status at all, within a few seconds: the watchdog's measure of alive.
func answers(h http.Handler) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		req, err := http.NewRequest(http.MethodGet, "/v1/devices", nil)
		if err != nil {
			return
		}
		req.RemoteAddr = "127.0.0.1:0"
		h.ServeHTTP(discardResponse{http.Header{}}, req)
	}()
	select {
	case <-done:
		return true
	case <-time.After(5 * time.Second):
		return false
	}
}

// discardResponse is a ResponseWriter that keeps nothing.
type discardResponse struct{ h http.Header }

func (d discardResponse) Header() http.Header         { return d.h }
func (d discardResponse) Write(b []byte) (int, error) { return len(b), nil }
func (d discardResponse) WriteHeader(int)             {}
