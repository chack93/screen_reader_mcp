// Package tools defines the MCP tool surface.
package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-vgo/robotgo"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"screenreader/internal/capture"
	"screenreader/internal/colist"
	"screenreader/internal/config"
	"screenreader/internal/geom"
	"screenreader/internal/input"
	"screenreader/internal/perms"
	"screenreader/internal/window"
)

// resolveRect picks the target region: explicit rect > per-call app/title > configured mode (rect/screen/app).
func resolveRect(cfg *config.Config, app, title string, rect *geom.Rect) (geom.Rect, error) {
	if rect != nil {
		return *rect, nil
	}
	// Per-call app or title → app target (app falls back to cfg.App).
	if app != "" || title != "" {
		a := app
		if a == "" {
			a = cfg.App
		}
		if a == "" {
			return geom.Rect{}, fmt.Errorf("app/title override given but no app target configured (set_target app=...)")
		}
		t := title
		if t == "" {
			t = cfg.Title
		}
		return window.Resolve(a, t)
	}
	// No per-call app/title: honor the configured mode.
	if cfg.Mode == "rect" && cfg.Rect.W > 0 && cfg.Rect.H > 0 {
		return geom.Rect(cfg.Rect), nil
	}
	if cfg.Mode == "screen" {
		return displayBounds(cfg.Screen)
	}
	if cfg.App != "" {
		return window.Resolve(cfg.App, cfg.Title)
	}
	return geom.Rect{}, fmt.Errorf("no target configured (set app, a pinned rect, or a screen)")
}

func errResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
}

func loadCfg() *config.Config {
	c, _ := config.Load(config.DefaultPath())
	return c
}

// RegisterReading wires the capture tool — the model's only reading channel,
// since it reads on-screen text from the returned image with its own vision.
// Tools the access mode does not permit are not registered at all.
func RegisterReading(server *mcp.Server, access string) {
	type regionArgs struct {
		App      string     `json:"app,omitempty" jsonschema:"App (process) name override"`
		Title    string     `json:"title,omitempty" jsonschema:"Window title regex override"`
		Rect     *geom.Rect `json:"rect,omitempty" jsonschema:"Pinned screen-absolute region {x,y,w,h}"`
		MaxWidth int        `json:"max_width,omitempty" jsonschema:"Downscale to this width in px (0 = config default, negative = native resolution)"`
	}

	add(server, access, "capture", &mcp.Tool{Name: "capture", Description: "Capture the target (window, pinned region, or whole screen) as a PNG image, with region bounds and image size. Read on-screen text from the image directly; pass max_width: -1 for native (un-downscaled) pixels when small text is unreadable."},
		func(ctx context.Context, req *mcp.CallToolRequest, a regionArgs) (*mcp.CallToolResult, any, error) {
			cfg := loadCfg()
			r, err := resolveRect(cfg, a.App, a.Title, a.Rect)
			if err != nil {
				return errResult(err), nil, nil
			}
			mw := a.MaxWidth
			if mw == 0 {
				mw = cfg.Capture.MaxWidth
			}
			res, err := capture.CaptureRegion(r, mw)
			if err != nil {
				return errResult(err), nil, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{
				&mcp.ImageContent{Data: res.PNG, MIMEType: "image/png"},
				&mcp.TextContent{Text: fmt.Sprintf("bounds={x:%d,y:%d,w:%d,h:%d} image=%dx%d", r.X, r.Y, r.W, r.H, res.ImgW, res.ImgH)},
			}}, nil, nil
		})
}

