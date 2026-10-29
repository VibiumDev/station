// Command station-probe checks a station from outside, the way a browser
// would: it signs in with an API token, holds a device, connects over
// WebRTC through TURN only (unless told otherwise), measures what arrives,
// taps the screen and confirms the screenshot changed, and releases the
// hold. It exits non-zero on any failure, so it doubles as a smoke test
// for any station, including a hosted one.
//
//	station-probe -station https://host -token stk_... [-device rig_x/phone] [-t 10s] [-ice relay|all] [-tap 0.5,0.5] [-insecure]
//
// With -bidi it is a plain WebDriver BiDi client instead: it opens the
// device's BiDi socket through the station (the URL the hold endpoint
// returns), runs session.new, session.status, browsingContext.getTree,
// browsingContext.captureScreenshot, a tap with input.performActions and
// session.end, and prints what came back. With -bidi-direct ws://roadie/session
// (and -bidi-direct-token for its own token) it runs the same sequence
// straight at the rig and compares the shapes of every answer, so the
// relay is shown to change nothing.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
)

type iceServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

type holdResponse struct {
	Grant    json.RawMessage `json:"grant"`
	ICE      []iceServer     `json:"ice"`
	Observer bool            `json:"observer"`
	Signal   string          `json:"signal"`
	Online   bool            `json:"online"`
	Hold     struct {
		ID      string `json:"id"`
		Seq     uint64 `json:"seq"`
		Subject string `json:"subject"`
	} `json:"hold"`
}

type device struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Caps      []string `json:"caps"`
	Online    bool     `json:"online"`
	Shortcuts []struct {
		Label string `json:"label"`
		Keys  string `json:"keys"`
	} `json:"shortcuts"`
}

type message struct {
	Type      string                   `json:"type"`
	SDP       string                   `json:"sdp,omitempty"`
	Candidate *webrtc.ICECandidateInit `json:"candidate,omitempty"`
	Error     string                   `json:"error,omitempty"`
	Grant     json.RawMessage          `json:"grant,omitempty"`
}

type probe struct {
	base, token string
	client      *http.Client
	failures    []string
}

func main() {
	station := flag.String("station", os.Getenv("STATION_URL"), "the station, https://host[:port] (or $STATION_URL)")
	token := flag.String("token", os.Getenv("STATION_TOKEN"), "an API token that may control (or $STATION_TOKEN)")
	dev := flag.String("device", "", "the device, rig_x/name (default: the first online one)")
	dur := flag.Duration("t", 10*time.Second, "how long to watch the picture")
	ice := flag.String("ice", "relay", "ICE transport policy: relay (through TURN only, as across the Internet) or all")
	tap := flag.String("tap", "0.5,0.5", "where to tap, as fractions of the picture; empty for no tap")
	insecure := flag.Bool("insecure", false, "accept the station's self-signed certificate (LAN mode)")
	diff := flag.Bool("diff", false, "no probe: print the mean luma difference between the two JPEG files given, for scripts")
	bidi := flag.Bool("bidi", false, "run the WebDriver BiDi sequence through the station instead of the WebRTC probe")
	bidiDirect := flag.String("bidi-direct", "", "with -bidi: also run the sequence straight at the rig's BiDi socket, this URL, and compare")
	bidiDirectToken := flag.String("bidi-direct-token", os.Getenv("ROADIE_TOKEN"), "with -bidi-direct: the rig's own bearer token (or $ROADIE_TOKEN)")
	bidiEntry := flag.String("bidi-entry", "", "with -bidi: open this path on the station instead of the device's, such as /session (the standard entry point), with no hold taken first")
	flag.Parse()
	if *diff {
		a, err1 := os.ReadFile(flag.Arg(0))
		b, err2 := os.ReadFile(flag.Arg(1))
		if err1 != nil || err2 != nil {
			fmt.Fprintln(os.Stderr, "station-probe -diff A.jpg B.jpg")
			os.Exit(2)
		}
		d, err := difference(a, b)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Printf("%.1f\n", d)
		return
	}
	if *station == "" || *token == "" {
		fmt.Fprintln(os.Stderr, "station-probe needs -station and -token")
		os.Exit(2)
	}
	p := &probe{base: strings.TrimRight(*station, "/"), token: *token,
		client: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: *insecure}}}}
	if *bidi {
		if err := p.runBiDi(*dev, *tap, *bidiDirect, *bidiDirectToken, *bidiEntry); err != nil {
			p.fail(err.Error())
		}
	} else if err := p.run(*dev, *dur, *ice, *tap); err != nil {
		p.fail(err.Error())
	}
	if len(p.failures) > 0 {
		fmt.Printf("FAIL: %s\n", strings.Join(p.failures, "; "))
		os.Exit(1)
	}
	fmt.Println("PASS")
}

