package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"

	qrcode "github.com/skip2/go-qrcode"
)

// QR geometry of server/link.py qr_as_png: error correction M, version fitted
// to the data, 8 pixels per module, a quiet zone of 2 modules, black on white.
// (skip2/go-qrcode has a fixed 4-module border and no box size, so the PNG is
// drawn here from the module matrix.)
const (
	qrBoxSize = 8
	qrBorder  = 2
)

type qrRenderer struct{}

// PNG implements web.QRRenderer.
func (qrRenderer) PNG(data string) ([]byte, error) {
	code, err := qrcode.New(data, qrcode.Medium)
	if err != nil {
		return nil, err
	}
	code.DisableBorder = true
	modules := code.Bitmap()
	side := (len(modules) + 2*qrBorder) * qrBoxSize
	img := image.NewGray(image.Rect(0, 0, side, side))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	for r, row := range modules {
		for c, dark := range row {
			if !dark {
				continue
			}
			x0, y0 := (c+qrBorder)*qrBoxSize, (r+qrBorder)*qrBoxSize
			for y := y0; y < y0+qrBoxSize; y++ {
				for x := x0; x < x0+qrBoxSize; x++ {
					img.SetGray(x, y, color.Gray{Y: 0})
				}
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
