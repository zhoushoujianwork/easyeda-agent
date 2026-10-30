package app

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

type manufactureEntry struct {
	Name   string `json:"name"`
	Size   int    `json:"size"`
	SHA256 string `json:"sha256"`
	Kind   string `json:"kind,omitempty"`
}

func readManufactureZip(data []byte) (map[string][]byte, []manufactureEntry, error) {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, fmt.Errorf("invalid manufacture ZIP: %w", err)
	}
	if len(z.File) == 0 || len(z.File) > 4096 {
		return nil, nil, fmt.Errorf("manufacture ZIP needs 1..4096 entries")
	}
	files := map[string][]byte{}
	seen := map[string]bool{}
	var entries []manufactureEntry
	var total uint64
	for _, f := range z.File {
		clean := path.Clean(f.Name)
		if strings.ContainsAny(f.Name, "\\:\x00") || strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") || clean == "." || f.Mode()&os.ModeSymlink != 0 || seen[clean] {
			return nil, nil, fmt.Errorf("unsafe or duplicate manufacture ZIP path %q", f.Name)
		}
		seen[clean] = true
		if f.FileInfo().IsDir() {
			continue
		}
		total += f.UncompressedSize64
		if f.UncompressedSize64 > 100<<20 || total > 100<<20 {
			return nil, nil, fmt.Errorf("manufacture ZIP expands beyond 100 MiB")
		}
		r, err := f.Open()
		if err != nil {
			return nil, nil, err
		}
		b, err := io.ReadAll(io.LimitReader(r, (100<<20)+1))
		r.Close()
		if err != nil {
			return nil, nil, err
		}
		if uint64(len(b)) != f.UncompressedSize64 {
			return nil, nil, fmt.Errorf("manufacture ZIP size mismatch")
		}
		files[clean] = b
		entries = append(entries, manufactureEntry{Name: clean, Size: len(b), SHA256: sha256Hex(b)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return files, entries, nil
}

func inspectGerberArchive(data []byte) ([]manufactureEntry, error) {
	files, entries, err := readManufactureZip(data)
	if err != nil {
		return nil, err
	}
	gerbers := 0
	for i := range entries {
		name, b := strings.ToLower(entries[i].Name), files[entries[i].Name]
		body := strings.ToUpper(string(b))
		ext := strings.ToLower(path.Ext(name))
		if strings.Contains(body, "%FS") && strings.Contains(body, "%MO") && strings.Contains(body, "M02*") {
			entries[i].Kind = "gerber"
			switch ext {
			case ".gtl", ".gbl":
				entries[i].Kind = "copper"
			case ".gts", ".gbs":
				entries[i].Kind = "solder-mask"
			case ".gto", ".gbo":
				entries[i].Kind = "silkscreen"
			case ".gko", ".gm1":
				entries[i].Kind = "outline"
			case ".gtp", ".gbp":
				entries[i].Kind = "paste"
			}
			gerbers++
		} else if strings.Contains(body, "M48") && strings.Contains(body, "M30") {
			entries[i].Kind = "drill"
		} else {
			switch ext {
			case ".gtl", ".gbl", ".gts", ".gbs", ".gto", ".gbo", ".gko", ".gbr", ".drl":
				return nil, fmt.Errorf("manufacture entry %s lacks expected Gerber/Excellon structure", entries[i].Name)
			}
		}
	}
	if gerbers == 0 {
		return nil, fmt.Errorf("manufacture ZIP contains no recognizable Gerber files")
	}
	return entries, nil
}

type manufacturePlacement struct {
	Designator string  `json:"designator"`
	MidX       float64 `json:"midX"`
	MidY       float64 `json:"midY"`
	Layer      string  `json:"layer"`
	Rotation   float64 `json:"rotation"`
	Sheet      string  `json:"sheet,omitempty"`
}

func placementHeader(value string) string {
	s := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(value), "\ufeff"))
	s = strings.NewReplacer(" ", "", "_", "", "-", "", "(", "", ")", "", "[", "", "]", "", "°", "").Replace(s)
	for _, suffix := range []string{"mm", "mil", "deg", "degrees"} {
		s = strings.TrimSuffix(s, suffix)
	}
	switch s {
	case "designator", "reference", "refdes", "位号":
		return "designator"
	case "midx", "centerx", "x", "中心x", "坐标x":
		return "x"
	case "midy", "centery", "y", "中心y", "坐标y":
		return "y"
	case "layer", "side", "层", "层名":
		return "layer"
	case "rotation", "rotate", "angle", "角度", "旋转角度":
		return "rotation"
	}
	return ""
}

