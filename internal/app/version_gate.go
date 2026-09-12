package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/selfupdate"
	"github.com/zhoushoujianwork/easyeda-agent/internal/version"
)

// ── 版本一致性门 (issue #181 复盘第 2 条) ──────────────────────────────────
//
// 用户实测:CLI 已升级而 daemon 仍跑旧构建,行为不一致 —— 而且
// 「明明修过的 bug 又复现」会把**后续每一条排查都染上噪音**,一轮排查白烧。
// 所以这条不是提示级别的问题,是「先把地基对齐再谈别的」。
//
// 三个版本各自的来源:
//   - CLI       : internal/version.Version(ldflags 注入,`make dev-build` 每次重建刷新)
//   - daemon    : /health 的 version 字段 —— 同一份二进制,但很可能是**另一个进程**
//                 (启动时那份构建),这正是错位的高发处
//   - connector : /health 每个 window 的 connectorVersion(连接器注册时上报)
//
// 判据分级(非对称,理由见 README 式说明):
//
//   daemon:任何差异(含 patch)→ **拒绝**。
//     它跟 CLI 是同一份二进制、同一个版本号,发布时**必然相等**;不等只可能是
//     「老进程没重启」这一种意外。修复代价近乎零(air 自己会重启 / 换个终端),
//     漏判代价却是整轮排查的噪音 —— 所以宁可拦。
//
//   connector:major/minor 不一致 → **拒绝**;仅 patch 不一致 → **通过**。
//     它走的是**另一条分发渠道**:插件市场没有发布 API,每次发版靠人工重投,
//     所以「市场版落后 CLI 一点」是多数用户的**常态**(CLAUDE.md 明确记着这条)。
//     修复代价也高得多 —— 卸载 → 重导入 → **完全退出重启 EasyEDA**,还可能丢
//     未存盘的编辑。patch 发布按约定不含连接器 runtime 改动,因此同 minor 直接兼容;而
//     minor 以上落后意味着连接器可能**根本没有**这版 CLI 要调的 handler,
//     那种"静默走偏"比打断更贵。
//
// dev 戳豁免(CLAUDE.md 硬约束,不能破):`make dev` 下二进制由 git describe 打戳
// (v1.1.1-19-g…-dirty),它的 semver core 是**旧 tag**,不代表真实代码水位。
// 只要 CLI / daemon 任一侧不是 clean release tag,就**绝不做硬判定**。唯一的
// 例外是「两边都是 dev 戳、但戳串不同」——那不是 false flag,是两个不同 dev
// 构建的实锤,给 warn(仍不拦)。

const (
	versionSevOK      = "ok"
	versionSevWarn    = "warn"
	versionSevBlock   = "block"
	versionSevSkipped = "skipped"
)

// envSkipVersionCheck is the environment equivalent of --skip-version-check,
// for callers that cannot add a flag (scripts, MCP adapters, CI).
const envSkipVersionCheck = "EASYEDA_SKIP_VERSION_CHECK"

// versionFinding is one component's verdict against the running CLI.
type versionFinding struct {
	Component string `json:"component"` // daemon | connector
	Version   string `json:"version"`
	Severity  string `json:"severity"` // ok | warn | block | skipped
	Reason    string `json:"reason"`
	Fix       string `json:"fix,omitempty"`
}

// versionGateReport is the whole three-way verdict, also surfaced by
// `easyeda health` as the "versionGate" block.
type versionGateReport struct {
	CLI        string           `json:"cli"`
	Daemon     string           `json:"daemon,omitempty"`
	Connectors []string         `json:"connectors,omitempty"`
	Verdict    string           `json:"verdict"` // ok | warn | block | skipped
	Findings   []versionFinding `json:"findings,omitempty"`
}

// evaluateVersionGate is the PURE core: given the three version strings it
// returns the graded verdict. No I/O, no globals — the whole rule set is
// unit-testable from here.
func evaluateVersionGate(cli, daemon string, connectors []string) versionGateReport {
	if selfupdate.IsLocalVersion(normVersion(cli)) {
		return evaluateLocalRuntime(cli, daemon, connectors)
	}
	rep := versionGateReport{CLI: strings.TrimSpace(cli), Daemon: strings.TrimSpace(daemon)}
	rep.Findings = append(rep.Findings, daemonFinding(cli, daemon))

	seen := map[string]bool{}
	for _, c := range connectors {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		rep.Connectors = append(rep.Connectors, c)
		rep.Findings = append(rep.Findings, connectorFinding(cli, c))
	}
	rep.Verdict = worstSeverity(rep.Findings)
	return rep
}

