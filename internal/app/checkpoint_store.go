package app

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Checkpoints keep the source of one schematic page as it was immediately
// before a write command, so that command and everything after it can be
// rolled back. One append-only index per page; the first entry of a chain is a
// complete gzip, later entries store only what changed against the previous
// one. Nothing here talks to the editor.

const (
	envCheckpoint      = "EASYEDA_CHECKPOINT"       // "off" disables automatic capture
	envCheckpointDir   = "EASYEDA_CHECKPOINT_DIR"   // storage root override
	envCheckpointLabel = "EASYEDA_CHECKPOINT_LABEL" // opaque caller id stored with the entry

	checkpointKeep     = 300 // entries kept per page
	checkpointMaxDepth = 16  // deltas between two complete copies
)

type checkpointEntry struct {
	SchemaVersion int       `json:"schemaVersion"`
	ID            string    `json:"id"`
	At            time.Time `json:"at"`
	Project       string    `json:"project"`
	Document      string    `json:"document"`
	Command       string    `json:"command,omitempty"`
	Action        string    `json:"action,omitempty"`
	Label         string    `json:"label,omitempty"`
	// Kind is "full" (complete gzip), "delta" (changes against Base) or "same"
	// (the page had not changed since Base; no blob).
	Kind         string `json:"kind"`
	Base         string `json:"base,omitempty"`
	Depth        int    `json:"depth"`
	BodySHA256   string `json:"bodySha256"`
	SourceSHA256 string `json:"sourceSha256,omitempty"`
	Bytes        int    `json:"bytes"`
	Stored       int    `json:"stored"`
}

type checkpointStore struct{ dir string }

func defaultCheckpointDir() string {
	if dir := os.Getenv(envCheckpointDir); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".easyeda-agent", "checkpoints")
}

var checkpointUUID = regexp.MustCompile(`^[0-9A-Za-z_-]{1,64}$`)

// openCheckpointStore refuses identifiers that could escape the storage root.
func openCheckpointStore(root, project, document string) (*checkpointStore, error) {
	if !checkpointUUID.MatchString(project) || !checkpointUUID.MatchString(document) {
		return nil, fmt.Errorf("checkpoint target is not a plain project/document id")
	}
	return &checkpointStore{dir: filepath.Join(root, project, document)}, nil
}

func (s *checkpointStore) blob(e checkpointEntry) string {
	return filepath.Join(s.dir, e.ID+"."+e.Kind+".gz")
}

func (s *checkpointStore) entries() ([]checkpointEntry, error) {
	f, err := os.Open(filepath.Join(s.dir, "index.jsonl"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []checkpointEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var e checkpointEntry
		// A torn last line from a crashed writer is skipped, never guessed at.
		if json.Unmarshal(scanner.Bytes(), &e) == nil && e.SchemaVersion == 1 && e.ID != "" {
			out = append(out, e)
		}
	}
	return out, scanner.Err()
}

// sourceBody is the page without its first line. The editor regenerates three
// DOCHEAD values on every read, so only the body identifies a page state.
func sourceBody(source string) string {
	if i := strings.IndexByte(source, '\n'); i >= 0 {
		return source[i+1:]
	}
	return ""
}

func gzipBytes(data []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(data)
	_ = w.Close()
	return buf.Bytes()
}

func gunzipFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	// A reconstructed page is bounded like a freshly read one.
	data, err := io.ReadAll(io.LimitReader(r, 8*documentSourceLimit+1))
	if err == nil && len(data) > 8*documentSourceLimit {
		err = fmt.Errorf("checkpoint blob is larger than any page this tool reads")
	}
	return data, err
}

// The ticket is a per-page running number: deleting one record renumbers every
// record after it. Matching lines with the ticket masked keeps such a shift
// from turning the whole page into literal text.
var checkpointTicket = regexp.MustCompile(`^(\{"type":"[^"]*","ticket":)([0-9]+)`)

func maskTicket(line string) (masked, ticket string) {
	m := checkpointTicket.FindStringSubmatchIndex(line)
	if m == nil {
		return line, ""
	}
	return line[:m[3]] + line[m[5]:], line[m[4]:m[5]]
}

