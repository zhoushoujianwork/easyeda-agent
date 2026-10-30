package app

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func drawingsEmbeddedSVGForTest(inner string) []byte {
	return []byte(`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><image xlink:href="data:image/svg+xml;base64,` + base64.StdEncoding.EncodeToString([]byte(inner)) + `"/></svg>`)
}

func TestProjectDrawingsEmbeddedSVGBytePreservation(t *testing.T) {
	// Synthetic geometry models the static logo in official Web 4.1.78 exports;
	// no customer drawing or third-party logo is included in this fixture.
	inner := `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" xml:space="preserve" viewBox="0 0 2000 629" style="enable-background: new 0 0 2000 629;"><desc>synthetic vector</desc><g style="display: inline; fill: #5588FF;"><path d="M0 0 L10 10 Z"/><rect width="5" height="5"/><ellipse cx="10" cy="10" rx="2" ry="3"/></g></svg>`
	outer := drawingsEmbeddedSVGForTest(inner)
	dir, spec := drawingsTestFixture(t)
	spec.Artifacts[0].SHA256 = drawingsTestChangeFile(t, dir, "schematic.svg", func([]byte) []byte { return outer })
	out := filepath.Join(dir, "result")
	manifest, err := generateProjectDrawings(drawingsTestWriteSpec(t, dir, spec), out)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range manifest.Artifacts {
		if a.Role == "schematic-svg" {
			copied, err := os.ReadFile(filepath.Join(out, a.Path))
			if err != nil || !bytes.Equal(copied, outer) || a.SHA256 != drawingsHash(outer) {
				t.Fatalf("official bytes changed: %v", err)
			}
			return
		}
	}
	t.Fatal("schematic SVG missing")
}

func TestProjectDrawingsEmbeddedSVGRejectsActiveOrExternalContent(t *testing.T) {
	for _, inner := range []string{
		`<svg><script>evil()</script></svg>`,
		`<svg onload="evil()"/>`,
		`<svg><foreignObject/></svg>`,
		`<svg><animate attributeName="href"/></svg>`,
		`<svg><a href="https://example.invalid/"><path/></a></svg>`,
		`<svg><image href="data:image/svg+xml;base64,PHN2Zy8+"/></svg>`,
		`<svg><metadata id="easyeda-actual-readback">{}</metadata></svg>`,
		`<svg><style>@import 'https://example.invalid/style.css';</style></svg>`,
		`<svg><path fill="url(https://example.invalid/paint)"/></svg>`,
		`<svg><path fill="&#117;rl(https://example.invalid/paint)"/></svg>`,
		`<svg><path style="fill: u\72l(https://example.invalid/paint)"/></svg>`,
		`<svg><path style="fill: url (#paint)"/></svg>`,
		`<svg><path style="background-image: image-set(x)"/></svg>`,
		`<svg><path style="fill: var(--paint)"/></svg>`,
		`<svg><path style="fill: red; filter: blur(1px)"/></svg>`,
		`<svg xmlns="http://www.w3.org/1999/xhtml"><path/></svg>`,
		`<!DOCTYPE svg [<!ENTITY x SYSTEM "file:///secret">]><svg>&x;</svg>`,
		`<?xml-stylesheet href="file:///secret"?><svg/>`,
		`<svg/><svg/>`,
		`<svg>`,
		`outside<svg/>`,
		strings.Repeat(`<svg>`, 65) + strings.Repeat(`</svg>`, 65),
	} {
		t.Run(inner[:min(len(inner), 60)], func(t *testing.T) {
			if _, err := drawingsSVGMetadata(drawingsEmbeddedSVGForTest(inner)); err == nil {
				t.Fatal("unsafe/unsupported embedded SVG accepted")
			}
		})
	}
	for _, uri := range []string{"data:image/svg+xml;base64,!!", "data:image/svg+xml;base64,", "data:image/svg+xml,%3Csvg/%3E", "data:text/html;base64,PHN2Zy8+", "data:image/svg+xml;base64," + strings.Repeat("A", base64.StdEncoding.EncodedLen(maxDrawingsEmbeddedSVGBytes)+4)} {
		if _, err := drawingsSVGMetadata([]byte(`<svg><image href="` + uri + `"/></svg>`)); err == nil {
			t.Fatal("bad or unbounded image accepted")
		}
	}
	if _, err := drawingsSVGMetadata([]byte(`<svg><use href="data:image/svg+xml;base64,PHN2Zy8+"/></svg>`)); err == nil {
		t.Fatal("SVG data URI accepted outside an image")
	}
	for _, outer := range []string{
		`<svg><image src="data:image/svg+xml;base64,PHN2Zy8+"/></svg>`,
		`<svg><image xmlns="http://www.w3.org/1999/xhtml" href="data:image/svg+xml;base64,PHN2Zy8+"/></svg>`,
	} {
		if _, err := drawingsSVGMetadata([]byte(outer)); err == nil {
			t.Fatal("SVG data URI accepted on an unsupported attribute or namespace")
		}
	}
}

func TestProjectDrawingsEmbeddedSVGBoundaries(t *testing.T) {
	for _, inner := range []string{
		strings.Repeat(`<svg>`, 64) + strings.Repeat(`</svg>`, 64),
		`<svg>` + strings.Repeat(" ", maxDrawingsEmbeddedSVGBytes-len(`<svg></svg>`)) + `</svg>`,
	} {
		if _, err := drawingsSVGMetadata(drawingsEmbeddedSVGForTest(inner)); err != nil {
			t.Fatalf("document at the supported limit rejected: %v", err)
		}
	}
	outer := strings.ReplaceAll(string(drawingsEmbeddedSVGForTest(`<svg><path d="M0 0L1 1"/></svg>`)), "data:image/svg+xml;base64,", "DATA:IMAGE/SVG+XML;BASE64,")
	if _, err := drawingsSVGMetadata([]byte(outer)); err != nil {
		t.Fatalf("case-insensitive MIME header changed base64 contents: %v", err)
	}
}
