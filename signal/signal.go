// Package signal is the viewer's signaling endpoint on the station,
// /v1/devices/{id}/signal: a WebSocket relayed over the rig's tunnel to
// the rig's own signaling handler. The viewer's first message carries its
// hold grant and the rig checks it; the station only checks that the
// caller is signed in and the device exists.
package signal

import (
	"context"
	"net/http"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/relay"
)

// RigPath is where a rig's signaling handler is.
const RigPath = "/api/webrtc/signal"

// Handler relays signaling for one device.
type Handler struct {
	Relay   *relay.Relay
	Devices station.DeviceStore
	// Touch is called with the device id when a signaling session starts
	// and ends, so the hold counts as active. Optional.
	Touch func(ctx context.Context, deviceID string)
}

// ServeDevice relays the signaling WebSocket for deviceID.
func (h *Handler) ServeDevice(w http.ResponseWriter, r *http.Request, deviceID string) {
	dev, err := h.Devices.Device(r.Context(), deviceID)
	if err != nil {
		http.Error(w, "no such device", http.StatusNotFound)
		return
	}
	if !relay.IsWebSocket(r) {
		http.Error(w, "signaling is a WebSocket", http.StatusBadRequest)
		return
	}
	rigID, _, _ := station.SplitDeviceID(dev.ID)
	if h.Touch != nil {
		h.Touch(r.Context(), deviceID)
		defer h.Touch(context.Background(), deviceID)
	}
	h.Relay.Serve(w, r, rigID, RigPath)
}
