.PHONY: help test mcp-test fmt actions api-index build install dev-build daemon dev eext eext-fresh connector lint-test blocks-audit layout-calibrate release publish-skill publish-skill-hub skillhub-check replay demo-replay replay-sch replay-pcb

DIST := dist

# Bare `make` prints the cheatsheet below.
.DEFAULT_GOAL := help

help: ## show this cheatsheet
	@echo "easyeda-agent — make targets"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "Daemon runs under 'make dev' (air, background). To pick up daemon code"
	@echo "changes just edit a .go file — air reloads & the connector auto-reconnects."
	@echo "Don't kill/swap daemons by hand; it wedges the connector (→ click Reconnect)."

test: ## go test ./...
	go test ./...

mcp-test: build ## install MCP deps and run unit + stdio protocol tests
	npm --prefix mcp ci --ignore-scripts
	EASYEDA_BIN="$(CURDIR)/bin/easyeda" npm --prefix mcp test

# Rule-trust harness for the schematic linter: orientation-table consistency
# (orientation.json derives to its frozenTable; matches the connector) +
# fixture goldens (known-good board stays clean, known-bad cases still fire).
lint-test: ## linter rule-trust harness (orientation + fixtures)
	python3 skills/easyeda-agent/scripts/tests/run.py
	python3 -m unittest discover -s skills/easyeda-agent/scripts/tests -p '*_test.py'

blocks-audit: ## check every block pin ref against real symbol pins (offline; --probe to refresh)
	python3 skills/easyeda-agent/scripts/blocks-pin-audit.py

# 金标准好板回归(#167 第五层)：参考板九维不该掉分,负对照九维必须还会响。
# 全离线、不连编辑器。改了 pcb_score_*.go 的判据/阈值/权重后先跑这个。
# fixture 与「怎么加一块真板」见 internal/app/testdata/boards/README.md。
layout-calibrate: ## layout-score 金标准板回归(offline; 好板不掉分 + 坏板仍报警)
	go test ./internal/app/ -run TestLayoutScore_GoldenBoards -v

fmt: ## gofmt cmd + internal
	gofmt -w cmd internal

actions: ## print the typed action catalog
	go run ./cmd/easyeda actions

# ── playbook 回放(esp32-mini 录制样例)────────────────────────────────────
# PROJECT 可覆写(默认 ceshi);moves.playbook.json 的 s7-s24 是幂等移件区间。
PROJECT ?= ceshi

replay: ## 回放 esp32-mini 移件 playbook,恢复布局(PROJECT=ceshi)
	easyeda apply examples/esp32-mini/moves.playbook.json --from 7 --to 24 --project $(PROJECT)

demo-replay: ## 演示:挪乱4件→观察→逐步回放恢复(PAUSE=30 STEP_DELAY=1.2 可覆写)
	bash examples/esp32-mini/demo-replay.sh

DOC_SCH ?= P1
DOC_PCB ?= PCB1
replay-sch: ## 阶段一:原理图从零全流程回放(PROJECT/DOC_SCH 可覆写)
	easyeda apply examples/esp32-mini/schematic.playbook.json --project $(PROJECT) --doc $(DOC_SCH) --yes

replay-pcb: ## 阶段二:PCB 从零全流程回放(PROJECT/DOC_PCB 可覆写;uniqueId 见 examples/esp32-mini/README)
	easyeda apply examples/esp32-mini/pcb.playbook.json --project $(PROJECT) --doc $(DOC_PCB) --yes

api-index: ## regenerate the embedded eda.* API index (run after bumping pro-api-types)
	python3 internal/apidoc/gen.py

# Dev version stamp: `git describe` (e.g. v0.5.1-3-g1d7b7c8[-dirty]) so a locally
# built binary reports a meaningful version via `easyeda -v` instead of "dev".
DEV_VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
DEV_LDFLAGS := -X 'github.com/zhoushoujianwork/easyeda-agent/internal/version.Version=$(DEV_VERSION)'
# Where `make install` drops the binary (matches install.sh's default).
PREFIX ?= /usr/local

build: ## build bin/easyeda (version-stamped via git describe; embeds block library)
	go build -ldflags "$(DEV_LDFLAGS)" -o bin/easyeda ./cmd/easyeda

