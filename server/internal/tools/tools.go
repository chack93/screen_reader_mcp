// Package tools defines the MCP tool surface.
package tools

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-vgo/robotgo"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"screenreader/internal/capture"
	"screenreader/internal/config"
	"screenreader/internal/geom"
	"screenreader/internal/input"
	"screenreader/internal/ocr"
	"screenreader/internal/window"
)

// resolveRect picks the target region: explicit rect > app/title (override or cfg) > cfg pinned rect.
func resolveRect(cfg *config.Config, app, title string, rect *geom.Rect) (geom.Rect, error) {
	if rect != nil {
		return *rect, nil
	}
	appName := app
	if appName == "" {
		appName = cfg.App
	}
	titleRe := title
	if titleRe == "" {
		titleRe = cfg.Title
	}
	if appName != "" {
		return window.Resolve(appName, titleRe)
	}
	if cfg.Mode == "rect" && cfg.Rect.W > 0 && cfg.Rect.H > 0 {
		return geom.Rect(cfg.Rect), nil
	}
	return geom.Rect{}, fmt.Errorf("no target configured (set app or a pinned rect)")
}

func errResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
}

func loadCfg() *config.Config {
	c, _ := config.Load(config.DefaultPath())
	return c
}

// tempPNG writes bytes to a temp file and returns its path (caller removes it).
func tempPNG(data []byte) (string, error) {
	f, err := os.CreateTemp("", "sr-ocr-*.png")
	if err != nil {
		return "", err
	}
	p := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(p)
		return "", err
	}
	f.Close()
	return p, nil
}

// RegisterReading wires the capture, ocr, and click_text tools.
func RegisterReading(server *mcp.Server) {
	type regionArgs struct {
		App      string     `json:"app,omitempty" jsonschema:"App (process) name override"`
		Title    string     `json:"title,omitempty" jsonschema:"Window title regex override"`
		Rect     *geom.Rect `json:"rect,omitempty" jsonschema:"Pinned screen-absolute region {x,y,w,h}"`
		MaxWidth int        `json:"max_width,omitempty" jsonschema:"Downscale capture to this width in px"`
	}

	mcp.AddTool(server, &mcp.Tool{Name: "capture", Description: "Capture the target window (or region) as a PNG image, with region bounds and image size."},
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

	type ocrArgs struct {
		App          string     `json:"app,omitempty"`
		Title        string     `json:"title,omitempty"`
		Rect         *geom.Rect `json:"rect,omitempty"`
		Languages    []string   `json:"languages,omitempty"`
		Level        string     `json:"level,omitempty" jsonschema:"fast|accurate"`
		IncludeBoxes bool       `json:"include_boxes"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "ocr", Description: "OCR the target window (or region) with Apple Vision; returns text and screen-absolute boxes."},
		func(ctx context.Context, req *mcp.CallToolRequest, a ocrArgs) (*mcp.CallToolResult, any, error) {
			cfg := loadCfg()
			r, err := resolveRect(cfg, a.App, a.Title, a.Rect)
			if err != nil {
				return errResult(err), nil, nil
			}
			// OCR runs on full resolution (no downscale).
			res, err := capture.CaptureRegion(r, 0)
			if err != nil {
				return errResult(err), nil, nil
			}
			p, err := tempPNG(res.PNG)
			if err != nil {
				return errResult(err), nil, nil
			}
			defer os.Remove(p)
			langs := a.Languages
			if len(langs) == 0 {
				langs = cfg.OCR.Languages
			}
			lvl := a.Level
			if lvl == "" {
				lvl = cfg.OCR.Level
			}
			lines, err := ocr.OCR(p, langs, lvl, res.ImgW, res.ImgH, r)
			if err != nil {
				return errResult(err), nil, nil
			}
			var out string
			for i, l := range lines {
				if a.IncludeBoxes {
					out += fmt.Sprintf("%d: [%0.2f] %s (x:%d y:%d w:%d h:%d)\n", i+1, l.Confidence, l.Text, l.Box.X, l.Box.Y, l.Box.W, l.Box.H)
				} else {
					out += l.Text + "\n"
				}
			}
			if out == "" {
				out = "(no text recognized)"
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil, nil
		})

	type clickTextArgs struct {
		App        string     `json:"app,omitempty"`
		Title      string     `json:"title,omitempty"`
		Rect       *geom.Rect `json:"rect,omitempty"`
		Text       string     `json:"text" jsonschema:"Text to find and click"`
		Match      string     `json:"match,omitempty" jsonschema:"exact|contains|regex (default contains)"`
		Occurrence int        `json:"occurrence,omitempty" jsonschema:"Which match (1-based, default 1)"`
		DryRun     bool       `json:"dry_run"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "click_text", Description: "OCR-locate text in the target and click its center (screen-absolute)."},
		func(ctx context.Context, req *mcp.CallToolRequest, a clickTextArgs) (*mcp.CallToolResult, any, error) {
			cfg := loadCfg()
			r, err := resolveRect(cfg, a.App, a.Title, a.Rect)
			if err != nil {
				return errResult(err), nil, nil
			}
			res, err := capture.CaptureRegion(r, 0)
			if err != nil {
				return errResult(err), nil, nil
			}
			p, err := tempPNG(res.PNG)
			if err != nil {
				return errResult(err), nil, nil
			}
			defer os.Remove(p)
			langs := cfg.OCR.Languages
			lines, err := ocr.OCR(p, langs, cfg.OCR.Level, res.ImgW, res.ImgH, r)
			if err != nil {
				return errResult(err), nil, nil
			}
			occ := a.Occurrence
			if occ < 1 {
				occ = 1
			}
			found := 0
			for _, l := range lines {
				match := false
				switch a.Match {
				case "exact":
					match = l.Text == a.Text
				case "regex":
					match = func() bool { re, e := regexpCompile(a.Text); return e == nil && re.MatchString(l.Text) }()
				default: // contains
					match = containsFold(l.Text, a.Text)
				}
				if !match {
					continue
				}
				found++
				if found != occ {
					continue
				}
				cx, cy := geom.Center(l.Box)
				inputClick(cx, cy, a.DryRun)
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
					Text: fmt.Sprintf("clicked %q at (%d,%d) box=%+v dry_run=%v", l.Text, cx, cy, l.Box, a.DryRun),
				}}}, nil, nil
			}
			return errResult(fmt.Errorf("text %q not found (match=%q)", a.Text, orDefault(a.Match, "contains"))), nil, nil
		})
}

