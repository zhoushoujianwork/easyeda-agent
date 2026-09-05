package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// placeTimeout fails `sch place` fast instead of waiting out the full default
// window. A successful placement returns near-instantly; a hang almost always
// means the EasyEDA API never settled on a bad {libraryUuid, uuid} — most often
// because --uuid is a placed-instance id (from `sch list`) rather than a device
// library uuid (from `lib search`). See placeUUIDHint.
const placeTimeout = 8 * time.Second

// rebind/replace run a long SERIAL eda.* chain (identity resolution via online
// library search → lib_Device copy/modify → delete → create → restore); the
// clone fallback pushed the worst case past the default 20s dispatch window.
const rebindTimeout = 90 * time.Second

// placeUUIDHint translates a bare deadline-exceeded into an actionable message:
// the most common cause of a hung placement is replaying an instance uuid that
// `sch list` exposes (component/symbol/footprint/uniqueId) instead of the
// device-library uuid that `lib search` returns.
func placeUUIDHint(timeout time.Duration) error {
	return fmt.Errorf(
		"placement timed out after %s — the EasyEDA API never returned for this {libraryUuid, uuid}.\n"+
			"This usually means --uuid is NOT a device-library uuid. The component/symbol/footprint/uniqueId\n"+
			"fields from `easyeda sch list` are placed-INSTANCE ids and cannot be replayed into `sch place`.\n"+
			"Get a replayable device uuid first: `easyeda lib search --query \"<part>\"` → use its `uuid` + `libraryUuid`.",
		timeout,
	)
}

// netflagKindAliases maps user-friendly CLI shorthands to the canonical kind
// enum the connector (extension/src/actions.ts NET_FLAG_KINDS / NET_PORT_KINDS)
// actually accepts. Canonical names also pass through unchanged so both
// `--kind gnd` and `--kind ground` work. Keep this list in sync with the
// connector's accepted set to avoid CLI↔connector drift.
var netflagKindAliases = map[string]string{
	// shorthands
	"gnd":     "ground",
	"agnd":    "analog_ground",
	"pgnd":    "protective_ground",
	"netport": "net_port_bi", // bidirectional port is the most general default
	// canonical passthrough (connector-native names)
	"power":             "power",
	"ground":            "ground",
	"analog_ground":     "analog_ground",
	"protective_ground": "protective_ground",
	"protect_ground":    "protect_ground",
	"net_port_in":       "net_port_in",
	"net_port_out":      "net_port_out",
	"net_port_bi":       "net_port_bi",
	"netlabel":          "net_label",
	"net_label":         "net_label",
}

// netflagKindHelp is the single source of truth for the --kind help text so the
// listed values stay in sync with what resolveNetflagKind actually accepts.
const netflagKindHelp = "flag kind (required). Shorthands: gnd→ground, agnd→analog_ground, " +
	"pgnd→protective_ground, netport→net_port_bi. Canonical: power, ground, analog_ground, " +
	"protective_ground, protect_ground, net_port_in, net_port_out, net_port_bi, net_label"

// resolveNetflagKind translates a CLI --kind value (shorthand or canonical) to
// the canonical kind the connector accepts. Unknown values get a friendly CLI
// error listing every valid value, instead of leaking the raw connector error.
func resolveNetflagKind(kind string) (string, error) {
	if canonical, ok := netflagKindAliases[kind]; ok {
		return canonical, nil
	}
	valid := []string{
		"gnd", "agnd", "pgnd", "netport",
		"power", "ground", "analog_ground", "protective_ground", "protect_ground",
		"net_port_in", "net_port_out", "net_port_bi", "netlabel", "net_label",
	}
	return "", fmt.Errorf("unknown --kind %q; expected one of: %v", kind, valid)
}

