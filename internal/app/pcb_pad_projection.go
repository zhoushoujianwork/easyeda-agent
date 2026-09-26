package app

import (
	"fmt"
	"strings"
)

// Standard pad shapes contain local dimensions. POLYGON paths instead contain
// absolute board coordinates, including nested contours and ARC endpoints.
func projectBoardPadShape(p boardPad, transform func(float64, float64) (float64, float64)) (any, error) {
	if p.SpecialPad != nil {
		special, ok := p.SpecialPad.([]any)
		if !ok || len(special) != 0 {
			return nil, fmt.Errorf("specialPad projection is unsupported or malformed")
		}
	}
	if p.Shape == nil {
		return nil, nil // Legacy bbox-only snapshots do not claim exact copper geometry.
	}
	shape, ok := p.Shape.([]any)
	if !ok || len(shape) < 2 {
		return nil, fmt.Errorf("shape is malformed")
	}
	kind, ok := shape[0].(string)
	if !ok {
		return nil, fmt.Errorf("shape name is malformed")
	}
	out := append([]any(nil), shape...)
	switch strings.ToUpper(strings.TrimSpace(kind)) {
	case "RECT", "ELLIPSE", "OVAL", "NGON":
		want := 3
		if strings.ToUpper(strings.TrimSpace(kind)) == "RECT" {
			want = 4
		}
		if len(shape) != want {
			return nil, fmt.Errorf("%s shape requires exactly %d fields", kind, want)
		}
		var parsed pcbPadP
		if err := parseNetPathPadShape(map[string]any{"shape": shape}, &parsed, "pad"); err != nil {
			return nil, err
		}
		return out, nil
	case "POLYGON":
		if len(shape) != 2 {
			return nil, fmt.Errorf("POLYGON shape must contain exactly one path source")
		}
		// Validate real contours, including ARC semantics, without replacing the
		// source with a flattened approximation.
		path, err := projectBoardPadPath(shape[1], transform)
		if err != nil {
			return nil, err
		}
		if _, err := polygonSourceContours(shape[1]); err != nil {
			return nil, fmt.Errorf("POLYGON source: %w", err)
		}
		out[1] = path
		return out, nil
	default:
		return nil, fmt.Errorf("pad shape %q projection is unsupported", kind)
	}
}

func projectBoardPadPath(raw any, transform func(float64, float64) (float64, float64)) (any, error) {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return nil, fmt.Errorf("path source is not a non-empty array")
	}
	if _, nested := items[0].([]any); nested {
		out := make([]any, len(items))
		for i, item := range items {
			path, err := projectBoardPadPath(item, transform)
			if err != nil {
				return nil, fmt.Errorf("contour %d: %w", i, err)
			}
			out[i] = path
		}
		return out, nil
	}
	if len(items) < 5 {
		return nil, fmt.Errorf("path source is too short")
	}
	out := append([]any(nil), items...)
	point := func(i int) error {
		x, xok := netPathOptionalFinite(items[i])
		y, yok := netPathOptionalFinite(items[i+1])
		if !xok || !yok {
			return fmt.Errorf("path coordinate at %d must be finite", i)
		}
		x, y = transform(x, y)
		if _, ok := netPathOptionalFinite(x); !ok {
			return fmt.Errorf("projected x coordinate is not finite")
		}
		if _, ok := netPathOptionalFinite(y); !ok {
			return fmt.Errorf("projected y coordinate is not finite")
		}
		out[i], out[i+1] = x, y
		return nil
	}
	if err := point(0); err != nil {
		return nil, err
	}
	for i := 2; i < len(items); {
		command, ok := items[i].(string)
		if !ok {
			return nil, fmt.Errorf("invalid path command at %d", i)
		}
		i++
		switch command {
		case "L":
			start := i
			for i < len(items) {
				if _, next := items[i].(string); next {
					break
				}
				if i+1 >= len(items) {
					return nil, fmt.Errorf("incomplete L coordinate pair")
				}
				if err := point(i); err != nil {
					return nil, err
				}
				i += 2
			}
			if i == start {
				return nil, fmt.Errorf("L command has no coordinate pair")
			}
		case "ARC":
			if i+2 >= len(items) {
				return nil, fmt.Errorf("incomplete ARC")
			}
			if _, ok := netPathOptionalFinite(items[i]); !ok {
				return nil, fmt.Errorf("ARC sweep must be finite")
			}
			if err := point(i + 1); err != nil {
				return nil, err
			}
			i += 3 // Rotation/translation preserve the original signed sweep.
		default:
			return nil, fmt.Errorf("path command %q is unsupported", command)
		}
	}
	return out, nil
}
