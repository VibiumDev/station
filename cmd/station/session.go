package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/coder/websocket"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/hold"
	"github.com/VibiumDev/station/relay"
	"github.com/VibiumDev/station/token"
)

// DeviceCapability is the vendor-prefixed capability a client puts in
// session.new to say which device it wants at /session.
const DeviceCapability = "vibium:device"

// serveSession is the standard WebDriver BiDi entry point,
// wss://<station>/session: a client opens it, authenticated at the
// upgrade, and sends session.new; the station reads that one message to
// pick the device (the "vibium:device" capability, else the first online
// device, the account's default while devices are private and
// dedicated), takes the hold, opens the rig's own BiDi socket over the
// tunnel, forwards the message it read untouched, and from then on moves
// frames both ways without looking at them.
func (s *server) serveSession(w http.ResponseWriter, r *http.Request, p station.Principal) {
	if !relay.IsWebSocket(r) {
		apiError(w, http.StatusBadRequest, "the BiDi endpoint is a WebSocket")
		return
	}
	client, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true}) // Origin was checked by api()
	if err != nil {
		return
	}
	defer client.CloseNow()
	client.SetReadLimit(16 << 20)
	ctx := r.Context()

	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	typ, first, err := client.Read(readCtx)
	cancel()
	if err != nil {
		return
	}
	var cmd struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Capabilities struct {
				AlwaysMatch map[string]any   `json:"alwaysMatch"`
				FirstMatch  []map[string]any `json:"firstMatch"`
			} `json:"capabilities"`
		} `json:"params"`
	}
	json.Unmarshal(first, &cmd)
	bidiError := func(code, msg string) {
		id := cmd.ID
		if len(id) == 0 {
			id = json.RawMessage("null")
		}
		out, _ := json.Marshal(map[string]any{"type": "error", "id": id, "error": code, "message": msg})
		client.Write(ctx, websocket.MessageText, out)
		client.Close(websocket.StatusPolicyViolation, msg)
	}
	want, _ := cmd.Params.Capabilities.AlwaysMatch[DeviceCapability].(string)
	for _, fm := range cmd.Params.Capabilities.FirstMatch {
		if want != "" {
			break
		}
		want, _ = fm[DeviceCapability].(string)
	}
	devID, err := s.pickDevice(ctx, want)
	if err != nil {
		bidiError("session not created", err.Error())
		return
	}
	rigID, _, _ := station.SplitDeviceID(devID)
	scopes := []station.Scope{station.ScopeView, station.ScopeBidi}
	if err := s.auth.Authorize(ctx, p, actionFor(scopes), devID); err != nil {
		bidiError("session not created", "this token may only view")
		return
	}
	before, _ := s.db.Current(ctx, devID) // whose the device was, if anyone's
	res, err := s.holds.Take(ctx, hold.Request{DeviceID: devID, Subject: p.Subject, Scopes: scopes, Recall: person(p)})
	var queued *hold.Queued
	switch {
	case errors.As(err, &queued):
		bidiError("session not created", queued.Error()+": take the device first, or wait in line")
		return
	case err != nil:
		bidiError("session not created", err.Error())
		return
	}
	// The socket that took the device gives it back when it closes. A
	// device the subject already held, through the hold endpoint, stays
	// theirs until they release it.
	if before.Subject != p.Subject {
		defer s.releaseAfter(devID, res.Hold.ID, p.Subject)
	}

	// The rig's own BiDi socket, over the tunnel, with the grant.
	header := http.Header{}
	header.Set("Authorization", token.Header(res.Event))
	rig, _, err := websocket.Dial(ctx, "ws://rig/session", &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: s.tunnel.Transport(rigID)}, HTTPHeader: header,
	})
	if err != nil {
		fmt.Printf("session: %s: the rig's BiDi socket: %v\n", devID, err)
		bidiError("session not created", "the rig did not answer: "+err.Error())
		return
	}
	defer rig.CloseNow()
	rig.SetReadLimit(16 << 20)

	// The hold's end closes both sides.
	stop := s.relay.Watch(res.Hold.ID, func() { client.Close(websocket.StatusGoingAway, "the hold ended"); rig.CloseNow() })
	defer stop()

	if err := rig.Write(ctx, typ, first); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	pump := func(from, to *websocket.Conn) {
		defer func() { done <- struct{}{} }()
		for {
			typ, data, err := from.Read(ctx)
			if err != nil {
				var ce websocket.CloseError
				if errors.As(err, &ce) {
					to.Close(ce.Code, ce.Reason)
				} else {
					to.Close(websocket.StatusGoingAway, "")
				}
				return
			}
			if err := to.Write(ctx, typ, data); err != nil {
				return
			}
		}
	}
	go pump(client, rig)
	go pump(rig, client)
	<-done
}

// releaseAfter ends the hold a BiDi socket took, once the socket is
// done; a hold that ended on its own is left alone.
func (s *server) releaseAfter(devID, holdID, subject string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.holds.Release(ctx, holdID, subject); err != nil {
		fmt.Printf("session: %s: release %s: %v\n", devID, holdID, err)
	}
}

// pickDevice resolves what a client asked for at /session: a device id
// or its name alone, or, with nothing asked, the first online device.
func (s *server) pickDevice(ctx context.Context, want string) (string, error) {
	devs, err := s.devices(ctx)
	if err != nil {
		return "", err
	}
	sort.Slice(devs, func(i, j int) bool { return devs[i].ID < devs[j].ID })
	var online []deviceView
	for _, d := range devs {
		if d.ID == "" {
			continue
		}
		if want != "" && (d.ID == want || d.Name == want) {
			if !d.Online {
				return "", fmt.Errorf("%s is offline", d.ID)
			}
			return d.ID, nil
		}
		if d.Online {
			online = append(online, d)
		}
	}
	if want != "" {
		return "", fmt.Errorf("no device %q; the %q capability names one of the station's devices", want, DeviceCapability)
	}
	if len(online) == 0 {
		return "", errors.New("no device is online")
	}
	return online[0].ID, nil
}