// Shared small helpers (used by Task 9 and later tasks).
func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
func regexpCompile(s string) (*regexp.Regexp, error) { return regexp.Compile(s) }
func inputClick(x, y int, dryRun bool)               { input.Click(x, y, "left", 1, dryRun) }

func RegisterInput(server *mcp.Server) {
	delay := func() int { return loadCfg().Input.DelayMs }

	type clickArgs struct {
		X      int        `json:"x"`
		Y      int        `json:"y"`
		Button string     `json:"button,omitempty" jsonschema:"left|right|middle"`
		Clicks int        `json:"clicks,omitempty" jsonschema:"1 or 2"`
		DryRun bool       `json:"dry_run"`
		Coords string     `json:"coords,omitempty" jsonschema:"screen|image"`
		Rect   *geom.Rect `json:"rect,omitempty" jsonschema:"Region for image-relative coords"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "click", Description: "Click at screen-absolute (x,y)."},
		func(ctx context.Context, req *mcp.CallToolRequest, a clickArgs) (*mcp.CallToolResult, any, error) {
			x, y := a.X, a.Y
			if a.Coords == "image" && a.Rect != nil {
				x, y = geom.MapImageToScreen(a.X, a.Y, a.Rect.W, a.Rect.H, *a.Rect)
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
	}
	mcp.AddTool(server, &mcp.Tool{Name: "move", Description: "Move the cursor to (x,y)."},
		func(ctx context.Context, req *mcp.CallToolRequest, a xyArgs) (*mcp.CallToolResult, any, error) {
			x, y := a.X, a.Y
			if a.Coords == "image" && a.Rect != nil {
				x, y = geom.MapImageToScreen(a.X, a.Y, a.Rect.W, a.Rect.H, *a.Rect)
			}
			input.Move(x, y)
			input.Delay(delay())
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("moved to (%d,%d)", x, y)}}}, nil, nil
		})

	type scrollArgs struct {
		DX int `json:"dx"`
		DY int `json:"dy"`
		X  int `json:"x,omitempty"`
		Y  int `json:"y,omitempty"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "scroll", Description: "Scroll by (dx,dy) at (x,y) if given."},
		func(ctx context.Context, req *mcp.CallToolRequest, a scrollArgs) (*mcp.CallToolResult, any, error) {
			if a.X != 0 || a.Y != 0 {
				input.Move(a.X, a.Y)
			}
			input.Scroll(a.DX, a.DY)
			input.Delay(delay())
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("scrolled %d,%d", a.DX, a.DY)}}}, nil, nil
		})

	type dragArgs struct {
		X1     int    `json:"x1"`
		Y1     int    `json:"y1"`
		X2     int    `json:"x2"`
		Y2     int    `json:"y2"`
		Button string `json:"button,omitempty"`
		DryRun bool   `json:"dry_run"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "drag", Description: "Drag from (x1,y1) to (x2,y2)."},
		func(ctx context.Context, req *mcp.CallToolRequest, a dragArgs) (*mcp.CallToolResult, any, error) {
			button := orDefault(a.Button, "left")
			r := input.Drag(a.X1, a.Y1, a.X2, a.Y2, button, a.DryRun)
			input.Delay(delay())
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("drag to (%d,%d) executed=%v", r.X, r.Y, r.Executed)}}}, nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "type", Description: "Type text via keystrokes."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct {
			Text string `json:"text"`
		}) (*mcp.CallToolResult, any, error) {
			input.Type(a.Text)
			input.Delay(delay())
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "typed " + itoa(len(a.Text)) + " chars"}}}, nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "paste", Description: "Paste text (clipboard + Cmd+V); reliable for long text/commands."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct {
			Text string `json:"text"`
		}) (*mcp.CallToolResult, any, error) {
			input.Paste(a.Text)
			input.Delay(delay())
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pasted"}}}, nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "key", Description: "Press a key/shortcut combo, e.g. \"cmd+v\", \"return\"."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct {
			Combo string `json:"combo"`
		}) (*mcp.CallToolResult, any, error) {
			if err := input.Key(a.Combo); err != nil {
				return errResult(err), nil, nil
			}
			input.Delay(delay())
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pressed " + a.Combo}}}, nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "wait", Description: "Sleep for ms milliseconds."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct {
			Ms int `json:"ms"`
		}) (*mcp.CallToolResult, any, error) {
			input.Delay(a.Ms)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "waited " + itoa(a.Ms) + "ms"}}}, nil, nil
		})
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

func RegisterTarget(server *mcp.Server) {
	type setTargetArgs struct {
		App   string     `json:"app,omitempty"`
		Title string     `json:"title,omitempty"`
		Rect  *geom.Rect `json:"rect,omitempty"`
		Mode  string     `json:"mode,omitempty" jsonschema:"app|rect"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "set_target", Description: "Set + persist the default target (app/title or pinned rect)."},
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
			if a.Mode != "" {
				cfg.Mode = a.Mode
			} else if a.Rect != nil {
				cfg.Mode = "rect"
			}
			if err := config.Save(config.DefaultPath(), cfg); err != nil {
				return errResult(err), nil, nil
			}
			r, err := resolveRect(cfg, "", "", nil)
			msg := fmt.Sprintf("target saved (mode=%s app=%q title=%q rect=%+v)", cfg.Mode, cfg.App, cfg.Title, cfg.Rect)
			if err == nil {
				msg += fmt.Sprintf(" resolved=%+v", r)
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: msg}}}, nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "focus_window", Description: "Activate the target app and raise its window; return bounds."},
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

	mcp.AddTool(server, &mcp.Tool{Name: "window_info", Description: "Report the resolved target (app, title, mode, bounds)."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct{}) (*mcp.CallToolResult, any, error) {
			cfg := loadCfg()
			r, err := resolveRect(cfg, "", "", nil)
			if err != nil {
				return errResult(err), nil, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("mode=%s app=%q title=%q bounds=%+v", cfg.Mode, cfg.App, cfg.Title, r),
			}}}, nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "list_windows", Description: "List visible windows (app, title, bounds) to choose a target."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct{}) (*mcp.CallToolResult, any, error) {
			wins, err := window.List()
			if err != nil {
				return errResult(err), nil, nil
			}
			var out string
			for _, w := range wins {
				out += fmt.Sprintf("%s | %s | %+v\n", w.App, w.Title, w.Rect)
			}
			if out == "" {
				out = "(no visible windows)"
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil, nil
		})

	type waitTextArgs struct {
		Text       string `json:"text"`
		TimeoutMs  int    `json:"timeout_ms"`
		IntervalMs int    `json:"interval_ms,omitempty"`
		Match      string `json:"match,omitempty" jsonschema:"exact|contains|regex"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "wait_for_text", Description: "Poll OCR until the text appears or timeout."},
		func(ctx context.Context, req *mcp.CallToolRequest, a waitTextArgs) (*mcp.CallToolResult, any, error) {
			cfg := loadCfg()
			timeout := a.TimeoutMs
			if timeout <= 0 {
				timeout = 10000
			}
			interval := a.IntervalMs
			if interval <= 0 {
				interval = 500
			}
			deadline := time.Now().Add(time.Duration(timeout) * time.Millisecond)
			for {
				if found, box, l := ocrFind(cfg, a.Text, a.Match); found {
					return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
						Text: fmt.Sprintf("found %q at %+v (line %q)", a.Text, box, l),
					}}}, nil, nil
				}
				if time.Now().After(deadline) {
					return errResult(fmt.Errorf("timed out after %dms waiting for %q", timeout, a.Text)), nil, nil
				}
				input.Delay(interval)
			}
		})

	mcp.AddTool(server, &mcp.Tool{Name: "doctor", Description: "Self-check: target resolution and screen size."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct{}) (*mcp.CallToolResult, any, error) {
			cfg := loadCfg()
			sx, sy := screenSize()
			var out string
			out += fmt.Sprintf("screen: %dx%d\n", sx, sy)
			out += fmt.Sprintf("config: mode=%s app=%q title=%q rect=%+v\n", cfg.Mode, cfg.App, cfg.Title, cfg.Rect)
			if r, err := resolveRect(cfg, "", "", nil); err != nil {
				out += "target: ERROR " + err.Error() + " (try list_windows / set_target)\n"
			} else {
				out += fmt.Sprintf("target: ok %+v\n", r)
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil, nil
		})
}

// ocrFind runs OCR on the current target and searches for text; returns found, box, line.
func ocrFind(cfg *config.Config, text, match string) (bool, geom.Rect, string) {
	r, err := resolveRect(cfg, "", "", nil)
	if err != nil {
		return false, geom.Rect{}, ""
	}
	res, err := capture.CaptureRegion(r, 0)
	if err != nil {
		return false, geom.Rect{}, ""
	}
	p, err := tempPNG(res.PNG)
	if err != nil {
		return false, geom.Rect{}, ""
	}
	defer os.Remove(p)
	lines, err := ocr.OCR(p, cfg.OCR.Languages, cfg.OCR.Level, res.ImgW, res.ImgH, r)
	if err != nil {
		return false, geom.Rect{}, ""
	}
	for _, l := range lines {
		ok := false
		switch match {
		case "exact":
			ok = l.Text == text
		case "regex":
			if re, e := regexpCompile(text); e == nil {
				ok = re.MatchString(l.Text)
			}
		default:
			ok = containsFold(l.Text, text)
		}
		if ok {
			return true, l.Box, l.Text
		}
	}
	return false, geom.Rect{}, ""
}

func screenSize() (int, int) {
	return robotgo.GetScreenSize()
}