install: build ## build + install to $(PREFIX)/bin (default /usr/local/bin; may need sudo)
	@mkdir -p "$(PREFIX)/bin" 2>/dev/null || true
	@if install -m 0755 bin/easyeda "$(PREFIX)/bin/easyeda" 2>/dev/null; then \
		printf '✅ installed → %s/bin/easyeda  (%s)\n' "$(PREFIX)" "$(DEV_VERSION)"; \
	else \
		echo "  $(PREFIX)/bin not writable — retrying with sudo…"; \
		sudo install -m 0755 bin/easyeda "$(PREFIX)/bin/easyeda" && \
		printf '✅ installed → %s/bin/easyeda  (%s)\n' "$(PREFIX)" "$(DEV_VERSION)"; \
	fi

dev-build: ## (air hook) version-stamped build to bin + best-effort refresh of the PATH CLI
	@go build -ldflags "$(DEV_LDFLAGS)" -o bin/easyeda ./cmd/easyeda
	@install -m 0755 bin/easyeda "$(PREFIX)/bin/easyeda" 2>/dev/null \
		&& printf '  ↻ PATH CLI refreshed → %s/bin/easyeda (%s)\n' "$(PREFIX)" "$(DEV_VERSION)" \
		|| printf '  ⚠ PATH CLI NOT refreshed (%s/bin not writable) — run `make install` once with sudo\n' "$(PREFIX)"

daemon: ## one-shot daemon (no reload) — prefer `make dev`
	go run ./cmd/easyeda daemon

# Live-reload the daemon for development (.air.toml): rebuilds + restarts on any
# .go change; the connector auto-reconnects (it retries 60832 with backoff). Keep
# this running in a terminal while developing so the daemon is always up.
dev: ## hot-reload the daemon (air) — mirrors output to tmp/daemon.log (truncated each start)
	@command -v air >/dev/null 2>&1 || { echo "air not found — install: go install github.com/air-verse/air@latest"; exit 1; }
	@mkdir -p tmp
	@# Kill any leftover daemon+watcher from a prior session so we always bind 60832.
	@pkill -TERM -f '/easyeda daemon' 2>/dev/null || true
	@sleep 0.4
	air 2>&1 | tee tmp/daemon.log

# Build the connector .eext at the CURRENT version (no bump).
connector: ## build .eext at the current version/uuid (no bump)
	npm --prefix extension run build

# Cut an importable connector .eext (default: STABLE uuid). Bump PATCH + typecheck
# + build. EasyEDA dedups installed extensions by uuid, so to load this you update
# in place: uninstall the old one in EasyEDA's 已安装 tab, then import the printed
# .eext. Keeps ONE extension entry. Use `make eext-fresh` only if the installed
# one won't uninstall.
eext: ## bump patch + build importable .eext (STABLE uuid; uninstall old → import)
	node extension/scripts/bump.mjs patch
	npm --prefix extension run typecheck
	npm --prefix extension run build
	@printf '\n✅ uninstall old in 已安装, then import → extension/build/dist/easyeda-agent-connector_v%s.eext\n' "$$(node -p "require('./extension/extension.json').version")"

# Fallback only: mint a FRESH uuid so it imports as a NEW extension with no
# uninstall — but it leaves a duplicate "EasyEDA Agent" entry you must delete
# afterward (else multiple connectors fight over the daemon).
eext-fresh: ## bump patch + FRESH uuid (imports as new entry; delete the old one)
	node extension/scripts/bump.mjs patch --uuid
	npm --prefix extension run typecheck
	npm --prefix extension run build
	@printf '\n✅ fresh-uuid build → import extension/build/dist/easyeda-agent-connector_v%s.eext, then DELETE the old entry\n' "$$(node -p "require('./extension/extension.json').version")"

# ── Release ───────────────────────────────────────────────────────────────────
# Usage: make release VERSION=v0.2.0
# Prerequisites:
#   1. gh CLI logged in (gh auth login)
#   2. connector built: make eext   (only needed when connector changed)
#   3. repo is public or you have release permissions
#
# What it does:
#   • cross-compiles CLI for darwin/linux/windows (amd64 + arm64)
#   • copies the latest .eext from extension/build/dist/
#   • tarballs the merged easyeda-agent skill into skills.tar.gz
#   • creates a git tag, pushes it, and creates a GitHub Release with all assets
#   • publishes the skill to ClawHub at the same version (best-effort — a hub
#     outage won't fail the release; retry with `make publish-skill VERSION=…`)
_LDFLAGS = -s -w -X 'github.com/zhoushoujianwork/easyeda-agent/internal/version.Version=$(VERSION)'

