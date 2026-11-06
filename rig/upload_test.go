package rig

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/blob"
	"github.com/VibiumDev/station/records"
	"github.com/VibiumDev/station/sqlite"
)

// TestUpload sends a recording to a real Record Store behind httptest.
func TestUpload(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	rigKey, stationKey := nostr.Generate(), nostr.Generate()
	if err := db.PutRig(ctx, station.Rig{ID: "rig_1", Name: "pi", PubKey: rigKey.Public(), EnrolledAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	db.SetDevices(ctx, "rig_1", []station.Device{{ID: "rig_1/phone", Kind: "phone"}})
	now := time.Now()
	db.Create(ctx, station.Hold{ID: "hld_1", DeviceID: "rig_1/phone", RigID: "rig_1", Subject: "owner", Scopes: []station.Scope{station.ScopeView},
		CreatedAt: now, ActiveAt: now, ExpiresAt: now.Add(time.Hour)})
	store := &records.Store{Blobs: blob.Disk{Dir: t.TempDir()}, Index: db, Events: db, Holds: db, Devices: db, Signer: records.Key(stationKey), HoldsOf: db.HoldsOf}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /upload", store.ServeUpload)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	store.BaseURL = srv.URL
	e := &StationEntry{Name: "test", URL: srv.URL, RigID: "rig_1"}

	body := []byte("PK a hold's recording")
	sum := sha256.Sum256(body)
	desc, err := Upload(ctx, e, rigKey, Recording{Body: bytes.NewReader(body), Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:]),
		Name: "hold.zip", Type: "application/zip", DeviceID: "rig_1/phone", HoldID: "hld_1"})
	if err != nil {
		t.Fatal(err)
	}
	if desc["uploader"] != "rig:rig_1" || desc["hold"] != "hld_1" || desc["device"] != "rig_1/phone" || !strings.HasSuffix(desc["url"].(string), ".zip") {
		t.Fatalf("descriptor: %v", desc)
	}
	// Another rig's device is refused by the station, and the error says so.
	_, err = Upload(ctx, e, rigKey, Recording{Body: bytes.NewReader(body), Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:]),
		Name: "hold.zip", DeviceID: "rig_2/phone"})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("another rig's device: %v", err)
	}
	// A key the station does not know.
	_, err = Upload(ctx, e, nostr.Generate(), Recording{Body: bytes.NewReader(body), Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:])})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("a stranger's key: %v", err)
	}
}
