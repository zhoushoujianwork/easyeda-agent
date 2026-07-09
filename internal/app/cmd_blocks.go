package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/blocks"
)

// newBlocksCmd returns the "blocks" subcommand group — offline query of the
// embedded circuit-block library (电路块库). No daemon, no window, no skill
// files: the block data rides inside the binary (go:embed), so an agent running
// anywhere `easyeda` is installed can look up a known-good peripheral subcircuit
// without a GitHub checkout.
func newBlocksCmd(stdout, stderr io.Writer) *cobra.Command {
	b := &cobra.Command{
		Use:   "blocks",
		Short: "Query the embedded circuit-block library (电路块库) — offline, no daemon",
		Long: `Query the standard circuit-block library — proven peripheral subcircuits
(CH340 USB-serial, ESP32 auto-download, XL1509 buck, SP3485 RS-485, CC1101 RF,
microSD…) whose internal topology is fixed and can be copied verbatim; only the
boundary nets (ports) get rebound to the host design.

The library is EMBEDDED in the binary, so these commands need no daemon, no open
EasyEDA window, and no skill files — a bare 'easyeda' install can look blocks up.`,
	}
	b.AddCommand(
		newBlocksLsCmd(stdout, stderr),
		newBlocksShowCmd(stdout, stderr),
		newBlocksSearchCmd(stdout, stderr),
	)
	return b
}

func printBlocksTable(w io.Writer, list []blocks.Block) {
	fmt.Fprintf(w, "%-32s %-8s %-12s %-18s %s\n", "BLOCK", "STATUS", "CATEGORY", "AUTHOR", "DESC")
	ready := 0
	for _, blk := range list {
		status := "draft"
		if blk.Ready() {
			status = "ready"
			ready++
		}
		fmt.Fprintf(w, "%-32s %-8s %-12s %-18s %s\n", blk.ID, status, blk.Category, blk.Author, blk.Desc)
	}
	fmt.Fprintf(w, "\n%d block(s): %d ready, %d draft. `easyeda blocks show <id>` for detail.\n",
		len(list), ready, len(list)-ready)
}

func newBlocksLsCmd(stdout, stderr io.Writer) *cobra.Command {
	var category string
	var asJSON, readyOnly, draftOnly bool
	c := &cobra.Command{
		Use:   "ls",
		Short: "List blocks (optionally filter by --category / --ready / --draft)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, err := blocks.Load()
			if err != nil {
				return err
			}
			var list []blocks.Block
			for _, b := range all {
				if category != "" && b.Category != category {
					continue
				}
				if readyOnly && !b.Ready() {
					continue
				}
				if draftOnly && b.Ready() {
					continue
				}
				list = append(list, b)
			}
			if asJSON {
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(list)
			}
			printBlocksTable(stdout, list)
			return nil
		},
	}
	c.Flags().StringVar(&category, "category", "", "filter by category (power|comms|rf|mcu|…)")
	c.Flags().BoolVar(&asJSON, "json", false, "output the block list as JSON")
	c.Flags().BoolVar(&readyOnly, "ready", false, "only validated (ready) blocks")
	c.Flags().BoolVar(&draftOnly, "draft", false, "only unvalidated (draft) blocks")
	return c
}

func newBlocksShowCmd(stdout, stderr io.Writer) *cobra.Command {
	c := &cobra.Command{
		Use:   "show <block.id>",
		Short: "Print a block's full JSON (parts, internal_nets, ports, notes)",
		Args:  cobra.ExactArgs(1),
		Example: `  easyeda blocks show block.xl1509_buck_12v_5v
  easyeda blocks show cc1101_433m_balun_ipex   # block. prefix optional`,
		RunE: func(cmd *cobra.Command, args []string) error {
			blk, ok, err := blocks.Get(args[0])
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("no block %q — run `easyeda blocks ls`", args[0])
			}
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, blk.Raw, "", "  "); err != nil {
				return err
			}
			fmt.Fprintln(stdout, pretty.String())
			return nil
		},
	}
	return c
}

func newBlocksSearchCmd(stdout, stderr io.Writer) *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:     "search <query>",
		Short:   "Find blocks by id/desc/category/port/part (case-insensitive)",
		Args:    cobra.ExactArgs(1),
		Example: `  easyeda blocks search rs485` + "\n  easyeda blocks search buck",
		RunE: func(cmd *cobra.Command, args []string) error {
			list, err := blocks.Search(args[0])
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(list)
			}
			if len(list) == 0 {
				fmt.Fprintf(stdout, "no block matches %q\n", args[0])
				return nil
			}
			printBlocksTable(stdout, list)
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "output matches as JSON")
	return c
}