// newSchCmd returns the "sch" subcommand group with all schematic actions.
// --window is a persistent flag on the group so every subcommand inherits it.
func newSchCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	var window string

	sch := &cobra.Command{
		Use:   "sch",
		Short: "Schematic operations",
	}
	sch.PersistentFlags().StringVar(&window, "window", "", "EasyEDA window ID")
	sch.AddCommand(newSchConnectivityCmd(cfg, &window, stdout, stderr))
	sch.AddCommand(newSchConnectivityDiffCmd(stdout))
	sch.AddCommand(newSchPlanCmd(stdout))
	sch.AddCommand(newSchMaterializeCmd(stdout, stderr))
	// `sch apply` is the schematic-domain entry point for the shared, ordered
	// playbook executor. The executor itself remains shared so queue semantics
	// and WebSocket response handling stay identical across domains.
	sch.AddCommand(newApplyCmd(cfg, stdout, stderr))

	// ── pages ────────────────────────────────────────────────────────────
	// schematic.pages.list
	sch.AddCommand(&cobra.Command{
		Use:   "pages",
		Short: "List schematic documents and pages in the current project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cfg, "schematic.pages.list", window, nil, stdout, stderr)
		},
	})

	// ── open ─────────────────────────────────────────────────────────────
	// schematic.page.open
	{
		var page string
		c := &cobra.Command{
			Use:     "open",
			Short:   "Open or activate a schematic page by UUID",
			Args:    cobra.NoArgs,
			Example: `  easyeda sch open --page 6b3a2f01-...`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if page == "" {
					return fmt.Errorf("--page is required")
				}
				return dispatch(cfg, "schematic.page.open", window,
					map[string]any{"schematicPageUuid": page}, stdout, stderr)
			},
		}
		c.Flags().StringVar(&page, "page", "", "schematic page UUID (required)")
		sch.AddCommand(c)
	}

	// ── titleblock get ─────────────────────────────────────────────────────
	// schematic.titleblock.get — 明细表读取（含可编辑字段 key）
	{
		var page string
		c := &cobra.Command{
			Use:   "titleblock-get",
			Short: "Read a page's 明细表 (title block): show flag + field keys/values",
			Args:  cobra.NoArgs,
			Example: `  easyeda sch titleblock-get
  easyeda sch titleblock-get --page <pageUuid>`,
			RunE: func(cmd *cobra.Command, args []string) error {
				var payload map[string]any
				if page != "" {
					payload = map[string]any{"pageUuid": page}
				}
				return dispatch(cfg, "schematic.titleblock.get", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&page, "page", "", "schematic page UUID (default: focused page)")
		sch.AddCommand(c)
	}

	// ── titleblock modify ──────────────────────────────────────────────────
	// schematic.titleblock.modify — 明细表调整（显隐 + 字段值）
	{
		var dataJSON string
		var show, hide bool
		c := &cobra.Command{
			Use:   "titleblock",
			Short: "Adjust the focused page's 明细表 (title block): visibility and/or fields",
			Long: `Adjust the FOCUSED page's 明细表 (title block): visibility and/or field values.

Only the focused page — the official API takes no pageUuid (titleblock-get does,
the two are asymmetric). Switch pages first if you mean another one.

The platform reports success for fields it silently dropped ("无法识别的明细项将被
忽略" yet still returns true), so this command reads the title block back and
compares item by item. Items that did not land come back in result.notApplied and
exit non-zero; items that are not title-block fields at all are named separately
in result.unknownKeys — for those, fix the key, do not retry.

The title block CANNOT set paper size. EasyEDA Pro exposes no set-paper-size API,
and Size / Width / Height / "Page Size" are not title-block items. Run
` + "`easyeda sch titleblock-get`" + ` first to see the keys this page actually has.`,
			Args: cobra.NoArgs,
			Example: `  easyeda sch titleblock --show
  easyeda sch titleblock --hide
  easyeda sch titleblock --data '{"Title":{"value":"电源模块"},"Designer":{"value":"Mika"}}'`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if show && hide {
					return fmt.Errorf("--show and --hide are mutually exclusive")
				}
				payload := map[string]any{}
				if show {
					payload["showTitleBlock"] = true
				}
				if hide {
					payload["showTitleBlock"] = false
				}
				var userPatch map[string]any
				if dataJSON != "" {
					var data map[string]any
					if err := json.Unmarshal([]byte(dataJSON), &data); err != nil {
						return fmt.Errorf("invalid --data json: %w", err)
					}
					userPatch = data
					full, needShow, ferr := schTitleBlockMerge(cfg, window, data)
					if ferr != nil {
						return ferr
					}
					payload["titleBlockData"] = full
					if needShow && !hide {
						payload["showTitleBlock"] = true // 图签还没显示,顺手打开
					}
				}
				if len(payload) == 0 {
					return fmt.Errorf("pass at least one of --show / --hide / --data")
				}
				res, err := dispatchCapture(cfg, "schematic.titleblock.modify", window, payload, stdout)
				// **假失败复核**:连接器按「本次调用改变了什么」判定成败,于是
				// 我们主动按住的结构开关(值本来就对)永远算「没应用」,而重复写
				// 同一个值(幂等重跑/批量重放)会让它判定「一项也没证明写入」并报错
				// —— 实测四页图签内容**全部写对了**,命令却 ok:false。
				// 假失败比假成功更难缠:调用方会去重试、回滚,或认定这条路不通。
				// 判据换成画布的最终状态:用户要的内容在不在图签上。
				if err != nil && userPatch != nil {
					if landed, _ := tbPatchLanded(cfg, window, userPatch); landed {
						fmt.Fprintf(stderr, "note: 平台报写入失败,但回读确认请求的 %d 项内容都已是目标值"+
							"(幂等重写,或我们按住的结构开关本就正确)—— 以画布为准,按成功处理\n", len(userPatch))
						// **必须在这里返回**:dispatchCapture 失败时 res 是 nil,
						// 只把 err 抹掉会让下面读 res.Result 的部分应用检查当场 panic
						// (真机首跑实见)。图纸自检仍要做 —— 内容写对了不代表图框没被写坏。
						return warnIfSheetLost(cfg, window, stderr)
					}
				}
				// **写后自检:图纸边框还在不在**。写图签是「读全量→改几项→整包回传」,
				// 一旦结构开关(Title Block / Border)在回传里被平台按默认值处理,
				// 图框和明细表会被整个关掉 —— 页面看着还在,sheet 图元却没了,
				// 于是 layout-lint 的 sheet-check 变 unavailable、越界判据集体失明,
				// 而这条命令本身可能还报的是别的错(2026-08-15 esp32Mini E2E:四页
				// 图纸被静默弄丢,直到 `sch gate --strict` 才暴露)。损坏必须当场说。
				if sheetErr := warnIfSheetLost(cfg, window, stderr); sheetErr != nil && err == nil {
					return sheetErr
				}
				if err != nil {
					return err
				}
				// 部分应用退出码约定与 `sch modify` 对齐(#151)。明细表这条另有
				// unknownKeys:平台对不认识的明细项静默忽略并回 true,单独点名
				// 让调用方知道该换 key 而不是重试。
				na, _ := res.Result["notApplied"].([]any)
				visOK, hasVis := res.Result["visibilityApplied"].(bool)
				if len(na) > 0 || (hasVis && !visOK) {
					keys := make([]string, 0, len(na)+1)
					for _, k := range na {
						keys = append(keys, fmt.Sprint(k))
					}
					if hasVis && !visOK {
						keys = append(keys, "showTitleBlock")
					}
					msg := fmt.Sprintf("partial apply: title-block items not applied: %s", strings.Join(keys, ", "))
					if uk, _ := res.Result["unknownKeys"].([]any); len(uk) > 0 {
						names := make([]string, 0, len(uk))
						for _, k := range uk {
							names = append(names, fmt.Sprint(k))
						}
						msg += fmt.Sprintf(" — %s are not title-block items on this page (the title block cannot set paper size; run `easyeda sch titleblock-get` for the available keys)", strings.Join(names, ", "))
					}
					return fmt.Errorf("%s", msg)
				}
				return nil
			},
		}
		c.Flags().BoolVar(&show, "show", false, "show the title block")
		c.Flags().BoolVar(&hide, "hide", false, "hide the title block")
		c.Flags().StringVar(&dataJSON, "data", "", `JSON of fields to patch, e.g. '{"Title":{"value":"..."}}'`)
		sch.AddCommand(c)
	}

	// ── page-new ───────────────────────────────────────────────────────────
	// schematic.page.create
	{
		var schUuid string
		c := &cobra.Command{
			Use:     "page-new",
			Short:   "Create a new schematic page under a schematic document",
			Args:    cobra.NoArgs,
			Example: `  easyeda sch page-new --schematic <schematicUuid>`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if schUuid == "" {
					return fmt.Errorf("--schematic is required")
				}
				return dispatch(cfg, "schematic.page.create", window,
					map[string]any{"schematicUuid": schUuid}, stdout, stderr)
			},
		}
		c.Flags().StringVar(&schUuid, "schematic", "", "parent schematic document UUID (required)")
		sch.AddCommand(c)
	}

	// ── page-rename ────────────────────────────────────────────────────────
	// schematic.page.rename
	{
		var page, name string
		c := &cobra.Command{
			Use:     "page-rename",
			Short:   "Rename a schematic page",
			Args:    cobra.NoArgs,
			Example: `  easyeda sch page-rename --page <pageUuid> --name "电源"`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if page == "" {
					return fmt.Errorf("--page is required")
				}
				if name == "" {
					return fmt.Errorf("--name is required")
				}
				return dispatch(cfg, "schematic.page.rename", window,
					map[string]any{"pageUuid": page, "name": name}, stdout, stderr)
			},
		}
		c.Flags().StringVar(&page, "page", "", "schematic page UUID (required)")
		c.Flags().StringVar(&name, "name", "", "new page name (required)")
		sch.AddCommand(c)
	}

	// ── page-delete ────────────────────────────────────────────────────────
	// schematic.page.delete
	{
		var page string
		c := &cobra.Command{
			Use:     "page-delete",
			Short:   "Delete a schematic page (no undo)",
			Args:    cobra.NoArgs,
			Example: `  easyeda sch page-delete --page <pageUuid>`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if page == "" {
					return fmt.Errorf("--page is required")
				}
				return dispatch(cfg, "schematic.page.delete", window,
					map[string]any{"pageUuid": page}, stdout, stderr)
			},
		}
		c.Flags().StringVar(&page, "page", "", "schematic page UUID (required)")
		sch.AddCommand(c)
	}

	// ── clear ──────────────────────────────────────────────────────────────
	// schematic.page.clear
	{
		var noPreserveSheet, dryRun bool
		c := &cobra.Command{
			Use:   "clear",
			Short: "Clear the active schematic page (delete all page primitives: components, flags, wires, buses, graphics)",
			Args:  cobra.NoArgs,
			Example: `  easyeda sch clear                      # clear the page, keep the sheet/title block (图框)
  easyeda sch clear --dry-run            # report what would be deleted, delete nothing
  easyeda sch clear --no-preserve-sheet  # also delete the sheet/title block`,
			RunE: func(cmd *cobra.Command, args []string) error {
				// 清页会把图元全删掉,而虚拟组表存的是**位号引用** —— 不一起作废
				// 就会留下一批指向已不存在器件的孤儿组,下一次 block-apply 想登记
				// 同名位号时会撞上「该位号已属于组 gN」而拒绝归组(ADR-0003 落地
				// 时真机踩到)。先记下当前页,清成功后再作废它的组表。
				var docUUID, project string
				if !dryRun {
					if _, _, d, pr, _, _, err := loadSchGroupsContext(cfg, window); err == nil {
						docUUID, project = d, pr
					}
				}
				if err := dispatch(cfg, "schematic.page.clear", window, map[string]any{
					"preserveSheet": !noPreserveSheet,
					"dryRun":        dryRun,
				}, stdout, stderr); err != nil {
					return err
				}
				if !dryRun && docUUID != "" {
					dropSchGroupsForPage(project, docUUID, stderr)
				}
				return nil
			},
		}
		c.Flags().BoolVar(&dryRun, "dry-run", false, "report counts without deleting anything")
		c.Flags().BoolVar(&noPreserveSheet, "no-preserve-sheet", false, "also delete the sheet/title block (图框); by default it is kept")
		sch.AddCommand(c)
	}

	// ── group-arrange:第二层(组与组之间按跨组信号关系排布,ADR-0003)────────
	{
		var gap float64
		var dryRun, annotate bool
		c := &cobra.Command{
			Use:   "group-arrange",
			Short: "第二层排布:把虚拟组当刚体,按**跨组信号网**关系铺进图纸可用区(ADR-0003)",
			Long: `第二层布局:排的是**组**,不是器件。

层次(ADR-0003):part → group → zone → sheet。第一层(block-apply)把每个块解成
刚体并封组;这一层把组当刚体排。关系**从网表算出来**,不需要块作者声明:

  - 两组之间的**跨组信号网条数** = 耦合强度,强的相邻(USB→桥芯片→下载电路)
  - 电源/地**不计入**耦合:它们连着几乎每个器件,算进去会把整页揉成一团

落位复用已验证的刚体平移(删净→modify→一遍性重连→电气自检),所以排完之后
网表逐引脚不变。放不下就明确报错(拆页),不硬塞、不溢出图纸。`,
			Args: cobra.NoArgs,
			Example: `  easyeda sch group-arrange --dry-run    # 只看计划(耦合强度 + 落位)
  easyeda sch group-arrange              # 执行`,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runGroupArrange(cfg, window, gap, dryRun, annotate, stdout, stderr)
			},
		}
		c.Flags().Float64Var(&gap, "gap", 60, "组与组之间的可见间隙(区内紧凑、区间有隔;填满纸张不是目标)")
		c.Flags().BoolVar(&dryRun, "dry-run", false, "只打印计划,不改动画布")
		c.Flags().BoolVar(&annotate, "annotate", true, "同时画功能区框 + 组名 + 电路说明(它们的空间在排布时已计入,不是事后捡缝)")
		sch.AddCommand(c)
	}

	// ── rename (whole schematic document) ──────────────────────────────────
	// schematic.rename
	{
		var schUuid, name string
		c := &cobra.Command{
			Use:     "rename",
			Short:   "Rename a schematic document (the whole sheet, not a single page)",
			Args:    cobra.NoArgs,
			Example: `  easyeda sch rename --schematic <schematicUuid> --name "主原理图"`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if schUuid == "" {
					return fmt.Errorf("--schematic is required")
				}
				if name == "" {
					return fmt.Errorf("--name is required")
				}
				return dispatch(cfg, "schematic.rename", window,
					map[string]any{"schematicUuid": schUuid, "name": name}, stdout, stderr)
			},
		}
		c.Flags().StringVar(&schUuid, "schematic", "", "schematic document UUID (required)")
		c.Flags().StringVar(&name, "name", "", "new schematic name (required)")
		sch.AddCommand(c)
	}

	// ── list ─────────────────────────────────────────────────────────────
	// schematic.components.list
	{
		var allPages, includeBBox, includePins, stay bool
		var page string
		c := &cobra.Command{
			Use:   "list",
			Short: "List components on the active (or all) schematic page(s)",
			Args:  cobra.NoArgs,
			Long: `List components on the active (or all) schematic page(s).

Each component carries a structured ` + "`device`" + ` field
{libraryUuid, uuid, name} — the device-library identity of the placed part
(the same identity the rebind path resolves via ` + "`lib_Device.search`" + `). This is
distinct from the ` + "`component`/`symbol`/`footprint`/`uniqueId`" + ` fields, which are
placed-INSTANCE sub-primitive ids and cannot be replayed into ` + "`sch place`" + `.

Use ` + "`device.uuid`" + ` to lock onto the exact symbol variant of a golden design
instead of re-searching by LCSC C-number (which may hit a different pin-numbering
variant). NOTE: imported devices (Altium/KiCad → EasyEDA) often report an EMPTY
` + "`device.libraryUuid`" + `; when empty, resolve it via ` + "`lib search`" + ` / ` + "`lib by-lcsc`" + `
before feeding it back into ` + "`sch place --uuid`" + `.`,
			Example: `  easyeda sch list
  easyeda sch list --all-pages
  easyeda sch list --include-bbox
  easyeda sch list --include-pins`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if page != "" && allPages {
					return fmt.Errorf("--page and --all-pages are mutually exclusive")
				}
				if page != "" {
					scope, err := switchToPage(cfg, window, page)
					if err != nil {
						return err
					}
					if !stay {
						defer func() { _ = scope.restore(cfg) }()
					}
					window = scope.window
				}
				payload := map[string]any{}
				if allPages {
					payload["allPages"] = true
				}
				if includeBBox {
					payload["includeBBox"] = true
				}
				if includePins {
					payload["includePins"] = true
				}
				if len(payload) == 0 {
					return dispatch(cfg, "schematic.components.list", window, nil, stdout, stderr)
				}
				return dispatch(cfg, "schematic.components.list", window, payload, stdout, stderr)
			},
		}
		c.Flags().BoolVar(&allPages, "all-pages", false, "list components across all schematic pages (WARNING: non-active pages return shallow data — pins/bbox may be empty; use `doc switch` to that page for accurate data)")
		c.Flags().StringVar(&page, "page", "", "switch to this page (name|uuid), wait for it to settle, then list — makes the page an explicit parameter instead of relying on the active tab (issue #67)")
		c.Flags().BoolVar(&stay, "stay", false, "with --page, stay on the target page after listing instead of switching back")
		c.Flags().BoolVar(&includeBBox, "include-bbox", false, "attach each component's rendered extent {minX,minY,maxX,maxY}")
		c.Flags().BoolVar(&includePins, "include-pins", false, "attach each pin's {pinName,pinNumber,x,y,noConnected,net} — the data plane for routing/connectivity checks (net is the pin's current authoritative net, null when the netlist is unavailable; output grows, esp. with --all-pages)")
		sch.AddCommand(c)
	}

	// ── place ─────────────────────────────────────────────────────────────
	// schematic.component.place
	{
		var lib, uuid, designator string
		var x, y, rotation float64
		var mirror bool
		c := &cobra.Command{
			Use:   "place",
			Short: "Place a component from the device library at coordinates",
			Args:  cobra.NoArgs,
			Long: `Place a device/component from the EasyEDA device library at coordinates.

--uuid MUST be a device-library uuid (from ` + "`easyeda lib search`" + `), NOT one of
the uuid-looking fields ` + "`component`/`symbol`/`footprint`/`uniqueId`" + ` that
` + "`easyeda sch list`" + ` reports — those are placed-INSTANCE ids and are not valid
` + "`sch place`" + ` inputs. Passing an instance uuid makes the EasyEDA API hang; this
command fails fast after a short timeout with a hint instead of stalling.

Pass --designator to atomically assign the final designator on the connector
side right after create, so you skip the place→` + "`sch list`" + `→` + "`sch modify`" + ` round-trip
and the coordinate-based primitiveId re-matching that batch placement otherwise
needs. The response's ` + "`primitiveId`" + ` and ` + "`component.designator`" + ` reflect the
final placed state.`,
			Example: `  easyeda sch place --lib <libraryUuid> --uuid <deviceUuid> --x 100 --y 200
  easyeda sch place --lib <l> --uuid <u> --x 100 --y 200 --rotation 90 --mirror
  easyeda sch place --lib <l> --uuid <u> --x 100 --y 200 --designator R12`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if lib == "" {
					return fmt.Errorf("--lib is required")
				}
				if uuid == "" {
					return fmt.Errorf("--uuid is required")
				}
				payload := map[string]any{
					"libraryUuid": lib,
					"uuid":        uuid,
					"x":           x,
					"y":           y,
				}
				if cmd.Flags().Changed("rotation") {
					payload["rotation"] = rotation
				}
				if cmd.Flags().Changed("mirror") {
					payload["mirror"] = mirror
				}
				if cmd.Flags().Changed("designator") {
					payload["designator"] = designator
				}
				err := dispatchTimed(cfg, "schematic.component.place", window, payload, placeTimeout, stdout, stderr)
				if err != nil && errors.Is(err, context.DeadlineExceeded) {
					return placeUUIDHint(placeTimeout)
				}
				return err
			},
		}
		c.Flags().StringVar(&lib, "lib", "", "library UUID (required)")
		c.Flags().StringVar(&uuid, "uuid", "", "device UUID within the library (required)")
		c.Flags().Float64Var(&x, "x", 0, "X coordinate")
		c.Flags().Float64Var(&y, "y", 0, "Y coordinate")
		c.Flags().Float64Var(&rotation, "rotation", 0, "rotation in degrees (0/90/180/270)")
		c.Flags().BoolVar(&mirror, "mirror", false, "mirror the component")
		c.Flags().StringVar(&designator, "designator", "", "final designator to assign atomically after placement, e.g. R12 (avoids the place→list→modify round-trip; the response's component.designator reflects the assigned value)")
		sch.AddCommand(c)
	}

	// ── modify ────────────────────────────────────────────────────────────
	// schematic.component.modify
	{
		var id, patchJSON, designator string
		var mx, my, mrot float64
		c := &cobra.Command{
			Use:   "modify",
			Short: "Modify component position, designator, BOM flags, or custom properties",
			Args:  cobra.NoArgs,
			Long: `Modify component position, designator, BOM flags, or custom properties.

Common tweaks go straight on the command line — --x / --y / --rotation /
--designator (same flags as ` + "`sch place`" + `). --patch takes a JSON object for
everything else (customAttributes, BOM flags, ...). Both may be combined; on a
key collision the explicit flag wins and the override is reported on stderr.

Patch keys are validated up front against the EasyEDA SDK modify signature
(x/y/rotation/mirror/addIntoBom/addIntoPcb/designator/name/uniqueId/
manufacturer/manufacturerId/supplier/supplierId/otherProperty, plus the
customAttributes alias for otherProperty — use one, not both); unknown
top-level keys are rejected before any canvas mutation (the SDK silently
drops them). Property patches MERGE with the component's existing custom
properties and are verified by fresh readback, with tiered semantics (#151):

  - all requested properties applied      → success
  - PARTIAL application                   → wire-level ok (the applied subset
    stays on canvas and autosaves) but THIS COMMAND EXITS NON-ZERO with
    result.{partial,applied,alreadySet,notApplied,addedKeys,propertiesBefore};
    replaying propertiesBefore restores overwritten values only — keys newly
    added by this call (addedKeys) cannot be removed via modify
  - pure-property patch, nothing applied  → error (canvas unchanged)
  - readback channel itself fails         → success with verified:false +
    warning (erroring would skip autosave and lose the applied edit)

MERGE also holds for top-level-only patches (#175): the platform's modify
rewrites otherProperty WHOLESALE, so a patch like {"supplierId":...} used to
silently wipe all custom properties. The connector now reads the existing
custom properties and re-writes them in the same modify call; preserved keys
come back in result.propertiesPreserved (+propertiesBefore), and any key the
platform still dropped is reported in result.notApplied (non-zero exit).`,
			Example: `  easyeda sch modify --id <primitiveId> --x 150 --y 200
  easyeda sch modify --id <id> --rotation 90 --designator R12
  easyeda sch modify --id <id> --patch '{"customAttributes":{"Value":"10k"}}'`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if id == "" {
					return fmt.Errorf("--id is required")
				}
				// Only EXPLICITLY-passed flags enter the patch (Changed guard):
				// a default-zero --x must never overwrite a real position.
				overrides := map[string]any{}
				if cmd.Flags().Changed("x") {
					overrides["x"] = mx
				}
				if cmd.Flags().Changed("y") {
					overrides["y"] = my
				}
				if cmd.Flags().Changed("rotation") {
					overrides["rotation"] = mrot
				}
				if cmd.Flags().Changed("designator") {
					overrides["designator"] = designator
				}
				patch, overridden, err := buildModifyPatch(patchJSON, overrides)
				if err != nil {
					return err
				}
				if len(overridden) > 0 {
					fmt.Fprintf(stderr, "note: flag value(s) override --patch key(s): %s\n", strings.Join(overridden, ", "))
				}
				res, err := dispatchCapture(cfg, "schematic.component.modify", window,
					map[string]any{"primitiveId": id, "patch": patch}, stdout)
				if err != nil {
					return err
				}
				// 部分应用(#151):已应用子集是既成事实(wire 级 ok:true 使
				// autosave 照常落盘),但脚本/CI 必须看到失败信号 —— wire 级
				// ok 与 CLI 退出码解耦(#113 精神:错误信号不丢)。
				if na, _ := res.Result["notApplied"].([]any); len(na) > 0 {
					keys := make([]string, 0, len(na))
					for _, k := range na {
						keys = append(keys, fmt.Sprint(k))
					}
					return fmt.Errorf("partial apply: properties not applied: %s (applied subset kept on canvas and autosaved; replaying result.propertiesBefore restores overwritten values only — keys newly added by this call (result.addedKeys) cannot be removed via modify)", strings.Join(keys, ", "))
				}
				return nil
			},
		}
		c.Flags().StringVar(&id, "id", "", "primitive ID to modify (required)")
		c.Flags().Float64Var(&mx, "x", 0, "new X coordinate (shortcut for --patch '{\"x\":…}')")
		c.Flags().Float64Var(&my, "y", 0, "new Y coordinate (shortcut for --patch '{\"y\":…}')")
		c.Flags().Float64Var(&mrot, "rotation", 0, "new rotation in degrees (shortcut for --patch '{\"rotation\":…}')")
		c.Flags().StringVar(&designator, "designator", "", "new designator, e.g. R12 (shortcut for --patch '{\"designator\":…}')")
		c.Flags().StringVar(&patchJSON, "patch", "", "JSON object with fields to update (for keys without a shortcut flag: customAttributes, BOM flags, …)")
		sch.AddCommand(c)
	}

	// ── rebind-footprint ────────────────────────────────────────────────────
	// schematic.rebind.footprint — swap a placed component's footprint (五步绑定法).
	{
		var id, footprint, footprintUUID, footprintLib, scope string
		c := &cobra.Command{
			Use:   "rebind-footprint",
			Short: "Swap a placed component's footprint (five-step rebind: modify→delete→create→restore)",
			Args:  cobra.NoArgs,
			Long: `Rebind the footprint of an already-placed schematic component to a same-named
(or explicitly identified) library footprint.

modify() cannot change the footprint reference of a placed instance, so this runs the
"five-step binding": lib_Device.modify → delete old instance → create fresh instance →
restore designator/position/props. Imported devices with an empty libraryUuid are
reverse-looked-up in the project library first.

NOTE: re-placing mints a NEW primitiveId; wires on the old pins may need re-drawing —
run ` + "`easyeda sch drc`" + ` / ` + "`easyeda sch check`" + ` after to confirm connectivity.`,
			Example: `  easyeda sch rebind-footprint --id <primitiveId> --footprint QFN-32_L5.0-W5.0
  easyeda sch rebind-footprint --id <id> --footprint-uuid <u> --footprint-lib <l>`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if id == "" {
					return fmt.Errorf("--id is required")
				}
				if footprint == "" && footprintUUID == "" {
					return fmt.Errorf("provide --footprint (name) or --footprint-uuid")
				}
				payload := map[string]any{"primitiveId": id}
				if footprint != "" {
					payload["footprint"] = footprint
				}
				if footprintUUID != "" {
					payload["footprintUuid"] = footprintUUID
				}
				if footprintLib != "" {
					payload["footprintLibraryUuid"] = footprintLib
				}
				if scope != "" {
					payload["scope"] = scope
				}
				return dispatchTimed(cfg, "schematic.rebind.footprint", window, payload, rebindTimeout, stdout, stderr)
			},
		}
		c.Flags().StringVar(&id, "id", "", "placed component primitive ID (required)")
		c.Flags().StringVar(&footprint, "footprint", "", "target footprint name to search and bind")
		c.Flags().StringVar(&footprintUUID, "footprint-uuid", "", "target footprint UUID (bypasses name search)")
		c.Flags().StringVar(&footprintLib, "footprint-lib", "", "target footprint library UUID (with --footprint-uuid)")
		c.Flags().StringVar(&scope, "scope", "", "library search scope (default: project)")
		sch.AddCommand(c)
	}

	// ── rebind-symbol ────────────────────────────────────────────────────────
	// schematic.rebind.symbol — swap a placed component's symbol (五步绑定法).
	{
		var id, symbol, symbolUUID, symbolLib, scope string
		c := &cobra.Command{
			Use:   "rebind-symbol",
			Short: "Swap a placed component's symbol (five-step rebind: modify→delete→create→restore)",
			Args:  cobra.NoArgs,
			Long: `Rebind the symbol of an already-placed schematic component to a same-named
(or explicitly identified) library symbol.

modify() cannot change the symbol reference of a placed instance, so this runs the
"five-step binding": lib_Device.modify → delete old instance → create fresh instance →
restore designator/position/props. Imported devices with an empty libraryUuid are
reverse-looked-up in the project library first.

NOTE: re-placing mints a NEW primitiveId; wires on the old pins may need re-drawing —
run ` + "`easyeda sch drc`" + ` / ` + "`easyeda sch check`" + ` after to confirm connectivity.`,
			Example: `  easyeda sch rebind-symbol --id <primitiveId> --symbol ESP32-S3
  easyeda sch rebind-symbol --id <id> --symbol-uuid <u> --symbol-lib <l>`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if id == "" {
					return fmt.Errorf("--id is required")
				}
				if symbol == "" && symbolUUID == "" {
					return fmt.Errorf("provide --symbol (name) or --symbol-uuid")
				}
				payload := map[string]any{"primitiveId": id}
				if symbol != "" {
					payload["symbol"] = symbol
				}
				if symbolUUID != "" {
					payload["symbolUuid"] = symbolUUID
				}
				if symbolLib != "" {
					payload["symbolLibraryUuid"] = symbolLib
				}
				if scope != "" {
					payload["scope"] = scope
				}
				return dispatchTimed(cfg, "schematic.rebind.symbol", window, payload, rebindTimeout, stdout, stderr)
			},
		}
		c.Flags().StringVar(&id, "id", "", "placed component primitive ID (required)")
		c.Flags().StringVar(&symbol, "symbol", "", "target symbol name to search and bind")
		c.Flags().StringVar(&symbolUUID, "symbol-uuid", "", "target symbol UUID (bypasses name search)")
		c.Flags().StringVar(&symbolLib, "symbol-lib", "", "target symbol library UUID (with --symbol-uuid)")
		c.Flags().StringVar(&scope, "scope", "", "library search scope (default: project)")
		sch.AddCommand(c)
	}

	// ── replace ──────────────────────────────────────────────────────────────
	// schematic.component.replace — swap a placed component for a DIFFERENT device.
	{
		var id, lcsc, deviceUUID, deviceLib, query string
		var keepProperties bool
		c := &cobra.Command{
			Use:   "replace",
			Short: "Replace a placed component with a different library device (换型号 / 器件标准化)",
			Args:  cobra.NoArgs,
			Long: `Replace an already-placed schematic component with a DIFFERENT library device —
the programmatic equivalent of the 器件标准化 panel's 使用推荐器件 (which has no
extension API of its own).

The official API cannot re-bind a placed instance to another device, so this
runs delete → create-at-same-pose → restore. Carried over: designator, uniqueId
(so sch→PCB import-changes UPDATES the footprint instead of delete+add),
position/rotation/mirror/BOM flags. Deliberately NOT carried over: name,
manufacturer(Id), supplier(Id)/LCSC — part identity follows the NEW device.
Pass --keep-properties to also carry old custom attributes (otherProperty).

Failure after the delete rolls back by re-creating the original device with its
full identity.

The result includes a pinDiff (removed/added/moved pins by pinNumber, compared
at the identical placement pose). A non-empty pinDiff means existing wires will
NOT line up — re-wire the affected pins, then run ` + "`easyeda sch drc`" + ` /
` + "`easyeda sch check`" + ` to confirm connectivity.`,
			Example: `  easyeda sch replace --id <primitiveId> --lcsc C14663
  easyeda sch replace --id <id> --device-uuid <u> --device-lib <l>
  easyeda sch replace --id <id> --query "CL05B104KO5NNNC" --keep-properties`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if id == "" {
					return fmt.Errorf("--id is required")
				}
				selectors := 0
				for _, s := range []string{lcsc, deviceUUID, query} {
					if s != "" {
						selectors++
					}
				}
				if selectors != 1 {
					return fmt.Errorf("provide exactly one of --lcsc, --device-uuid (+--device-lib), or --query")
				}
				if deviceUUID != "" && deviceLib == "" {
					return fmt.Errorf("--device-uuid requires --device-lib")
				}
				payload := map[string]any{"primitiveId": id}
				if lcsc != "" {
					payload["lcsc"] = lcsc
				}
				if deviceUUID != "" {
					payload["deviceUuid"] = deviceUUID
					payload["deviceLibraryUuid"] = deviceLib
				}
				if query != "" {
					payload["query"] = query
				}
				if keepProperties {
					payload["keepProperties"] = true
				}
				return dispatchTimed(cfg, "schematic.component.replace", window, payload, rebindTimeout, stdout, stderr)
			},
		}
		c.Flags().StringVar(&id, "id", "", "placed component primitive ID (required)")
		c.Flags().StringVar(&lcsc, "lcsc", "", "target LCSC C-number (must resolve uniquely, e.g. C14663)")
		c.Flags().StringVar(&deviceUUID, "device-uuid", "", "target device-library uuid (from `easyeda lib search` / `lib by-lcsc`)")
		c.Flags().StringVar(&deviceLib, "device-lib", "", "target device library UUID (required with --device-uuid)")
		c.Flags().StringVar(&query, "query", "", "target device name (must match uniquely; ambiguity errors out with candidates)")
		c.Flags().BoolVar(&keepProperties, "keep-properties", false, "also carry the OLD component's custom attributes (otherProperty) onto the replacement")
		sch.AddCommand(c)
	}

	// ── prim-delete ─────────────────────────────────────────────────────────
	// schematic.primitives.delete — the ONE delete entry point. The old
	// `sch delete` (components only, schematic.component.delete) was removed:
	// prim-delete covers components too, and two delete verbs with different
	// type coverage was a real agent trap.
	{
		var idsRaw string
		var allowSheet bool
		c := &cobra.Command{
			Use:   "prim-delete",
			Short: "Delete schematic primitives of ANY type by id (or the current selection if --ids omitted)",
			Args:  cobra.NoArgs,
			Example: `  easyeda sch prim-delete --ids id1,id2   # delete these (any primitive type)
  easyeda sch prim-delete                 # delete the current selection`,
			RunE: func(cmd *cobra.Command, args []string) error {
				payload := map[string]any{}
				var cascadePlan map[string]string // primitiveId → group-member designator
				if idsRaw != "" {
					ids, err := parseIDList(idsRaw)
					if err != nil {
						return err
					}
					// 图框守卫(2026-08-17 误删实锤):sheet 在 list 里就是「无位号
					// @(0,0)」,与 PARTIAL 残件同脸;删了没有 API 能重建。
					if err := schSheetGuard(cfg, window, ids, allowSheet, stderr); err != nil {
						return err
					}
					payload["primitiveIds"] = ids
					// 缺陷 2(P1):删器件必须级联删组注册,否则位号复用后新件被
					// 陈旧组吃掉。先于删除解析 id→位号(删完 list 就查不到了)。
					cascadePlan = planSchGroupMemberCascade(cfg, window, ids, stderr)
				}
				res, err := dispatchCapture(cfg, "schematic.primitives.delete", window, payload, stdout)
				if err != nil {
					return err
				}
				// 连接器的存活判定是删完**立刻**回读的,可能采到尚未落定的快照。
				// 幸存者 settle 一拍后复核一轮,用复核回执定案(sch_prim_delete_settle.go)。
				res = primDeleteSettleRecheck(cfg, window, res, stderr)
				// Registry leg of the delete cascade: only designators whose delete
				// was VERIFIED (not in result.survived) leave the group table.
				if len(cascadePlan) > 0 {
					survived := survivedIDSet(res.Result)
					var gone []string
					for id, desig := range cascadePlan {
						if !survived[id] {
							gone = append(gone, desig)
						}
					}
					cascadeSchGroupMembership(cfg, window, gone, stderr)
				}
				// ADR-0004 Decision 5: when a delete response carries a cascaded
				// cleanup block (component.delete does; renderer is generic), say so.
				printCascadeCleanup(res, stderr)
				// The handler reports a verified count now (#164): primitives that
				// survived the delete come back as result.partial. Fail the command
				// rather than let "ok:true" read as "they are gone" — that is exactly
				// how zone-draw labels accumulated while every sweep looked clean.
				return failOnSurvivingPrimitives(res, stderr)
			},
		}
		c.Flags().StringVar(&idsRaw, "ids", "", "primitive IDs to delete (any type) — CSV: id1,id2; omit to delete the current selection")
		c.Flags().BoolVar(&allowSheet, "allow-sheet", false, "allow deleting sheet/图框 primitives (blocked by default — a deleted sheet cannot be recreated via API)")
		sch.AddCommand(c)
	}

	// ── resolve-lcsc ─────────────────────────────────────────────────────────
	// schematic.component.resolve_lcsc (#158) — deterministic placed-part → C#.
	{
		var id, page string
		var apply, stay bool
		c := &cobra.Command{
			Use:   "resolve-lcsc",
			Short: "Deterministically resolve placed parts to their device's REAL LCSC C-number (dry-run; --apply writes back)",
			Args:  cobra.NoArgs,
			Long: `Resolve every placed part on the active page to its device's REAL LCSC
C-number — deterministically, never by fuzzy guessing (#158).

Chain: instance C# (if already real) → exact-MPN library match → project-library
name match; the match's footprint must equal the instance's. A bare
lib_Device.search picks fragment-matched garbage (a U.FL antenna socket resolved
to a C1017 ferrite bead), and a lone hit with a different footprint is a
package-variant mismatch — both land in result.unresolved WITH candidates
instead of being silently applied.

--apply writes each resolved C-number onto instances whose supplierId is not a
real C# (the platform defaults it to the subPartName, #157) — the one-command
version of a whole-board supplierId repair. Multi-page projects: run per page
(--page or doc switch); only the active page is scanned.`,
			Example: `  easyeda sch resolve-lcsc                 # dry-run report
  easyeda sch resolve-lcsc --apply         # write resolved C#s back
  easyeda sch resolve-lcsc --page P2 --apply
  easyeda sch resolve-lcsc --id <primitiveId>`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if page != "" {
					scope, err := switchToPage(cfg, window, page)
					if err != nil {
						return err
					}
					if !stay {
						defer func() { _ = scope.restore(cfg) }()
					}
					window = scope.window
				}
				payload := map[string]any{}
				if id != "" {
					payload["primitiveId"] = id
				}
				if apply {
					payload["apply"] = true
				}
				return dispatchTimed(cfg, "schematic.component.resolve_lcsc", window, payload, rebindTimeout, stdout, stderr)
			},
		}
		c.Flags().StringVar(&id, "id", "", "resolve a single part by primitive ID")
		c.Flags().BoolVar(&apply, "apply", false, "write resolved C-numbers back onto instances whose supplierId is not a real C#")
		c.Flags().StringVar(&page, "page", "", "switch to this page (name|uuid) first, then switch back")
		c.Flags().BoolVar(&stay, "stay", false, "with --page, stay on the target page")
		sch.AddCommand(c)
	}

	// ── text-list ────────────────────────────────────────────────────────────
	// schematic.text.list (#156) — read-only enumeration of text primitives.
	{
		var page string
		var stay bool
		c := &cobra.Command{
			Use:     "text-list",
			Aliases: []string{"text-ls"},
			Short:   "List ALL text primitives on the active schematic page (id/content/x/y/…)",
			Args:    cobra.NoArgs,
			Long: `Read-only list of every text primitive on the ACTIVE schematic page — the
typed enumeration that pairs with ` + "`sch prim-delete --ids`" + ` to clean up orphaned
zone-draw labels without the ` + "`debug exec`" + ` escape hatch (#156).

Page-lazy-load law: only the active page's texts are returned — pass --page (or
` + "`doc switch`" + `) per page to sweep a multi-page project.`,
			Example: `  easyeda sch text-list
  easyeda sch text-list --page P2
  easyeda sch text-list | jq -r '.result.texts[] | "\(.primitiveId)\t\(.content)"'`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if page != "" {
					scope, err := switchToPage(cfg, window, page)
					if err != nil {
						return err
					}
					if !stay {
						defer func() { _ = scope.restore(cfg) }()
					}
					window = scope.window
				}
				return dispatch(cfg, "schematic.text.list", window, nil, stdout, stderr)
			},
		}
		c.Flags().StringVar(&page, "page", "", "switch to this page (name|uuid) first, list, then switch back")
		c.Flags().BoolVar(&stay, "stay", false, "with --page, stay on the target page after listing")
		sch.AddCommand(c)
	}

	// ── wire ──────────────────────────────────────────────────────────────
	// schematic.wire.create
	//
	// ⚠ 这是**逃生口**,不是一等布线手段。正路是 autoconnect(电源/地/netport 短桩)
	// 和 block-apply(整块落地)——它们按真实 bbox/引脚/已有 flag 几何打分选方向和
	// 桩长,天然避开下面说的合并陷阱。本命令存在的理由只有两个:
	//   1. group-move 刚体平移半途失败时的残局修复(那条错误文案就写着 "finish
	//      manually with `sch wire`")——导线没有 modify-in-place,只能删了重建,
	//      重建炸在中间就得手工补;
	//   2. zone relayout 的串联链共线串接(cmd_sch_zone_relayout.go)内部走同一个
	//      action 落 pin-to-pin 直线。
	// 审计数据(2026-08):本命令 40 次调用 / 17.5% 失败率,而 connect_pin 是 17389
	// 次 / 2.9%——长尾失败率是主路径的 6 倍,典型「用得少所以坏了没人知道」。#170
	// 就是外部用户把它当一等手段画多网信号线,被 EasyEDA 自动合并成一条多段线,
	// 18 个引脚全并进 GND,而 `sch check` 全绿。故不加几何护栏(残局修复恰恰需要它
	// 无阻碍工作),改为在 help 里说清定位 + 落线后提示走 bridge-check 对账。
	{
		var pointsJSON, net, styleJSON string
		c := &cobra.Command{
			Use:   "wire",
			Short: "Create a schematic wire polyline — ESCAPE HATCH; 常规布线走 `sch autoconnect` / `sch block-apply`",
			Long: `手工画一条导线折线。**这是逃生口,不是常规布线手段。**

常规布线请走:
  • ` + "`easyeda sch autoconnect`" + ` — 电源/地/netport 短桩(按真实几何打分选方向+桩长)
  • ` + "`easyeda sch block-apply`" + ` — 整块落地(自带 netlist 对账门)

本命令主要用于 ` + "`sch group-move`" + ` 刚体平移半途失败后的残局修复
(导线无 modify-in-place,只能删除+重建;重建中断就要手工补齐剩余段)。

⚠ **EasyEDA 会自动合并共线相接的导线** —— 这是手工画线最容易静默毁掉电路的坑:
  • 同 x 的两条竖线只要 y 区间**相接或被元件本体填满**(即使中间隔着元件),
    就会被合并成一条多段线;竖线**穿过非目标引脚**同样算电气接触 → 合并;
  • 合并后多个网络并成一个,而 ` + "`sch check`" + ` **不报网络归属错误**(只报
    wire-crossing / dangling),肉眼和常规检查都看不出来。

所以手工画多网信号线时:每个网分配**独立的竖线通道 x**(互不重叠,避开已有
netflag 桩线占用的 x),把所有引脚点当作**点障碍**绕行,画完必须跑
` + "`easyeda sch bridge-check`" + ` + ` + "`easyeda sch read`" + ` 逐网对账。`,
			Args: cobra.NoArgs,
			Example: `  easyeda sch wire --points '[[100,200],[100,300]]'        # nested pairs
  easyeda sch wire --points '[100,200,100,300]'            # flat (also accepted)
  easyeda sch wire --points '[[100,200],[100,300]]' --net VCC`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if pointsJSON == "" {
					return fmt.Errorf("--points is required")
				}
				var points []any
				if err := json.Unmarshal([]byte(pointsJSON), &points); err != nil {
					return fmt.Errorf("invalid --points json (expected array): %w", err)
				}
				payload := map[string]any{"points": points}
				if net != "" {
					payload["net"] = net
				}
				if cmd.Flags().Changed("style") {
					var style map[string]any
					if err := json.Unmarshal([]byte(styleJSON), &style); err != nil {
						return fmt.Errorf("invalid --style json: %w", err)
					}
					payload["style"] = style
				}
				if err := dispatch(cfg, "schematic.wire.create", window, payload, stdout, stderr); err != nil {
					return err
				}
				// 手画线是合并短路的主要来源(#170):落线成功不等于网络正确,而
				// `sch check` 抓不到网络归属错误。提示走能抓到的那两个判据。
				fmt.Fprintln(stderr, "提示: 手工画线可能被 EasyEDA 与共线导线自动合并成多网短路(sch check 不报)。"+
					"跑 `easyeda sch bridge-check` + `easyeda sch read` 逐网对账确认。")
				return nil
			},
		}
		c.Flags().StringVar(&pointsJSON, "points", "", `JSON coordinate list, nested '[[x,y],...]' or flat '[x1,y1,x2,y2,...]' (connector normalizes; required)`)
		c.Flags().StringVar(&net, "net", "", "net name to assign to the wire")
		c.Flags().StringVar(&styleJSON, "style", "", "JSON object with wire style overrides")
		sch.AddCommand(c)
	}

	// ── group-move ────────────────────────────────────────────────────────
	// schematic.group.move — rigid translation. Two input modes:
	//   --ids    stateless: pass the full member id list every call;
	//   --group  persistent virtual group (cmd_sch_group.go): members resolve
	//            from designators, and their stub wires + far-end flags are
	//            discovered automatically (no more hand-collecting ids).
	// NOT backed by EasyEDA's native "组合" UI field (verified 2026-07-07:
	// that field has zero extension-API surface — no primitive type, no
	// getter/setter, not smuggled into OtherProperty either).
	{
		var idsRaw, groupRef, groupsRaw string
		var dx, dy float64
		var groupMoveMaxAttempts int
		c := &cobra.Command{
			Use:   "group-move",
			Short: "Translate components+wires together as one rigid assembly (dx,dy) — by --ids or persistent --group/--groups",
			Long: `Move a component and its surrounding stub wires/flags together as a single
unit — internal relative layout is untouched, only the whole assembly shifts by
(dx,dy). Two ways to name the members:

  --ids    STATELESS: pass every member's primitiveId on each call, nothing is
           remembered between invocations.
  --group  PERSISTENT virtual group (see ` + "`sch group create`" + `): members are
           stored as designators, resolved to live primitiveIds at call time,
           and each member's ATTACHMENTS ride along automatically — the stub
           wires hanging off its pins plus the netflag/netport/netlabel at the
           far end (wire-tree semantics, same as ` + "`sch disconnect`" + `). A wire
           tree that also touches a NON-member pin is real inter-part wiring,
           not a stub — it is left in place and reported.

There is no EasyEDA grouping API to persist against (probed 3.2.121: zero
group/parent surface), so --group reads easyeda-agent's own page-scoped store.

--group runs a COMPLETENESS PRECHECK and refuses over half-moving: a wire whose
own LINE passes through a member pin (perpendicular offset ≤1 unit) but whose
span stops SHORT of it by up to 12 units without attaching is the signature of
residue from an earlier half-move — a stranded stub displaced ALONG its own
line (seen live: line start 820 vs pin 810, same y, folded-back vertex list).
Such residue attaches to nothing, so a naive expansion would leave it behind
and every later move would strand it further (dangling wires, flags parked on
other parts). The test is deliberately COLLINEAR, not radial: a healthy
NEIGHBORING stub runs parallel one pin half-pitch away (can be <12 radially,
e.g. y=485 stub vs y=475 pin) and must never trip it. On detection the move is
REJECTED with the offending wire ids + coordinates; clean up first
(` + "`sch prim-delete --ids <wireId>`" + `, audit with ` + "`sch check`" + `), re-connect the
pin, then retry.

Components translate via a plain position modify (same primitiveId survives).
Wires have no modify-in-place, so each is deleted and recreated at the shifted
endpoints (net/color/width/lineType preserved) — a wire's primitiveId CHANGES;
pull fresh ids before any follow-up mutation on it.`,
			Args: cobra.NoArgs,
			Example: `  easyeda sch group-move --ids idComp1,idWire1,idWire2 --dx 200 --dy 0
  easyeda sch group-move --group g1 --dx 100 --dy 0   # members + stubs + flags auto-expanded
  easyeda sch group-move --groups g2,g3 --dx 0 --dy -80   # 同块多子组:一次内核调用整体移动`,
			RunE: func(cmd *cobra.Command, args []string) error {
				var groupRefs []string
				if groupRef != "" {
					groupRefs = append(groupRefs, groupRef)
				}
				for _, r := range strings.Split(groupsRaw, ",") {
					if r = strings.TrimSpace(r); r != "" {
						groupRefs = append(groupRefs, r)
					}
				}
				if idsRaw != "" && len(groupRefs) > 0 {
					return fmt.Errorf("--ids and --group/--groups are mutually exclusive")
				}
				if idsRaw == "" && len(groupRefs) == 0 {
					return fmt.Errorf("pass --ids (primitiveId CSV) or --group/--groups (persistent groups, `sch group list`)")
				}
				if !cmd.Flags().Changed("dx") && !cmd.Flags().Changed("dy") {
					return fmt.Errorf("at least one of --dx / --dy is required (a zero-move is a no-op)")
				}
				// --group/--groups 走 ADR-0004 安全 move 内核(快照→删证→移动→
				// 重连→对账,失败自动恢复)。**不再带着导线一起搬**:平台会合并
				// 共享端点的同网导线,逐根删建时新桩线会被邻居吞掉 —— 真机可复现,
				// ch340c 块平移一次静默丢 3 个 GND 引脚而命令报告一切正常。
				// 多组必须一次调用整体移动(逐组 move 会撕裂共享导线,ADR-0004
				// Decision 2 推论)。--ids 是裸图元模式,调用方自己负责,保持原语义。
				if len(groupRefs) > 0 {
					return groupsMoveRebuild(cfg, window, groupRefs, dx, dy, groupMoveMaxAttempts, stdout, stderr)
				}
				var ids []string
				{
					var err error
					ids, err = parseIDList(idsRaw)
					if err != nil {
						return err
					}
				}
				// 裸图元模式**也要过电气自检**。语义不变(只搬你点名的 id),但
				// 「搬走器件、把它的桩线留在原地」在画布上就是**静默断网**,而命令
				// 照样报成功 —— 2026-08-15 esp32Mini E2E 连踩四次:每次只传器件 id,
				// 器件走了、marker 留下,`sch check` 才在几步之后报出悬空引脚。
				// 移动已经发生,所以这里不是拦截而是**如实报告**(#151 部分应用约定):
				// 网表变了就非零退出并点名哪条网丢了谁,让调用方立刻补连或改用 --group。
				before, _, berr := readLiveNets(cfg, window)
				if berr != nil {
					fmt.Fprintf(stderr, "warning: 移动前读不到网表(%v)—— 本次无法做电气自检\n", berr)
				}
				payload := map[string]any{"primitiveIds": ids, "dx": dx, "dy": dy}
				if err := dispatch(cfg, "schematic.group.move", window, payload, stdout, stderr); err != nil {
					return err
				}
				// **「没能校验」不许降级成「校验通过」**(2026-08-26 实测):读不到
				// 网表时,过去只往 stderr 打一行 warning 就 return nil —— 退出码 0、
				// stdout 上与「自检通过」长得一模一样。连接器负载高时 readLiveNets
				// 很容易失败,于是「搬走器件、把桩线留在原地」这种静默断网就被当成
				// 成功放过去了(那一轮连移三件、6 个 orphan-tree,直到几步之后
				// bridge-check 才把它翻出来)。同 gate 的 blocked ≠ pass。
				if berr != nil {
					return schMoveIDsUnverified(stdout, "移动前读不到网表", berr)
				}
				after, _, aerr := readLiveNets(cfg, window)
				if aerr != nil {
					fmt.Fprintf(stderr, "warning: 移动后读不到网表(%v)—— 电气自检未完成\n", aerr)
					return schMoveIDsUnverified(stdout, "移动后读不到网表", aerr)
				}
				if diff := groupRebuildNetDiff(groupRebuildSnapshotOf(before), groupRebuildSnapshotOf(after)); len(diff) > 0 {
					fmt.Fprintf(stderr, "✗ 电气自检:平移改变了网表(--ids 只搬点名的图元,器件的桩线/旗不会自动跟随)\n")
					for _, d := range diff {
						fmt.Fprintf(stderr, "  %s\n", d)
					}
					return fmt.Errorf("group-move --ids 造成 %d 处网表变化 —— 用 `sch autoconnect` 补回受影响的引脚,"+
						"或改用 `sch group-move --group <id>`(它会自动带上桩线+旗并一遍性重连)", len(diff))
				}
				fmt.Fprintln(stdout, "✓ 电气自检:网表逐引脚不变")
				return nil
			},
		}
		c.Flags().StringVar(&idsRaw, "ids", "", "primitiveIds (components and/or wires) to move together — CSV: id1,id2 (mutually exclusive with --group)")
		c.Flags().StringVar(&groupRef, "group", "", "persistent group id/name (`sch group list`) — members' stub wires + flags are auto-included")
		c.Flags().StringVar(&groupsRaw, "groups", "", "多个持久组一次整体移动 — CSV: g2,g3(同块多子组必须一次调用,逐组 move 会撕裂共享导线;可与 --group 并用取并集)")
		c.Flags().Float64Var(&dx, "dx", 0, "X translation (mil)")
		c.Flags().Float64Var(&dy, "dy", 0, "Y translation (mil)")
		c.Flags().IntVar(&groupMoveMaxAttempts, "max-attempts", schConvergeDefaultMaxAttempts,
			"**跨调用**上限(仅 --group/--groups):同一个组连续多少次得到同一个失败结果(位移被钳到 0 等)后停手并给结论(0 = 不限)。"+
				"组本身比整幅可用区还大时,拒绝消息会换成真话(独立成页/拆页),而不是那条走不通的「减小位移试试」")
		sch.AddCommand(c)
	}

	// ── netflag ───────────────────────────────────────────────────────────
	// schematic.netflag.create
	{
		var kind, net string
		var x, y, rotation float64
		c := &cobra.Command{
			Use:   "netflag",
			Short: "Create a power/ground/net flag or port",
			Args:  cobra.NoArgs,
			Example: `  easyeda sch netflag --kind power --net VCC --x 100 --y 200
  easyeda sch netflag --kind gnd --net GND --x 100 --y 100 --rotation 180`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if kind == "" {
					return fmt.Errorf("--kind is required")
				}
				if net == "" {
					return fmt.Errorf("--net is required")
				}
				canonicalKind, err := resolveNetflagKind(kind)
				if err != nil {
					return err
				}
				payload := map[string]any{
					"kind": canonicalKind,
					"net":  net,
					"x":    x,
					"y":    y,
				}
				if cmd.Flags().Changed("rotation") {
					payload["rotation"] = rotation
				}
				return dispatch(cfg, "schematic.netflag.create", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&kind, "kind", "", netflagKindHelp)
		c.Flags().StringVar(&net, "net", "", "net name (required)")
		c.Flags().Float64Var(&x, "x", 0, "X coordinate")
		c.Flags().Float64Var(&y, "y", 0, "Y coordinate")
		c.Flags().Float64Var(&rotation, "rotation", 0, "rotation in degrees")
		sch.AddCommand(c)
	}

	// ── connect ───────────────────────────────────────────────────────────
	// schematic.power.connect_pin
	{
		var kind, net, direction, pinRef string
		var x, y, offset, rotation float64
		c := &cobra.Command{
			Use:   "connect",
			Short: "Stub a wire out of a pin and place a netflag/netport at its far end",
			Args:  cobra.NoArgs,
			Example: `  easyeda sch connect --pin U1:5 --kind power --net VCC
  easyeda sch connect --x 100 --y 200 --kind gnd --net GND --direction down --offset 40`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if kind == "" {
					return fmt.Errorf("--kind is required")
				}
				if net == "" {
					return fmt.Errorf("--net is required")
				}
				canonicalKind, err := resolveNetflagKind(kind)
				if err != nil {
					return err
				}
				if err := validatePinTarget(pinRef != "", cmd.Flags().Changed("x"), cmd.Flags().Changed("y")); err != nil {
					return err
				}
				if pinRef != "" {
					x, y, err = resolveSchPinXY(cfg, window, pinRef)
					if err != nil {
						return err
					}
				}
				payload := map[string]any{
					"pinX": x,
					"pinY": y,
					"kind": canonicalKind,
					"net":  net,
				}
				if cmd.Flags().Changed("direction") {
					payload["direction"] = direction
				}
				if cmd.Flags().Changed("offset") {
					payload["offset"] = offset
				}
				if cmd.Flags().Changed("rotation") {
					payload["rotation"] = rotation
				}
				// connect_pin's worst-case connector path is ~21.25s (measured;
				// see acConnectPinTimeout in cmd_sch_autoconnect_run.go) — the
				// 20s dispatch default made slow-but-successful connects report
				// failure, so bare `sch connect` uses the same budget the
				// autoconnect path already does.
				dispErr := dispatchTimed(cfg, "schematic.power.connect_pin", window, payload, acConnectPinTimeout, stdout, stderr)
				if dispErr == nil {
					return nil
				}
				// Slow-landed recheck: a timeout/DISPATCH_FAILED after the write
				// actually applied is a FAKE failure, and a blind retry then
				// creates a duplicate flag+stub. When the target pin is known,
				// one light read settles it: pin already on the target net →
				// report success instead.
				if pinRef == "" {
					return dispErr
				}
				desig, pinNum, ok := splitPinRef(pinRef)
				if !ok {
					return dispErr
				}
				res, rerr := requestActionTimed(cfg, "schematic.read", window,
					map[string]any{"includeCheck": false}, acConnectPinTimeout)
				if rerr != nil || res == nil || !connectLanded(res.Result, desig, pinNum, net) {
					return dispErr
				}
				// 回传假失败(通道 B):daemon 把这次转发记成失败了,而回读证明写
				// 其实落地了 —— 不回传的话,健康度会把一次「连接器慢」算成一次
				// 「连接器坏」,degraded 在错误的方向上响。
				reportWriteVerified(cfg, window, writeVerdict{
					action: "schematic.power.connect_pin", source: "sch connect",
					returnedOK: false, landed: 1,
				})
				fmt.Fprintf(stderr, "⚠ connect_pin 报超时/派发失败,但回读确认 %s 已在网络 %s 上 —— slow-landed,按成功处理(不要重试,会造重复旗)。\n", pinRef, net)
				return json.NewEncoder(stdout).Encode(map[string]any{
					"ok": true,
					"result": map[string]any{
						"slowLanded": true,
						"pin":        pinRef,
						"net":        net,
					},
				})
			},
		}
		c.Flags().StringVar(&pinRef, "pin", "", "target pin as DESIGNATOR:PIN, e.g. U1:5 (resolved to coordinates; mutually exclusive with --x/--y)")
		c.Flags().Float64Var(&x, "x", 0, "pin X coordinate (use with --y instead of --pin)")
		c.Flags().Float64Var(&y, "y", 0, "pin Y coordinate (use with --x instead of --pin)")
		c.Flags().StringVar(&kind, "kind", "", netflagKindHelp)
		c.Flags().StringVar(&net, "net", "", "net name (required)")
		c.Flags().StringVar(&direction, "direction", "", "visual stub direction (up=higher on canvas, down=lower): up, down, left, right")
		c.Flags().Float64Var(&offset, "offset", 0, "wire length in schematic units")
		c.Flags().Float64Var(&rotation, "rotation", 0, "flag rotation override in degrees")
		sch.AddCommand(c)
	}

	// ── disconnect ────────────────────────────────────────────────────────
	// schematic.pin.disconnect — inverse of connect: removes a pin's stub wire
	// AND its netflag/netport together (issue #51).
	{
		var pin, flagID, wireID string
		c := &cobra.Command{
			Use:   "disconnect",
			Short: "Remove a pin's stub wire and its netflag/netport together (inverse of connect)",
			Args:  cobra.NoArgs,
			Example: `  easyeda sch disconnect --pin U1:5
  easyeda sch disconnect --flag-id <flagPrimitiveId>
  easyeda sch disconnect --wire-id <wirePrimitiveId>`,
			RunE: func(cmd *cobra.Command, args []string) error {
				payload := map[string]any{}
				if pin != "" {
					parts := strings.SplitN(pin, ":", 2)
					if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
						return fmt.Errorf("--pin must be DESIGNATOR:PIN (e.g. U1:5)")
					}
					payload["designator"] = parts[0]
					payload["pin"] = parts[1]
				}
				if flagID != "" {
					payload["flagPrimitiveId"] = flagID
				}
				if wireID != "" {
					payload["wirePrimitiveId"] = wireID
				}
				if len(payload) == 0 {
					return fmt.Errorf("provide --pin U1:5, or --flag-id / --wire-id")
				}
				res, err := dispatchCapture(cfg, "schematic.pin.disconnect", window, payload, stdout)
				if err != nil {
					return err
				}
				warnDisconnectPartial(res, stderr)
				return nil
			},
		}
		c.Flags().StringVar(&pin, "pin", "", "target pin as DESIGNATOR:PIN (e.g. U1:5)")
		c.Flags().StringVar(&flagID, "flag-id", "", "netflag/netport primitiveId (from connect output)")
		c.Flags().StringVar(&wireID, "wire-id", "", "stub wire primitiveId (from connect output)")
		sch.AddCommand(c)
	}

	// ── no-connect ──────────────────────────────────────────────────────────
	// schematic.pin.set_no_connect
	{
		var designator string
		var pins []string
		var clear bool
		c := &cobra.Command{
			Use:   "no-connect",
			Short: "Mark (or clear) a pin's no-connect flag (非连接标识)",
			Args:  cobra.NoArgs,
			Example: `  easyeda sch no-connect --designator U1 --pin 23
  easyeda sch no-connect --designator U1 --pin 23,24,25
  easyeda sch no-connect --designator U1 --pin 23 --clear`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if designator == "" {
					return fmt.Errorf("--designator is required")
				}
				if len(pins) == 0 {
					return fmt.Errorf("--pin is required (one or more pin numbers)")
				}
				anyPins := make([]any, len(pins))
				for i, p := range pins {
					anyPins[i] = p
				}
				payload := map[string]any{
					"designator": designator,
					"pins":       anyPins,
				}
				if clear {
					payload["noConnected"] = false
				}
				res, err := dispatchCapture(cfg, "schematic.pin.set_no_connect", window, payload, stdout)
				if err != nil {
					return err
				}
				// notApplied 退出码约定与 `sch modify` 对齐(#151):部分 pin 未
				// 持久化时 wire 级 ok:true(已持久化子集照常 autosave),但脚本/CI
				// 必须从退出码看到失败信号。
				if na, _ := res.Result["notApplied"].([]any); len(na) > 0 {
					pins := make([]string, 0, len(na))
					for _, p := range na {
						pins = append(pins, fmt.Sprint(p))
					}
					return fmt.Errorf("partial apply: no-connect not persisted on pin(s): %s (persisted subset kept; re-run for the listed pins)", strings.Join(pins, ", "))
				}
				return nil
			},
		}
		c.Flags().StringVar(&designator, "designator", "", "component designator, e.g. U1 (required)")
		c.Flags().StringSliceVar(&pins, "pin", nil, "pin number(s); repeat the flag or comma-separate (required)")
		c.Flags().BoolVar(&clear, "clear", false, "clear the no-connect flag instead of setting it")
		sch.AddCommand(c)
	}

	// ── select ────────────────────────────────────────────────────────────
	// schematic.select
	{
		var idsRaw string
		c := &cobra.Command{
			Use:     "select",
			Short:   "Select schematic primitives by ID",
			Args:    cobra.NoArgs,
			Example: `  easyeda sch select --ids id1,id2`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if idsRaw == "" {
					return fmt.Errorf("--ids is required")
				}
				ids, err := parseIDList(idsRaw)
				if err != nil {
					return err
				}
				return dispatch(cfg, "schematic.select", window,
					map[string]any{"primitiveIds": ids}, stdout, stderr)
			},
		}
		c.Flags().StringVar(&idsRaw, "ids", "", "primitive IDs to select — CSV: id1,id2 (required)")
		sch.AddCommand(c)
	}

	// ── drc ───────────────────────────────────────────────────────────────
	// schematic.drc.check
	{
		var strict, verbose, asJSON bool
		c := &cobra.Command{
			Use:   "drc",
			Short: "Run the official schematic DRC SDK gate (may be boolean/aggregate only)",
			Long: `Run the official schematic DRC SDK gate.

Current EasyEDA builds may return only boolean/aggregate data even when the SDK
type declares verbose per-item detail. The connector normalizes whatever the SDK
returns, but 'sch drc' must not be treated as the full UI DRC warning list.

Use 'easyeda sch check' for reconstructed per-item warnings such as floating pins
and net-marker/wire-name mismatches.

Exit code: non-zero ONLY when the fatal count (error + fatal severities) is > 0.
Warnings alone exit 0, so the design-flow S5 gate can demand "0 fatal" while
still surfacing warnings for review.`,
			Args: cobra.NoArgs,
			Example: `  easyeda sch drc
  easyeda sch drc --strict          # treat warnings as errors (SDK strict mode)
  easyeda sch drc --json            # normalized SDK result
  easyeda sch check --json          # reconstructed per-item warnings`,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runSchDrc(cfg, window, strict, verbose, asJSON, stdout, stderr)
			},
		}
		c.Flags().BoolVar(&strict, "strict", false, "treat warnings as errors (SDK strict mode)")
		c.Flags().BoolVar(&verbose, "verbose", false, "also print each violation's raw EDA object")
		c.Flags().BoolVar(&asJSON, "json", false, "emit the normalized report in the {id,type,version,ok,result} envelope (report under result)")
		sch.AddCommand(c)
	}

	// ── check ─────────────────────────────────────────────────────────────
	// schematic.check — reconstructed per-item design check (floating pins, …).
	// Fills the gap the SDK schematic DRC API can't: eda.sch_Drc.check returns
	// only an aggregate, so the itemized findings the UI panel shows are computed
	// here from primitives. Output (designator + pin numbers) feeds `sch no-connect`.
	{
		var allPages, strict, asJSON, stay bool
		var page string
		var overlapEps float64
		c := &cobra.Command{
			Use:   "check",
			Short: "Reconstructed per-item design check the SDK DRC can't itemize",
			Long: `Reconstructed per-item design check — the detail the EDA schematic DRC API can't expose.

eda.sch_Drc.check (what 'sch drc' uses) may return only a boolean/aggregate result;
the itemized findings the UI DRC panel shows are not in any public API. 'sch check'
recomputes them from primitives and the official manufacture netlist JSON.

Covered rules include net-marker/wire-name mismatches, duplicate/multiple net names
on a wire, floating pins (netlist-confirmed and geometric), wire crossings, and
wire-over-pin hazards.

The floating-pin output is the exact input 'sch no-connect' takes, so the loop is:
sch check → wire the real ones / sch no-connect the intentional ones → sch check.

Exit code: 0 by default (floating IO pins are normal until NC-marked); --strict
exits non-zero when there are any findings, to use it as a gate.

--json wraps the report in the same {id,type,version,ok,result} envelope the
other sch commands emit; the findings are under result.findings (v0.10.0+;
prior versions emitted a bare {passed,summary,findings}).`,
			Args: cobra.NoArgs,
			Example: `  easyeda sch check
  easyeda sch check --json
  easyeda sch check --strict      # non-zero exit if any findings`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if page != "" && allPages {
					return fmt.Errorf("--page and --all-pages are mutually exclusive")
				}
				if page != "" {
					scope, err := switchToPage(cfg, window, page)
					if err != nil {
						return err
					}
					if !stay {
						defer func() { _ = scope.restore(cfg) }()
					}
					window = scope.window
				}
				return runSchCheck(cfg, window, allPages, strict, asJSON, overlapEps, stdout, stderr)
			},
		}
		c.Flags().BoolVar(&allPages, "all-pages", false, "check components across all schematic pages (WARNING: non-active pages return shallow data — pins/bbox may be empty; use `doc switch` to that page for accurate data)")
		c.Flags().StringVar(&page, "page", "", "switch to this page (name|uuid), wait for it to settle, then check — makes the page an explicit parameter instead of relying on the active tab (issue #67)")
		c.Flags().BoolVar(&stay, "stay", false, "with --page, stay on the target page after checking instead of switching back")
		c.Flags().BoolVar(&strict, "strict", false, "exit non-zero when there are findings (gate mode)")
		c.Flags().BoolVar(&asJSON, "json", false, "emit the report in the {id,type,version,ok,result} envelope (findings under result.findings)")
		c.Flags().Float64Var(&overlapEps, "overlap-eps", schMarkerOverlapEps, "min positive-area extent (mm, smaller axis) for the marker-overlap/titleblock-overlap rules — below it, edge grazing and parallel-port float noise are ignored (issue #148)")
		sch.AddCommand(c)
	}

	// ── bridge-check ────────────────────────────────────────────────────────
	// schematic.bridgeCheck — tree-granularity net-vs-copper consistency gate.
	// `sch check`'s multi-net-wire rule is per SINGLE wire; when EasyEDA merges
	// collinear touching stubs of DIFFERENT nets into one tree the short spans
	// several wires and no single wire carries two names, so it under-reports.
	// bridge-check groups wires into trees (shared-vertex union-find) and
	// aggregates the netflag/netport net names per tree: >1 net → BRIDGE (real
	// short, ERROR, non-zero exit = gate); empty + touches a pin → ORPHAN (WARN).
	{
		var allPages, asJSON bool
		c := &cobra.Command{
			Use:   "bridge-check",
			Short: "Detect共线合并短路 (bridges) and孤儿桩 (orphans) at wire-tree granularity",
			Long: `Tree-granularity net-vs-copper consistency check — the盲区 'sch check' can't see.

EasyEDA merges two collinear touching stubs of DIFFERENT nets into ONE wire tree
that spans several wire primitives. No single wire then carries two net names, so
'sch check''s per-wire multi-net-wire rule under-reports the short. 'sch drc'
doesn't flag it either (the merged tree looks like an ordinary wire). Only the
"one wire tree carries several net names" data view exposes it.

bridge-check groups every page wire into trees by shared vertices (union-find),
then aggregates the net names of the netflag/netport anchored on each tree:

  • len(set(nets)) > 1                    → BRIDGE (real short)        ERROR
  • nets empty & tree touches a comp pin  → ORPHAN (dangling stub)     WARN

Each problem tree reports its wire ids / flag ids / touched pins (designator:pin)
so the fix — delete the whole tree (sch prim-delete) then re-connect each pin to
its own net (sch connect) — is actionable. This is the third pillar of the S5
verification gate: layout-lint (placement) + check/drc (structure) + bridge-check
(network-semantics vs physical-copper).

Exit code: non-zero when any BRIDGE exists (real short → gate). Orphans alone
exit 0 (they are WARN). Run it after autoconnect / manual routing as a self-heal
post-step.

NOTE: --all-pages reads non-active pages shallowly (same limit as 'sch check' /
'sch list' — pins may be empty), so cross-page trees can be under-reported; switch
to a page for authoritative results.`,
			Args: cobra.NoArgs,
			Example: `  easyeda sch bridge-check
  easyeda sch bridge-check --json
  easyeda sch bridge-check --all-pages`,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runSchBridgeCheck(cfg, window, allPages, asJSON, stdout, stderr)
			},
		}
		c.Flags().BoolVar(&allPages, "all-pages", false, "check wire trees across all schematic pages (WARNING: non-active pages return shallow data — pins may be empty; use `doc switch` to that page for accurate results)")
		c.Flags().BoolVar(&asJSON, "json", false, "emit the report in the {id,type,version,ok,result} envelope (trees under result.trees)")
		sch.AddCommand(c)
	}

	// ── read ──────────────────────────────────────────────────────────────
	// schematic.read — one-call semantic snapshot (components + pin nets + nets +
	// check), so the agent reads the whole circuit at once.
	{
		var allPages, noCheck, stay bool
		var page string
		c := &cobra.Command{
			Use:   "read",
			Short: "One-call semantic snapshot of the circuit (components + nets + check)",
			Long: `Read the whole circuit in ONE call instead of stitching components.list +
netlist + check together. Returns components (each pin tagged with its
JSON-authoritative net), nets (net → connected designator.pin keys, degree,
power/ground flag), floating pins, and the geometric design check.

Pin→net comes from the official manufacture netlist (same source as 'sch check'),
so it's authoritative, not geometry-guessed. Use --no-check to skip the design
check for a faster read.`,
			Args: cobra.NoArgs,
			Example: `  easyeda sch read
  easyeda sch read --all-pages
  easyeda sch read --no-check`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if page != "" && allPages {
					return fmt.Errorf("--page and --all-pages are mutually exclusive")
				}
				if page != "" {
					scope, err := switchToPage(cfg, window, page)
					if err != nil {
						return err
					}
					if !stay {
						defer func() { _ = scope.restore(cfg) }()
					}
					window = scope.window
				}
				payload := map[string]any{}
				if allPages {
					payload["allPages"] = true
				}
				if noCheck {
					payload["includeCheck"] = false
				}
				return dispatch(cfg, "schematic.read", window, payload, stdout, stderr)
			},
		}
		c.Flags().BoolVar(&allPages, "all-pages", false, "read components across all schematic pages (WARNING: non-active pages return shallow data — pins/bbox may be empty; use `doc switch` to that page for accurate data)")
		c.Flags().StringVar(&page, "page", "", "switch to this page (name|uuid), wait for it to settle, then read — makes the page an explicit parameter instead of relying on the active tab (issue #67)")
		c.Flags().BoolVar(&stay, "stay", false, "with --page, stay on the target page after reading instead of switching back")
		c.Flags().BoolVar(&noCheck, "no-check", false, "skip the geometric design check for a faster read")
		sch.AddCommand(c)
	}

	// ── save ──────────────────────────────────────────────────────────────
	// schematic.save
	sch.AddCommand(&cobra.Command{
		Use:   "save",
		Short: "Save the active schematic document",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cfg, "schematic.save", window, nil, stdout, stderr)
		},
	})

	// ── layout-lint ───────────────────────────────────────────────────────
	// Go-side placement check on schematic.components.list(includeBBox). The
	// "teeth" of the verify→adjust loop: detect bbox overlaps (ERROR) and
	// too-tight spacing (WARN) so layout overlap is mechanically caught, not
	// eyeballed. Exits non-zero when overlaps exist → usable as a gate.
	{
		var minGap, pinEps float64
		var asJSON, allPages, includeNonParts, strict bool
		c := &cobra.Command{
			Use:   "layout-lint",
			Short: "Check component placement for bbox overlaps and tight spacing",
			Long: `Check component placement on the schematic for overlaps and tight spacing.

Pulls every component's rendered extent (schematic.components.list --include-bbox)
and runs these placement checks in Go:

  • overlap          — two component bounding boxes intersect            → ERROR
  • pin-coincidence  — two pins of DIFFERENT parts land on the same point → ERROR
  • spacing          — bbox gap is below --min-gap (default 2.54mm)       → WARN
  • off-grid         — part anchor is not on the 5-unit connection grid   → WARN

Pin coincidence is an implicit short: any wire/stub through the shared point ties
the two nets together, yet the bboxes may never touch (a small 2-pin part tucked
against a large one), so bbox-only overlap detection misses it. Pins are compared
across different components only; a symbol's own pins are expected to sit at fixed
offsets. Use --pin-eps to treat near-coincident pins (within N mm) as errors too.

Only real parts (componentType "part") are checked by default. The drawing
sheet / title block (图框) spans the whole page, so including it would false-flag
an overlap against nearly every component; netflag/netport/netlabel and other
non-part primitives are likewise excluded. Pass --include-non-parts to score them
too (e.g. to inspect the sheet bbox).

This is the mechanical ground truth for the place→verify→adjust loop: run it
after each placement stage, fix every ERROR (move/align/distribute), then re-run.
Exits non-zero when any overlap is found, so it can gate a workflow. Pass
--strict to also fail on tight spacing, off-grid anchors, zone violations, or
components whose anchor/bbox/pin geometry was unavailable, malformed, or came
from a legacy connector that cannot prove the pin read succeeded. When zone
claims exist, --strict also fails if the active sheet is unavailable and the
zone check could not be proven. Strict proof is per active page and real parts:
combine it with neither --all-pages (inactive-page data is shallow) nor
--include-non-parts (sheet/markers are not placement bodies).`,
			Args: cobra.NoArgs,
			Example: `  easyeda sch layout-lint
  easyeda sch layout-lint --strict
  easyeda sch layout-lint --min-gap 5.08
  easyeda sch layout-lint --all-pages --json`,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runLayoutLint(cfg, window, minGap, pinEps, allPages, asJSON, includeNonParts, strict, stdout, stderr)
			},
		}
		c.Flags().Float64Var(&minGap, "min-gap", 2.54, "minimum gap between component bboxes in mm (closer = WARN)")
		c.Flags().Float64Var(&pinEps, "pin-eps", 0, "max distance in mm for two pins of DIFFERENT components to count as coincident (implicit short → ERROR); 0 = strict equality")
		c.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
		c.Flags().BoolVar(&allPages, "all-pages", false, "lint components across all schematic pages (WARNING: non-active pages return shallow data — components with no bbox are SKIPPED from overlap checks, not confirmed clear; use `doc switch` to that page for accurate linting)")
		c.Flags().BoolVar(&includeNonParts, "include-non-parts", false, "also lint non-part primitives (sheet/title-frame, netflag/netport/…); excluded by default")
		c.Flags().BoolVar(&strict, "strict", false, "per-active-page proof: also fail on tight/off-grid/zone, invalid or unproven geometry, or unavailable configured zones (incompatible with --all-pages/--include-non-parts)")
		sch.AddCommand(c)
	}

	// ── reconcile ─────────────────────────────────────────────────────────
	// S5「设计意图门」的机械化:从块库重推应有连接,与活体网表对账。
	sch.AddCommand(newSchReconcileCmd(cfg, &window, stdout, stderr))

	// ── clusters ──────────────────────────────────────────────────────────
	// L1 虚拟组判据(器件 + 它自己的 marker/桩线),核心在 cmd_sch_clusters.go。
	sch.AddCommand(newSchClustersCmd(cfg, &window, stdout, stderr))

	// ── sheet-geometry ────────────────────────────────────────────────────
	// Normalized sheet bounds + title-block keep-out (issue #26). The single
	// source placement/routing planners (autoconnect/autolayout) consume, so A4
	// coordinates aren't re-hardcoded per tool. Derives the keep-out from the
	// live sheet bbox + a known-template ratio, with explicit provenance +
	// warnings (never false precision). Pure core in cmd_sch_sheet.go.
	{
		var asJSON bool
		c := &cobra.Command{
			Use:   "sheet-geometry",
			Short: "Report sheet bounds + title-block keep-out geometry (provenance-tagged)",
			Long: `Report the schematic sheet's bounds and the title-block (图框/明细表) keep-out.

Placement/routing planners must avoid dropping flags or parts on top of the
title block. EasyEDA Pro exposes no set-paper-size API and no separate bbox for
the title block, so the geometry is DERIVED:

  • sheet bbox  — live, from the componentType "sheet" primitive
  • template    — matched best-effort by the sheet's aspect ratio (A-series ≈ √2)
  • title block — a corner sub-rect from the matched template's normalized ratio
  • visibility  — schematic.titleblock.get → showTitleBlock (hidden ⇒ no keep-out)

The result tags provenance (known-template-ratio / fallback-ratio / none) and
emits warnings instead of false precision when geometry can't be determined.
The keepouts[] format is what sch autoconnect / autolayout consume.`,
			Args: cobra.NoArgs,
			Example: `  easyeda sch sheet-geometry
  easyeda sch sheet-geometry --json`,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runSheetGeometry(cfg, window, asJSON, stdout, stderr)
			},
		}
		c.Flags().BoolVar(&asJSON, "json", false, "emit the geometry in the {id,type,version,ok,result} envelope (geometry under result)")
		sch.AddCommand(c)
	}

	// ── autoconnect ───────────────────────────────────────────────────────
	// Pin-aware deterministic connect planner: pick direction/offset by scoring
	// real geometry, then delegate to schematic.power.connect_pin. Scorer is pure
	// (cmd_sch_autoconnect.go); orchestration in cmd_sch_autoconnect_run.go.
	sch.AddCommand(newAutoconnectCmd(cfg, &window, stdout, stderr))

	// ── autolayout ──────────────────────────────────────────────────────────
	// Module-aware deterministic placement planner: partition the canvas into
	// named zones, place each module's core IC + peripherals with collision
	// retry, preserve pin-fanout channels + the title-block keep-out, and emit
	// lint-clean coordinates. Pure planner in cmd_sch_autolayout.go; I/O +
	// --apply in cmd_sch_autolayout_run.go. See issue #25.
	sch.AddCommand(newAutolayoutCmd(cfg, &window, stdout, stderr))

	// ── autoplace-free ────────────────────────────────────────────────────
	// Zone-less packer: drop movable parts into the sheet's blank space,
	// top-left first-fit, collision-free against fixed parts + title block.
	// Pure planner in cmd_sch_autoplace_free.go; I/O + --apply in
	// cmd_sch_autoplace_free_run.go. Parts-only (no wires/flags).
	sch.AddCommand(newAutoplaceFreeCmd(cfg, &window, stdout, stderr))

	// ── netlist ───────────────────────────────────────────────────────────
	// schematic.export.netlist
	{
		var netlistType string
		c := &cobra.Command{
			Use:   "netlist",
			Short: "Export schematic netlist as an artifact",
			Args:  cobra.NoArgs,
			Example: `  easyeda sch netlist
  easyeda sch netlist --type kicad`,
			RunE: func(cmd *cobra.Command, args []string) error {
				var payload map[string]any
				if netlistType != "" {
					payload = map[string]any{"netlistType": netlistType}
				}
				return dispatch(cfg, "schematic.export.netlist", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&netlistType, "type", "", "netlist format (e.g. kicad, spice, protel)")
		sch.AddCommand(c)
	}

	// ── export-image ──────────────────────────────────────────────────────
	// schematic.export.image (#166)
	{
		var idsRaw, format, scope, out, page, theme, lineWidth string
		var stay bool
		c := &cobra.Command{
			Use:   "export-image",
			Short: "Render the page — or only the given primitives — to SVG/PNG/PDF",
			Args:  cobra.NoArgs,
			Long: `Render the active schematic page, or ONLY the primitives you name, to
SVG / PNG / PDF (#166).

Why not a viewport capture (the removed ` + "`sch snapshot`" + `): that path is viewport-dependent — a
backgrounded tab never repaints, so it silently hands back the previous
full-page frame. This renders the requested primitives directly: no viewport,
no foreground requirement, no dialog. SVG is vector, so an agent can zoom into
dense wiring without resampling a blurry screenshot.

--ids selects those primitives and exports just them (the export box shrinks to
the selection). Without --ids it exports the whole active page.`,
			Example: `  easyeda sch export-image --ids id1,id2 --out block.svg
  easyeda sch export-image --format png --out page.png
  easyeda sch export-image --scope page --format pdf --page P2 --out p2.pdf`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if page != "" {
					scopeRes, err := switchToPage(cfg, window, page)
					if err != nil {
						return err
					}
					if !stay {
						defer func() { _ = scopeRes.restore(cfg) }()
					}
					window = scopeRes.window
				}
				payload := map[string]any{}
				if idsRaw != "" {
					ids, err := parseIDList(idsRaw)
					if err != nil {
						return err
					}
					payload["primitiveIds"] = ids
				}
				if format != "" {
					payload["format"] = format
				}
				if scope != "" {
					payload["scope"] = scope
				}
				if theme != "" {
					payload["theme"] = theme
				}
				if lineWidth != "" {
					payload["lineWidth"] = lineWidth
				}
				if out != "" {
					payload["fileName"] = filepath.Base(out)
				}
				res, err := dispatchCapture(cfg, "schematic.export.image", window, payload, stdout)
				if err != nil {
					return err
				}
				if out != "" {
					return saveFirstArtifact(res, out, stderr)
				}
				return nil
			},
		}
		c.Flags().StringVar(&idsRaw, "ids", "", "primitive IDs to export alone — CSV: id1,id2 (omit to export the whole page)")
		c.Flags().StringVar(&format, "format", "", "svg | png | pdf (default svg)")
		c.Flags().StringVar(&scope, "scope", "", "selection | page | project (default: selection when --ids given, else page)")
		c.Flags().StringVarP(&out, "out", "o", "", "write the rendered file to this path")
		c.Flags().StringVar(&page, "page", "", "schematic page to export (name or uuid)")
		c.Flags().StringVar(&theme, "theme", "", "Default | White on Black | Black on White")
		c.Flags().StringVar(&lineWidth, "line-width", "", "Default | Always 1px | Follow the Zoom Change")
		c.Flags().BoolVar(&stay, "stay", false, "with --page: stay on that page instead of restoring the previous one")
		sch.AddCommand(c)
	}

	// ── block-apply ──────────────────────────────────────────────────────
	// Circuit-block instantiation: the executable path for internal/blocks.
	sch.AddCommand(newSchBlockApplyCmd(cfg, &window, stdout, stderr))
	sch.AddCommand(newSchExtractLayoutCmd(cfg, &window, stdout, stderr))
	sch.AddCommand(newSchZonesCmd(cfg, &window, stdout, stderr))
	sch.AddCommand(newSchZoneDrawCmd(cfg, &window, stdout, stderr))
	sch.AddCommand(newSchZonePlanCmd(cfg, &window, stdout, stderr))
	sch.AddCommand(newSchZoneArrangeCmd(cfg, &window, stdout, stderr))
	// 电路说明文本:分区框(zone-draw)只给模块命名,note 给模块配「作用+关键参数」
	// 的一两行说明 —— 原理图布局默认约定的另一半。
	sch.AddCommand(newSchNoteCmd(cfg, &window, stdout, stderr))
	// 持久化编组(用户点名;#173 的 sch 侧):平台无编组 API(真机探测坐实),
	// easyeda-agent 自己按 documentUuid 持久化组关系,group-move / align /
	// distribute / autolayout 消费。
	sch.AddCommand(newSchGroupCmd(cfg, &window, stdout, stderr))
	// ── 三层布局体系(docs/schematic-layout-hierarchy.md):Zone 层命令族 ──
	// `sch zone move` / `sch zone tidy` — 功能区刚移(带组带件带 note+框重画)与
	// 组间叠加布局;Group 层的 `sch group tidy`(组内布局计算)挂在 group 树上。
	{
		zone := &cobra.Command{
			Use:   "zone",
			Short: "功能区层操作(三层体系:Sheet→Zone→Group)— move 整区刚移 / tidy 组间叠加布局",
		}
		zone.AddCommand(newSchZoneMoveCommand(cfg, &window, stdout, stderr))
		zone.AddCommand(newSchZoneTidyCommand(cfg, &window, stdout, stderr))
		zone.AddCommand(newSchZoneRelayoutCommand(cfg, &window, stdout, stderr))
		sch.AddCommand(zone)
		// Sheet 层(最外层):功能区依据纸张排布。
		sheet := &cobra.Command{
			Use:   "sheet",
			Short: "Sheet 层操作(三层体系最外层)— tidy 全部功能区依据纸张排布",
		}
		sheet.AddCommand(newSchSheetTidyCommand(cfg, &window, stdout, stderr))
		sch.AddCommand(sheet)
	}
	// marker-overlap 的**修复**侧(#171):检测在 `sch check`(#148),这里负责安全
	// 批量搬迁(带桩线一起挪 + 真实 check 复验 + 恶化回滚)。
	sch.AddCommand(newSchDestaggerCommand(cfg, &window, stdout, stderr))
	sch.AddCommand(newSchAlignCmd(cfg, &window, stdout, stderr))
	sch.AddCommand(newSchDistributeCmd(cfg, &window, stdout, stderr))
	// 布局质量打分(诊断视角,不是门):折叠/反向/贴核心/长链可识别,归因带可
	// 执行 fix 命令。门仍是 layout-lint + check。
	sch.AddCommand(newSchLayoutScoreCmd(cfg, &window, stdout, stderr))
	// S5 校验门:把 layout-lint / check / bridge-check / drc 收成一条固定流水线。
	// 四个单命令原样保留(专家 + 局部复查),但主干路径走 gate。
	sch.AddCommand(newSchGateCmd(cfg, &window, stdout, stderr))
	sch.AddCommand(newSchStatusCmd(cfg, &window, stdout, stderr))
	sch.AddCommand(newSchNetsCmd(cfg, &window, stdout, stderr))

	return sch
}

