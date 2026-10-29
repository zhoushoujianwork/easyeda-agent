package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func drawingsTestFixture(t *testing.T) (string, projectDrawingsSpec) {
	t.Helper()
	dir := t.TempDir()
	entries, err := os.ReadDir("testdata/project-drawings")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join("testdata/project-drawings", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, entry.Name()), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "spec.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec projectDrawingsSpec
	if err = json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	return dir, spec
}

func drawingsTestWriteSpec(t *testing.T, dir string, spec projectDrawingsSpec) string {
	t.Helper()
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "spec.json")
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func drawingsTestChangeFile(t *testing.T, dir, path string, edit func([]byte) []byte) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, path))
	if err != nil {
		t.Fatal(err)
	}
	data = edit(data)
	if err = os.WriteFile(filepath.Join(dir, path), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return drawingsHash(data)
}

func TestProjectDrawingsFixtureAndBytePreservation(t *testing.T) {
	dir, spec := drawingsTestFixture(t)
	out := filepath.Join(dir, "result")
	manifest, err := generateProjectDrawings(drawingsTestWriteSpec(t, dir, spec), out)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.GenerationSuccess || *manifest.Status.DesignAccepted {
		t.Fatalf("generation changed acceptance: %+v", manifest.Status)
	}
	if manifest.Status.PCBReview != (projectDrawingsPCBReview{"pending", "pending", "pending"}) {
		t.Fatalf("missing review inferred a pass: %+v", manifest.Status.PCBReview)
	}
	if manifest.DimensionsMM.Width != 25.4 || manifest.DimensionsMM.Height != 50.8 {
		t.Fatalf("dimensions: %+v", manifest.DimensionsMM)
	}
	if len(manifest.Artifacts) != 6 || len(manifest.FileHashes) != 7 {
		t.Fatalf("incomplete package: %+v", manifest.FileHashes)
	}
	for _, artifact := range spec.Artifacts {
		input, err := os.ReadFile(filepath.Join(dir, artifact.Path))
		if err != nil {
			t.Fatal(err)
		}
		output, err := os.ReadFile(filepath.Join(out, projectDrawingRoles[artifact.Role].name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(input, output) {
			t.Fatalf("%s rewritten", artifact.Role)
		}
	}
	data, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(dir)) || bytes.Contains(data, []byte("sourcePath")) {
		t.Fatal("private source path leaked into portable manifest")
	}
	readme, err := os.ReadFile(filepath.Join(out, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"candidate", "unrouted", "false", "共同坐标", "镜像装配", "Synthetic clearance", "输入声明", "未提供身份回执", "网名密度 `pending`", "文字碰撞 `pending`", "铜几何 `pending`"} {
		if !bytes.Contains(readme, []byte(want)) {
			t.Fatalf("README lost %q", want)
		}
	}
	if _, err := generateProjectDrawings(filepath.Join(dir, "spec.json"), out); err == nil || !strings.Contains(err.Error(), "overwrite") {
		t.Fatalf("existing output accepted: %v", err)
	}
}

func TestProjectDrawingsRejectsInvalidSourcesAndBindings(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, string, *projectDrawingsSpec)
		want   string
	}{
		{"missing image", func(t *testing.T, dir string, s *projectDrawingsSpec) { s.Artifacts[1].Path = "missing.png" }, "no such file"},
		{"hash mismatch", func(t *testing.T, dir string, s *projectDrawingsSpec) {
			s.Artifacts[1].SHA256 = strings.Repeat("0", 64)
		}, "SHA256 mismatch"},
		{"invalid hash", func(t *testing.T, dir string, s *projectDrawingsSpec) { s.Artifacts[1].SHA256 = "invalid" }, "SHA256 mismatch"},
		{"broken PNG pixels", func(t *testing.T, dir string, s *projectDrawingsSpec) {
			s.Artifacts[1].SHA256 = drawingsTestChangeFile(t, dir, "synthetic.png", func(b []byte) []byte { return b[:35] })
		}, "corrupt PNG"},
		{"not SVG", func(t *testing.T, dir string, s *projectDrawingsSpec) {
			s.Artifacts[0].SHA256 = drawingsTestChangeFile(t, dir, "schematic.svg", func(b []byte) []byte { return []byte("<html/>") })
		}, "SVG root"},
		{"SVG external resource", func(t *testing.T, dir string, s *projectDrawingsSpec) {
			s.Artifacts[0].SHA256 = drawingsTestChangeFile(t, dir, "schematic.svg", func(b []byte) []byte {
				return []byte(`<svg xmlns="http://www.w3.org/2000/svg"><image href="file:///private/secret.png"/></svg>`)
			})
		}, "external SVG"},
		{"SVG active resource", func(t *testing.T, dir string, s *projectDrawingsSpec) {
			s.Artifacts[0].SHA256 = drawingsTestChangeFile(t, dir, "schematic.svg", func(b []byte) []byte {
				return []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>evil()</script></svg>`)
			})
		}, "active SVG"},
		{"missing role", func(t *testing.T, dir string, s *projectDrawingsSpec) { s.Artifacts = s.Artifacts[:5] }, "missing required"},
		{"unknown role", func(t *testing.T, dir string, s *projectDrawingsSpec) { s.Artifacts[0].Role = "../../escape" }, "unknown or duplicate"},
		{"duplicate role", func(t *testing.T, dir string, s *projectDrawingsSpec) {
			s.Artifacts = append(s.Artifacts, s.Artifacts[0])
		}, "unknown or duplicate"},
		{"relative traversal", func(t *testing.T, dir string, s *projectDrawingsSpec) { s.Artifacts[0].Path = "../schematic.svg" }, "traversal"},
		{"symlink source", func(t *testing.T, dir string, s *projectDrawingsSpec) {
			if err := os.Symlink(filepath.Join(dir, "schematic.svg"), filepath.Join(dir, "link.svg")); err != nil {
				t.Fatal(err)
			}
			s.Artifacts[0].Path = "link.svg"
		}, "symlink"},
		{"readback hash", func(t *testing.T, dir string, s *projectDrawingsSpec) {
			s.Readbacks.PCB.SHA256 = strings.Repeat("0", 64)
		}, "evidence SHA256"},
		{"project identity", func(t *testing.T, dir string, s *projectDrawingsSpec) { s.ProjectUUID = "other-project" }, "identity mismatch"},
		{"document identity", func(t *testing.T, dir string, s *projectDrawingsSpec) { s.PCBDocumentUUID = "other-board" }, "identity mismatch"},
		{"missing identity", func(t *testing.T, dir string, s *projectDrawingsSpec) {
			s.Readbacks.PCB.DocumentUUIDPointer = "/capture/missing"
		}, "nonempty string"},
		{"invalid time", func(t *testing.T, dir string, s *projectDrawingsSpec) {
			s.Readbacks.PCB.CapturedAtPointer = "/capture/projectUuid"
		}, "RFC3339"},
		{"identity unverified", func(t *testing.T, dir string, s *projectDrawingsSpec) {
			s.Readbacks.PCB.SHA256 = drawingsTestChangeFile(t, dir, "board.json", func(b []byte) []byte {
				return bytes.ReplaceAll(b, []byte(`"identityVerified": true`), []byte(`"identityVerified": false`))
			})
		}, "not verified"},
		{"capture identity mismatch", func(t *testing.T, dir string, s *projectDrawingsSpec) {
			s.Artifacts[2].Evidence.SHA256 = drawingsTestChangeFile(t, dir, "top-capture.json", func(b []byte) []byte { return bytes.ReplaceAll(b, []byte("synthetic-pcb"), []byte("different-pcb")) })
		}, "identity mismatch"},
		{"capture artifact hash", func(t *testing.T, dir string, s *projectDrawingsSpec) {
			s.Artifacts[2].Evidence.ArtifactSHA256Pointer = "/capture/documentUuid"
		}, "capture artifact SHA256"},
		{"capture image invalid", func(t *testing.T, dir string, s *projectDrawingsSpec) {
			s.Artifacts[2].Evidence.SHA256 = drawingsTestChangeFile(t, dir, "top-capture.json", func(b []byte) []byte {
				return bytes.ReplaceAll(b, []byte(`"imageValid": true`), []byte(`"imageValid": false`))
			})
		}, "image validity"},
		{"candidate SVG", func(t *testing.T, dir string, s *projectDrawingsSpec) {
			s.Artifacts[3].SHA256 = drawingsTestChangeFile(t, dir, "actual-top.svg", func(b []byte) []byte {
				return bytes.ReplaceAll(b, []byte(`"mode": "actual"`), []byte(`"mode": "candidate"`))
			})
		}, "candidate render"},
		{"actual missing metadata", func(t *testing.T, dir string, s *projectDrawingsSpec) {
			s.Artifacts[3].Path = s.Artifacts[0].Path
			s.Artifacts[3].SHA256 = s.Artifacts[0].SHA256
		}, "candidate render"},
		{"actual source mismatch", func(t *testing.T, dir string, s *projectDrawingsSpec) {
			s.Artifacts[3].SHA256 = drawingsTestChangeFile(t, dir, "actual-top.svg", func(b []byte) []byte {
				return bytes.ReplaceAll(b, []byte(s.Readbacks.PCB.SHA256), []byte(strings.Repeat("0", 64)))
			})
		}, "source hash"},
		{"actual side mismatch", func(t *testing.T, dir string, s *projectDrawingsSpec) {
			s.Artifacts[3].SHA256 = drawingsTestChangeFile(t, dir, "actual-top.svg", func(b []byte) []byte { return bytes.ReplaceAll(b, []byte(`"side": "top"`), []byte(`"side": "bottom"`)) })
		}, "side/orientation"},
		{"actual as official", func(t *testing.T, dir string, s *projectDrawingsSpec) {
			s.Artifacts[0].Path = s.Artifacts[3].Path
			s.Artifacts[0].SHA256 = s.Artifacts[3].SHA256
		}, "cannot be labeled official"},
		{"actual time mismatch", func(t *testing.T, dir string, s *projectDrawingsSpec) {
			s.Artifacts[3].SHA256 = drawingsTestChangeFile(t, dir, "actual-top.svg", func(b []byte) []byte {
				return bytes.ReplaceAll(b, []byte("2026-01-01T00:00:00Z"), []byte("2026-01-01T00:01:00Z"))
			})
		}, "capture time"},
		{"routing contradiction", func(t *testing.T, dir string, s *projectDrawingsSpec) { s.Status.Routing = "routed" }, "routing conflicts"},
		{"acceptance contradiction", func(t *testing.T, dir string, s *projectDrawingsSpec) { yes := true; s.Status.DesignAccepted = &yes }, "designAccepted conflicts"},
		{"acceptance missing", func(t *testing.T, dir string, s *projectDrawingsSpec) { s.Status.DesignAccepted = nil }, "explicitly declare"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, spec := drawingsTestFixture(t)
			tt.change(t, dir, &spec)
			out := filepath.Join(dir, "result")
			_, err := generateProjectDrawings(drawingsTestWriteSpec(t, dir, spec), out)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %q", err, tt.want)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatal("failed validation created output")
			}
		})
	}
}

func TestProjectDrawingsOutputAliasesAndSymlinks(t *testing.T) {
	dir, spec := drawingsTestFixture(t)
	path := drawingsTestWriteSpec(t, dir, spec)
	for _, output := range []string{dir, path, filepath.Join(dir, "schematic.svg")} {
		if _, err := generateProjectDrawings(path, output); err == nil {
			t.Fatalf("source/output alias accepted: %s", output)
		}
	}
	link := filepath.Join(dir, "out-link")
	if err := os.Symlink(filepath.Join(dir, "missing-target"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := generateProjectDrawings(path, link); err == nil {
		t.Fatal("output symlink accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "missing-target")); !os.IsNotExist(err) {
		t.Fatal("wrote symlink target")
	}
}

func TestProjectDrawingsOptionalPDFAndSameImage(t *testing.T) {
	dir, spec := drawingsTestFixture(t)
	pdf := []byte("%PDF-1.7\n1 0 obj << /Type /Catalog >> endobj\n%%EOF\n")
	if err := os.WriteFile(filepath.Join(dir, "schematic.pdf"), pdf, 0o600); err != nil {
		t.Fatal(err)
	}
	spec.Artifacts = append(spec.Artifacts, projectDrawingsArtifact{Role: "schematic-pdf", Path: "schematic.pdf", SHA256: drawingsHash(pdf)})
	// Identical PNG bytes from a later successful capture are not stale evidence.
	spec.Artifacts[2].Evidence.SHA256 = drawingsTestChangeFile(t, dir, "top-capture.json", func(b []byte) []byte {
		return bytes.Replace(b, []byte(`"imageValid": true`), []byte(`"imageValid": true, "sameImageAsPrevious": true, "stale": true`), 1)
	})
	manifest, err := generateProjectDrawings(drawingsTestWriteSpec(t, dir, spec), filepath.Join(dir, "result"))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Artifacts) != 7 {
		t.Fatal("optional PDF missing")
	}
}

func TestProjectDrawingsCLIHelpAndOffline(t *testing.T) {
	var out bytes.Buffer
	cmd := newProjectCmd(nil, &out, &out)
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"drawings", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--from", "--out", "offline", "single-schematic-page", "schemaVersion", "never infers design acceptance"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help lacks %q", want)
		}
	}
	dir, spec := drawingsTestFixture(t)
	out.Reset()
	// nil appConfig is intentional: local generation must not contact the daemon.
	cmd = newProjectCmd(nil, &out, &out)
	cmd.SetArgs([]string{"drawings", "--from", drawingsTestWriteSpec(t, dir, spec), "--out", filepath.Join(dir, "result")})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"generationSuccess": true`) {
		t.Fatalf("unexpected result: %s", out.String())
	}
}

func TestProjectDrawingsJSONPointers(t *testing.T) {
	value := map[string]any{"context/identity": map[string]any{"~id": "p"}, "rows": []any{"zero", "one"}}
	for pointer, expected := range map[string]string{"/context~1identity/~0id": "p", "/rows/1": "one"} {
		got, err := drawingsPointerString(value, pointer)
		if err != nil || got != expected {
			t.Fatalf("%s: got %q %v", pointer, got, err)
		}
	}
	for _, pointer := range []string{"", "rows/1", "/rows/8", "/rows/no", "/context~2identity", "/rows"} {
		if _, err := drawingsPointerString(value, pointer); err == nil {
			t.Fatalf("invalid pointer accepted: %q", pointer)
		}
	}
}

func TestProjectDrawingsActualRendererIntegration(t *testing.T) {
	dir, spec := drawingsTestFixture(t)
	board := actualRenderFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "board.json"), board, 0o600); err != nil {
		t.Fatal(err)
	}
	spec.Readbacks.PCB.SHA256 = drawingsHash(board)
	for i := range spec.Artifacts {
		a := &spec.Artifacts[i]
		if !projectDrawingRoles[a.Role].actual {
			continue
		}
		side := "top"
		if a.Role == "pcb-actual-bottom-svg" {
			side = "bottom"
		}
		var svg bytes.Buffer
		if err := renderPCBActualSVG(&svg, board, side); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, a.Path), svg.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		a.SHA256 = drawingsHash(svg.Bytes())
	}
	manifest, err := generateProjectDrawings(drawingsTestWriteSpec(t, dir, spec), filepath.Join(dir, "result"))
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(manifest.DimensionsMM.Width-12.7) > 1e-9 || math.Abs(manifest.DimensionsMM.Height-17.78) > 1e-9 {
		t.Fatalf("lost actual outline dimensions: %+v", manifest.DimensionsMM)
	}
	actualCount := 0
	for _, artifact := range manifest.Artifacts {
		if artifact.SourceKind == "actual-readback-render" {
			actualCount++
			if artifact.Binding != "embedded-actual-metadata" || artifact.ReadbackSHA256 != drawingsHash(board) || len(artifact.Limitations) == 0 {
				t.Fatalf("lost actual evidence or limitations: %+v", artifact)
			}
		}
	}
	if actualCount != 2 {
		t.Fatal("missing actual top/bottom artifacts")
	}
}

func TestProjectDrawingsSummarizesLargeLimitationsWithoutLosingManifestEvidence(t *testing.T) {
	dir, spec := drawingsTestFixture(t)
	limitations := make([]string, 450)
	for i := range limitations {
		limitations[i] = fmt.Sprintf("missing observation for synthetic primitive %d", i)
	}
	encoded, err := json.Marshal(limitations)
	if err != nil {
		t.Fatal(err)
	}
	for i := range spec.Artifacts {
		a := &spec.Artifacts[i]
		if !projectDrawingRoles[a.Role].actual {
			continue
		}
		a.SHA256 = drawingsTestChangeFile(t, dir, a.Path, func(raw []byte) []byte {
			return bytes.Replace(raw, []byte(`["Synthetic packaging fixture; not live EDA evidence."]`), encoded, 1)
		})
	}
	out := filepath.Join(dir, "result")
	manifest, err := generateProjectDrawings(drawingsTestWriteSpec(t, dir, spec), out)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range manifest.Artifacts {
		if a.SourceKind == "actual-readback-render" && (len(a.Limitations) != 450 || a.Limitations[449] != limitations[449]) {
			t.Fatalf("manifest lost per-object limitations: %s %d", a.Role, len(a.Limitations))
		}
	}
	readme, err := os.ReadFile(filepath.Join(out, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(readme), "450 条渲染限制") != 2 || bytes.Contains(readme, []byte(limitations[0])) || bytes.Count(readme, []byte("\n")) > 70 {
		t.Fatalf("one-page explanation expanded per-object details: %s", readme)
	}
	persisted, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(persisted, []byte(limitations[0])) || !bytes.Contains(persisted, []byte(limitations[449])) {
		t.Fatal("persisted manifest lost full limitation details")
	}
}

// A fully connected/DRC-clean declaration must not stand in for visual review.
func TestProjectDrawingsPCBReviewAcceptance(t *testing.T) {
	_, spec := drawingsTestFixture(t)
	yes := true
	spec.Status.Layout, spec.Status.Routing = "verified", "routed"
	spec.Status.DRCPassed, spec.Status.DesignAccepted = &yes, &yes
	spec.Status.Unresolved = nil
	spec.Status.PCBReview = projectDrawingsPCBReview{"pass", "pass", "pass"}
	if err := validateDrawingsSpec(spec); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"labelDensity", "textCollisions", "copperGeometry"} {
		for _, result := range []string{"", "pending", "fail", "invalid"} {
			t.Run(field+"/"+result, func(t *testing.T) {
				s := spec
				switch field {
				case "labelDensity":
					s.Status.PCBReview.LabelDensity = result
				case "textCollisions":
					s.Status.PCBReview.TextCollisions = result
				case "copperGeometry":
					s.Status.PCBReview.CopperGeometry = result
				}
				if err := validateDrawingsSpec(s); err == nil || !strings.Contains(err.Error(), field) {
					t.Fatalf("unchecked review accepted or wrong error: %v", err)
				}
			})
		}
	}
}

func TestProjectDrawingsPreservesSeparatePCBReviews(t *testing.T) {
	for _, result := range []string{"pending", "fail", "pass", "invalid"} {
		t.Run(result, func(t *testing.T) {
			dir, spec := drawingsTestFixture(t)
			spec.Status.PCBReview = projectDrawingsPCBReview{result, "pass", "fail"}
			out := filepath.Join(dir, "result")
			manifest, err := generateProjectDrawings(drawingsTestWriteSpec(t, dir, spec), out)
			if result == "invalid" {
				if err == nil || !strings.Contains(err.Error(), "labelDensity") {
					t.Fatalf("invalid review: %v", err)
				}
				if _, err := os.Stat(out); !os.IsNotExist(err) {
					t.Fatalf("invalid review created output: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Status.PCBReview != spec.Status.PCBReview || *manifest.Status.DesignAccepted {
				t.Fatalf("review changed: %+v", manifest.Status)
			}
			data, err := os.ReadFile(filepath.Join(out, "manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			var saved projectDrawingsManifest
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Status.PCBReview != spec.Status.PCBReview {
				t.Fatalf("manifest lost review: %+v", saved.Status)
			}
			readme, err := os.ReadFile(filepath.Join(out, "README.md"))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"网名密度 `" + result + "`", "文字碰撞 `pass`", "铜几何 `fail`", "打包器不检测宿主网名或文字重叠"} {
				if !bytes.Contains(readme, []byte(want)) {
					t.Fatalf("README lost %q", want)
				}
			}
		})
	}
}