// Delta ops, applied in order to rebuild the target's lines:
// ["c", start, count] copies base lines; ["t", index, ticket] copies one base
// line with a new ticket; ["l", text] is a literal line.
type checkpointDelta struct {
	V   int     `json:"v"`
	Ops [][]any `json:"ops"`
}

func makeDelta(base, target string) checkpointDelta {
	baseLines, targetLines := strings.Split(base, "\n"), strings.Split(target, "\n")
	unused := map[string][]int{}
	for i, line := range baseLines {
		key, _ := maskTicket(line)
		unused[key] = append(unused[key], i)
	}
	delta := checkpointDelta{V: 1}
	for _, line := range targetLines {
		key, ticket := maskTicket(line)
		queue := unused[key]
		if len(queue) == 0 {
			delta.Ops = append(delta.Ops, []any{"l", line})
			continue
		}
		j := queue[0]
		unused[key] = queue[1:]
		if baseLines[j] != line {
			delta.Ops = append(delta.Ops, []any{"t", j, ticket})
			continue
		}
		if n := len(delta.Ops); n > 0 {
			if last := delta.Ops[n-1]; last[0] == "c" && last[1].(int)+last[2].(int) == j {
				last[2] = last[2].(int) + 1
				continue
			}
		}
		delta.Ops = append(delta.Ops, []any{"c", j, 1})
	}
	return delta
}

func applyDelta(base string, delta checkpointDelta) (string, error) {
	if delta.V != 1 {
		return "", fmt.Errorf("unsupported checkpoint delta version %d", delta.V)
	}
	baseLines := strings.Split(base, "\n")
	index := func(v any) (int, bool) {
		var n int
		switch x := v.(type) {
		case int:
			n = x
		case float64:
			n = int(x)
			if float64(n) != x {
				return 0, false
			}
		default:
			return 0, false
		}
		return n, n >= 0 && n <= len(baseLines)
	}
	var out []string
	for _, op := range delta.Ops {
		bad := fmt.Errorf("malformed checkpoint delta op %v", op)
		if len(op) < 2 {
			return "", bad
		}
		switch op[0] {
		case "l":
			text, ok := op[1].(string)
			if !ok || len(op) != 2 {
				return "", bad
			}
			out = append(out, text)
		case "c":
			if len(op) != 3 {
				return "", bad
			}
			start, ok1 := index(op[1])
			count, ok2 := index(op[2])
			if !ok1 || !ok2 || start+count > len(baseLines) {
				return "", bad
			}
			out = append(out, baseLines[start:start+count]...)
		case "t":
			if len(op) != 3 {
				return "", bad
			}
			j, ok := index(op[1])
			ticket, isString := op[2].(string)
			if !ok || j >= len(baseLines) || !isString {
				return "", bad
			}
			if _, err := strconv.ParseUint(ticket, 10, 64); err != nil {
				return "", bad
			}
			m := checkpointTicket.FindStringSubmatchIndex(baseLines[j])
			if m == nil {
				return "", bad
			}
			out = append(out, baseLines[j][:m[3]]+ticket+baseLines[j][m[5]:])
		default:
			return "", bad
		}
	}
	return strings.Join(out, "\n"), nil
}

// source rebuilds the page text an entry stands for and proves it against the
// recorded hashes; a damaged chain is an error, never a best guess.
func (s *checkpointStore) source(entries []checkpointEntry, id string) (string, error) {
	byID := map[string]checkpointEntry{}
	for _, e := range entries {
		byID[e.ID] = e
	}
	var build func(id string, hops int) (string, error)
	build = func(id string, hops int) (string, error) {
		e, ok := byID[id]
		if !ok {
			return "", fmt.Errorf("checkpoint %s is not in this page's history (pruned or never recorded)", id)
		}
		if hops > len(entries) {
			return "", fmt.Errorf("checkpoint chain for %s loops", id)
		}
		var source string
		switch e.Kind {
		case "full":
			data, err := gunzipFile(s.blob(e))
			if err != nil {
				return "", fmt.Errorf("checkpoint %s: %w", id, err)
			}
			source = string(data)
		case "delta":
			base, err := build(e.Base, hops+1)
			if err != nil {
				return "", err
			}
			data, err := gunzipFile(s.blob(e))
			if err != nil {
				return "", fmt.Errorf("checkpoint %s: %w", id, err)
			}
			var delta checkpointDelta
			if err = json.Unmarshal(data, &delta); err != nil {
				return "", fmt.Errorf("checkpoint %s: %w", id, err)
			}
			if source, err = applyDelta(base, delta); err != nil {
				return "", fmt.Errorf("checkpoint %s: %w", id, err)
			}
		case "same":
			return build(e.Base, hops+1)
		default:
			return "", fmt.Errorf("checkpoint %s has unknown kind %q", id, e.Kind)
		}
		if sourceSHA256(source) != e.SourceSHA256 {
			return "", fmt.Errorf("checkpoint %s does not match its recorded SHA-256; the stored history is damaged", id)
		}
		return source, nil
	}
	source, err := build(id, 0)
	if err == nil && sourceSHA256(sourceBody(source)) != byID[id].BodySHA256 {
		err = fmt.Errorf("checkpoint %s does not match its recorded body SHA-256", id)
	}
	return source, err
}

