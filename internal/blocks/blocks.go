// Package blocks embeds the standard circuit-block library (电路块库) into the
// binary so `easyeda blocks ls/show/search` works anywhere the CLI is installed —
// no skill files, no GitHub checkout. The block JSONs are the community
// source-of-truth under skills/easyeda-agent/references/blocks/; the Makefile
// syncs them into data/ before build (go:embed cannot reach across `..`).
package blocks

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// data holds every block JSON. A directory embed excludes `_`-prefixed files, so
// _schema.json (shared doc/schema, not a block) is left out automatically.
//
//go:embed data
var data embed.FS

// Block is the queryable projection of a circuit block. The full original JSON
// is kept in Raw for `show` so nothing is lost to the struct shape.
type Block struct {
	ID           string          `json:"id"`
	Desc         string          `json:"desc"`
	Category     string          `json:"category"`
	Author       string          `json:"author"`
	Contributors []string        `json:"contributors"`
	Added        string          `json:"added"`
	Updated      string          `json:"updated"`
	Source       string          `json:"source"`
	Validated    *string         `json:"validated"` // nil/null → draft, else ready
	Parts        map[string]any  `json:"parts"`
	Ports        map[string]any  `json:"ports"`
	Raw          json.RawMessage `json:"-"`
}

// Ready reports whether the block passed full-flow validation (validated != null).
func (b Block) Ready() bool { return b.Validated != nil && strings.TrimSpace(*b.Validated) != "" }

// Load parses every embedded block, sorted by id. It never touches the disk, so
// it works from a bare binary.
func Load() ([]Block, error) {
	entries, err := fs.ReadDir(data, "data")
	if err != nil {
		return nil, err
	}
	var out []Block
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, "_") {
			continue
		}
		raw, err := data.ReadFile("data/" + name)
		if err != nil {
			return nil, err
		}
		var b Block
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, fmt.Errorf("block %s: %w", name, err)
		}
		b.Raw = raw
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Get returns the block whose id matches exactly, with or without the `block.`
// prefix (both `block.xl1509_buck_12v_5v` and `xl1509_buck_12v_5v` resolve).
func Get(id string) (Block, bool, error) {
	all, err := Load()
	if err != nil {
		return Block{}, false, err
	}
	want := strings.TrimPrefix(id, "block.")
	for _, b := range all {
		if b.ID == id || strings.TrimPrefix(b.ID, "block.") == want {
			return b, true, nil
		}
	}
	return Block{}, false, nil
}

// Search returns blocks whose id/desc/category/ports/parts contain the query
// (case-insensitive), so an agent can find "the buck block" without knowing its id.
func Search(query string) ([]Block, error) {
	all, err := Load()
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return all, nil
	}
	var out []Block
	for _, b := range all {
		var sb strings.Builder
		sb.WriteString(b.ID)
		sb.WriteByte(' ')
		sb.WriteString(b.Desc)
		sb.WriteByte(' ')
		sb.WriteString(b.Category)
		for k := range b.Ports {
			sb.WriteByte(' ')
			sb.WriteString(k)
		}
		for k := range b.Parts {
			sb.WriteByte(' ')
			sb.WriteString(k)
		}
		if strings.Contains(strings.ToLower(sb.String()), q) {
			out = append(out, b)
		}
	}
	return out, nil
}
