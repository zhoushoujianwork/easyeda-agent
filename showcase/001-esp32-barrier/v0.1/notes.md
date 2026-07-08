# v0.1 进行中笔记(2026-07-08)

## 已完成
- S0 方案书 4 决策确认(全按推荐)→ spec.json
- 6 参考电路研究(authoritative,RF 双源验证)→ reference-circuits.json
- 35 种器件入 standard-parts.json(11 主料 + 24 通用料;24nH 缺货 0Ω 代)
- EasyEDA 工程 `esp32-barrier`(uuid 20d01dd0070c45c4989d903b6b544fa2),P1+P2 两页 A4
- 103 件放置+位号(P1 45 / P2 58),autolayout 过 lint(0 overlap)
- 317 条 autoconnect 全落地(P1 154 / P2 163),netlist-plan.md 是蓝图

## 当前问题(修复中)
1. **P2 71 脚黏连 blob**:3V3+GND+RS485_A 等被物理桥接成一张网(read v2 复现,非 stale)。
   形态=flag 符号骑到邻居 stub 上(check 的 multi-net-wire 只抓到 2 条 RS485_A/GND,
   flag-骑-线形态检不出——**工具盲区,记 finding**)。
2. **P2 器件压标题栏**:U6 簇+VDD_CC cap 排在明细表上(autolayout P2 避让失效——finding)。
3. **read 网表只含激活页**:跨页验证需分页 read 手动并(memory --all-pages 坑印证)。

## 修复方案(按序)
1. 建 P3,把 CC1101 模块(U6/X2/巴伦滤波 11 件/VDD_CC 供电 10 件/J_ANT2,共 ~26 件)
   迁过去:disconnect 其 stub → 删件 → P3 重放+位号 → autolayout → 重跑其 autoconnect 子集
   (spec 在 scratchpad/barrier-p2-connect.json,按 designator 过滤即可)。
2. P2 剩余件重新 autolayout(密度减半后标题栏避让应恢复)→ 桥点几何扫描
   (debug exec:wire+flag bbox 触碰图)→ 逐点 disconnect/重连。
3. 三页分别 read → 名字并网 → 对照 connect spec 全量核对(317 期望)。
4. S5 门:layout-lint + drc + check 三页全绿 → sch save → **确认点②停给用户**。

## 2026-07-08 晚间战况(下一会话从这里继续)

**布线陷入 flag 相叠泥潭后引擎罢工,已定位全部根因,待用干净配方一次重建:**

- 现状:103 件三页放置+位号 ✓;P3 布线 ✓(72);P1 曾 154/154 全过但现有 3 条混线
  (含 3V3×VDD33F——L102 滤波节点被短接);P2 经多轮手术后仍有 GND、GND 冗余旗;
  **整个文档 getNetlistFile 返回 0 网(引擎罢工,浏览器 reload 无效)**。
- 根因链(全部实证):① autoconnect 批量对**同件双脚**给同向同长 stub → 旗并排相叠
  → 每颗电容自短路;② 每列 10 间距 pin 的 netport 标签(宽 31)必然相叠;
  ③ 修复脚本宽半径匹配错线造成二次伤害;④ 多轮 replace/disconnect 后引擎被坏原语毒死。
- **下一会话配方(确定性,勿再迭代手术)**:
  1. 逐页 wipe 全部 wire+flag(getAll 枚举 → prim-delete 分批;活动页作用域)。
  2. wipe 后先验网表引擎复活(0 线时 read 应返回 0 网且不报错)。
  3. 重布线用**方向分治**:power 一律 up、gnd 一律 down(不同半平面永不相叠)、
     netport 左右出+列内 18/42/66 轮转;pin 坐标用每 pin 一次 autoconnect --dry-run
     解析 end=(x,y)∓offset 得到,然后 `sch connect --x --y --direction --offset` 显式落笔。
  4. 三页各自 read(网表只含激活页!)→ 按名并网 → 对照 spec 全量核对(317)。
  5. S5 三门 → 确认点②交用户。
