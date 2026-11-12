package records

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/VibiumDev/station"
)

// wavOf is a mono WAV: a loud tone for the first half, silence after.
func wavOf(format, bits uint16, frames int) []byte {
	var data bytes.Buffer
	for i := 0; i < frames; i++ {
		v := 0.0
		if i < frames/2 {
			v = 0.8 * math.Sin(float64(i)/5)
		}
		switch {
		case format == 3:
			binary.Write(&data, binary.LittleEndian, float32(v))
		case bits == 8:
			data.WriteByte(byte(128 + v*127))
		case bits == 16:
			binary.Write(&data, binary.LittleEndian, int16(v*32767))
		case bits == 24:
			n := int32(v * 8388607)
			data.Write([]byte{byte(n), byte(n >> 8), byte(n >> 16)})
		default:
			binary.Write(&data, binary.LittleEndian, int32(v*2147483647))
		}
	}
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+8+4+data.Len()))
	b.WriteString("WAVE")
	b.WriteString("LIST") // a chunk the reader skips
	binary.Write(&b, binary.LittleEndian, uint32(4))
	b.WriteString("INFO")
	b.WriteString("fmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	binary.Write(&b, binary.LittleEndian, format)
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint32(16000))
	binary.Write(&b, binary.LittleEndian, uint32(16000*int(bits/8)))
	binary.Write(&b, binary.LittleEndian, bits/8)
	binary.Write(&b, binary.LittleEndian, bits)
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(data.Len()))
	b.Write(data.Bytes())
	return b.Bytes()
}

func TestWavPeaks(t *testing.T) {
	for _, c := range []struct {
		format, bits uint16
	}{{1, 8}, {1, 16}, {1, 24}, {1, 32}, {3, 32}} {
		peaks, err := wavPeaks(bytes.NewReader(wavOf(c.format, c.bits, 16000)), 100)
		if err != nil {
			t.Fatalf("format %d, %d bits: %v", c.format, c.bits, err)
		}
		if peaks[10] < 0.7 || peaks[10] > 0.81 || peaks[90] > 0.01 {
			t.Errorf("format %d, %d bits: loud %.2f, quiet %.2f", c.format, c.bits, peaks[10], peaks[90])
		}
	}
	for name, raw := range map[string][]byte{
		"not a WAV":  []byte("ID3 an mp3, say"),
		"cut short":  wavOf(1, 16, 100)[:30],
		"no samples": wavOf(1, 16, 0),
		"a-law":      wavOf(6, 8, 100),
	} {
		if _, err := wavPeaks(bytes.NewReader(raw), 100); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestDrawWave(t *testing.T) {
	img := drawWave([]float64{0.4, 0.2, 0}, 21)
	filled := func(x int) (n int) {
		for y := 0; y < 21; y++ {
			if img.NRGBAAt(x, y).A != 0 {
				n++
			}
		}
		return n
	}
	// The loudest fills the height but for an edge; silence is a dot on
	// the middle line.
	if filled(0) != 19 || filled(1) != 11 || filled(2) != 1 {
		t.Errorf("filled %d, %d, %d", filled(0), filled(1), filled(2))
	}
}

func TestWaveformIsTheThumbnail(t *testing.T) {
	f := setup(t)
	f.store.UnpackDir = filepath.Join(t.TempDir(), "unpacked")
	body := wavOf(1, 16, 8000)
	ev, err := f.store.UploadAuth(context.Background(), station.Principal{Subject: "token:ci"}, UploadRequest{SHA256: hashOf(body), Size: int64(len(body)), Type: "audio/wav", Name: "said.wav", HoldID: "hld_1"})
	if err != nil {
		t.Fatal(err)
	}
	if w := f.do("PUT", "/upload", body, "Authorization", Header(ev), "Content-Type", "audio/wav"); w.Code != 200 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	b, _ := f.store.Index.Blob(context.Background(), hashOf(body))
	data, ctype, err := f.store.Thumbnail(context.Background(), b)
	if err != nil || ctype != "image/png" {
		t.Fatalf("thumbnail: %s %v", ctype, err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil || img.Bounds().Dx() != waveWidth || img.Bounds().Dy() != waveHeight {
		t.Fatalf("picture: %v %v", img.Bounds(), err)
	}
	if _, err := os.Stat(f.store.wavePath(b.SHA256)); err != nil {
		t.Error("the waveform is not kept")
	}
	if err := f.store.Delete(context.Background(), station.Principal{Subject: "owner", Admin: true}, b.SHA256); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.store.wavePath(b.SHA256)); err == nil {
		t.Error("the waveform outlives its record")
	}
	// A file sent as a WAV that is not one has no picture.
	fake := []byte("ID3 an mp3 under a WAV's type")
	ev, err = f.store.UploadAuth(context.Background(), station.Principal{Subject: "token:ci"}, UploadRequest{SHA256: hashOf(fake), Size: int64(len(fake)), Type: "audio/wav", Name: "fake.wav", HoldID: "hld_1"})
	if err != nil {
		t.Fatal(err)
	}
	if w := f.do("PUT", "/upload", fake, "Authorization", Header(ev), "Content-Type", "audio/wav"); w.Code != 200 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	b, _ = f.store.Index.Blob(context.Background(), hashOf(fake))
	if _, _, err := f.store.Thumbnail(context.Background(), b); !errors.Is(err, ErrNoThumbnail) {
		t.Errorf("a WAV that is not one: %v", err)
	}
}
