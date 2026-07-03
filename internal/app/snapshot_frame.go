package app

import (
	"fmt"
	"image"
	_ "image/png" // register PNG decoder for snapshot artifacts
	"io"
	"os"
	"strings"
)

// frameStats summarizes how much a captured snapshot PNG actually contains.
//
// WHY this exists (probe finding 2026-07-03): the connector captures the frame
// via eda.dmt_EditorControl.getCurrentRenderedAreaImage(), which reads the
// window's CURRENTLY RENDERED canvas. When the EasyEDA window is not visibly
// rendering the target document (minimized, on another Space, or its render
// loop otherwise idle), that readback comes back BLANK — a flat white frame —
// even though the document has primitives (verified: primitiveCount=33 while
// the saved PNG was entirely white). No connector-side nudge fixes this
// (zoomToAllPrimitives / startCalculatingRatline / openDocument(sameUuid) /
// tab-switch were all tried and none forced a repaint) because visible
// rendering is a precondition OUTSIDE the webview's control.
//
// So a blank frame is NOT the same failure as a STALE frame (byte-identical to
// a prior capture): stale means "the pixels didn't change", blank means "there
// are no pixels". A recording/demo workflow must reject BOTH — saving a blank
// frame as a stage screenshot silently passes off an empty canvas as a result.
// We detect blankness here on the CLI side by reading the persisted PNG, so no
// connector rebuild/re-import is needed.
type frameStats struct {
	Width         int
	Height        int
	Sampled       int
	NonBgFraction float64 // fraction of sampled pixels differing from the corner (background) color
	DistinctBins  int     // distinct coarse color bins seen (a flat fill collapses to 1)
	Blank         bool
}

// blank thresholds — a genuine schematic/PCB at fit-zoom covers well above
// these; a flat-fill (empty/hidden canvas) collapses to ~0 coverage and 1 bin.
const (
	blankCoverageThreshold = 0.0015 // <0.15% non-background pixels ⇒ effectively empty
	blankMaxDistinctBins   = 2      // ≤2 coarse color bins ⇒ a flat fill
)

// analyzeSnapshotFrame decodes a snapshot PNG and reports how much of it is
// actual content, so a caller can warn on (or gate against) a blank frame. It
// samples on a stride so a large capture stays cheap. A decode error is
// returned rather than guessed at — the caller decides whether that is fatal.
func analyzeSnapshotFrame(path string) (frameStats, error) {
	f, err := os.Open(path)
	if err != nil {
		return frameStats{}, err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return frameStats{}, fmt.Errorf("decode snapshot %s: %w", path, err)
	}

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return frameStats{Width: w, Height: h, Blank: true}, nil
	}

	// Background = the top-left corner pixel (canvas fill). Sample on a stride
	// so we look at ~40k pixels regardless of image size.
	const targetSamples = 40000
	stride := 1
	if total := w * h; total > targetSamples {
		stride = intSqrt(total / targetSamples)
		if stride < 1 {
			stride = 1
		}
	}

	bgR, bgG, bgB, _ := img.At(b.Min.X, b.Min.Y).RGBA()
	bins := make(map[uint32]struct{}, 64)
	var sampled, nonBg int
	for y := b.Min.Y; y < b.Max.Y; y += stride {
		for x := b.Min.X; x < b.Max.X; x += stride {
			r, g, bl, _ := img.At(x, y).RGBA()
			sampled++
			// RGBA() returns 16-bit channels; compare in 8-bit space with a
			// small tolerance so anti-aliasing against the background does not
			// count as content on its own.
			if diff8(r, bgR)+diff8(g, bgG)+diff8(bl, bgB) > 24 {
				nonBg++
			}
			// Coarse 4-bit-per-channel bin so a flat fill collapses to one bin
			// while real content (lines, text, silk) spreads across many.
			bin := (r>>12)<<8 | (g>>12)<<4 | (bl >> 12)
			bins[bin] = struct{}{}
		}
	}

	frac := 0.0
	if sampled > 0 {
		frac = float64(nonBg) / float64(sampled)
	}
	stats := frameStats{
		Width:         w,
		Height:        h,
		Sampled:       sampled,
		NonBgFraction: frac,
		DistinctBins:  len(bins),
	}
	stats.Blank = frac < blankCoverageThreshold || len(bins) <= blankMaxDistinctBins
	return stats, nil
}

// snapshotArtifact returns the persisted PNG path of a snapshot action result,
// or "" if the result carried no image artifact.
func snapshotArtifact(res *actionResult) string {
	if res == nil {
		return ""
	}
	for _, a := range res.Artifacts {
		if strings.HasPrefix(a.MimeType, "image/") && a.Path != "" {
			return a.Path
		}
	}
	return ""
}

// snapshotIsStale reports whether the connector flagged the frame as stale
// (byte-identical to the previous capture after a retry).
func snapshotIsStale(res *actionResult) bool {
	if res == nil || res.Result == nil {
		return false
	}
	if v, ok := res.Result["stale"].(bool); ok {
		return v
	}
	return false
}

// warnIfBlankSnapshot inspects a snapshot result and prints a loud WARN to
// stderr when the captured frame is blank — the "window not rendering" failure
// mode that a stale-check alone does not catch. Best-effort: a decode failure
// is silent (we do not want a warning path to break a normal snapshot).
func warnIfBlankSnapshot(res *actionResult, stderr io.Writer) {
	path := snapshotArtifact(res)
	if path == "" {
		return
	}
	stats, err := analyzeSnapshotFrame(path)
	if err != nil || !stats.Blank {
		return
	}
	fmt.Fprintf(stderr, "⚠️  snapshot frame looks BLANK (%.3f%% content, %d colors) — "+
		"the EasyEDA window is likely not rendering this document (minimized / on another "+
		"Space / behind other windows). Bring EasyEDA to the FOREGROUND on the target "+
		"canvas and re-capture. Judge state by data (list/drc/check); no API call can force "+
		"a hidden window to repaint.\n", stats.NonBgFraction*100, stats.DistinctBins)
}

// diff8 is the absolute difference of two 16-bit color channels, scaled to
// 8-bit (0-255) so tolerance thresholds read naturally.
func diff8(a, b uint32) uint32 {
	a >>= 8
	b >>= 8
	if a > b {
		return a - b
	}
	return b - a
}

// intSqrt is a small integer square root (floor) for stride sizing.
func intSqrt(n int) int {
	if n < 2 {
		return n
	}
	x := n
	y := (x + 1) / 2
	for y < x {
		x = y
		y = (x + n/x) / 2
	}
	return x
}