release: ## cross-compile + package + GitHub Release  (VERSION=vX.Y.Z required)
ifndef VERSION
	$(error VERSION is required — usage: make release VERSION=v0.5.1)
endif
	@echo "── Building release $(VERSION) ──"
	rm -rf $(DIST) && mkdir -p $(DIST)
	@echo "  syncing connector version to $(VERSION)..."
	node extension/scripts/bump.mjs $(VERSION:v%=%) --require-changelog
	@echo "  syncing skill version to $(VERSION)..."
	@# SKILL.md 的 metadata.version 不会被 clawhub/gh 自动更新 —— 不同步就漂移。
	python3 scripts/sync-skill-version.py $(VERSION:v%=%)
	@# 上面两步会改工作区(extension.json / package.json / SKILL.md)。**必须在打 tag
	@# 之前提交**,否则 tag 指向的 commit 里版本号还是旧的 —— v1.1.1 就这么发出去过:
	@# .eext 产物是 1.1.1(bump 在打包之前),但 `git show v1.1.1:extension/extension.json`
	@# 是 1.1.0,从 tag 检出源码构建会得到落后一个 patch 的连接器。
	@if ! git diff --quiet -- extension/extension.json extension/package.json skills/easyeda-agent/SKILL.md; then \
		echo "  committing version sync..."; \
		git add extension/extension.json extension/package.json skills/easyeda-agent/SKILL.md && \
		git commit -q -m "chore(release): sync version files to $(VERSION)" && \
		echo "    committed"; \
	else \
		echo "  version files already in sync"; \
	fi
	npm --prefix extension run typecheck
	npm --prefix extension run build
	@echo "  compiling CLI..."
	GOOS=darwin  GOARCH=amd64  go build -ldflags "$(_LDFLAGS)" -o $(DIST)/easyeda_darwin_amd64      ./cmd/easyeda
	GOOS=darwin  GOARCH=arm64  go build -ldflags "$(_LDFLAGS)" -o $(DIST)/easyeda_darwin_arm64      ./cmd/easyeda
	GOOS=linux   GOARCH=amd64  go build -ldflags "$(_LDFLAGS)" -o $(DIST)/easyeda_linux_amd64       ./cmd/easyeda
	GOOS=linux   GOARCH=arm64  go build -ldflags "$(_LDFLAGS)" -o $(DIST)/easyeda_linux_arm64       ./cmd/easyeda
	GOOS=windows GOARCH=amd64  go build -ldflags "$(_LDFLAGS)" -o $(DIST)/easyeda_windows_amd64.exe ./cmd/easyeda
	@echo "  packaging connector..."
	@EEXT=$$(ls extension/build/dist/*.eext 2>/dev/null | sort -V | tail -1); \
	 [ -n "$$EEXT" ] || { echo "connector build failed"; exit 1; }; \
	 cp "$$EEXT" $(DIST)/easyeda-agent-connector.eext && echo "  $$EEXT → connector.eext"
	@echo "  packaging skills..."
	tar --exclude='*/__pycache__' --exclude='*.pyc' -czf $(DIST)/skills.tar.gz -C skills easyeda-agent
	cp install.sh $(DIST)/install.sh
	@echo "  hashing assets..."
	@# checksums.txt is what `easyeda update` verifies the downloaded binary
	@# against before swapping it in. Names must stay BARE (no dist/ prefix) —
	@# the updater matches them against the release asset name.
	@cd $(DIST) && { command -v sha256sum >/dev/null 2>&1 && SHA=sha256sum || SHA="shasum -a 256"; } && \
	 $$SHA easyeda_darwin_amd64 easyeda_darwin_arm64 easyeda_linux_amd64 easyeda_linux_arm64 \
	       easyeda_windows_amd64.exe easyeda-agent-connector.eext skills.tar.gz install.sh > checksums.txt && \
	 echo "  checksums.txt ($$(wc -l < checksums.txt | tr -d ' ') entries)"
	@echo "  creating GitHub release..."
	git tag -a $(VERSION) -m "Release $(VERSION)" 2>/dev/null || echo "  (tag $(VERSION) already exists, reusing)"
	git push origin $(VERSION)
	@awk '/^## \[$(VERSION:v%=%)\]/{f=1} f&&/^## \[/&&!/^## \[$(VERSION:v%=%)\]/{exit} f' extension/CHANGELOG.md > $(DIST)/changelog-section.md
	@{ \
		cat $(DIST)/changelog-section.md; \
		printf '\n---\n\nAlready installed? Upgrade in place:\n```\neasyeda update          # CLI binary (sha256-verified) + skill dirs\neasyeda update --check  # report only\n```\n\nFirst install:\n```\ncurl -fsSL https://raw.githubusercontent.com/zhoushoujianwork/easyeda-agent/main/install.sh | sh\n```\n\nInstalls/updates:\n- easyeda CLI/daemon\n- easyeda-agent skill for Codex (~/.codex/skills) and/or Claude Code (~/.claude/skills) when detected\n- prints EasyEDA connector .eext import URL\n\nThe connector .eext is never auto-updated for sideloads — `easyeda update` reports a stale one and prints the re-import URL.\n\nSkill targets: set `EASYEDA_INSTALL_SKILLS=codex,claude` to force targets, `none` to skip, or `EASYEDA_SKILL_PRESERVE=1` to keep local edits.\n\n`checksums.txt` lists sha256 for every asset above.\n'; \
	} > $(DIST)/release-notes.md
	gh release create $(VERSION) \
		$(DIST)/easyeda_darwin_amd64 \
		$(DIST)/easyeda_darwin_arm64 \
		$(DIST)/easyeda_linux_amd64 \
		$(DIST)/easyeda_linux_arm64 \
		$(DIST)/easyeda_windows_amd64.exe \
		$(DIST)/easyeda-agent-connector.eext \
		$(DIST)/skills.tar.gz \
		$(DIST)/install.sh \
		$(DIST)/checksums.txt \
		--title "easyeda-agent $(VERSION)" \
		--notes-file $(DIST)/release-notes.md
	@echo "  publishing skill to ClawHub..."
	@$(MAKE) publish-skill VERSION=$(VERSION) \
		|| echo "  ⚠️  ClawHub publish failed — retry with: clawhub login && make publish-skill VERSION=$(VERSION)"
	@echo "✅ Released: https://github.com/zhoushoujianwork/easyeda-agent/releases/tag/$(VERSION)"

# 单独发布 skill 到 ClawHub(release 失败后重试用)。
# 注意:必须用 $(CURDIR) 绝对路径 —— clawhub 的 workdir 可能被全局配置(如 ~/clawd)
# 劫持,相对路径 skills/easyeda-agent 会解析到别处、把旧副本发上去(0.8.1 踩过)。
# ClawHub 版本号不可覆盖,重名直接报错;版本与 repo tag 对齐(去掉 v 前缀)。
#
# 发现性 tags:ClawHub 的 tags 是发布时随版本上传的 dist-tag 映射({tag:version},
# 兼作 listing/搜索的 topic 标签;来源=CLI --tags,不是 SKILL.md frontmatter)。
# --tags 会**整体覆盖**默认值,所以必须显式带上 latest,否则 latest 指针不更新、
# `clawhub install` 装到旧版。已发布的历史版本无法补挂 tag(CLI 无 tag 子命令,
# 版本也不可覆盖)——tags 只能随下一次发版生效。纯 ASCII(服务端对中文 tag 的
# 校验未知,别拿正式发版赌;中文关键词「嘉立创」走 SKILL.md description 供向量搜索)。
CLAWHUB_TAGS := latest,easyeda,jlceda,jlc,eda,circuit,schematic,pcb,hardware
publish-skill: ## publish skills/easyeda-agent to ClawHub  (VERSION=vX.Y.Z required)
ifndef VERSION
	$(error VERSION is required — usage: make publish-skill VERSION=v0.8.2)
endif
	@find $(CURDIR)/skills/easyeda-agent -name '__pycache__' -type d -prune -exec rm -rf {} + 2>/dev/null; \
		find $(CURDIR)/skills/easyeda-agent -name '*.pyc' -delete 2>/dev/null; true
	clawhub publish $(CURDIR)/skills/easyeda-agent --slug easyeda-agent --version $(VERSION:v%=%) \
		--tags "$(CLAWHUB_TAGS)" \
		--changelog "easyeda-agent $(VERSION) — https://github.com/zhoushoujianwork/easyeda-agent/releases/tag/$(VERSION)"

# ── skillhub.cn ───────────────────────────────────────────────────────────────
# 正常路径是 CI 自动发:`gh release create`(make release 的一步)发出 release →
# .github/workflows/publish-skill.yml 的 `release: published` 触发 → 跑本目标。
# 本目标存在的意义是**手动补发**(CI 挂了 / 后补版本),本地跑法:
#     export SKILLHUB_TOKEN=skh_xxx        # 别写进任何文件,别 echo
#     make publish-skill-hub VERSION=v1.0.3
#
# 为什么要 staging 副本而不是直接发 skills/easyeda-agent:
#   两套规范**互斥**,同一个 SKILL.md 不可能同时满足 ——
#     • skillhub 硬性要求 frontmatter 顶层有 slug + displayName(缺一个 die)
#     • 官方 Agent Skills 规范(npx skills-ref validate)**明确拒绝**这两个字段
#       (Unexpected fields in frontmatter: displayName, slug)
#   所以:repo 里的 SKILL.md 保持 spec 干净(给 skills-ref / Claude Code 用),
#   发布前拷一份到临时目录、只往副本的 frontmatter 里注入这两个键。
#   注入是**幂等**的 —— 哪天 SKILL.md 自己带了 slug/displayName,这步自动跳过。
#
# 版本号:走 CLI 的 `--version` 覆盖(实测存在,官网教程没写),所以不依赖
# SKILL.md 的 metadata.version,tag 就是唯一版本源。必须是合法 SemVer(去 v 前缀)。
# 认证:skillhub CLI **原生读 SKILLHUB_TOKEN 环境变量**
#   (优先级 --token > SKILLHUB_TOKEN > ~/.skillhub/credentials.json),
#   所以 CI 里不需要跑 `skillhub login`,更不用把 token 落到磁盘。
# 幂等性:同 slug 同 version 重复发会被服务端拒(和 ClawHub 一样版本不可覆盖),
#   补发请升版本号。发布后进 pending_review 审核队列,不是立刻可见。
SKILLHUB_HOST ?= https://api.skillhub.cn
# slug 与立创插件市场的连接器条目同名(jlc-ext 的 easyeda-agent-connector,displayName
# "EDA Agent Connector")——品牌统一。**slug 一旦发布就锁死,改不了**,覆盖用
# `make publish-skill-hub SKILLHUB_SLUG=…`。原 `easyeda-agent` 在 skillhub 上是
# 发不进去又查不到的孤儿记录(publish 报 already exists / verify 报 404),故换名。
SKILLHUB_SLUG ?= eda-agent-connector
SKILLHUB_DISPLAY_NAME ?= EDA Agent Connector
# SKILLHUB_DRY_RUN=1 只做本地预检(不需要 token、不发 HTTP),用来验证打包/元数据。
SKILLHUB_DRY_RUN ?=
# SKILLHUB_BIN=/path/to/skillhub 显式指定用哪个 CLI(仍然要过身份校验,不是后门)。
SKILLHUB_BIN ?=

# ── skillhub CLI 身份校验(机械检查,不是注释)──────────────────────────────
# 为什么非做不可:`skillhub` 这个 bin 名被**两个不同项目**占用,而且都可能同时
# 在 PATH 上 ——
#   A) npm/homebrew 的 `skillhub`(skills.palebluedot.live,Node):publish 只有
#      --namespace/--visibility/--registry,**没有** --version/--host。
#   B) skillhub.cn 官方(Python,~/.local/bin/skillhub → ~/.skillhub/skills_store_cli.py)。
# 实测这台开发机上 A 在 PATH 里**排在 B 前面**,只做 `command -v skillhub`
# 存在性检查会静默选中 A,然后炸在 `unknown flag: --version`。更坏的情况是
# 参数恰好兼容 —— 那就会**静默发布到错误的 registry**,比报错危险得多。
#
# 判据:探 `<bin> publish --help`,要求同时暴露我们**真正会传的**那几个 flag。
# 把判据绑定到「用得上的能力」而不是版本号字符串:将来官方改签名会在这里硬失败,
# 而不是静默降级成少传一个 --changelog 就发出去了。
# 不靠路径判断 —— 用户机器布局会变。
define SKILLHUB_RESOLVE_PY
import os, pathlib, shlex, subprocess, sys

REQUIRED = ("--version", "--host", "--changelog", "--dry-run")

def argv_for(raw):
    p = pathlib.Path(raw).expanduser()
    if not p.is_file():
        return None
    if p.suffix == ".py":
        return [sys.executable or "python3", str(p)]
    if os.access(str(p), os.X_OK):
        return [str(p)]
    return None

def probe(argv):
    env = dict(os.environ, SKILLHUB_SKIP_SELF_UPGRADE="1")
    try:
        r = subprocess.run(argv + ["publish", "--help"],
                           capture_output=True, text=True, timeout=90, env=env)
    except Exception as exc:
        return False, "探测失败: " + str(exc)
    out = (r.stdout or "") + (r.stderr or "")
    if r.returncode != 0:
        first = next((l.strip() for l in out.splitlines() if l.strip()), "(无输出)")
        return False, "跑不起来 (rc=" + str(r.returncode) + "): " + first
    missing = [f for f in REQUIRED if f not in out]
    if missing:
        return False, "publish 不支持 " + " ".join(missing) + " -> 是同名的另一个项目,不是 skillhub.cn 官方 CLI"
    return True, ""

explicit = os.environ.get("SKILLHUB_BIN", "").strip()
if explicit:
    candidates = [explicit]
else:
    candidates = ["~/.local/bin/skillhub", "~/.skillhub/skills_store_cli.py"]
    for d in os.environ.get("PATH", "").split(os.pathsep):
        if d:
            c = os.path.join(d, "skillhub")
            if c not in candidates:
                candidates.append(c)

report = []
for cand in candidates:
    argv = argv_for(cand)
    if argv is None:
        report.append((cand, "不存在或不可执行"))
        continue
    ok, why = probe(argv)
    if ok:
        wrapper = pathlib.Path(sys.argv[1])
        wrapper.write_text("exec " + " ".join(shlex.quote(a) for a in argv) + ' "$$@"\n',
                           encoding="utf-8")
        print("  skillhub CLI: " + " ".join(argv) + "  (identity OK)")
        raise SystemExit(0)
    report.append((cand, why))

sys.stderr.write("error: 没找到 skillhub.cn 官方 CLI。\n")
sys.stderr.write("  判据: publish 必须同时支持 " + " ".join(REQUIRED) + "\n")
for cand, why in report:
    sys.stderr.write("  探测 " + cand + "\n           -> " + why + "\n")
if explicit:
    sys.stderr.write("\n你显式设了 SKILLHUB_BIN=" + explicit + ",但它没通过身份校验。\n")
    sys.stderr.write("下一步: unset SKILLHUB_BIN,或把它指向官方 CLI。\n")
else:
    sys.stderr.write("\n下一步: curl -fsSL https://skillhub.cn/install/install.sh | bash -s -- --cli-only\n")
sys.stderr.write("然后: export SKILLHUB_BIN=$$HOME/.local/bin/skillhub\n")
sys.stderr.write("(注意 npm/homebrew 上的 'skillhub' 是同名的另一个项目,装它没用)\n")
raise SystemExit(1)
endef
export SKILLHUB_RESOLVE_PY

# 解析出的 CLI 会被写成一个无 shebang 的 bash 包装脚本(所以用 `bash <wrapper>`
# 调用)——这样 `python3 xxx.py` 这种两段式 argv 不用在 shell 里做引号杂技。
skillhub-check: ## 检查 PATH 上的 skillhub 是不是 skillhub.cn 官方 CLI(机械校验)
	@W=$$(mktemp -t skillhub-bin.XXXXXX); \
	trap 'rm -f "$$W"' EXIT; \
	python3 -c "$$SKILLHUB_RESOLVE_PY" "$$W"

# frontmatter 注入脚本。用 `define` + `export` 走环境变量传给 python3 -c ——
# **不能用 heredoc**:recipe 里的反斜杠续行会被 make 拼成一行,heredoc 当场失效。
define SKILLHUB_INJECT_PY
import sys, pathlib
path, slug, display = pathlib.Path(sys.argv[1]), sys.argv[2], sys.argv[3]
lines = path.read_text(encoding="utf-8").split("\n")
if not lines or lines[0].strip() != "---":
    sys.exit("error: SKILL.md 首行不是 ---(没有 frontmatter,skillhub 发不了)")
end = next((i for i in range(1, len(lines)) if lines[i].strip() == "---"), -1)
if end < 0:
    sys.exit("error: SKILL.md frontmatter 缺少结束标记 ---")
have = {l.partition(":")[0].strip() for l in lines[1:end] if ":" in l}
add = [k + ": " + v for k, v in (("slug", slug), ("displayName", display)) if k not in have]
if add:
    lines[1:1] = add
    path.write_text("\n".join(lines), encoding="utf-8")
print("  staged frontmatter + " + (", ".join(add) if add else "(already present, skipped)"))
endef
export SKILLHUB_INJECT_PY

# staging 里删「无扩展名的文件」(那条 `! -name '*.*'`)——服务端按扩展名白名单收文件,
# 无扩展名的一律 400。实测真发时报 `不允许的文件类型: LICENSE`。
# **dry-run 抓不到这条**:它只做本地 metadata 校验+打包,不碰服务端的文件类型规则,
# 所以别看 dry-run 绿了就以为能发 —— 这个坑只有真发才踩得到。
# 删 LICENSE 不影响规范合规:frontmatter 的 `license: MIT` 是许可证**名**而非文件引用
# (Agent Skills spec 两种都允许),repo 原件也照常带着 LICENSE,只是不进上传包。
publish-skill-hub: ## publish skills/easyeda-agent to skillhub.cn  (VERSION=vX.Y.Z required)
ifndef VERSION
	$(error VERSION is required — usage: make publish-skill-hub VERSION=v1.0.3)
endif
	@if [ -z "$(SKILLHUB_DRY_RUN)" ] && [ -z "$$SKILLHUB_TOKEN" ]; then \
		echo "error: SKILLHUB_TOKEN 未设置(skh_ 开头的 API Token)。"; \
		echo "  建 token: https://skillhub.cn/dashboard/keys"; \
		echo "  用法: export SKILLHUB_TOKEN=skh_xxx && make publish-skill-hub VERSION=$(VERSION)"; \
		echo "  (绝不要把 token 写进文件或 echo 出来)"; \
		exit 1; \
	fi
	@set -e; \
	STAGE=$$(mktemp -d -t skillhub-pkg.XXXXXX); \
	trap 'rm -rf "$$STAGE"' EXIT; \
	python3 -c "$$SKILLHUB_RESOLVE_PY" "$$STAGE/skillhub-bin"; \
	SH="bash $$STAGE/skillhub-bin"; \
	cp -R $(CURDIR)/skills/easyeda-agent "$$STAGE/$(SKILLHUB_SLUG)"; \
	find "$$STAGE/$(SKILLHUB_SLUG)" -name '__pycache__' -type d -prune -exec rm -rf {} + 2>/dev/null || true; \
	find "$$STAGE/$(SKILLHUB_SLUG)" -name '*.pyc' -delete 2>/dev/null || true; \
	find "$$STAGE/$(SKILLHUB_SLUG)" -type f ! -name '*.*' -delete 2>/dev/null || true; \
	python3 -c "$$SKILLHUB_INJECT_PY" "$$STAGE/$(SKILLHUB_SLUG)/SKILL.md" "$(SKILLHUB_SLUG)" "$(SKILLHUB_DISPLAY_NAME)"; \
	echo "  skillhub dry-run..."; \
	$$SH publish "$$STAGE/$(SKILLHUB_SLUG)" --version $(VERSION:v%=%) --host $(SKILLHUB_HOST) --dry-run; \
	if [ -n "$(SKILLHUB_DRY_RUN)" ]; then echo "  SKILLHUB_DRY_RUN=1 — 到此为止,未发布"; exit 0; fi; \
	echo "  publishing to $(SKILLHUB_HOST)..."; \
	$$SH publish "$$STAGE/$(SKILLHUB_SLUG)" --version $(VERSION:v%=%) --host $(SKILLHUB_HOST) \
		--changelog "easyeda-agent $(VERSION) — https://github.com/zhoushoujianwork/easyeda-agent/releases/tag/$(VERSION)"
