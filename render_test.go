package main

import (
	"image/png"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestRenderFrame draws the window off-screen for visual inspection:
//
//	go test -run TestRenderFrame . && xdg-open /tmp/frame.png
func TestRenderFrame(t *testing.T) {
	if os.Getenv("RENDER") == "" {
		t.Skip("set RENDER=1 to render a frame")
	}
	a := newFixtureApp()
	a.refresh()
	tt := ui.NewTester(a.view, 1120, 720)
	if err := tt.Click("nginx.service"); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create("/tmp/frame.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}
