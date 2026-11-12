package rig

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"fiatjaf.com/nostr"
)

// Recording is a file a rig sends to a station's Record Store: what it
// recorded of a hold, a screenshot, a log.
type Recording struct {
	Body     io.Reader
	Size     int64
	SHA256   string // lowercase hex
	Name     string // a file name hint, such as rec_9f3c0a11.zip
	Type     string // the media type; application/octet-stream when empty
	DeviceID string // the device, under this station's rig id
	HoldID   string // the hold it records, when it records one
}

// Upload sends a recording to the station's Record Store under the
// rig's own key: a Blossom authorization (kind 24242, t=upload) naming
// the file's hash and size and the device and hold it belongs to, good
// for five minutes, with the file as the body of PUT /upload. The
// station checks that the device and the hold are this rig's. It
// returns the blob descriptor the station answers with.
func Upload(ctx context.Context, e *StationEntry, key nostr.SecretKey, rec Recording) (map[string]any, error) {
	if rec.Size <= 0 || len(rec.SHA256) != 64 {
		return nil, fmt.Errorf("upload: a size and a sha256 are needed")
	}
	mediaType := rec.Type
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	now := time.Now()
	ev := nostr.Event{Kind: 24242, CreatedAt: nostr.Timestamp(now.Unix()), Content: "Upload " + rec.Name, Tags: nostr.Tags{
		{"t", "upload"},
		{"x", rec.SHA256},
		{"size", strconv.FormatInt(rec.Size, 10)},
		{"expiration", strconv.FormatInt(now.Add(5*time.Minute).Unix(), 10)},
		{"m", mediaType},
	}}
	for _, t := range [][2]string{{"dev", rec.DeviceID}, {"hold", rec.HoldID}, {"name", rec.Name}} {
		if t[1] != "" {
			ev.Tags = append(ev.Tags, nostr.Tag{t[0], t[1]})
		}
	}
	var nonce [8]byte
	rand.Read(nonce[:])
	ev.Tags = append(ev.Tags, nostr.Tag{"nonce", hex.EncodeToString(nonce[:])})
	if err := ev.Sign(key); err != nil {
		return nil, err
	}
	auth, err := json.Marshal(ev)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, strings.TrimSuffix(e.URL, "/")+"/upload", rec.Body)
	if err != nil {
		return nil, err
	}
	req.ContentLength = rec.Size
	req.Header.Set("Authorization", "Nostr "+base64.StdEncoding.EncodeToString(auth))
	req.Header.Set("Content-Type", mediaType)
	client := &http.Client{Timeout: 10 * time.Minute, Transport: &http.Transport{TLSClientConfig: TLSConfig(e)}}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var why struct {
			Error string `json:"error"`
		}
		json.Unmarshal(body, &why)
		if why.Error == "" {
			why.Error = strings.TrimSpace(string(body))
		}
		return nil, fmt.Errorf("upload: the station answered %d: %s", resp.StatusCode, why.Error)
	}
	var desc map[string]any
	if err := json.Unmarshal(body, &desc); err != nil {
		return nil, fmt.Errorf("upload: the station's answer is not a descriptor: %w", err)
	}
	return desc, nil
}
