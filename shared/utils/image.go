package utils

import (
	"bytes"
	"fmt"
	"image/jpeg"
	"image/png"
)

// ConvertPNGToJPEG converts a PNG byte slice to a JPEG byte slice with 90 quality.
func ConvertPNGToJPEG(pngData []byte) ([]byte, error) {
	if len(pngData) == 0 {
		return nil, fmt.Errorf("empty image data")
	}
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return nil, err
	}
	var jpegBuf bytes.Buffer
	err = jpeg.Encode(&jpegBuf, img, &jpeg.Options{Quality: 90})
	if err != nil {
		return nil, err
	}
	return jpegBuf.Bytes(), nil
}
