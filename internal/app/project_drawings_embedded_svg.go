package app

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

const maxDrawingsEmbeddedSVGBytes = 1 << 20

// Official schematic exports embed a static vector logo as an image data URI.
// Validate that small, self-contained subset without rewriting the original SVG.
// Images, links, stylesheets, animation and metadata are intentionally excluded;
// nested content cannot supply the outer actual-readback identity.
func validateDrawingsEmbeddedSVG(encoded string) error {
	if len(encoded) > base64.StdEncoding.EncodedLen(maxDrawingsEmbeddedSVGBytes) {
		return fmt.Errorf("embedded SVG exceeds 1 MiB")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) == 0 || len(data) > maxDrawingsEmbeddedSVGBytes {
		return fmt.Errorf("embedded SVG must be nonempty bounded base64")
	}
	d := xml.NewDecoder(bytes.NewReader(data))
	depth, roots := 0, 0
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid embedded SVG XML: %w", err)
		}
		switch t := token.(type) {
		case xml.Directive:
			return fmt.Errorf("embedded SVG directives are unsupported")
		case xml.ProcInst:
			if t.Target != "xml" || depth != 0 {
				return fmt.Errorf("embedded SVG processing instructions are unsupported")
			}
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots != 1 || t.Name.Local != "svg" {
					return fmt.Errorf("embedded SVG must have one SVG root")
				}
			}
			depth++
			if depth > 64 || (t.Name.Space != "" && t.Name.Space != "http://www.w3.org/2000/svg") {
				return fmt.Errorf("embedded SVG depth or namespace is unsupported")
			}
			switch t.Name.Local {
			case "svg", "g", "path", "rect", "circle", "ellipse", "line", "polyline", "polygon", "title", "desc":
			default:
				return fmt.Errorf("embedded SVG element %q is not static vector content", t.Name.Local)
			}
			for _, a := range t.Attr {
				if a.Name.Space == "xmlns" || a.Name.Local == "xmlns" {
					continue
				}
				if a.Name.Space != "" && !(a.Name.Space == "http://www.w3.org/XML/1998/namespace" && a.Name.Local == "space") {
					return fmt.Errorf("embedded SVG attribute namespace is unsupported")
				}
				name := strings.ToLower(a.Name.Local)
				switch name {
				case "id", "class", "version", "space", "viewbox", "preserveaspectratio", "width", "height", "x", "y", "x1", "x2", "y1", "y2", "cx", "cy", "r", "rx", "ry", "d", "points", "transform", "fill", "stroke", "color", "opacity", "fill-opacity", "stroke-opacity", "fill-rule", "stroke-width", "stroke-linecap", "stroke-linejoin", "stroke-miterlimit", "stroke-dasharray", "stroke-dashoffset", "display", "visibility", "style", "enable-background":
				default:
					return fmt.Errorf("embedded SVG attribute %q is unsupported", a.Name.Local)
				}
				value := strings.ToLower(strings.TrimSpace(a.Value))
				compact := strings.Join(strings.Fields(value), "")
				if strings.ContainsAny(value, "\\@{}\"'") || strings.Contains(value, "/*") || strings.Contains(value, "*/") || strings.Contains(compact, "url(") || strings.Contains(compact, "var(") || strings.Contains(compact, "expression(") || strings.Contains(compact, "image-set(") || strings.Contains(value, "://") || strings.Contains(value, "javascript:") {
					return fmt.Errorf("embedded SVG resources and escaped styles are unsupported")
				}
				if name == "style" {
					for _, declaration := range strings.Split(value, ";") {
						if strings.TrimSpace(declaration) == "" {
							continue
						}
						property, content, found := strings.Cut(declaration, ":")
						if !found || strings.TrimSpace(content) == "" {
							return fmt.Errorf("invalid embedded SVG inline style")
						}
						switch strings.TrimSpace(property) {
						case "fill", "stroke", "color", "opacity", "fill-opacity", "stroke-opacity", "fill-rule", "stroke-width", "stroke-linecap", "stroke-linejoin", "stroke-miterlimit", "stroke-dasharray", "stroke-dashoffset", "display", "visibility", "enable-background":
						default:
							return fmt.Errorf("embedded SVG style property %q is unsupported", property)
						}
					}
				}
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(t)) != "" {
				return fmt.Errorf("text outside embedded SVG root")
			}
		}
	}
	if roots != 1 || depth != 0 {
		return fmt.Errorf("incomplete embedded SVG")
	}
	return nil
}
