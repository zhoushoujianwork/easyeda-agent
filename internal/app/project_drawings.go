package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// This is a drawing handoff, not a design report or a new acceptance engine.
// Identity pointers only select strings from a supplied evidence file: no
// expression language, transformations, defaults or inferred project identity.
type projectDrawingsEvidence struct {
	Path                  string `json:"path"`
	SHA256                string `json:"sha256"`
	ProjectUUIDPointer    string `json:"projectUuidPointer"`
	DocumentUUIDPointer   string `json:"documentUuidPointer"`
	CapturedAtPointer     string `json:"capturedAtPointer"`
	ArtifactSHA256Pointer string `json:"artifactSha256Pointer,omitempty"`
}

type projectDrawingsArtifact struct {
	Role     string                   `json:"role"`
	Path     string                   `json:"path"`
	SHA256   string                   `json:"sha256"`
	Evidence *projectDrawingsEvidence `json:"evidence,omitempty"`
}

// Review results are caller declarations tied to the supplied drawing revision.
// They are not inferred from connectivity, DRC or successful packaging.
type projectDrawingsPCBReview struct {
	LabelDensity   string `json:"labelDensity"`
	TextCollisions string `json:"textCollisions"`
	CopperGeometry string `json:"copperGeometry"`
}

func (r *projectDrawingsPCBReview) normalize() {
	for _, value := range []*string{&r.LabelDensity, &r.TextCollisions, &r.CopperGeometry} {
		if *value == "" {
			*value = "pending"
		}
	}
}

type projectDrawingsStatus struct {
	PCBReview      projectDrawingsPCBReview `json:"pcbReview"`
	Layout         string                   `json:"layout"`
	Routing        string                   `json:"routing"`
	DRCPassed      *bool                    `json:"drcPassed"`
	DesignAccepted *bool                    `json:"designAccepted"`
	Unresolved     []string                 `json:"unresolved"`
}

type projectDrawingsSpec struct {
	SchemaVersion         int    `json:"schemaVersion"`
	Title                 string `json:"title"`
	Revision              string `json:"revision"`
	ProjectUUID           string `json:"projectUuid"`
	SchematicDocumentUUID string `json:"schematicDocumentUuid"`
	PCBDocumentUUID       string `json:"pcbDocumentUuid"`
	Readbacks             struct {
		Schematic projectDrawingsEvidence `json:"schematic"`
		PCB       projectDrawingsEvidence `json:"pcb"`
	} `json:"readbacks"`
	Artifacts []projectDrawingsArtifact `json:"artifacts"`
	Status    projectDrawingsStatus     `json:"status"`
}

type drawingsBoundEvidence struct {
	SHA256       string `json:"sha256"`
	ProjectUUID  string `json:"projectUuid"`
	DocumentUUID string `json:"documentUuid"`
	CapturedAt   string `json:"capturedAt"`
}

type drawingsManifestArtifact struct {
	Role           string                 `json:"role"`
	Path           string                 `json:"path"`
	SHA256         string                 `json:"sha256"`
	Size           int                    `json:"size"`
	SourceKind     string                 `json:"sourceKind"`
	View           string                 `json:"view"`
	ViewBasis      string                 `json:"viewBasis"`
	Binding        string                 `json:"binding"`
	Evidence       *drawingsBoundEvidence `json:"evidence,omitempty"`
	ReadbackSHA256 string                 `json:"readbackSha256"`
	Limitations    []string               `json:"limitations,omitempty"`
}

type drawingsDimensions struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type projectDrawingsManifest struct {
	SchemaVersion         int                              `json:"schemaVersion"`
	GenerationSuccess     bool                             `json:"generationSuccess"`
	GeneratedAt           string                           `json:"generatedAt"`
	SpecSHA256            string                           `json:"specSha256"`
	Title                 string                           `json:"title"`
	Revision              string                           `json:"revision"`
	ProjectUUID           string                           `json:"projectUuid"`
	SchematicDocumentUUID string                           `json:"schematicDocumentUuid"`
	PCBDocumentUUID       string                           `json:"pcbDocumentUuid"`
	Readbacks             map[string]drawingsBoundEvidence `json:"readbacks"`
	DimensionsMM          drawingsDimensions               `json:"dimensionsMM"`
	Status                projectDrawingsStatus            `json:"status"`
	StatusBasis           string                           `json:"statusBasis"`
	Artifacts             []drawingsManifestArtifact       `json:"artifacts"`
	FileHashes            map[string]string                `json:"fileHashes"`
	Limitations           []string                         `json:"limitations"`
}

