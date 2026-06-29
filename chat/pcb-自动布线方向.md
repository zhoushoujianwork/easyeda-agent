# PCB 自动布线方向

> 首次记录：2026-06-29。关联：survey §7.3 / §7.4、task #5。

## 起点问题

> 「官方有自动布线功能菜单，是不是可以采用？如果没暴露，也可以像 PCB DRC 那样向官方提 issue 对不对？」

## 关键区分：UI 有菜单 ≠ 有可编程 API

这是最容易踩的认知坑。EasyEDA 的自动布线菜单是给**人在 UI 里点**的，
背后不一定经过 `eda.*` 暴露的可编程入口。

**铁证**：官方自己的 `eext-kirouting-integration`（接 KiCad Rust A* 自动布线）
**也不调 `autoRouting()`**——它走**文件式往返**：导出 DSN → 外部布线引擎跑 → import 回写。
**连官方的自动布线扩展都绕开 typed API 走文件式**，基本坐实了
`pcb_Document.autoRouting` 在当前 build 不可用（§7.3 实测 3.2.148 为 `undefined`，
类型声明标 `@alpha`）。

## 三条路（按价值排序）

### ① 文件式往返（官方同款范式）— 「现在就能用自动布线」的真答案 🔥
导出 `getDsnFile` / `getAutoRouteJsonFile` → 跑外部布线器（KiCad A* 或 FreeRouting）
→ `import` 回写。**不用等官方、不用平台改任何东西**，这正是官方 kirouting 的做法。
是 R5 的真正可落地形态，比干等 `autoRouting()` GA 强得多。**主动路径，优先。**

### ② 向官方提 issue 请求暴露 `autoRouting()` — 该做 ✅ 但被动
和 PCB DRC 那次一样的玩法。**前提**：先在当前 connector build 用 `debug.exec_js`
实测一遍——万一新 build 已经把它从 undefined 变成可用了呢？确认仍 undefined 再提 issue，
请求把 `autoRouting`/`autoLayout` 经 `eda.*`(或 run-api-gateway) GA 暴露。
**提了不阻塞，同时推进 ①。**

### ③ 已有的坐标级布线(R2) + 约束/报告/DRC 门禁
单路避障、推挤、等长蛇形这些平台没 API，agent 做不了（§7.4 硬边界）。
老实写进 conventions：自动布线交给 ① 的外部引擎或人。

## 结论

- **别把宝押在等官方暴露 API 上。** 提 issue(②) 对但被动；文件式往返(①) 官方验证过、今天能动手、是主动路径。
- task #5 拆三步：**实测确认 → 提 issue(②) → 同时起文件式 POC(①)**。
- 实测第一步需 EasyEDA 已开 +「允许外部交互」（当前 `health` 的 `windows` 为空，连接器未连）。

---

## 2026-06-29 追加：撤回「提 issue」，改为接官方 Freerouting 路径

查官方 GitHub 后**结论翻转**——② 提 issue 是错的 ask，删掉。

### 决定性证据：官方早有程序化自动布线，故意不走 typed API

`easyeda` 组织里有一整套**专门做自动布线**的仓库，全是外部引擎 + WS/文件往返：

| 仓库 | 是什么 |
|---|---|
| **`easyeda-pcb-router`** | 官方 CLI/WebSocket 自动布线服务，基于 **Freerouting**（砍 GUI 改 headless），web 编辑器经 WS 连它布线 |
| `eext-freerouting-intergration` | Freerouting 自动布线扩展 |
| `eext-kirouting-integration` | KiCad Rust A* 自动布线扩展 |

**没有一个走 `eda.autoRouting()`**；`pro-api-sdk` 内搜 `autoRouting`/`自动布线` **无任何「请暴露」issue**。

### 推断

- 官方是**故意**把自动布线架在外部引擎上的——真正的迷宫搜索布线器太重，不该塞进 `eda.*`
  这种确定性 CRUD API 面。所以「请暴露 `autoRouting()` typed API」会被官方回「我们用
  Freerouting，不走那条」，**没价值还显得没做功课**。
- 之前 survey「`autoRouting` 本 build undefined」的观察没错，但**结论应是「这条路官方就不打算给」**，
  而非「等它 GA」。

### 新方向（task #5 已改）