func (p *probe) fail(msg string) {
	fmt.Println("   ✗ " + msg)
	p.failures = append(p.failures, msg)
}

// api calls the station with the token.
func (p *probe) api(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(data))
		}
		return fmt.Errorf("%s %s: %d %s", method, path, res.StatusCode, e.Error)
	}
	if out == nil {
		return nil
	}
	if raw, ok := out.(*[]byte); ok {
		*raw = data
		return nil
	}
	return json.Unmarshal(data, out)
}

func (p *probe) run(devID string, dur time.Duration, icePolicy, tap string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dur+90*time.Second)
	defer cancel()

	var devices []device
	if err := p.api(ctx, "GET", "/v1/devices", nil, &devices); err != nil {
		return err
	}
	var dev *device
	for i := range devices {
		d := &devices[i]
		if (devID == "" && d.Online && d.ID != "") || d.ID == devID {
			dev = d
			break
		}
	}
	if dev == nil {
		return fmt.Errorf("no device to probe among %d listed", len(devices))
	}
	fmt.Printf("device %s (%s) online=%v caps=%v\n", dev.ID, dev.Kind, dev.Online, dev.Caps)
	if !dev.Online {
		return errors.New("the device is offline")
	}

	var hold holdResponse
	if err := p.api(ctx, "POST", "/v1/devices/"+dev.ID+"/hold", map[string]any{"scopes": []string{"view", "input"}}, &hold); err != nil {
		return err
	}
	defer func() {
		var out map[string]any
		if err := p.api(context.Background(), "DELETE", "/v1/devices/"+dev.ID+"/hold", nil, &out); err != nil {
			p.fail("release: " + err.Error())
		} else if out["released"] != true {
			p.fail("the hold was not released")
		} else {
			fmt.Println("hold released")
		}
	}()
	var servers []webrtc.ICEServer
	hasTURN := false
	for _, s := range hold.ICE {
		servers = append(servers, webrtc.ICEServer{URLs: s.URLs, Username: s.Username, Credential: s.Credential})
		for _, u := range s.URLs {
			if strings.HasPrefix(u, "turn") {
				hasTURN = true
			}
		}
	}
	fmt.Printf("hold %s seq %d as %s, observer=%v, ICE servers: %v\n", hold.Hold.ID, hold.Hold.Seq, hold.Hold.Subject, hold.Observer, urls(hold.ICE))
	if hold.Observer {
		return errors.New("got an observer seat, not control")
	}
	if icePolicy == "relay" && !hasTURN {
		return errors.New("relay-only asked, but the station gave no TURN server")
	}

	// Signaling through the station, the grant in the hello.
	wsURL := "ws" + strings.TrimPrefix(p.base, "http") + hold.Signal
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: p.client, HTTPHeader: http.Header{"Authorization": {"Bearer " + p.token}}})
	if err != nil {
		return fmt.Errorf("signaling: %w", err)
	}
	defer conn.CloseNow()
	var wmu sync.Mutex
	send := func(m message) error {
		data, _ := json.Marshal(m)
		wmu.Lock()
		defer wmu.Unlock()
		return conn.Write(ctx, websocket.MessageText, data)
	}

	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		return err
	}
	reg := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, reg); err != nil {
		return err
	}
	cfg := webrtc.Configuration{ICEServers: servers}
	if icePolicy == "relay" {
		cfg.ICETransportPolicy = webrtc.ICETransportPolicyRelay
	}
	pc, err := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(reg)).NewPeerConnection(cfg)
	if err != nil {
		return err
	}
	defer pc.Close()

	var st struct {
		sync.Mutex
		packets, bytes, frames, keyframes int
		first, last                       time.Time
		lastTS                            uint32
		audioPackets, audioBytes          int
		audioCodec                        string
		lastControl                       string
	}
	gotVideo := make(chan struct{})
	var once sync.Once
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		fmt.Printf("track: %s %s\n", tr.Kind(), tr.Codec().MimeType)
		if tr.Kind() == webrtc.RTPCodecTypeAudio {
			for {
				pkt, _, err := tr.ReadRTP()
				if err != nil {
					return
				}
				st.Lock()
				st.audioPackets++
				st.audioBytes += len(pkt.Payload)
				st.audioCodec = tr.Codec().MimeType
				st.Unlock()
			}
		}
		go func() {
			t := time.NewTicker(2 * time.Second)
			defer t.Stop()
			for range t.C {
				if ctx.Err() != nil {
					return
				}
				pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(tr.SSRC())}})
			}
		}()
		var depacketizer codecs.H264Packet
		for {
			pkt, _, err := tr.ReadRTP()
			if err != nil {
				return
			}
			once.Do(func() { close(gotVideo) })
			st.Lock()
			st.packets++
			st.bytes += len(pkt.Payload)
			now := time.Now()
			if st.first.IsZero() {
				st.first = now
			}
			st.last = now
			if pkt.Timestamp != st.lastTS {
				st.frames++
				st.lastTS = pkt.Timestamp
			}
			if nal, err := depacketizer.Unmarshal(pkt.Payload); err == nil && pkt.Marker && hasIDR(nal) {
				st.keyframes++
			}
			st.Unlock()
		}
	})
	var hid *webrtc.DataChannel
	hidOpen := make(chan struct{})
	var hidOnce sync.Once
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		switch dc.Label() {
		case "hid":
			hid = dc
			dc.OnOpen(func() { hidOnce.Do(func() { close(hidOpen) }) })
		case "control":
			dc.OnMessage(func(m webrtc.DataChannelMessage) {
				st.Lock()
				st.lastControl = string(m.Data)
				st.Unlock()
			})
		}
	})
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			init := c.ToJSON()
			send(message{Type: "candidate", Candidate: &init})
		}
	})
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) { fmt.Printf("connection: %s\n", s) })

	if err := send(message{Type: "hello", Grant: hold.Grant}); err != nil {
		return err
	}
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var msg message
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			switch msg.Type {
			case "offer":
				if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: msg.SDP}); err != nil {
					fmt.Println("offer:", err)
					return
				}
				ans, err := pc.CreateAnswer(nil)
				if err != nil {
					fmt.Println("answer:", err)
					return
				}
				pc.SetLocalDescription(ans)
				send(message{Type: "answer", SDP: ans.SDP})
			case "candidate":
				if msg.Candidate != nil {
					pc.AddICECandidate(*msg.Candidate)
				}
			case "error":
				fmt.Println("rig:", msg.Error)
			}
		}
	}()

	select {
	case <-gotVideo:
	case <-time.After(25 * time.Second):
		return errors.New("no video within 25 s")
	}
	time.Sleep(dur)

	// The path the media took, and its round trip.
	if pair, err := pc.SCTP().Transport().ICETransport().GetSelectedCandidatePair(); err == nil && pair != nil {
		fmt.Printf("path: local %s %s:%d, remote %s %s:%d\n", pair.Local.Typ, pair.Local.Address, pair.Local.Port, pair.Remote.Typ, pair.Remote.Address, pair.Remote.Port)
		if icePolicy == "relay" && pair.Local.Typ != webrtc.ICECandidateTypeRelay {
			p.fail("relay-only asked, but the local candidate is " + pair.Local.Typ.String())
		}
	} else {
		p.fail("no selected candidate pair")
	}
	for _, s := range pc.GetStats() {
		if cp, ok := s.(webrtc.ICECandidatePairStats); ok && cp.Nominated {
			fmt.Printf("round trip: %.0f ms (current), %.0f ms (total over %d)\n", cp.CurrentRoundTripTime*1000, cp.TotalRoundTripTime*1000, cp.ResponsesReceived)
		}
	}
	st.Lock()
	span := st.last.Sub(st.first).Seconds()
	if span <= 0 {
		span = 1
	}
	fmt.Printf("video: %d frames (%.1f fps), %d packets, %.0f kbps, %d keyframes over %.1f s\n",
		st.frames, float64(st.frames)/span, st.packets, float64(st.bytes)*8/span/1e3, st.keyframes, span)
	if st.audioPackets > 0 {
		fmt.Printf("audio: %s, %d packets, %.0f kbps\n", st.audioCodec, st.audioPackets, float64(st.audioBytes)*8/span/1e3)
	} else {
		fmt.Println("audio: none")
	}
	if st.lastControl != "" {
		fmt.Printf("host stats: %s\n", st.lastControl)
	}
	frames := st.frames
	st.Unlock()
	if frames < 5 {
		p.fail(fmt.Sprintf("only %d video frames", frames))
	}

	// A tap, and whether the picture changed because of it.
	if tap != "" && hasCap(dev.Caps, "hid") {
		var fx, fy float64
		if _, err := fmt.Sscanf(tap, "%g,%g", &fx, &fy); err != nil {
			return fmt.Errorf("-tap %q: want x,y as fractions", tap)
		}
		select {
		case <-hidOpen:
		case <-time.After(5 * time.Second):
			p.fail("the hid channel did not open")
			return nil
		}
		var before, after []byte
		if err := p.api(ctx, "GET", "/v1/devices/"+dev.ID+"/screenshot?quality=60", nil, &before); err != nil {
			p.fail("screenshot: " + err.Error())
			return nil
		}
		x, y := int(fx*32767), int(fy*32767)
		hid.SendText(fmt.Sprintf(`{"cmd":"mouse_move","x":%d,"y":%d,"space":"picture"}`, x, y))
		time.Sleep(300 * time.Millisecond)
		hid.SendText(`{"cmd":"mouse_press","buttons":1}`)
		time.Sleep(80 * time.Millisecond)
		hid.SendText(`{"cmd":"mouse_release","buttons":1}`)
		time.Sleep(2500 * time.Millisecond)
		if err := p.api(ctx, "GET", "/v1/devices/"+dev.ID+"/screenshot?quality=60", nil, &after); err != nil {
			p.fail("screenshot after the tap: " + err.Error())
			return nil
		}
		diff, err := difference(before, after)
		if err != nil {
			p.fail("screenshots: " + err.Error())
		} else {
			fmt.Printf("tap at %.2f,%.2f: screenshots %d and %d bytes, mean luma difference %.1f\n", fx, fy, len(before), len(after), diff)
			if diff < 2 {
				p.fail("the screen did not change after the tap")
			}
		}
		// Back to where it was, when the device says how.
		for _, s := range dev.Shortcuts {
			if s.Label == "Home" {
				if codes, ok := keycodes(s.Keys); ok {
					for _, c := range codes {
						hid.SendText(fmt.Sprintf(`{"cmd":"key_press","keycode":%d}`, c))
					}
					for i := len(codes) - 1; i >= 0; i-- {
						hid.SendText(fmt.Sprintf(`{"cmd":"key_release","keycode":%d}`, codes[i]))
					}
					time.Sleep(time.Second)
				}
				break
			}
		}
	}
	send(message{Type: "bye"})
	return nil
}