type drawingsActualMetadata struct {
	SchemaVersion    int                `json:"schemaVersion"`
	Mode             string             `json:"mode"`
	SourceSHA256     string             `json:"sourceSha256"`
	ProjectUUID      string             `json:"projectUuid"`
	DocumentUUID     string             `json:"documentUuid"`
	IdentityVerified bool               `json:"identityVerified"`
	CapturedAt       string             `json:"capturedAt"`
	Side             string             `json:"side"`
	Orientation      string             `json:"orientation"`
	DimensionsMM     drawingsDimensions `json:"dimensionsMM"`
	RoutingStatus    string             `json:"routingStatus"`
	Limitations      []string           `json:"limitations"`
}

type drawingsRole struct {
	name, format, caption, view string
	actual                      bool
}

var projectDrawingRoles = map[string]drawingsRole{
	"schematic-svg":           {"schematic.svg", "svg", "原理图 · 官方 SVG", "schematic", false},
	"schematic-png":           {"schematic.png", "png", "原理图 · 官方 PNG", "schematic", false},
	"schematic-pdf":           {"schematic.pdf", "pdf", "原理图 · 官方 PDF", "schematic", false},
	"pcb-official-top-png":    {"pcb-official-top.png", "png", "PCB 正面层 · 官方截图", "top-layer-focus", false},
	"pcb-official-bottom-png": {"pcb-official-bottom.png", "png", "PCB 底面层 · 官方截图（共同坐标，非镜像装配底视）", "bottom-layer-focus-unmirrored", false},
	"pcb-actual-top-svg":      {"pcb-actual-top.svg", "svg", "PCB 正面 · 实际回读图", "top-unmirrored", true},
	"pcb-actual-bottom-svg":   {"pcb-actual-bottom.svg", "svg", "PCB 底面 · 实际回读镜像装配图", "bottom-mirrored-about-board-center", true},
}

const maxDrawingsSourceBytes = 128 << 20

