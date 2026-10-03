// Package ocr runs Apple Vision text recognition via tmc/apple.
package ocr

import (
	"fmt"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/vision"

	"screenreader/internal/geom"
)

type Line struct {
	Text       string
	Confidence float64
	Box        geom.Rect // screen-absolute
}

// OCR runs Vision on the PNG at pngPath and returns lines with screen-absolute
// boxes. imgW/imgH are the PNG's pixel dims; rect is the captured region.
func OCR(pngPath string, languages []string, level string, imgW, imgH int, rect geom.Rect) ([]Line, error) {
	url := foundation.NewURLFileURLWithPath(pngPath)
	handler := vision.NewImageRequestHandlerWithURLOptions(url, nil)
	req := vision.NewVNRecognizeTextRequest()
	if len(languages) > 0 {
		req.SetRecognitionLanguages(languages)
	}
	if level == "fast" {
		req.SetRecognitionLevel(vision.VNRequestTextRecognitionLevelFast)
	} else {
		req.SetRecognitionLevel(vision.VNRequestTextRecognitionLevelAccurate)
	}
	ok, err := handler.PerformRequestsError([]vision.VNRequest{req.VNRequest})
	if err != nil {
		return nil, fmt.Errorf("vision: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("vision: request failed")
	}
	var lines []Line
	for _, obs := range req.Results() {
		o := vision.VNRecognizedTextObservationFromID(obs.ID)
		cands := o.TopCandidates(1)
		if len(cands) == 0 {
			continue
		}
		c := cands[0]
		bb := o.BoundingBox() // normalized, bottom-left origin
		box := geom.VisionBBoxToScreen(bb.Origin.X, bb.Origin.Y, bb.Size.Width, bb.Size.Height, imgW, imgH, rect)
		lines = append(lines, Line{Text: c.String(), Confidence: float64(c.Confidence()), Box: box})
	}
	return lines, nil
}
