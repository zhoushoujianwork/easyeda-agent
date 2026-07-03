# 验收用例 — ESP32-S3 **芯片级** N8R8 最小系统板(天线 + flash + PSRAM)

> **状态:验收目标(task #35),尚未端到端跑通。** 这是比
> [`esp32MiniRequire.md`](../esp32MiniRequire.md)(模组开发板原始需求)更硬的回归基准——
> 用**裸 ESP32-S3 芯片**(不是 WROOM 模组、不用现成模板)从零搭最小系统,把
> `pcb check` 的**走线**(非正交 / 压焊盘)和**丝印正反**规则、天线 keep-out、
> 晶振布局全压满。跑通标准见文末验收清单。

## 为什么要一块「芯片级」板

模组板(WROOM-1)把天线、晶振、flash 都封装进模组,agent 只要摆一个大方块 + 电源就完事,
压不到真实难点。**芯片级**逼出模组挡掉的所有约束:

- **板载天线 + 匹配网络** —— PCB IFA/倒 F 天线或陶瓷天线 + π 型匹配(C-L-C),天线区
  必须 keep-out(禁铜/禁布线),且馈线走 50Ω 阻抗。
- **N8R8** = **8MB flash(N8)+ 8MB PSRAM(R8)**。ESP32-S3 的 R8 八线 PSRAM 走 SPI0,
  对走线长度/等长敏感。
- **40MHz 晶振** —— 贴近芯片、GND 包地、负载电容对称。
- **EN / IO0(boot straps)** —— 上电 RC 复位 + boot 上拉。

## BOM(芯片级,**需先补进 `standard-parts.json`**)

| 位号 | 器件 | 说明 |
|---|---|---|
| U1 | **ESP32-S3**(裸芯片,QFN-56) | 主控,内置无 flash/psram → 外挂 |
| U2 | **8MB flash**(如 W25Q64,SOP-8/USON) | N8 |
| U3 | **8MB PSRAM**(八线,如 APS6404L / 乐鑫 R8 die) | R8;注意与 flash 共用 SPI0 |
| Y1 | 40MHz 晶振(3225) | 主时钟 |
| C(load) ×2 | 晶振负载电容(如 12pF 0402) | 对称贴 Y1 |
| ANT1 | PCB 天线 / 陶瓷天线 | 板载天线,下方 keep-out |
| C/L (match) ×3 | π 型匹配网络(C-L-C 0402) | 天线阻抗匹配,预留可不贴 |
| 去耦 ×N | 100nF/0402 × 每个电源脚 + 10µF 体电容 | 3V3 就近 |
| R1/R2 | 10k(EN 上拉 / IO0 boot 上拉) | straps |
| C3 | 100nF(EN 复位 RC) | 上电复位 |

> 供电假设同模组板:板外提供已稳压 3.3V(3V3/GND flag 接入)。USB/LDO 是可选扩展,不属核心。

## 网络要点

- **3V3 / GND** —— 全部电源脚去耦,GND 铺铜 + 天线区避让。
- **SPI0**(flash + PSRAM 共用):CLK / CMD(D1) / D0..D3 /(八线再加 D4..D7)/ 两片 CS。
  高速,尽量短、少过孔;八线 PSRAM 尽量等长。
- **XTAL_P / XTAL_N** —— 晶振差分,包地。
- **天线馈线** —— RF 脚 → π 匹配 → 天线,50Ω,下方 + 周边 keep-out。
- **EN**(R1 上拉 + C3 到 GND)、**IO0**(R2 上拉)。

## 跑测流程(复用 PCB 流程脊柱 + 芯片级约束)

沿用 `skills/easyeda-agent/references/design-flow.md` 的 PCB 脊柱 P0–P10,额外强约束:

| 阶段 | 芯片级额外约束 |
|---|---|
| P1 板框+禁区 | **天线区 keep-out**(禁铜/禁布线/禁过孔),天线外沿净空;板边 clearance |
| P2 布局 | 晶振/负载电容贴紧芯片且对称;flash/PSRAM 靠近 SPI0 脚;去耦贴每个电源脚;天线区净空 |
| P4 布线 | SPI0 短且尽量等长;晶振差分包地;天线馈线 50Ω 不穿 keep-out |
| P6 校验门 | `pcb drc` 0 fatal + **`pcb check` 0 ERROR** |

## 验收清单(跑通标准)

- [ ] 器件齐(芯片 + flash + PSRAM + 晶振 + 天线 + 匹配 + 去耦 + straps),全部来自
      `standard-parts.json`(先补芯片级选型)。
- [ ] 网络连通:3V3 / GND / SPI0 各线 / XTAL / EN / IO0 全通,0 未连。
- [ ] `sch layout-lint` 0 overlap;`sch check` 0 fatal。
- [ ] 板框 + **天线 keep-out** 就位;`pcb drc` keep-out 被尊重、0 fatal、无 Connection Error。
- [ ] **`pcb check` 0 ERROR**:
      - **silkscreen-flipped = 0**(无丝印正反 / 放反);
      - **track-over-pad = 0**(无走线压焊盘短路);
      - non-orthogonal(自由角度走线)尽量清零,至少人工确认;
      - dangling / acute / via / duplicate 归零。
- [ ] BOM 导出后 `bom-enrich.py` 补全 LCSC C 号,可下单。
- [ ] 已 `save` 落盘。

## 备注

- 测试工程用 `ceshi`(一次性,可清空重来);测完清理还原。
- **前置**:`standard-parts.json` 目前只有模组级 ESP32-S3-WROOM-1,没有裸芯片 / 外挂
  flash / PSRAM / 天线器件——**必须先做芯片级选型并写回**,否则本用例无法起步。
- 与模组板(#34)互补:模组板证明「大方块 + 电源」闭环,芯片板证明「天线 / 高速 /
  晶振 / 丝印正反」这些真实工程约束下 agent + `pcb check` 的把关能力。