// daemonFinding grades the CLI ↔ daemon pair: any clean-release difference
// blocks (they ship as ONE version; a difference can only mean a stale
// process), dev stamps never block.
func daemonFinding(cli, daemon string) versionFinding {
	f := versionFinding{Component: "daemon", Version: strings.TrimSpace(daemon)}
	switch {
	case f.Version == "":
		f.Severity = versionSevSkipped
		f.Reason = "daemon 未上报版本,无法判定"
	case !selfupdate.IsCleanRelease(cli) || !selfupdate.IsCleanRelease(daemon):
		// dev 戳豁免。两边都是 dev 戳且戳串不同 → 这不是 false flag,是实锤。
		if !selfupdate.IsCleanRelease(cli) && !selfupdate.IsCleanRelease(daemon) &&
			normVersion(cli) != normVersion(daemon) {
			f.Severity = versionSevWarn
			f.Reason = fmt.Sprintf("CLI 与 daemon 都是 dev 构建但戳不同(CLI %s / daemon %s)—— daemon 很可能没跟上最近一次重建",
				display(cli), display(daemon))
			f.Fix = fixDaemonStale
			return f
		}
		f.Severity = versionSevSkipped
		f.Reason = fmt.Sprintf("dev 构建(CLI %s / daemon %s),git-describe 戳不代表真实代码水位,不做硬判定",
			display(cli), display(daemon))
	case normVersion(cli) == normVersion(daemon):
		f.Severity = versionSevOK
		f.Reason = fmt.Sprintf("与 CLI 同版 %s", display(cli))
	default:
		f.Severity = versionSevBlock
		f.Reason = fmt.Sprintf("CLI %s ≠ daemon %s —— 正在跑的是旧构建的 daemon:你「已经修好的 bug」会照样复现,之后每一条排查都会被这层噪音污染",
			display(cli), display(daemon))
		f.Fix = fixDaemonStale
	}
	return f
}

// connectorFinding grades the CLI ↔ connector pair: major/minor drift blocks
// (the connector may simply not have the handler this CLI calls), while patch
// drift is compatible by release policy (patches contain no connector runtime
// changes and the marketplace channel structurally lags).
func connectorFinding(cli, connector string) versionFinding {
	f := versionFinding{Component: "connector", Version: strings.TrimSpace(connector)}
	cliCore, connCore := selfupdate.SemverCore(cli), selfupdate.SemverCore(connector)
	switch {
	case !selfupdate.IsCleanRelease(cli) || connCore == "":
		f.Severity = versionSevSkipped
		f.Reason = fmt.Sprintf("CLI %s 或连接器 %s 非 clean release tag,不做硬判定",
			display(cli), display(connector))
	case cliCore == connCore:
		f.Severity = versionSevOK
		f.Reason = fmt.Sprintf("与 CLI 同版 %s", display(cli))
	case sameMajorMinor(cliCore, connCore):
		f.Severity = versionSevOK
		f.Reason = fmt.Sprintf("CLI %s 与 connector %s 同属 major.minor 兼容线;patch 发布不要求升级插件市场版本",
			display(cli), display(connector))
	default:
		f.Severity = versionSevBlock
		f.Reason = fmt.Sprintf("CLI %s ≠ connector %s(差 minor 及以上)—— 连接器可能根本没有这版 CLI 要调的 handler,动作会静默走偏",
			display(cli), display(connector))
		f.Fix = fixConnectorStale
	}
	return f
}

const fixDaemonStale = `重启 daemon(它跑的是启动那一刻的构建):
  · 开发中(air):切到跑 ` + "`make dev`" + ` 的终端 —— 任何 .go 改动它都会自动重建+重启;
    若它已经退出,重新开一个 ` + "`make dev`" + `。**别手动 kill air 下的 daemon**(会卡死连接器)。
  · 非开发:直接 ` + "`easyeda daemon start`" + ` —— 它会自动接管 60832 上的旧 easyeda daemon,
    不需要你先去 kill。
确认:` + "`easyeda health`" + ` 的 version 应与 ` + "`easyeda version`" + ` 一致。`

