package camera

import (
	"fmt"
	"image"
)

// bgraToRGBA converts a camera frame (little-endian BGRA rows padded to
// rowBytes) into image.RGBA. Pure so it unit-tests without cgo or a device.
func bgraToRGBA(bgra []byte, width, height, rowBytes int) (*image.RGBA, error) {
	if width <= 0 || height <= 0 || rowBytes < width*4 {
		return nil, fmt.Errorf("camera frame: bad geometry %dx%d rowBytes=%d", width, height, rowBytes)
	}
	if len(bgra) < (height-1)*rowBytes+width*4 {
		return nil, fmt.Errorf("camera frame: buffer %d bytes < %d needed", len(bgra), (height-1)*rowBytes+width*4)
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		src := bgra[y*rowBytes:]
		dst := img.Pix[y*img.Stride:]
		for x := 0; x < width; x++ {
			b, g, r, a := src[x*4], src[x*4+1], src[x*4+2], src[x*4+3]
			d := dst[x*4:]
			d[0], d[1], d[2], d[3] = r, g, b, a
		}
	}
	return img, nil
}