**接官方 Freerouting 路径**，而非提 issue：
1. 扒 `easyeda-pcb-router` 的 `script.js` 用了哪些 `eda.*` 写回 API（多半是
   `pcb_PrimitiveLine.create` 等我们**已有**的）；
2. 评估把该引擎接进我们 daemon 的工程量（同构：都是 WS 桥接）；
3. 出文件式往返 POC（导出 DSN → Freerouting → import 回写）。

> 路径 ①（文件式往返）= 官方现成实现，可落地、不等任何人。路径 ②（提 issue）作废。
> 路径 ③（坐标级 R2 + 约束/报告/DRC 门禁）不变。

---

## 2026-06-29 再追加：扒官方仓库后的缺口分析（集成很小）

本地克隆到 `~/github/easyeda-upstream/`：`easyeda-pcb-router`(GPLv3, Freerouting headless 服务)、
`eext-freerouting-intergration`(Apache-2.0, EasyEDA 侧扩展)、`eext-kirouting-integration`(KiCad A*)。

### Freerouting 集成数据流（已确认）

```
pcb_ManufactureData.getDsnFile('design.dsn')   → 导出 Specctra DSN（Freerouting 原生输入）
  → sys_ClientUrl.request POST 给本地服务(easyeda-pcb-router 默认 127.0.0.1:3579)
  → 服务 headless 跑 Freerouting → 返回 SES(Specctra Session，含布好的线)
  → SESImporter.ts 解析 SES → pcb_PrimitiveLine.create / pcb_PrimitiveVia.create 写回
  → startCalculatingRatline + pcb_Drc.check 刷飞线/校验；rip-up = getAllPrimitiveId + delete
```

整个官方扩展布线逻辑仅 ~445 行（SESImporter **62** / Router 292 / API 91）。

### 缺口对照（我们已有 30 个 PCB action）

| 需要 | 我方 | 缺口 |
|---|---|---|
| 写回线/过孔 | ✅ `pcb.line.create`/`pcb.via.create`(A2) | 无 |
| rip-up/清布线 | ✅ `route.rip_up`/`clear_routing`(R2) | 无 |
| 读 nets/components/layers/DRC/report | ✅ 全有 | 无 |
| 重算飞线 | ✅ `import_changes` 已 recompute ratline | 无（必要时补独立开关） |
| **导出 DSN** `getDsnFile` | ❌ | **低**：connector handler + typed action + `pcb export-dsn` |
| **解析 SES 写回** | ❌ | **低-中**：移植 SESImporter(62 行, Apache-2.0) |
| POST 给布线服务 | —— | **daemon 直接 HTTP**，不过 connector（架构优势） |

### 关键判断

1. 新增的只有 **DSN 导出 + SES 解析** 两件，都小；写回原语全有。
2. **License 干净**：扩展 Apache-2.0 可移植；Freerouting 引擎 GPLv3 但**进程外 WS/HTTP 调用**
   （arms-length，用户自起 server），不构成衍生链接，与官方同款模型。
3. **唯一真机依赖**：实测当前 build `getDsnFile` 可用（官方扩展在用，基本稳）；其余纯本地开发。

### POC 落地形态（task #5）

`easyeda pcb autoroute` → daemon：① `pcb.export.dsn` → ② POST `:3579` → ③ 收 SES →
④ 解析 → 批量 `pcb.line.create/via.create`(复用) → ⑤ `Drc.check` 验收。

### 2026-06-29 真机探针（connector 0.5.21 / EasyEDA 3.2.149）—— 路打通

`debug.exec_js` 实测 typeof：

| API | 结果 |
|---|---|
| `pcb_Document.autoRouting` / `autoLayout` | **undefined** —— 仍没暴露，死路确认，不提 issue 不等它 |
| **`pcb_ManufactureData.getDsnFile`** | **function** ✅ —— DSN 导出可用，#5 唯一真机未知项正面解决 |
| `pcb_ManufactureData.getSesFile` | undefined —— 无妨，SES 由 Freerouting 服务回，自解析 |
| `pcb_Document.startCalculatingRatline` | function ✅ |
| `pcb_PrimitiveLine/Via.create` | function ✅ 写回原语确认 |
| `sch_ManufactureData.getNetlistFile` | function ✅（#1 A7 源） |

**结论：Freerouting 路全打通，#5 可开工**（DSN 导出 action + SES 解析器 + autoroute 编排）。