// warnDisconnectPartial makes a partial `schematic.pin.disconnect` unmissable.
//
// The platform delete can silently keep a stub wire (merged/collinear trees are
// the usual offenders) while returning true, so the connector now re-reads and
// reports survivors as result.partial + result.survivedIds/notApplied instead
// of lying disconnected:true. Rendering that as a plain success at the CLI
// layer would preserve the original defect — the pin is likely STILL connected.
// Follows the partial-application convention (#151): the action stays ok=true
// (the canvas did change for whatever really got deleted), the CLI surfaces a
// loud stderr warning so no caller mistakes it for a clean disconnect.
func warnDisconnectPartial(res *actionResult, stderr io.Writer) {
	if res == nil || res.Result == nil {
		return
	}
	if disconnected, ok := res.Result["disconnected"].(bool); ok && disconnected {
		return
	}
	partial, _ := res.Result["partial"].(bool)
	if !partial {
		return
	}
	var survived []string
	if raw, ok := res.Result["survivedIds"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && s != "" {
				survived = append(survived, s)
			}
		}
	}
	fmt.Fprintf(stderr, "⚠ disconnect 未完全生效:%d 个图元删除后仍存活(%s)— 该 pin 很可能仍然连着。\n",
		len(survived), strings.Join(survived, ", "))
	fmt.Fprintln(stderr, "  先用 sch nets / netlist 对账确认,再重试 disconnect 或用 sch prim-delete 显式删除存活 id;")
	fmt.Fprintln(stderr, "  合并导线树上的桩线是常见诱因(平台 delete 静默 no-op 仍返 true)。")
}