const fixConnectorStale = `重装连接器 .eext(跨 major/minor 兼容线时需要):
  1. 下载 latest .eext:https://github.com/` + versionGateRepoSlug + `/releases/latest
  2. EasyEDA「扩展管理 → 已安装」**先卸载旧的**(uuid 相同,不卸载直接导入会静默失败)
  3. 导入新的 .eext
  4. **完全退出并重启 EasyEDA** —— 重导入不会重载已开窗口,旧窗口会继续跑旧代码并抢 daemon
  (插件市场版可原地自动更新但可能滞后;同 major.minor 的 patch 差异无需处理。)`

// versionGateRepoSlug is the GitHub owner/repo shipping the connector .eext.
const versionGateRepoSlug = "zhoushoujianwork/easyeda-agent"

// sameMajorMinor reports whether two semver cores share major.minor.
func sameMajorMinor(a, b string) bool {
	ap, bp := strings.Split(a, "."), strings.Split(b, ".")
	if len(ap) != 3 || len(bp) != 3 {
		return false
	}
	return ap[0] == bp[0] && ap[1] == bp[1]
}

// normVersion strips whitespace and a leading 'v' for literal comparison.
func normVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// display renders a version for a message, defaulting to "(unknown)".
func display(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "(unknown)"
	}
	if !strings.HasPrefix(v, "v") && selfupdate.SemverCore(v) != "" {
		return "v" + v
	}
	return v
}

// worstSeverity folds findings into one verdict: block > warn > ok > skipped.
func worstSeverity(findings []versionFinding) string {
	rank := map[string]int{versionSevSkipped: 0, versionSevOK: 1, versionSevWarn: 2, versionSevBlock: 3}
	worst := versionSevSkipped
	for _, f := range findings {
		if rank[f.Severity] > rank[worst] {
			worst = f.Severity
		}
	}
	return worst
}

// blockingFindings returns only the findings that refuse the run.
func (r versionGateReport) blockingFindings() []versionFinding {
	var out []versionFinding
	for _, f := range r.Findings {
		if f.Severity == versionSevBlock {
			out = append(out, f)
		}
	}
	return out
}

// warningFindings returns only the findings that warn without refusing.
func (r versionGateReport) warningFindings() []versionFinding {
	var out []versionFinding
	for _, f := range r.Findings {
		if f.Severity == versionSevWarn {
			out = append(out, f)
		}
	}
	return out
}

// versionGateFromHealth builds the report straight from a /health body — the
// SAME bytes postAction already fetched to find the daemon, so the gate costs
// ZERO extra round-trips.
func versionGateFromHealth(raw []byte) versionGateReport {
	var parsed struct {
		Version string `json:"version"`
		Windows []struct {
			ConnectorVersion string `json:"connectorVersion"`
		} `json:"windows"`
	}
	if json.Unmarshal(raw, &parsed) != nil {
		return versionGateReport{CLI: version.Version, Verdict: versionSevSkipped}
	}
	conns := make([]string, 0, len(parsed.Windows))
	for _, w := range parsed.Windows {
		conns = append(conns, w.ConnectorVersion)
	}
	return evaluateVersionGate(version.Version, parsed.Version, conns)
}

// ── 拦截点:每进程一次,在第一条真正要走连接器的动作之前 ────────────────────
//
// 拦在 postAction(CLI 的唯一派发咽喉)上,而不是每条命令各查一次:
//   - 它已经为了找 daemon 做过 /health 扫描,报文里三个版本齐全 → **零额外往返**;
//   - 一个进程只判一次(sync.Once):复合命令(pcb check / report)一次跑几十个
//     action,重复刷同一条告警就等于没有告警;
//   - `easyeda health` / `version` / `update` / `daemon start` 都不经过 postAction,
//     所以**诊断与修复路径永远不会被自己拦死** —— 这是选这个点最重要的理由。

var (
	versionGateOnce   sync.Once
	versionGateCached error
)

// checkVersionGate refuses the dispatch when a blocking mismatch is present,
// prints warnings once, and honours the audited escape hatch. Evaluated once
// per process; later calls replay the cached decision.
func checkVersionGate(cfg *appConfig, healthRaw []byte, stderr io.Writer) error {
	if selfupdate.IsLocalVersion(normVersion(version.Version)) {
		// Re-check each fresh health snapshot: a reconnect must not inherit a pass.
		rep := versionGateFromHealth(healthRaw)
		if rep.Verdict != versionSevOK {
			return fmt.Errorf("local runtime mismatch: %v", rep.Findings)
		}
		return nil
	}
	versionGateOnce.Do(func() {
		versionGateCached = runVersionGate(cfg, healthRaw, stderr)
	})
	return versionGateCached
}

