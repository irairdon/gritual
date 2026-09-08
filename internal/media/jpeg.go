package media

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
)

const (
	maxUpload     = 4 << 20
	maxLongEdge   = 1280
	jpegQuality   = 70
	maxDecodeEdge = 8192
)

var errInvalid = errors.New("invalid image")

func transcode(r io.Reader) ([]byte, error) {
	img, format, err := image.Decode(io.LimitReader(r, maxUpload+1))
	if err != nil {
		return nil, errInvalid
	}
	if format != "jpeg" && format != "png" {
		return nil, errInvalid
	}
	b := img.Bounds()
	if b.Dx() > maxDecodeEdge || b.Dy() > maxDecodeEdge || b.Dx() < 1 || b.Dy() < 1 {
		return nil, errInvalid
	}
	img = fitLongEdge(img, maxLongEdge)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func fitLongEdge(src image.Image, maxEdge int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxEdge && h <= maxEdge {
		return src
	}
	nw, nh := w, h
	if w >= h {
		nw = maxEdge
		nh = max(1, h*maxEdge/w)
	} else {
		nh = maxEdge
		nw = max(1, w*maxEdge/h)
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy := b.Min.Y + y*h/nh
		for x := 0; x < nw; x++ {
			sx := b.Min.X + x*w/nw
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}