func urls(servers []iceServer) []string {
	var out []string
	for _, s := range servers {
		out = append(out, s.URLs...)
	}
	return out
}

func hasCap(caps []string, c string) bool {
	for _, x := range caps {
		if x == c {
			return true
		}
	}
	return false
}

func hasIDR(b []byte) bool {
	for i := 0; i+3 < len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 && b[i+3]&0x1f == 5 {
			return true
		}
	}
	return false
}

// difference is the mean absolute luma difference between two JPEGs on a
// coarse grid, 0 to 255; a static screen stays under 1, a tap that opens
// something lands far above.
func difference(a, b []byte) (float64, error) {
	ia, err := jpeg.Decode(bytes.NewReader(a))
	if err != nil {
		return 0, err
	}
	ib, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	ra, rb := ia.Bounds(), ib.Bounds()
	if ra.Dx() != rb.Dx() || ra.Dy() != rb.Dy() {
		return 255, nil
	}
	luma := func(im image.Image, x, y int) float64 {
		r, g, bl, _ := im.At(x, y).RGBA()
		return float64(r*299+g*587+bl*114) / 1000 / 257
	}
	const grid = 48
	var sum float64
	for gy := 0; gy < grid; gy++ {
		for gx := 0; gx < grid; gx++ {
			x := ra.Min.X + gx*ra.Dx()/grid
			y := ra.Min.Y + gy*ra.Dy()/grid
			d := luma(ia, x, y) - luma(ib, rb.Min.X+x-ra.Min.X, rb.Min.Y+y-ra.Min.Y)
			if d < 0 {
				d = -d
			}
			sum += d
		}
	}
	return sum / (grid * grid), nil
}

