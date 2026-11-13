package records

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"os"
	"strings"

	"github.com/VibiumDev/station"
)

// A sound file's picture is its waveform: a wide, short PNG, drawn once
// and kept beside the unpacked recordings.
const (
	waveWidth  = 800
	waveHeight = 160
	// maxWaveBytes is the most of a sound file read for its picture.
	maxWaveBytes = 256 << 20
)

// waveColor is the station's accent. The pages show it on black.
var waveColor = color.NRGBA{0xF5, 0x82, 0x2A, 0xFF}

// IsWAV says whether a media type is a WAV file.
func IsWAV(mediaType string) bool {
	t, _, _ := strings.Cut(mediaType, ";")
	t = strings.ToLower(strings.TrimSpace(t))
	return t == "audio/wav" || t == "audio/x-wav" || t == "audio/wave" || t == "audio/vnd.wave"
}

// IsSound says whether a media type is sound of any kind.
func IsSound(mediaType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(mediaType)), "audio/")
}

func (s *Store) wavePath(sha string) string { return s.unpackDir(sha) + ".wave.png" }

// waveform is a WAV record's picture as a PNG.
func (s *Store) waveform(b station.Blob) ([]byte, error) {
	if s.UnpackDir != "" {
		if data, err := os.ReadFile(s.wavePath(b.SHA256)); err == nil {
			return data, nil
		}
	}
	f, err := s.Blobs.Open(b.SHA256)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	peaks, err := wavPeaks(io.LimitReader(f, maxWaveBytes), waveWidth)
	if err != nil {
		return nil, ErrNoThumbnail
	}
	var out bytes.Buffer
	if err := png.Encode(&out, drawWave(peaks, waveHeight)); err != nil {
		return nil, err
	}
	if s.UnpackDir != "" {
		if err := os.MkdirAll(s.UnpackDir, 0o700); err == nil {
			tmp := s.wavePath(b.SHA256) + ".tmp"
			if os.WriteFile(tmp, out.Bytes(), 0o600) == nil {
				os.Rename(tmp, s.wavePath(b.SHA256))
			}
		}
	}
	return out.Bytes(), nil
}

// wavPeaks reads a PCM or float WAV and gives the loudest sample of each
// of n stretches of it, from 0 to 1, across all channels.
func wavPeaks(r io.Reader, n int) ([]float64, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	var head [12]byte
	if _, err := io.ReadFull(br, head[:]); err != nil || string(head[0:4]) != "RIFF" || string(head[8:12]) != "WAVE" {
		return nil, errors.New("not a WAV file")
	}
	var format, channels, bits uint16
	for {
		var ch [8]byte
		if _, err := io.ReadFull(br, ch[:]); err != nil {
			return nil, errors.New("a WAV file with no data chunk")
		}
		id, size := string(ch[0:4]), binary.LittleEndian.Uint32(ch[4:8])
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, errors.New("a WAV format chunk too short")
			}
			fmtChunk := make([]byte, size)
			if _, err := io.ReadFull(br, fmtChunk); err != nil {
				return nil, err
			}
			format, channels, bits = binary.LittleEndian.Uint16(fmtChunk[0:2]), binary.LittleEndian.Uint16(fmtChunk[2:4]), binary.LittleEndian.Uint16(fmtChunk[14:16])
			// WAVE_FORMAT_EXTENSIBLE names the real format in its sub-format.
			if format == 0xFFFE && size >= 26 {
				format = binary.LittleEndian.Uint16(fmtChunk[24:26])
			}
			if size%2 == 1 {
				br.Discard(1)
			}
		case "data":
			if channels == 0 {
				return nil, errors.New("a WAV data chunk before its format")
			}
			return peaksOf(br, size, format, int(channels), int(bits), n)
		default:
			if _, err := br.Discard(int(size) + int(size%2)); err != nil {
				return nil, err
			}
		}
	}
}

func peaksOf(r io.Reader, size uint32, format uint16, channels, bits, n int) ([]float64, error) {
	width := bits / 8
	switch {
	case format == 1 && (bits == 8 || bits == 16 || bits == 24 || bits == 32):
	case format == 3 && bits == 32:
	default:
		return nil, errors.New("a WAV encoding with no picture")
	}
	frame := width * channels
	frames := int(size) / frame
	if frames == 0 {
		return nil, errors.New("a WAV file with no sound")
	}
	peaks := make([]float64, n)
	buf := make([]byte, frame)
	for i := 0; i < frames; i++ {
		if _, err := io.ReadFull(r, buf); err != nil {
			break // a file cut short shows what it has
		}
		col := i * n / frames
		for c := 0; c < channels; c++ {
			v := sample(buf[c*width:(c+1)*width], format, bits)
			if v < 0 {
				v = -v
			}
			if v > peaks[col] {
				peaks[col] = v
			}
		}
	}
	return peaks, nil
}

// sample is one sample from -1 to 1.
func sample(b []byte, format uint16, bits int) float64 {
	switch {
	case format == 3:
		v := float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
		return math.Max(-1, math.Min(1, v))
	case bits == 8:
		return (float64(b[0]) - 128) / 128
	case bits == 16:
		return float64(int16(binary.LittleEndian.Uint16(b))) / 32768
	case bits == 24:
		return float64(int32(uint32(b[0])<<8|uint32(b[1])<<16|uint32(b[2])<<24)>>8) / 8388608
	default:
		return float64(int32(binary.LittleEndian.Uint32(b))) / 2147483648
	}
}

// drawWave draws peaks as bars about a middle line, scaled so the loudest
// fills the height: a quiet recording is as readable as a loud one.
func drawWave(peaks []float64, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, len(peaks), height))
	loudest := 0.0
	for _, p := range peaks {
		loudest = math.Max(loudest, p)
	}
	mid := height / 2
	for x, p := range peaks {
		half := 0
		if loudest > 0 {
			half = int(math.Round(p / loudest * float64(height/2-1)))
		}
		for y := mid - half; y <= mid+half; y++ {
			img.SetNRGBA(x, y, waveColor)
		}
	}
	return img
}