func generateProjectDrawings(specPath, outDir string) (_ *projectDrawingsManifest, err error) {
	specAbs, err := filepath.Abs(specPath)
	if err != nil {
		return nil, err
	}
	specData, err := drawingsReadFile(specAbs)
	if err != nil {
		return nil, fmt.Errorf("read specification: %w", err)
	}
	var spec projectDrawingsSpec
	dec := json.NewDecoder(bytes.NewReader(specData))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&spec); err != nil {
		return nil, fmt.Errorf("decode specification: %w", err)
	}
	if dec.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("specification must contain one JSON object")
	}
	spec.Status.PCBReview.normalize()
	if err = validateDrawingsSpec(spec); err != nil {
		return nil, err
	}
	base := filepath.Dir(specAbs)
	schematic, _, err := readDrawingsEvidence(base, spec.Readbacks.Schematic, spec.ProjectUUID, spec.SchematicDocumentUUID, "")
	if err != nil {
		return nil, fmt.Errorf("schematic readback: %w", err)
	}
	pcb, _, err := readDrawingsEvidence(base, spec.Readbacks.PCB, spec.ProjectUUID, spec.PCBDocumentUUID, "")
	if err != nil {
		return nil, fmt.Errorf("PCB readback: %w", err)
	}
	manifest := &projectDrawingsManifest{
		SchemaVersion: 1, GenerationSuccess: true, GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano), SpecSHA256: drawingsHash(specData),
		Title: spec.Title, Revision: spec.Revision, ProjectUUID: spec.ProjectUUID,
		SchematicDocumentUUID: spec.SchematicDocumentUUID, PCBDocumentUUID: spec.PCBDocumentUUID,
		Readbacks: map[string]drawingsBoundEvidence{"schematic": schematic, "pcb": pcb}, Status: spec.Status,
		StatusBasis: "input-declaration; generation does not establish design acceptance", FileHashes: map[string]string{},
		Limitations: []string{"Single schematic page and one PCB; drawings are not a complete multi-page project report.", "Generation verifies supplied files and bindings, not electrical, placement, manufacturing or hardware acceptance."},
	}
	files := map[string][]byte{}
	for _, artifact := range spec.Artifacts {
		role := projectDrawingRoles[artifact.Role]
		path, pathErr := drawingsSourcePath(base, artifact.Path)
		if pathErr != nil {
			return nil, fmt.Errorf("%s: %w", artifact.Role, pathErr)
		}
		data, readErr := drawingsReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("%s: %w", artifact.Role, readErr)
		}
		if !drawingsHashMatches(data, artifact.SHA256) {
			return nil, fmt.Errorf("%s: source SHA256 mismatch", artifact.Role)
		}
		actual, formatErr := validateDrawingsFormat(data, role.format)
		if formatErr != nil {
			return nil, fmt.Errorf("%s: %w", artifact.Role, formatErr)
		}
		bound := pcb
		if role.view == "schematic" {
			bound = schematic
		}
		entry := drawingsManifestArtifact{Role: artifact.Role, Path: role.name, SHA256: drawingsHash(data), Size: len(data), View: role.view, ReadbackSHA256: bound.SHA256}
		if role.actual {
			if err = validateDrawingsActual(actual, spec, pcb, role.view); err != nil {
				return nil, fmt.Errorf("%s: %w", artifact.Role, err)
			}
			if manifest.DimensionsMM.Width != 0 && manifest.DimensionsMM != actual.DimensionsMM {
				return nil, fmt.Errorf("actual top and bottom dimensions differ")
			}
			manifest.DimensionsMM = actual.DimensionsMM
			entry.SourceKind, entry.Binding = "actual-readback-render", "embedded-actual-metadata"
			entry.ViewBasis = "embedded-actual-metadata"
			entry.Limitations = actual.Limitations
		} else {
			if actual != nil {
				return nil, fmt.Errorf("%s: actual render cannot be labeled official export", artifact.Role)
			}
			entry.SourceKind, entry.Binding = "official-export-supplied", "declared-readback-association"
			entry.ViewBasis = "input-declaration"
			entry.Limitations = []string{"No capture identity receipt supplied; association with this readback is declared, not verified."}
			if artifact.Evidence != nil {
				evidence, _, evidenceErr := readDrawingsEvidence(base, *artifact.Evidence, bound.ProjectUUID, bound.DocumentUUID, artifact.SHA256)
				if evidenceErr != nil {
					return nil, fmt.Errorf("%s capture evidence: %w", artifact.Role, evidenceErr)
				}
				entry.Evidence = &evidence
				entry.Binding, entry.Limitations = "capture-identity-and-artifact-hash", nil
			}
			if role.format == "pdf" {
				entry.Limitations = append(entry.Limitations, "PDF header and EOF checked only; PDF syntax and rendering are not validated.")
			}
		}
		files[role.name] = data
		manifest.FileHashes[role.name] = entry.SHA256
		manifest.Artifacts = append(manifest.Artifacts, entry)
	}
	sort.Slice(manifest.Artifacts, func(i, j int) bool { return manifest.Artifacts[i].Role < manifest.Artifacts[j].Role })
	files["README.md"] = []byte(drawingsREADME(manifest))
	manifest.FileHashes["README.md"] = drawingsHash(files["README.md"])
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	files["manifest.json"] = append(manifestData, '\n')
	// Validate everything first, then reserve a new directory atomically. O_EXCL
	// also refuses any symlink inserted into it before an individual write.
	outAbs, err := filepath.Abs(outDir)
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Lstat(outAbs); statErr == nil || !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("output path already exists or is inaccessible; refusing overwrite")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(outAbs))
	if err != nil {
		return nil, fmt.Errorf("output parent must exist: %w", err)
	}
	outAbs = filepath.Join(parent, filepath.Base(outAbs))
	if err = os.Mkdir(outAbs, 0o755); err != nil {
		return nil, fmt.Errorf("reserve output directory: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(outAbs)
		}
	}()
	for name, data := range files {
		f, createErr := os.OpenFile(filepath.Join(outAbs, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if createErr != nil {
			return nil, createErr
		}
		_, writeErr := f.Write(data)
		closeErr := f.Close()
		if writeErr != nil {
			return nil, writeErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	return manifest, nil
}

func validateDrawingsSpec(spec projectDrawingsSpec) error {
	if spec.SchemaVersion != 1 {
		return fmt.Errorf("unsupported schemaVersion; expected 1")
	}
	for key, value := range map[string]string{"title": spec.Title, "revision": spec.Revision, "projectUuid": spec.ProjectUUID, "schematicDocumentUuid": spec.SchematicDocumentUUID, "pcbDocumentUuid": spec.PCBDocumentUUID} {
		if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("%s must be a nonempty single line", key)
		}
	}
	if spec.Status.Layout != "candidate" && spec.Status.Layout != "verified" {
		return fmt.Errorf("status.layout must be candidate or verified")
	}
	switch spec.Status.Routing {
	case "unrouted", "partial", "routed", "unknown":
	default:
		return fmt.Errorf("status.routing must be unrouted, partial, routed or unknown")
	}
	if spec.Status.DesignAccepted == nil {
		return fmt.Errorf("status.designAccepted must explicitly declare true or false")
	}
	if *spec.Status.DesignAccepted && (spec.Status.Layout != "verified" || spec.Status.Routing != "routed" || spec.Status.DRCPassed == nil || !*spec.Status.DRCPassed || len(spec.Status.Unresolved) > 0) {
		return fmt.Errorf("status.designAccepted conflicts with candidate, routing, DRC or unresolved status")
	}
	for name, result := range map[string]string{
		"labelDensity":   spec.Status.PCBReview.LabelDensity,
		"textCollisions": spec.Status.PCBReview.TextCollisions,
		"copperGeometry": spec.Status.PCBReview.CopperGeometry,
	} {
		switch result {
		case "", "pending", "pass", "fail":
		default:
			return fmt.Errorf("status.pcbReview.%s must be pending, pass or fail", name)
		}
		if *spec.Status.DesignAccepted && result != "pass" {
			return fmt.Errorf("status.designAccepted conflicts with status.pcbReview.%s: explicit pass required", name)
		}
	}
	seen := map[string]bool{}
	for _, a := range spec.Artifacts {
		role, ok := projectDrawingRoles[a.Role]
		if !ok || seen[a.Role] {
			return fmt.Errorf("unknown or duplicate artifact role %q", a.Role)
		}
		if role.actual && a.Evidence != nil {
			return fmt.Errorf("actual renders use embedded metadata, not official capture evidence")
		}
		seen[a.Role] = true
	}
	for role := range projectDrawingRoles {
		if role != "schematic-pdf" && !seen[role] {
			return fmt.Errorf("missing required artifact role %q", role)
		}
	}
	return nil
}

func readDrawingsEvidence(base string, evidence projectDrawingsEvidence, projectUUID, documentUUID, artifactSHA string) (drawingsBoundEvidence, []byte, error) {
	var bound drawingsBoundEvidence
	path, err := drawingsSourcePath(base, evidence.Path)
	if err != nil {
		return bound, nil, err
	}
	data, err := drawingsReadFile(path)
	if err != nil {
		return bound, nil, err
	}
	if !drawingsHashMatches(data, evidence.SHA256) {
		return bound, nil, fmt.Errorf("evidence SHA256 mismatch")
	}
	var document any
	if err = json.Unmarshal(data, &document); err != nil {
		return bound, nil, fmt.Errorf("invalid evidence JSON: %w", err)
	}
	project, err := drawingsPointerString(document, evidence.ProjectUUIDPointer)
	if err != nil {
		return bound, nil, err
	}
	doc, err := drawingsPointerString(document, evidence.DocumentUUIDPointer)
	if err != nil {
		return bound, nil, err
	}
	stamp, err := drawingsPointerString(document, evidence.CapturedAtPointer)
	if err != nil {
		return bound, nil, err
	}
	if project != projectUUID || doc != documentUUID {
		return bound, nil, fmt.Errorf("evidence project/document identity mismatch")
	}
	if _, err = time.Parse(time.RFC3339Nano, stamp); err != nil {
		return bound, nil, fmt.Errorf("evidence capture time must be RFC3339")
	}
	if capture, ok := document.(map[string]any); ok {
		if valid, exists := capture["imageValid"]; exists && valid != true {
			return bound, nil, fmt.Errorf("evidence image validity is not confirmed")
		}
		if blank, exists := capture["blank"]; exists && blank == true {
			return bound, nil, fmt.Errorf("evidence describes a blank image")
		}
		if observed, ok := capture["capture"].(map[string]any); ok {
			if verified, exists := observed["identityVerified"]; exists && verified != true {
				return bound, nil, fmt.Errorf("evidence capture identity is not verified")
			}
		}
	}
	if artifactSHA != "" {
		hash, pointerErr := drawingsPointerString(document, evidence.ArtifactSHA256Pointer)
		if pointerErr != nil {
			return bound, nil, pointerErr
		}
		if !strings.EqualFold(hash, artifactSHA) {
			return bound, nil, fmt.Errorf("capture artifact SHA256 mismatch")
		}
	}
	return drawingsBoundEvidence{drawingsHash(data), project, doc, stamp}, data, nil
}

func drawingsPointerString(value any, pointer string) (string, error) {
	if !strings.HasPrefix(pointer, "/") {
		return "", fmt.Errorf("evidence requires a nonempty JSON pointer, got %q", pointer)
	}
	for _, raw := range strings.Split(pointer[1:], "/") {
		// RFC 6901 permits only ~0 and ~1 escapes.
		for i := 0; i < len(raw); i++ {
			if raw[i] == '~' {
				if i+1 >= len(raw) || (raw[i+1] != '0' && raw[i+1] != '1') {
					return "", fmt.Errorf("invalid JSON pointer %q", pointer)
				}
				i++
			}
		}
		key := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		switch current := value.(type) {
		case map[string]any:
			value = current[key]
		case []any:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(current) {
				return "", fmt.Errorf("JSON pointer %q is absent", pointer)
			}
			value = current[i]
		default:
			return "", fmt.Errorf("JSON pointer %q is absent", pointer)
		}
	}
	s, ok := value.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("JSON pointer %q must select a nonempty string", pointer)
	}
	return s, nil
}

func drawingsSourcePath(base, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("source path is required")
	}
	if !filepath.IsAbs(path) {
		for _, component := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
			if component == ".." {
				return "", fmt.Errorf("relative source path traversal is not allowed")
			}
		}
		path = filepath.Join(base, path)
	}
	return filepath.Clean(path), nil
}