// runVersionGate is the one-shot body of checkVersionGate (separated so tests
// can drive it without the process-wide Once).
func runVersionGate(cfg *appConfig, healthRaw []byte, stderr io.Writer) error {
	rep := versionGateFromHealth(healthRaw)
	skip := versionCheckSkipped(cfg)

	for _, f := range rep.warningFindings() {
		fmt.Fprintf(stderr, "⚠ 版本错位(%s):%s\n%s\n", f.Component, f.Reason, indentFix(f.Fix))
	}

	blocking := rep.blockingFindings()
	if len(blocking) == 0 {
		return nil
	}
	if skip {
		for _, f := range blocking {
			fmt.Fprintf(stderr, "⚠ 版本错位(%s)已被 --skip-version-check 放行:%s\n", f.Component, f.Reason)
		}
		auditVersionCheckSkip(rep)
		return nil
	}
	var b strings.Builder
	b.WriteString("版本一致性门:拒绝执行 —— 工具链版本错位\n")
	for _, f := range blocking {
		fmt.Fprintf(&b, "\n  ✗ %s %s\n    %s\n%s\n", f.Component, display(f.Version), f.Reason, indentFix(f.Fix))
	}
	fmt.Fprintf(&b, "\n  当前:CLI %s | daemon %s", display(rep.CLI), display(rep.Daemon))
	if len(rep.Connectors) > 0 {
		fmt.Fprintf(&b, " | connector %s", strings.Join(rep.Connectors, ", "))
	}
	b.WriteString("\n  明知故犯:加 --skip-version-check(或 " + envSkipVersionCheck + "=1)强行继续 —— 会写一行审计。")
	return fmt.Errorf("%s", b.String())
}

// indentFix indents a multi-line fix block under its finding.
func indentFix(fix string) string {
	if strings.TrimSpace(fix) == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(fix, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "    " + l
	}
	return strings.Join(lines, "\n")
}

// versionCheckSkipped reports whether the escape hatch is engaged, from the
// --skip-version-check flag or the env var.
func versionCheckSkipped(cfg *appConfig) bool {
	if cfg != nil && cfg.skipVersionCheck {
		return true
	}
	switch strings.TrimSpace(os.Getenv(envSkipVersionCheck)) {
	case "", "0", "false", "no":
		return false
	}
	return true
}

// auditVersionCheckSkip records every bypass as its own JSONL row, mirroring
// the daemon's `daemon.stale_read.force` discipline (stalereads.go): a guard
// that can be silently bypassed is indistinguishable from a guard that never
// fired. The pseudo-action name is never a real catalog action, so per-action
// call/failure statistics stay intact while remaining greppable.
//
// Best-effort: an audit failure must never break the dispatch path.
func auditVersionCheckSkip(rep versionGateReport) {
	dir := os.Getenv("EASYEDA_AUDIT_DIR")
	if dir == "" {
		// Same rule as the daemon's writer: never append test fixtures to the
		// user's real audit log.
		if testing.Testing() {
			return
		}
		dir = defaultAuditDir()
	}
	blocked := make([]string, 0, 2)
	for _, f := range rep.blockingFindings() {
		blocked = append(blocked, f.Component+" "+display(f.Version))
	}
	now := time.Now().UTC()
	entry := map[string]any{
		"ts":       now.Format(time.RFC3339Nano),
		"clientId": cliClientID(),
		"action":   "cli.version_check.skip",
		"ok":       true,
		"result": map[string]any{
			"cli":        rep.CLI,
			"daemon":     rep.Daemon,
			"connectors": rep.Connectors,
			"blocked":    blocked,
		},
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, now.Format("2006-01-02")+".jsonl"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_ = json.NewEncoder(f).Encode(entry)
}

// versionGateSummary renders the one-line human verdict `easyeda health`
// prints alongside its JSON, so the same judgement is readable without
// re-deriving it from the report.
func versionGateSummary(rep versionGateReport) string {
	switch rep.Verdict {
	case versionSevBlock:
		return "✗ 版本一致性:错位,动作会被拒 —— 见 versionGate.findings[].fix"
	case versionSevWarn:
		return "⚠ 版本一致性:有落后组件(不拦)—— 见 versionGate.findings[].fix"
	case versionSevOK:
		return "✓ 版本一致性:CLI / daemon 同版,connector major.minor 兼容 " + display(rep.CLI)
	default:
		return "· 版本一致性:未判定(dev 构建或无连接器上报版本)"
	}
}
