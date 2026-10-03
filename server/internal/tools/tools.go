// Package tools defines the MCP tool surface.
package tools

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

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