func drawingsReadFile(path string) ([]byte, error) {
	stat, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() {
		return nil, fmt.Errorf("source must be a regular file, not a symlink or directory")
	}
	if stat.Size() > maxDrawingsSourceBytes {
		return nil, fmt.Errorf("source exceeds 128 MiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(stat, opened) {
		return nil, fmt.Errorf("source changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxDrawingsSourceBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDrawingsSourceBytes {
		return nil, fmt.Errorf("source exceeds 128 MiB")
	}
	return data, nil
}

func drawingsHash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func drawingsHashMatches(data []byte, expected string) bool {
	decoded, err := hex.DecodeString(expected)
	return err == nil && len(decoded) == 32 && strings.EqualFold(drawingsHash(data), expected)
}

func validateDrawingsFormat(data []byte, format string) (*drawingsActualMetadata, error) {
	switch format {
	case "png":
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("invalid PNG: %w", err)
		}
		if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 100_000_000 {
			return nil, fmt.Errorf("PNG dimensions are invalid or exceed 100 million pixels")
		}
		if _, err = png.Decode(bytes.NewReader(data)); err != nil {
			return nil, fmt.Errorf("corrupt PNG pixels: %w", err)
		}
		return nil, nil
	case "pdf":
		if !bytes.HasPrefix(data, []byte("%PDF-")) || !bytes.Contains(data, []byte("%%EOF")) {
			return nil, fmt.Errorf("invalid PDF signature or missing EOF")
		}
		return nil, nil
	case "svg":
		return drawingsSVGMetadata(data)
	default:
		return nil, fmt.Errorf("unsupported drawing format %q", format)
	}
}

func drawingsSVGMetadata(data []byte) (*drawingsActualMetadata, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	depth, roots := 0, 0
	var actual *drawingsActualMetadata
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid SVG XML: %w", err)
		}
		switch t := token.(type) {
		case xml.Directive:
			return nil, fmt.Errorf("SVG directives and external entities are unsupported")
		case xml.ProcInst:
			if t.Target != "xml" {
				return nil, fmt.Errorf("SVG processing instructions are unsupported")
			}
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots != 1 || t.Name.Local != "svg" || (t.Name.Space != "" && t.Name.Space != "http://www.w3.org/2000/svg") {
					return nil, fmt.Errorf("drawing must contain exactly one SVG root")
				}
			}
			depth++
			if t.Name.Local == "script" || t.Name.Local == "foreignObject" {
				return nil, fmt.Errorf("active SVG elements are unsupported")
			}
			isMeta := false
			for _, a := range t.Attr {
				name, val := strings.ToLower(a.Name.Local), strings.ToLower(strings.TrimSpace(a.Value))
				if strings.HasPrefix(name, "on") {
					return nil, fmt.Errorf("SVG event handlers are unsupported")
				}
				if (name == "href" || name == "src") && val != "" && !strings.HasPrefix(val, "#") && !strings.HasPrefix(val, "data:image/png;base64,") {
					const embeddedSVGPrefix = "data:image/svg+xml;base64,"
					if name != "href" || t.Name.Local != "image" || (t.Name.Space != "" && t.Name.Space != "http://www.w3.org/2000/svg") || !strings.HasPrefix(val, embeddedSVGPrefix) {
						return nil, fmt.Errorf("external SVG resource is unsupported")
					}
					if err := validateDrawingsEmbeddedSVG(strings.TrimSpace(a.Value)[len(embeddedSVGPrefix):]); err != nil {
						return nil, err
					}
				}
				if strings.Contains(val, "url(") && !strings.Contains(val, "url(#") {
					return nil, fmt.Errorf("external SVG style resource is unsupported")
				}
				if a.Name.Local == "id" && a.Value == "easyeda-actual-readback" && t.Name.Local == "metadata" {
					isMeta = true
				}
			}
			if isMeta {
				if actual != nil {
					return nil, fmt.Errorf("duplicate actual-readback metadata")
				}
				var value string
				if err = d.DecodeElement(&value, &t); err != nil {
					return nil, err
				}
				depth--
				actual = &drawingsActualMetadata{}
				if err = json.Unmarshal([]byte(value), actual); err != nil {
					return nil, fmt.Errorf("invalid actual-readback metadata: %w", err)
				}
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(t)) != "" {
				return nil, fmt.Errorf("text outside SVG root")
			}
			if bytes.Contains(bytes.ToLower(t), []byte("@import")) || bytes.Contains(bytes.ToLower(t), []byte("url(http")) {
				return nil, fmt.Errorf("external SVG stylesheet is unsupported")
			}
		}
	}
	if roots != 1 || depth != 0 {
		return nil, fmt.Errorf("incomplete SVG document")
	}
	return actual, nil
}