// keycodes turns a shortcut such as "GUI+h" into USB HID usage codes, as
// the shared viewer's input module does.
func keycodes(keys string) ([]int, bool) {
	named := map[string]int{"GUI": 227, "Meta": 227, "Cmd": 227, "Command": 227, "Win": 227, "Super": 227,
		"Alt": 226, "Option": 226, "Ctrl": 224, "Control": 224, "Shift": 225,
		"Enter": 40, "Return": 40, "Escape": 41, "Esc": 41, "Backspace": 42, "Tab": 43, "Space": 44,
		"Delete": 76, "Del": 76, "Home": 74, "End": 77, "PageUp": 75, "PageDown": 78,
		"ArrowRight": 79, "Right": 79, "ArrowLeft": 80, "Left": 80, "ArrowDown": 81, "Down": 81, "ArrowUp": 82, "Up": 82}
	var codes []int
	for _, tok := range strings.Split(keys, "+") {
		tok = strings.TrimSpace(tok)
		switch {
		case tok == "":
			return nil, false
		case named[tok] != 0:
			codes = append(codes, named[tok])
		case len(tok) == 1 && tok[0] >= 'a' && tok[0] <= 'z':
			codes = append(codes, 4+int(tok[0]-'a'))
		case len(tok) == 1 && tok[0] >= 'A' && tok[0] <= 'Z':
			codes = append(codes, 4+int(tok[0]-'A'))
		case len(tok) == 1 && tok[0] >= '1' && tok[0] <= '9':
			codes = append(codes, 30+int(tok[0]-'1'))
		case tok == "0":
			codes = append(codes, 39)
		case len(tok) > 1 && tok[0] == 'F':
			n, err := strconv.Atoi(tok[1:])
			if err != nil || n < 1 || n > 12 {
				return nil, false
			}
			codes = append(codes, 57+n)
		default:
			n, err := strconv.Atoi(tok)
			if err != nil {
				return nil, false
			}
			codes = append(codes, n)
		}
	}
	return codes, len(codes) > 0
}

