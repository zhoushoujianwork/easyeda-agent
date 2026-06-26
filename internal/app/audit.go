package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// newAuditCmd returns the "audit" subcommand group.
func newAuditCmd(stdout, stderr io.Writer) *cobra.Command {
	audit := &cobra.Command{
		Use:   "audit",
		Short: "Inspect the action audit log",
	}
	audit.AddCommand(newAuditTailCmd(stdout, stderr))
	return audit
}

// newAuditTailCmd returns "audit tail [-n N] [--dir <dir>]".
func newAuditTailCmd(stdout, stderr io.Writer) *cobra.Command {
	var n int
	var dir string

	cmd := &cobra.Command{
		Use:   "tail",
		Short: "Show the most recent dispatches from the JSONL audit log",
		Args:  cobra.NoArgs,
		Example: `  easyeda audit tail
  easyeda audit tail -n 50
  easyeda audit tail --dir /path/to/audit`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dir == "" {
				home, err := os.UserHomeDir()
				if err != nil || home == "" {
					home = os.Getenv("HOME")
				}
				dir = filepath.Join(home, ".easyeda-agent", "audit")
			}

			lines, err := readLastLines(dir, n)
			if err != nil {
				return err
			}
			for _, line := range lines {
				fmt.Fprintln(stdout, line)
			}
			return nil
		},
	}

	cmd.Flags().IntVarP(&n, "lines", "n", 20, "number of lines to show")
	cmd.Flags().StringVar(&dir, "dir", "", "audit log directory (default ~/.easyeda-agent/audit)")
	return cmd
}

// readLastLines walks the audit directory in reverse chronological order
// (newest day first) and accumulates up to n lines from the most recent files.
func readLastLines(dir string, n int) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no audit log directory at %s (run the daemon first)", dir)
		}
		return nil, err
	}
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		files = append(files, e.Name())
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))

	var collected []string
	for _, name := range files {
		if len(collected) >= n {
			break
		}
		path := filepath.Join(dir, name)
		lines, err := readFileLines(path)
		if err != nil {
			return nil, err
		}
		need := n - len(collected)
		if len(lines) > need {
			lines = lines[len(lines)-need:]
		}
		// Older lines first within the file.
		collected = append(lines, collected...)
	}
	return collected, nil
}

func readFileLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	// 1 MiB per line is enough for the largest action result payloads we expect.
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	var out []string
	for scanner.Scan() {
		out = append(out, scanner.Text())
	}
	return out, scanner.Err()
}

// (unused but available for a future `audit since <duration>` subcommand)
var _ = time.Now