func validateDrawingsActual(actual *drawingsActualMetadata, spec projectDrawingsSpec, pcb drawingsBoundEvidence, orientation string) error {
	if actual == nil || actual.SchemaVersion != 1 || actual.Mode != "actual" {
		return fmt.Errorf("actual-readback SVG metadata is required; candidate render is not accepted")
	}
	if !actual.IdentityVerified {
		return fmt.Errorf("actual-render capture identity is not verified")
	}
	if actual.SourceSHA256 != pcb.SHA256 || actual.ProjectUUID != spec.ProjectUUID || actual.DocumentUUID != spec.PCBDocumentUUID {
		return fmt.Errorf("actual-render source hash or project/document identity mismatch")
	}
	if actual.CapturedAt != pcb.CapturedAt {
		return fmt.Errorf("actual-render capture time differs from bound PCB readback")
	}
	side := "top"
	if orientation == "bottom-mirrored-about-board-center" {
		side = "bottom"
	}
	if actual.Side != side || actual.Orientation != orientation {
		return fmt.Errorf("actual-render side/orientation mismatch")
	}
	if actual.DimensionsMM.Width <= 0 || actual.DimensionsMM.Height <= 0 || math.IsNaN(actual.DimensionsMM.Width) || math.IsInf(actual.DimensionsMM.Width, 0) || math.IsNaN(actual.DimensionsMM.Height) || math.IsInf(actual.DimensionsMM.Height, 0) {
		return fmt.Errorf("actual-render dimensions are missing or invalid")
	}
	// The renderer currently knows whether traces are absent/present, not whether
	// all nets are routed. Only an explicit contradictory unrouted state is fatal.
	noRoutes := actual.RoutingStatus == "unrouted" || actual.RoutingStatus == "no-routed-primitives"
	hasRoutes := actual.RoutingStatus == "routed" || actual.RoutingStatus == "routed-primitives-present"
	if (noRoutes && (spec.Status.Routing == "partial" || spec.Status.Routing == "routed")) || (hasRoutes && spec.Status.Routing == "unrouted") {
		return fmt.Errorf("declared routing conflicts with actual-render metadata")
	}
	return nil
}