// failOnSurvivingPrimitives turns a verified-partial `schematic.primitives.delete`
// into a non-zero exit (issue #164).
//
// The handler now re-reads the page instead of echoing the requested count, so
// a primitive class that accepts the delete call and keeps the primitive shows
// up as result.partial + result.survived. Exiting 0 there would preserve the
// original defect at the CLI layer: `sch prim-delete` reported a clean sweep
// while the zone-draw labels it "removed" were still on the page (and came back
// in full after a doc reload).
func failOnSurvivingPrimitives(res *actionResult, stderr io.Writer) error {
	if res == nil || res.Result == nil {
		return nil
	}
	partial, _ := res.Result["partial"].(bool)
	if !partial {
		return nil
	}
	survived, _ := res.Result["survivedTotal"].(float64)
	fmt.Fprintf(stderr, "✗ %d primitive(s) survived the delete (settle 复核后仍在) — they are still on the page.\n",
		int(survived))
	primDeleteResidueGuidance(stderr, res)
	return errActionFailed
}

// splitPinRef parses a "DESIGNATOR:PIN" reference (e.g. "U1:5") into its parts.
func splitPinRef(ref string) (designator, pin string, ok bool) {
	parts := strings.SplitN(ref, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// connectLanded reports whether a schematic.read result already shows the pin
// as a member of net — the post-failure recheck that separates a slow-landed
// connect_pin (write applied, response lost to the timeout) from a real
// failure. Net names must match exactly (they are canonical; +3V3 vs 3V3 are
// DIFFERENT nets and must not be conflated); the member key ("U1.5") matches
// case-insensitively, mirroring readLiveNets' designator normalization.
func connectLanded(result map[string]any, designator, pin, net string) bool {
	if result == nil {
		return false
	}
	nets, ok := result["nets"].([]any)
	if !ok {
		return false
	}
	want := designator + "." + pin
	for _, n := range nets {
		m, ok := n.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := m["net"].(string); name != net {
			continue
		}
		members, ok := m["pins"].([]any)
		if !ok {
			continue
		}
		for _, p := range members {
			if s, ok := p.(string); ok && strings.EqualFold(s, want) {
				return true
			}
		}
	}
	return false
}
