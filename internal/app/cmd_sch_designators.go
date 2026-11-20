package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

func newSchDesignatorsCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{Use: "designators", Short: "Allocate official reference numbers and repair placed component bindings"}
	var prefixesPath, outPath, changesPath string
	allocate := &cobra.Command{
		Use: "allocate <full-project-connectivity.json>", Short: "Allocate missing numeric references from measured library prefixes (offline)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := schDesignatorsDistinctFiles(args[0], prefixesPath, outPath, changesPath); err != nil {
				return err
			}
			var source connectivity.Document
			if err := schDesignatorsReadJSON(args[0], &source); err != nil {
				return err
			}
			prefixes := map[string]string{}
			if prefixesPath != "" {
				if err := schDesignatorsReadJSON(prefixesPath, &prefixes); err != nil {
					return err
				}
			}
			target, changes, err := connectivity.AllocateDesignators(source, prefixes)
			if err != nil {
				return err
			}
			if changesPath != "" {
				if changes == nil {
					changes = []connectivity.DesignatorChange{}
				}
				if err := schDesignatorsWriteJSON(changesPath, changes, stdout); err != nil {
					return err
				}
			}
			return schDesignatorsWriteJSON(outPath, target, stdout)
		},
	}
	allocate.Flags().StringVar(&prefixesPath, "prefixes", "", "JSON map of libraryUuid/deviceUuid to official designator prefix (U or U?)")
	allocate.Flags().StringVar(&outPath, "out", "", "allocated connectivity JSON (default: stdout)")
	allocate.Flags().StringVar(&changesPath, "changes", "", "write ordered reference changes to this JSON file")
	root.AddCommand(allocate)
	root.AddCommand(newSchDesignatorsPlanCmd(stdout))
	root.AddCommand(newSchDesignatorsBindCmd(stdout))
	root.AddCommand(newSchDesignatorsVerifyCmd(cfg, window, stdout))
	return root
}

// Protect input and companion output files even when callers use ./ aliases,
// hard links or symlinks. This check happens before the first output write.
func schDesignatorsDistinctFiles(paths ...string) error {
	type file struct {
		path, abs string
		info      os.FileInfo
	}
	var files []file
	for _, path := range paths {
		if path == "" {
			continue
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		} else if parent, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
			abs = filepath.Join(parent, filepath.Base(abs))
		}
		info, err := os.Stat(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		for _, prev := range files {
			if prev.abs == abs || (prev.info != nil && info != nil && os.SameFile(prev.info, info)) {
				return fmt.Errorf("input and output files must be distinct: %s and %s refer to the same file", prev.path, path)
			}
		}
		files = append(files, file{path: path, abs: abs, info: info})
	}
	return nil
}

func schDesignatorsReadJSON(path string, out any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func schDesignatorsWriteJSON(path string, v any, stdout io.Writer) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if path == "" {
		_, err = stdout.Write(raw)
		return err
	}
	return os.WriteFile(path, raw, 0600)
}
