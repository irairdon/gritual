package media

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestTranscodeStripsEXIF(t *testing.T) {
	in := jpegWithEXIF()
	if !bytes.Contains(in, []byte("Exif")) || !hasJPEGAPP1(in) {
		t.Fatal("fixture missing APP1/Exif")
	}
	out, err := transcode(bytes.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("re-encoded jpeg: %v", err)
	}
	if hasJPEGAPP1(out) || bytes.Contains(out, []byte("Exif")) || bytes.Contains(out, []byte("GPS")) {
		t.Fatal("EXIF/GPS/orientation APP1 preserved")
	}
}

func TestTranscodePNG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.RGBA{10, 20, 30, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	out, err := transcode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 4 || cfg.Height != 4 {
		t.Fatalf("size = %dx%d", cfg.Width, cfg.Height)
	}
}

func TestTranscodeRejectsNonImage(t *testing.T) {
	if _, err := transcode(bytes.NewReader([]byte("not an image"))); err != ErrInvalid {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestTranscodeRejectsGIF(t *testing.T) {
	img := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.White, color.Black})
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := transcode(&buf); err != ErrInvalid {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestFitLongEdge(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2000, 1000))
	got := fitLongEdge(src, 1280)
	b := got.Bounds()
	if b.Dx() != 1280 || b.Dy() != 640 {
		t.Fatalf("size = %dx%d", b.Dx(), b.Dy())
	}
}

func jpegWithEXIF() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 8), B: 40, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		panic(err)
	}
	raw := buf.Bytes()
	payload := []byte("Exif\x00\x00MM\x00*\x00\x00\x00\x08GPS\x00\x00\x01\x12\x00\x03\x00\x00\x00\x01\x00\x06\x00\x00")
	n := len(payload) + 2
	app1 := []byte{0xFF, 0xE1, byte(n >> 8), byte(n)}
	app1 = append(app1, payload...)
	out := make([]byte, 0, 2+len(app1)+len(raw)-2)
	out = append(out, raw[:2]...)
	out = append(out, app1...)
	out = append(out, raw[2:]...)
	return out
}

func hasJPEGAPP1(b []byte) bool {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return false
	}
	i := 2
	for i+3 < len(b) && b[i] == 0xFF {
		marker := b[i+1]
		if marker == 0xDA || marker == 0xD9 {
			return false
		}
		if marker == 0xE1 {
			return true
		}
		if marker == 0x00 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD8) {
			i++
			continue
		}
		n := int(b[i+2])<<8 | int(b[i+3])
		if n < 2 || i+2+n > len(b) {
			return false
		}
		i += 2 + n
	}
	return false
}