// orDefault returns d when s is empty.
func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func RegisterInput(server *mcp.Server, access string) {
	delay := func() int { return loadCfg().Input.DelayMs }

	type clickArgs struct {
		X      int        `json:"x"`
		Y      int        `json:"y"`
		Button string     `json:"button,omitempty" jsonschema:"left|right|middle"`
		Clicks int        `json:"clicks,omitempty" jsonschema:"1 or 2"`
		DryRun bool       `json:"dry_run"`
		Coords string     `json:"coords,omitempty" jsonschema:"screen|image"`
		Rect   *geom.Rect `json:"rect,omitempty" jsonschema:"Region for image-relative coords"`
		ImgW   int        `json:"img_w,omitempty" jsonschema:"Captured image width (from capture's image=WxH)"`
		ImgH   int        `json:"img_h,omitempty" jsonschema:"Captured image height (from capture's image=WxH)"`
	}
	add(server, access, "click", &mcp.Tool{Name: "click", Description: "Click at screen-absolute (x,y)."},
		func(ctx context.Context, req *mcp.CallToolRequest, a clickArgs) (*mcp.CallToolResult, any, error) {
			x, y := a.X, a.Y
			if a.Coords == "image" && a.Rect != nil {
				x, y = mapImageXY(a.X, a.Y, a.ImgW, a.ImgH, *a.Rect)
			}
			if e := inBoundsErr(x, y); e != nil {
				return errResult(e), nil, nil
			}
			button := orDefault(a.Button, "left")
			clicks := a.Clicks
			if clicks < 1 {
				clicks = 1
			}
			r := input.Click(x, y, button, clicks, a.DryRun)
			input.Delay(delay())
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("click %s @(%d,%d) executed=%v", button, x, y, r.Executed),
			}}}, nil, nil
		})

	type xyArgs struct {
		X      int        `json:"x"`
		Y      int        `json:"y"`
		Coords string     `json:"coords,omitempty" jsonschema:"screen|image"`
		Rect   *geom.Rect `json:"rect,omitempty"`
		ImgW   int        `json:"img_w,omitempty" jsonschema:"Captured image width (from capture's image=WxH)"`
		ImgH   int        `json:"img_h,omitempty" jsonschema:"Captured image height (from capture's image=WxH)"`
	}
	add(server, access, "move", &mcp.Tool{Name: "move", Description: "Move the cursor to (x,y)."},
		func(ctx context.Context, req *mcp.CallToolRequest, a xyArgs) (*mcp.CallToolResult, any, error) {
			x, y := a.X, a.Y
			if a.Coords == "image" && a.Rect != nil {
				x, y = mapImageXY(a.X, a.Y, a.ImgW, a.ImgH, *a.Rect)
			}
			if e := inBoundsErr(x, y); e != nil {
				return errResult(e), nil, nil
			}
			input.Move(x, y)
			input.Delay(delay())
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("moved to (%d,%d)", x, y)}}}, nil, nil
		})

	type scrollArgs struct {
		DX     int        `json:"dx"`
		DY     int        `json:"dy"`
		X      int        `json:"x,omitempty"`
		Y      int        `json:"y,omitempty"`
		Coords string     `json:"coords,omitempty" jsonschema:"screen|image"`
		Rect   *geom.Rect `json:"rect,omitempty" jsonschema:"Region for image-relative coords"`
		ImgW   int        `json:"img_w,omitempty" jsonschema:"Captured image width (from capture's image=WxH)"`
		ImgH   int        `json:"img_h,omitempty" jsonschema:"Captured image height (from capture's image=WxH)"`
	}
	add(server, access, "scroll", &mcp.Tool{Name: "scroll", Description: "Scroll by (dx,dy) at (x,y) if given."},
		func(ctx context.Context, req *mcp.CallToolRequest, a scrollArgs) (*mcp.CallToolResult, any, error) {
			x, y := a.X, a.Y
			if a.Coords == "image" && a.Rect != nil {
				x, y = mapImageXY(a.X, a.Y, a.ImgW, a.ImgH, *a.Rect)
			}
			if x != 0 || y != 0 {
				if e := inBoundsErr(x, y); e != nil {
					return errResult(e), nil, nil
				}
				input.Move(x, y)
			}
			input.Scroll(a.DX, a.DY)
			input.Delay(delay())
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("scrolled %d,%d", a.DX, a.DY)}}}, nil, nil
		})

	type dragArgs struct {
		X1     int        `json:"x1"`
		Y1     int        `json:"y1"`
		X2     int        `json:"x2"`
		Y2     int        `json:"y2"`
		Button string     `json:"button,omitempty"`
		DryRun bool       `json:"dry_run"`
		Coords string     `json:"coords,omitempty" jsonschema:"screen|image"`
		Rect   *geom.Rect `json:"rect,omitempty" jsonschema:"Region for image-relative coords"`
		ImgW   int        `json:"img_w,omitempty" jsonschema:"Captured image width (from capture's image=WxH)"`
		ImgH   int        `json:"img_h,omitempty" jsonschema:"Captured image height (from capture's image=WxH)"`
	}
	add(server, access, "drag", &mcp.Tool{Name: "drag", Description: "Drag from (x1,y1) to (x2,y2)."},
		func(ctx context.Context, req *mcp.CallToolRequest, a dragArgs) (*mcp.CallToolResult, any, error) {
			x1, y1, x2, y2 := a.X1, a.Y1, a.X2, a.Y2
			if a.Coords == "image" && a.Rect != nil {
				x1, y1 = mapImageXY(a.X1, a.Y1, a.ImgW, a.ImgH, *a.Rect)
				x2, y2 = mapImageXY(a.X2, a.Y2, a.ImgW, a.ImgH, *a.Rect)
			}
			if e := inBoundsErr(x1, y1); e != nil {
				return errResult(e), nil, nil
			}
			if e := inBoundsErr(x2, y2); e != nil {
				return errResult(e), nil, nil
			}
			button := orDefault(a.Button, "left")
			r := input.Drag(x1, y1, x2, y2, button, a.DryRun)
			input.Delay(delay())
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("drag to (%d,%d) executed=%v", r.X, r.Y, r.Executed)}}}, nil, nil
		})

	add(server, access, "type", &mcp.Tool{Name: "type", Description: "Type text via keystrokes."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct {
			Text string `json:"text"`
		}) (*mcp.CallToolResult, any, error) {
			input.Type(a.Text)
			input.Delay(delay())
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "typed " + itoa(len(a.Text)) + " chars"}}}, nil, nil
		})

	add(server, access, "paste", &mcp.Tool{Name: "paste", Description: "Paste text (clipboard + Cmd+V); reliable for long text/commands."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct {
			Text string `json:"text"`
		}) (*mcp.CallToolResult, any, error) {
			input.Paste(a.Text)
			input.Delay(delay())
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pasted"}}}, nil, nil
		})

	add(server, access, "key", &mcp.Tool{Name: "key", Description: "Press a key/shortcut combo, e.g. \"cmd+v\", \"return\"."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct {
			Combo string `json:"combo"`
		}) (*mcp.CallToolResult, any, error) {
			if err := input.Key(a.Combo); err != nil {
				return errResult(err), nil, nil
			}
			input.Delay(delay())
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pressed " + a.Combo}}}, nil, nil
		})

	add(server, access, "wait", &mcp.Tool{Name: "wait", Description: "Sleep for ms milliseconds."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct {
			Ms int `json:"ms"`
		}) (*mcp.CallToolResult, any, error) {
			input.Delay(a.Ms)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "waited " + itoa(a.Ms) + "ms"}}}, nil, nil
		})
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