- CLI 改进票(待开):autoconnect 缺方向提示/批内标签避碰/同件双脚自动分向;
  netlist 引擎对坏原语静默返 0(应报错);flag 无法 modify 只能删建。


## 2026-07-08 深夜二轮(重建进行时)

- **旧 schematic1 文档级死亡确诊**:全部线/flag 清空+浏览器 reload+双脚探针后 getNetlistFile 仍返 NULL
  ——文档不可救,重大 upstream 素材。新建 schematic(barrier-clean,uuid 486692a1b118a25c,
  页 p1=b62eb903dd4165bf / p2=99ab802416b6ea60 / p3=7b58fee69858ebaf)后引擎立即复活(探针 1 网 ✓)。
- 新文档:103 件三页重放+位号+autolayout 全绿 ✓。
- **避碰布线器**(scratchpad 脚本,dry-run 反推 pin 坐标 + 三档冲突模式 + 24 步长阶梯):
  P1 118/154 求解器落笔;36 个死角回退 autoconnect 单发 → 又引入 8 条混线(3V3×GND 在列)。
  教训:**回退路径必须也走避碰**;正确续法=对 8 条混线用并查集扫描定位→拆桥→用求解器(而非
  autoconnect)在放宽阶梯(>306)下重下这 36 个死角 pin。
- P2(91)/P3(72)未重布,直接用修好的求解器跑(先 dry-run 生成 plans)。
- 布线器脚本位置:见本轮会话 scratchpad;固化 TODO:成熟后进 skills/scripts/。


## 2026-07-09 凌晨三轮(原理图收官)

**三页电气干净达成**(以 EasyEDA 几何 check 为准):P1/P2/P3 跨网混线 0、dangling 0、
悬空=备用脚白名单完全吻合(U1×19 备用 GPIO / J2 B6-B8 单取向+SBU / U3 modem 7 脚)。

关键新根因(全部实证,按发现顺序):
1. **列蛇线**:求解器方向回退在引脚列上画垂直桩 → auto-merge 熔成穿多 pin 的科学怪线
   (QFN 两条、U3 两条)——pin 障碍模型(±4)补丁后不再产生。
2. **pin-to-pin 放置级短路**:autolayout 帽阵零间隙排列使相邻电容引脚物理重合
   (P1 两对 3V3↔GND、P2 一对 GND↔RS485_A)——即放置本身就短路,与布线无关!
   修法=挪件重接;autolayout 需要 pin 间隙约束(CLI 票)。
3. **引脚网络戳陈旧**:线删了 pin 上的 net 戳还在 → autoconnect 幂等检查误报
   "already connected"(CLI 票)。
4. autoconnect 兜底=桥接制造机(6/6 复现)——兜底必须走避碰求解器,铁律。

**上游重磅**:getNetlistFile 文档级死亡二次复现(prim-delete 重手术后引擎静默返 0,
wipe+浏览器重载不可恢复;check 几何引擎不受影响)——两引擎权威性分家。
成员级网表验证移交 PCB 阶段(add-component 手喂 spec + DRC 连通把关)。

**下一步(确认点②之后)**:P0-P10 PCB 阶段,pinmap 用 spec.json + netlist-plan.md,
add-component --nets 手喂;确认点②证据=check 三页终态 + 本笔记根因链。

## 关键文件
- 连线蓝图:netlist-plan.md;autoconnect spec:scratchpad/barrier-p{1,2}-connect.json
- 引脚字典:scratchpad/barrier-p{1,2}-read.json(U1 QFN56:GPIO15/16=pin21/22,GPIO48=pin36,
  GPIO39-42=MTCK/MTDO/MTDI/MTMS=pin44/45/47/48;U6:SO=2,SI=20;J3:CLK 拼作 CLX,DAT1 拼作 CAT1)