func placementTable(rows [][]string, unit, sheet string) ([]manufacturePlacement, error) {
	if len(rows) > 100000 {
		return nil, fmt.Errorf("placement table exceeds 100000 rows")
	}
	var cols map[string]int
	header := -1
	for i, row := range rows {
		if i >= 50 {
			break
		}
		candidate := map[string]int{}
		duplicate := false
		for j, v := range row {
			if k := placementHeader(v); k != "" {
				if _, ok := candidate[k]; ok {
					duplicate = true
				}
				candidate[k] = j
			}
		}
		if len(candidate) == 5 && !duplicate {
			cols, header = candidate, i
			break
		}
	}
	if header < 0 {
		return nil, fmt.Errorf("placement table needs Designator, Mid X, Mid Y, Layer and Rotation columns")
	}
	for _, key := range []string{"x", "y"} {
		h := strings.ToLower(rows[header][cols[key]])
		if (strings.Contains(h, "mm") && unit != "mm") || (strings.Contains(h, "mil") && unit != "mil") {
			return nil, fmt.Errorf("placement header unit differs from requested %s", unit)
		}
	}
	number := func(v, suffix string) (float64, error) {
		v = strings.TrimSpace(v)
		v = strings.TrimSpace(strings.TrimSuffix(strings.ToLower(v), suffix))
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return 0, fmt.Errorf("invalid placement number %q", v)
		}
		return n, nil
	}
	var result []manufacturePlacement
	seen := map[string]bool{}
	for i, row := range rows[header+1:] {
		empty := true
		for _, v := range row {
			empty = empty && strings.TrimSpace(v) == ""
		}
		if empty {
			continue
		}
		values := map[string]string{}
		for key, col := range cols {
			if col >= len(row) {
				return nil, fmt.Errorf("placement row %d is incomplete", i+header+2)
			}
			values[key] = strings.TrimSpace(row[col])
		}
		if values["designator"] == "" || values["layer"] == "" || seen[values["designator"]] {
			return nil, fmt.Errorf("empty/duplicate designator or missing layer in placement row %d", i+header+2)
		}
		x, err := number(values["x"], unit)
		if err != nil {
			return nil, err
		}
		y, err := number(values["y"], unit)
		if err != nil {
			return nil, err
		}
		rotation, err := number(strings.TrimSuffix(values["rotation"], "°"), "deg")
		if err != nil {
			return nil, err
		}
		result = append(result, manufacturePlacement{Designator: values["designator"], MidX: x, MidY: y, Layer: values["layer"], Rotation: rotation, Sheet: sheet})
		seen[values["designator"]] = true
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("placement file has no component rows")
	}
	return result, nil
}

func inspectPlacement(data []byte, format, unit string) ([]manufacturePlacement, error) {
	if format == "csv" {
		var last error
		for _, delimiter := range []rune{',', '\t', ';'} {
			r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
			r.Comma = delimiter
			r.FieldsPerRecord = -1
			rows, err := r.ReadAll()
			if err != nil {
				last = err
				continue
			}
			if result, err := placementTable(rows, unit, ""); err == nil {
				return result, nil
			} else {
				last = err
			}
		}
		return nil, fmt.Errorf("invalid placement CSV: %w", last)
	}
	if format != "xlsx" {
		return nil, fmt.Errorf("unsupported placement format")
	}
	files, _, err := readManufactureZip(data)
	if err != nil {
		return nil, err
	}
	if files["[Content_Types].xml"] == nil || files["xl/workbook.xml"] == nil {
		return nil, fmt.Errorf("placement XLSX lacks workbook metadata")
	}
	type richText struct {
		Text string `xml:"t"`
		Runs []struct {
			Text string `xml:"t"`
		} `xml:"r"`
	}
	text := func(v richText) string {
		s := v.Text
		for _, run := range v.Runs {
			s += run.Text
		}
		return s
	}
	var shared struct {
		Items []richText `xml:"si"`
	}
	if b := files["xl/sharedStrings.xml"]; b != nil {
		if err := xml.Unmarshal(b, &shared); err != nil {
			return nil, err
		}
	}
	var names []string
	for name := range files {
		if strings.HasPrefix(name, "xl/worksheets/") && strings.HasSuffix(name, ".xml") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var result []manufacturePlacement
	for _, name := range names {
		var doc struct {
			Rows []struct {
				Cells []struct {
					Ref     string   `xml:"r,attr"`
					Type    string   `xml:"t,attr"`
					Value   string   `xml:"v"`
					Inline  richText `xml:"is"`
					Formula *string  `xml:"f"`
				} `xml:"c"`
			} `xml:"sheetData>row"`
		}
		if err := xml.Unmarshal(files[name], &doc); err != nil {
			return nil, err
		}
		if len(doc.Rows) == 0 {
			continue
		}
		var rows [][]string
		for _, row := range doc.Rows {
			values := []string{}
			for _, cell := range row.Cells {
				if cell.Formula != nil {
					return nil, fmt.Errorf("placement XLSX formulas are unsupported")
				}
				col := 0
				for _, ch := range cell.Ref {
					if ch < 'A' || ch > 'Z' {
						break
					}
					col = col*26 + int(ch-'A'+1)
					if col > 256 {
						return nil, fmt.Errorf("placement XLSX exceeds 256 columns")
					}
				}
				if col < 1 {
					return nil, fmt.Errorf("invalid placement cell reference")
				}
				for len(values) < col {
					values = append(values, "")
				}
				value := cell.Value
				switch cell.Type {
				case "s":
					i, err := strconv.Atoi(value)
					if err != nil || i < 0 || i >= len(shared.Items) {
						return nil, fmt.Errorf("invalid XLSX shared string")
					}
					value = text(shared.Items[i])
				case "inlineStr":
					value = text(cell.Inline)
				case "", "n", "str":
				default:
					return nil, fmt.Errorf("unsupported placement XLSX cell type %q", cell.Type)
				}
				values[col-1] = value
			}
			rows = append(rows, values)
		}
		placements, err := placementTable(rows, unit, name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		result = append(result, placements...)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("placement XLSX has no component rows")
	}
	return result, nil
}