func drawingsREADME(m *projectDrawingsManifest) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# %s\n\n版本：%s · 板尺寸：%.3f × %.3f mm\n\n", drawingsMarkdown(m.Title), drawingsMarkdown(m.Revision), m.DimensionsMM.Width, m.DimensionsMM.Height)
	fmt.Fprintf(&out, "状态（输入声明）：布局 `%s`，布线 `%s`，DRC `%s`，设计验收 `%t`。图册生成成功只证明文件与关联检查完成，不替代电路、布局、制造或硬件验收。\n\n", m.Status.Layout, m.Status.Routing, drawingsBoolText(m.Status.DRCPassed), *m.Status.DesignAccepted)
	fmt.Fprintf(&out, "PCB 复核（输入声明）：网名密度 `%s`，文字碰撞 `%s`，铜几何 `%s`。三项独立于连通性与 DRC；打包器不检测宿主网名或文字重叠。\n\n", m.Status.PCBReview.LabelDensity, m.Status.PCBReview.TextCollisions, m.Status.PCBReview.CopperGeometry)
	fmt.Fprintf(&out, "工程 `%s`；原理图页 `%s`；PCB `%s`。仅包含这一页原理图和这一块 PCB。\n\n", drawingsMarkdown(m.ProjectUUID), drawingsMarkdown(m.SchematicDocumentUUID), drawingsMarkdown(m.PCBDocumentUUID))
	if len(m.Status.Unresolved) > 0 {
		out.WriteString("待解决：\n\n")
		for _, issue := range m.Status.Unresolved {
			fmt.Fprintf(&out, "- %s\n", drawingsMarkdown(issue))
		}
		out.WriteByte('\n')
	}
	for _, role := range []string{"schematic-svg", "pcb-actual-top-svg", "pcb-actual-bottom-svg", "pcb-official-top-png", "pcb-official-bottom-png"} {
		r := projectDrawingRoles[role]
		fmt.Fprintf(&out, "**%s**\n\n![%s](%s)\n\n", r.caption, r.caption, r.name)
	}
	out.WriteString("其他格式：[原理图 PNG](schematic.png)")
	for _, a := range m.Artifacts {
		if a.Role == "schematic-pdf" {
			out.WriteString(" · [原理图 PDF](schematic.pdf)")
		}
	}
	out.WriteString(" · [文件哈希与关联记录](manifest.json)\n\n")
	declared := false
	for _, a := range m.Artifacts {
		if a.Binding == "declared-readback-association" {
			declared = true
		}
		if a.SourceKind == "actual-readback-render" && len(a.Limitations) > 0 {
			fmt.Fprintf(&out, "- %s：%d 条渲染限制（含缺测及绘制边界），逐项保留于 [manifest](manifest.json) 的对应图项。\n", a.Role, len(a.Limitations))
		}
	}
	if declared {
		out.WriteString("\n部分官方图未提供身份回执，与回读版本的关联来自输入声明；具体文件见 manifest 的 binding。\n")
	}
	out.WriteString("\n官方图保持原字节。实际回读图按读取的几何与文字位置生成；底视以板框中心镜像。缺测项和图像身份限制单独保留，图册不补签验收。\n")
	return out.String()
}

func drawingsMarkdown(value string) string {
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " ")
	return strings.NewReplacer("\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "<", "&lt;", ">", "&gt;").Replace(value)
}
func drawingsBoolText(value *bool) string {
	if value == nil {
		return "unknown"
	}
	return strconv.FormatBool(*value)
}