// add records source as the page state before a write. The entry is appended
// in one write; blobs are complete before the index names them.
func (s *checkpointStore) add(e checkpointEntry, source string) (checkpointEntry, error) {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return e, err
	}
	entries, err := s.entries()
	if err != nil {
		return e, err
	}
	e.SchemaVersion, e.Bytes = 1, len(source)
	e.BodySHA256, e.SourceSHA256 = sourceSHA256(sourceBody(source)), sourceSHA256(source)
	if e.ID == "" {
		e.ID = fmt.Sprintf("%s-%04x", time.Now().UTC().Format("20060102T150405.000000000"), os.Getpid()&0xffff)
		e.ID = strings.Replace(e.ID, ".", "", 1)
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	full := gzipBytes([]byte(source))
	blob := full
	e.Kind, e.Base, e.Depth = "full", "", 0
	if n := len(entries); n > 0 {
		last := entries[n-1]
		if last.BodySHA256 == e.BodySHA256 {
			e.Kind, e.Base, e.Depth, e.SourceSHA256, blob = "same", last.ID, last.Depth, "", nil
			if last.Kind == "same" {
				e.Base = last.Base
			}
		} else if last.Depth < checkpointMaxDepth {
			// A chain that no longer rebuilds is not extended; start a new one.
			if base, baseErr := s.source(entries, last.ID); baseErr == nil {
				delta := makeDelta(base, source)
				data, _ := json.Marshal(delta)
				rebuilt, applyErr := applyDelta(base, delta)
				if packed := gzipBytes(data); applyErr == nil && rebuilt == source && len(packed)*10 < len(full)*6 {
					e.Kind, e.Base, e.Depth, blob = "delta", last.ID, last.Depth+1, packed
				}
			}
		}
	}
	e.Stored = len(blob)
	if blob != nil {
		if err = writeFileAtomic(s.blob(e), blob); err != nil {
			return e, err
		}
	}
	line, err := json.Marshal(e)
	if err != nil {
		return e, err
	}
	f, err := os.OpenFile(filepath.Join(s.dir, "index.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return e, err
	}
	if _, err = f.Write(append(line, '\n')); err != nil {
		f.Close()
		return e, err
	}
	if err = f.Close(); err != nil {
		return e, err
	}
	s.prune(append(entries, e))
	return e, nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// prune drops the oldest entries once the page keeps more than checkpointKeep,
// cutting only at a complete copy so every kept entry still rebuilds. Failure
// leaves the longer history in place.
func (s *checkpointStore) prune(entries []checkpointEntry) {
	cut := -1
	for i := len(entries) - checkpointKeep; i >= 0 && i < len(entries); i++ {
		if entries[i].Kind == "full" {
			cut = i
			break
		}
	}
	if cut <= 0 {
		return
	}
	var index bytes.Buffer
	for _, e := range entries[cut:] {
		line, err := json.Marshal(e)
		if err != nil {
			return
		}
		index.Write(append(line, '\n'))
	}
	if writeFileAtomic(filepath.Join(s.dir, "index.jsonl"), index.Bytes()) != nil {
		return
	}
	for _, e := range entries[:cut] {
		if e.Kind != "same" {
			_ = os.Remove(s.blob(e))
		}
	}
}
