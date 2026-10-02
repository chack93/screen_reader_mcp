package main

import (
	"fmt"

	"github.com/go-vgo/robotgo"
	"github.com/tmc/apple/vision"
)

func main() {
	sx, sy := robotgo.GetScreenSize()
	fmt.Printf("screen: %dx%d\n", sx, sy)
	// Touch the Vision symbol to force the framework link.
	_ = vision.NewVNRecognizeTextRequest
	fmt.Println("probe ok")
}