// --- WebDriver BiDi ---

// bidiStep is one command of the sequence and what came back.
type bidiStep struct {
	Method string
	Params map[string]any
	Reply  map[string]any
	Took   time.Duration
}

// bidiRun opens a BiDi socket and runs the sequence, tapping at fx, fy
// of the viewport when tap is set. It returns every reply.
func (p *probe) bidiRun(ctx context.Context, wsURL, bearer string, tap string, caps map[string]any) ([]bidiStep, error) {
	header := http.Header{}
	if bearer != "" {
		header.Set("Authorization", "Bearer "+bearer)
	}
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: p.client, HTTPHeader: header})
	if err != nil {
		return nil, fmt.Errorf("bidi: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(16 << 20)
	id := 0
	call := func(method string, params map[string]any) (bidiStep, error) {
		id++
		if params == nil {
			params = map[string]any{}
		}
		req, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
		start := time.Now()
		if err := conn.Write(ctx, websocket.MessageText, req); err != nil {
			return bidiStep{}, err
		}
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return bidiStep{}, fmt.Errorf("%s: %w", method, err)
			}
			var reply map[string]any
			if err := json.Unmarshal(data, &reply); err != nil {
				return bidiStep{}, fmt.Errorf("%s: not JSON: %w", method, err)
			}
			// Events have no id; the answer to this command carries ours.
			if rid, ok := reply["id"].(float64); ok && int(rid) == id {
				return bidiStep{Method: method, Params: params, Reply: reply, Took: time.Since(start)}, nil
			}
		}
	}
	var steps []bidiStep
	add := func(st bidiStep, err error) error {
		if err != nil {
			return err
		}
		steps = append(steps, st)
		return nil
	}
	if err := add(call("session.status", nil)); err != nil {
		return steps, err
	}
	if caps == nil {
		caps = map[string]any{}
	}
	if err := add(call("session.new", map[string]any{"capabilities": caps})); err != nil {
		return steps, err
	}
	var width, height float64
	if res, ok := steps[len(steps)-1].Reply["result"].(map[string]any); ok {
		if caps, ok := res["capabilities"].(map[string]any); ok {
			if vp, ok := caps["roadie:viewport"].(map[string]any); ok {
				width, _ = vp["width"].(float64)
				height, _ = vp["height"].(float64)
			}
		}
	}
	if err := add(call("browsingContext.getTree", nil)); err != nil {
		return steps, err
	}
	if err := add(call("browsingContext.captureScreenshot", map[string]any{"context": "roadie"})); err != nil {
		return steps, err
	}
	if tap != "" && width > 0 && height > 0 {
		var fx, fy float64
		if _, err := fmt.Sscanf(tap, "%g,%g", &fx, &fy); err != nil {
			return steps, fmt.Errorf("-tap %q: want x,y as fractions", tap)
		}
		x, y := math.Round(fx*width), math.Round(fy*height)
		if err := add(call("input.performActions", map[string]any{"context": "roadie", "actions": []map[string]any{{
			"type": "pointer", "id": "finger", "parameters": map[string]any{"pointerType": "touch"},
			"actions": []map[string]any{
				{"type": "pointerMove", "x": x, "y": y},
				{"type": "pointerDown", "button": 0},
				{"type": "pause", "duration": 80},
				{"type": "pointerUp", "button": 0},
			}}}})); err != nil {
			return steps, err
		}
		time.Sleep(2 * time.Second)
		if err := add(call("browsingContext.captureScreenshot", map[string]any{"context": "roadie"})); err != nil {
			return steps, err
		}
	}
	if err := add(call("session.end", nil)); err != nil {
		return steps, err
	}
	return steps, nil
}