func RegisterTarget(server *mcp.Server, access string) {
	type setTargetArgs struct {
		App        string     `json:"app,omitempty"`
		Title      string     `json:"title,omitempty"`
		Rect       *geom.Rect `json:"rect,omitempty"`
		Mode       string     `json:"mode,omitempty" jsonschema:"app|rect|screen"`
		Screen     *int       `json:"screen,omitempty" jsonschema:"Display index for mode=screen (0 = main; see list_screens)"`
		ClearApp   bool       `json:"clear_app,omitempty" jsonschema:"Clear the stored app target"`
		ClearTitle bool       `json:"clear_title,omitempty" jsonschema:"Clear the stored title filter"`
	}
	add(server, access, "set_target", &mcp.Tool{Name: "set_target", Description: "Set + persist the default target (app/title, pinned rect, or a whole screen)."},
		func(ctx context.Context, req *mcp.CallToolRequest, a setTargetArgs) (*mcp.CallToolResult, any, error) {
			cfg := loadCfg()
			if a.App != "" {
				cfg.App = a.App
			}
			if a.Title != "" {
				cfg.Title = a.Title
			}
			if a.Rect != nil {
				cfg.Rect = config.Rect{X: a.Rect.X, Y: a.Rect.Y, W: a.Rect.W, H: a.Rect.H}
			}
			if a.Screen != nil {
				if _, err := displayBounds(*a.Screen); err != nil {
					return errResult(err), nil, nil
				}
				cfg.Screen = *a.Screen
			}
			if a.Mode != "" {
				switch a.Mode {
				case "app", "rect", "screen":
					cfg.Mode = a.Mode
				default:
					return errResult(fmt.Errorf("invalid mode %q (want app|rect|screen)", a.Mode)), nil, nil
				}
			} else if a.Rect != nil {
				cfg.Mode = "rect"
			}
			if a.ClearApp {
				cfg.App = ""
			}
			if a.ClearTitle {
				cfg.Title = ""
			}
			if err := config.Save(config.DefaultPath(), cfg); err != nil {
				return errResult(err), nil, nil
			}
			r, err := resolveRect(cfg, "", "", nil)
			msg := fmt.Sprintf("target saved (mode=%s screen=%d app=%q title=%q rect=%+v)", cfg.Mode, cfg.Screen, cfg.App, cfg.Title, cfg.Rect)
			if err == nil {
				msg += fmt.Sprintf(" resolved=%+v", r)
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: msg}}}, nil, nil
		})

	add(server, access, "focus_window", &mcp.Tool{Name: "focus_window", Description: "Activate the target app and raise its window; return bounds."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct {
			App   string `json:"app,omitempty"`
			Title string `json:"title,omitempty"`
		}) (*mcp.CallToolResult, any, error) {
			cfg := loadCfg()
			app := orDefault(a.App, cfg.App)
			title := orDefault(a.Title, cfg.Title)
			if app == "" {
				return errResult(fmt.Errorf("no app target configured")), nil, nil
			}
			r, err := window.Focus(app, title)
			if err != nil {
				return errResult(err), nil, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("focused %s %+v", app, r)}}}, nil, nil
		})

	add(server, access, "window_info", &mcp.Tool{Name: "window_info", Description: "Report the resolved target (app, title, mode, bounds)."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct{}) (*mcp.CallToolResult, any, error) {
			cfg := loadCfg()
			r, err := resolveRect(cfg, "", "", nil)
			if err != nil {
				return errResult(err), nil, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("mode=%s screen=%d app=%q title=%q bounds=%+v", cfg.Mode, cfg.Screen, cfg.App, cfg.Title, r),
			}}}, nil, nil
		})

	add(server, access, "list_windows", &mcp.Tool{Name: "list_windows", Description: "List visible and minimized windows (app, title, bounds) to choose a target."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct{}) (*mcp.CallToolResult, any, error) {
			wins, err := fastListWindows()
			if err != nil || len(wins) == 0 {
				// Fall back to the (slow but dependency-free) AppleScript walk.
				// An empty CGWindowList result counts as a miss: on recent
				// macOS the window list is only populated for processes with
				// Screen Recording, so a bare shell/server without the grant
				// sees zero windows.
				wins, err = window.List()
			}
			if err != nil {
				return errResult(err), nil, nil
			}
			window.Sort(wins)
			var out string
			for _, w := range wins {
				out += fmt.Sprintf("%s | %s | %+v\n", w.App, w.Title, w.Rect)
			}
			if out == "" {
				out = "(no visible windows)"
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil, nil
		})

	add(server, access, "list_screens", &mcp.Tool{Name: "list_screens", Description: "List active displays (index, main flag, screen-absolute bounds) to choose a screen target."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct{}) (*mcp.CallToolResult, any, error) {
			n := robotgo.DisplaysNum()
			var out string
			for i := 0; i < n; i++ {
				r, err := displayBounds(i)
				if err != nil {
					return errResult(err), nil, nil
				}
				// Index 0 is the main display (the underlying screenshot
				// package's contract — robotgo.GetMainId() returns a raw
				// CGDirectDisplayID, not this 0-based index, so don't use it).
				tag := ""
				if i == 0 {
					tag = " (main)"
				}
				out += fmt.Sprintf("%d%s | %+v\n", i, tag, r)
			}
			if out == "" {
				out = "(no active displays)"
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil, nil
		})

	add(server, access, "doctor", &mcp.Tool{Name: "doctor", Description: "Self-check: displays, target resolution and permissions."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct{}) (*mcp.CallToolResult, any, error) {
			cfg := loadCfg()
			var out string
			out += fmt.Sprintf("displays: %d\n", robotgo.DisplaysNum())
			for i := 0; i < robotgo.DisplaysNum(); i++ {
				r, err := displayBounds(i)
				if err != nil {
					out += fmt.Sprintf("  %d: ERROR %v\n", i, err)
					continue
				}
				tag := ""
				if i == 0 { // index 0 = main display (see list_screens)
					tag = " (main)"
				}
				out += fmt.Sprintf("  %d%s: %+v\n", i, tag, r)
			}
			out += fmt.Sprintf("config: access=%s mode=%s screen=%d app=%q title=%q rect=%+v\n", cfg.Access, cfg.Mode, cfg.Screen, cfg.App, cfg.Title, cfg.Rect)
			out += "permissions:\n"
			out += permLine("screen_recording", perms.ScreenRecording(), "System Settings → Privacy & Security → Screen Recording (restart the app after granting)")
			out += permLine("accessibility", perms.Accessibility(), "System Settings → Privacy & Security → Accessibility")
			out += permLine("automation", perms.Automation(), "System Settings → Privacy & Security → Automation → allow your terminal to control System Events")
			if r, err := resolveRect(cfg, "", "", nil); err != nil {
				out += "target: ERROR " + err.Error() + " (try list_windows / set_target)\n"
			} else {
				out += fmt.Sprintf("target: ok %+v\n", r)
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil, nil
		})
}

// fastListWindows enumerates visible windows without touching per-window
// Apple events: one CGWindowListCopyWindowInfo call for geometry/titles plus
// one batched System Events query to map PIDs to process names (the names
// Resolve/Focus accept). Takes ~1s total; window.List() takes 30-50s on
// machines with many apps because System Events round-trips every window of
// every process.
func fastListWindows() ([]window.Win, error) {
	raws, err := colist.List()
	procs, perr := window.ProcessIndex()
	wins := window.MergeWindows(raws, procs)
	if len(wins) == 0 {
		// One diagnostic line per anomaly for T17 triage (window servers,
		// attribution and TCC gating are invisible from the outside).
		msg := fmt.Sprintf("list_windows empty: colist n=%d err=%v procs n=%d err=%v\n",
			len(raws), err, len(procs), perr)
		if err == nil && perr == nil && len(raws) > 0 && len(procs) > 0 {
			msg += "  (windows+procs present but pid join empty — pids differ?)"
		}
		_ = os.WriteFile(filepath.Join(os.TempDir(), "sr-list-diag.txt"), []byte(msg), 0o644)
	}
	if err != nil {
		return nil, err
	}
	if perr != nil {
		return nil, perr
	}
	return wins, nil
}

// permLine renders one permission status line for the doctor report.
func permLine(name string, ok bool, hint string) string {
	status := "ok"
	if !ok {
		status = "MISSING — grant: " + hint
	}
	return "  " + name + ": " + status + "\n"
}

// displayBounds returns the screen-absolute bounds of display i (0 = main).
func displayBounds(i int) (geom.Rect, error) {
	if i < 0 || i >= robotgo.DisplaysNum() {
		return geom.Rect{}, fmt.Errorf("no display %d (%d active — see list_screens)", i, robotgo.DisplaysNum())
	}
	x, y, w, h := robotgo.GetDisplayBounds(i)
	return geom.Rect{X: x, Y: y, W: w, H: h}, nil
}

// virtualScreen returns the bounding union of all active displays. Secondary
// displays often sit at negative or oversized offsets relative to the main
// one, so input coords are validated against this union, not the main size.
func virtualScreen() (geom.Rect, error) {
	n := robotgo.DisplaysNum()
	if n == 0 {
		return geom.Rect{}, fmt.Errorf("no active displays")
	}
	x, y, w, h := robotgo.GetDisplayBounds(0)
	r := geom.Rect{X: x, Y: y, W: w, H: h}
	for i := 1; i < n; i++ {
		x, y, w, h = robotgo.GetDisplayBounds(i)
		if x < r.X {
			r.W += r.X - x
			r.X = x
		}
		if y < r.Y {
			r.H += r.Y - y
			r.Y = y
		}
		if x+w > r.X+r.W {
			r.W = x + w - r.X
		}
		if y+h > r.Y+r.H {
			r.H = y + h - r.Y
		}
	}
	return r, nil
}

// mapImageXY maps an image-space point to screen-absolute coords, using r as the
// screen-absolute bounds and (imgW,imgH) as the captured image pixel size. If
// imgW/imgH are <=0 it falls back to r's own size (1:1; non-Retina / no downscale).
func mapImageXY(imgX, imgY, imgW, imgH int, r geom.Rect) (int, int) {
	if imgW <= 0 {
		imgW = r.W
	}
	if imgH <= 0 {
		imgH = r.H
	}
	return geom.MapImageToScreen(imgX, imgY, imgW, imgH, r)
}

// inBoundsErr is the input safety net: (x,y) must lie inside the union of all
// active displays (virtual screen), so secondary-display clicks are accepted.
func inBoundsErr(x, y int) error {
	vs, err := virtualScreen()
	if err != nil {
		return err
	}
	if x < vs.X || y < vs.Y || x >= vs.X+vs.W || y >= vs.Y+vs.H {
		return fmt.Errorf("coordinates (%d,%d) out of bounds (virtual screen %+v)", x, y, vs)
	}
	return nil
}