// shape describes a JSON value by its keys and types, not its values, so
// two runs can be compared although screenshots and timings differ.
func shape(v any) string {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("{")
		for i, k := range keys {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(k + ":" + shape(x[k]))
		}
		b.WriteString("}")
		return b.String()
	case []any:
		if len(x) == 0 {
			return "[]"
		}
		return "[" + shape(x[0]) + "...]"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "bool"
	case nil:
		return "null"
	}
	return "?"
}

func summarize(st bidiStep) string {
	if e, ok := st.Reply["error"]; ok {
		return fmt.Sprintf("error %v: %v", e, st.Reply["message"])
	}
	res, _ := st.Reply["result"].(map[string]any)
	switch st.Method {
	case "browsingContext.captureScreenshot":
		data, _ := res["data"].(string)
		return fmt.Sprintf("%d bytes of %v", len(data)*3/4, res["format"])
	default:
		out, _ := json.Marshal(res)
		if len(out) > 160 {
			out = append(out[:160], "..."...)
		}
		return string(out)
	}
}

func (p *probe) runBiDi(devID, tap, direct, directToken, entry string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var devices []device
	if err := p.api(ctx, "GET", "/v1/devices", nil, &devices); err != nil {
		return err
	}
	var dev *device
	for i := range devices {
		d := &devices[i]
		if (devID == "" && d.Online && d.ID != "") || d.ID == devID {
			dev = d
			break
		}
	}
	if dev == nil && entry != "" && devID != "" {
		// At the entry point the name goes to the station as asked, so a
		// wrong one shows what the station says to it.
		dev = &device{ID: devID}
	}
	if dev == nil {
		return fmt.Errorf("no device to probe among %d listed", len(devices))
	}
	fmt.Printf("device %s (%s) online=%v\n", dev.ID, dev.Kind, dev.Online)
	// The hold endpoint names the BiDi URL; the socket is opened with the
	// same bearer token and nothing else: no handshake of ours. With an
	// entry path, that path is opened cold, as a generic client would.
	var hold struct {
		BiDi string `json:"bidi"`
	}
	if entry != "" {
		hold.BiDi = entry
	} else if err := p.api(ctx, "POST", "/v1/devices/"+dev.ID+"/hold", map[string]any{"scopes": []string{"view", "input", "bidi"}}, &hold); err != nil {
		return err
	}
	if hold.BiDi == "" {
		return errors.New("the hold response names no bidi URL")
	}
	defer p.api(context.Background(), "DELETE", "/v1/devices/"+dev.ID+"/hold", nil, nil)
	wsURL := "ws" + strings.TrimPrefix(p.base, "http") + hold.BiDi
	fmt.Printf("bidi through the station: %s\n", wsURL)
	// At the standard entry point the device is named by capability when
	// one was asked for; with none, the station picks its default.
	var caps map[string]any
	if entry != "" && devID != "" {
		caps = map[string]any{"alwaysMatch": map[string]any{"vibium:device": dev.ID}}
	}
	via, err := p.bidiRun(ctx, wsURL, p.token, tap, caps)
	for _, st := range via {
		fmt.Printf("   %-36s %6.0f ms  %s\n", st.Method, float64(st.Took)/float64(time.Millisecond), summarize(st))
	}
	if err != nil {
		return err
	}
	if len(via) < 5 {
		p.fail(fmt.Sprintf("only %d replies", len(via)))
	}
	for _, st := range via {
		if _, isErr := st.Reply["error"]; isErr {
			p.fail(st.Method + " answered an error through the station")
		}
	}
	if tap != "" && len(via) >= 7 {
		before, _ := via[3].Reply["result"].(map[string]any)["data"].(string)
		after, _ := via[5].Reply["result"].(map[string]any)["data"].(string)
		b1, _ := base64.StdEncoding.DecodeString(before)
		b2, _ := base64.StdEncoding.DecodeString(after)
		if d, err := difference(b1, b2); err != nil {
			p.fail("screenshots: " + err.Error())
		} else {
			fmt.Printf("tap at %s: screenshots differ by %.1f\n", tap, d)
			if d < 2 {
				p.fail("the screen did not change after the BiDi tap")
			}
		}
	}
	if direct == "" {
		return nil
	}
	fmt.Printf("bidi straight at the rig: %s\n", direct)
	raw, err := p.bidiRun(ctx, direct, directToken, tap, nil)
	for _, st := range raw {
		fmt.Printf("   %-36s %6.0f ms  %s\n", st.Method, float64(st.Took)/float64(time.Millisecond), summarize(st))
	}
	if err != nil {
		return err
	}
	if len(raw) != len(via) {
		p.fail(fmt.Sprintf("%d replies direct, %d through the station", len(raw), len(via)))
		return nil
	}
	same := true
	for i := range raw {
		a, b := shape(raw[i].Reply), shape(via[i].Reply)
		if a != b {
			same = false
			fmt.Printf("   %s differs:\n      direct  %s\n      station %s\n", raw[i].Method, a, b)
		}
	}
	if same {
		fmt.Println("every reply has the same shape direct and through the station")
	} else {
		p.fail("replies differ between the direct socket and the station")
	}
	return nil
}
