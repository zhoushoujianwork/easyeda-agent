# 板级吸收记录 — M5Stack StickS3 (K150)

> ESP32-S3 掌上控制器,从官方公开原理图对标吸收。**PCB 无公开工程/gerber**
> (`M5_Hardware/Products/K150_StickS3` 仅有外壳 `StickS3.stl`),故 PCB 侧只从
> 原理图注释 + 尺寸图反推布局/贴边/尺寸约束。
>
> **来源**:M5Stack StickS3 原理图 PDF V0.6 20251111(4 页,官方 docs 站公开)。
> 与 `docs/test-case-esp32-chip-n8r8.md`、`pcb-layout-conventions.md §7.8-7.9`
> 的三板对标基准互补——StickS3 是**第 4 块对标板**:极密集消费成品形态。

## 为什么吸收它(相对现有库的净新增)

StickS3 强在**咱们完全没有的品类**,正好补库空白:

| 净新增 | StickS3 提供 | 现有库状态 |
|---|---|---|
| 原生 USB 下载(无桥芯片) | ESP32-S3-PICO 原生 USB,22R+共模扼流,RC 复位 | 只有 CH340 桥 + 双三极管自动下载 |
| 音频(codec+功放+mic) | ES8311 + AW8737A + MEMS mic + I2C 隔离 | ❌ 无 `audio` 类目 |
| 显示(SPI TFT) | ST7789P3 BTB 接口 + 背光开关 | ❌ 无 `display` 类目 |
| IMU | BMI270 I2C | ❌ |
| 红外收发 | VSOP38338 + IR928 | ❌ |
| 锂电分立电源路径 | LGS4056 + CH213K + 电池 ADC 门控 | 只有 bq24074/AXP2101 集成方案 |
| 升压 | SY7088 boost | 只有 buck/buck-boost |
| I2C 域隔离 | 2N7002DW 双向 MOSFET | ❌ |
| USB-C 双取向数据 | A6+B6/A7+B7 tie 实锤 | 部分(usbc_ufp_power_or 仅电源) |

## 电源架构参考:PY32 单片机充当 PMIC(不做块,固件私有)

**关键认知**:文档里叫 "M5PM1" 的 PMIC 真身是 **`PY32L020F15U6`**——一颗普雅
(Puya)PY32 单片机跑私有电源固件充当 PMIC。硬件可仿(PY32L020 LCSC 有货),
**但价值全在固件里**,无固件即死芯片 → **只作架构参考,不入块库**。

多电源域(PMIC 固件调度):

```
L0 Shipping  → L1 Standby → L2 DeepSleep → L3A CoreActive → L3B AllActive
(4.2V@14µA)   (52µA)        (102µA)         (36mA)           (519mA 满载)
```

电源树(PMIC 管使能/检测):
```
USB Type-C ──▶ AW32901 保护(OVP 5V) ──▶ LGS4056 充电 ──▶ CH213K 电源路径 OR ──▶ VBUS_L0
                                            ▲                          │
                                         锂电 250mAh ──────────────────┘
VBUS_L0 ──┬─ CM1801 LDO(常开) ──▶ 3V3_L0
          ├─ CM1801 LDO(L1门控) ─▶ 3V3_L1  (IMU)
          ├─ JW5712 DCDC 600mA ──▶ 3V3_L2  (ESP32-S3 主轨/KEY)
          ├─ CM1801 ─▶ 3V3_L3B_AU  (音频专用干净轨)
          ├─ AW35122 负载开关 ────▶ 3V3_L3B / LCD / LCD_BL
          └─ SY7088 boost 1A ─────▶ 5V ─▶ TPS22916/AW35122(EXT_5V_EN)─▶ GROVE/Hat/IR
```

PMIC 职责:CHG_EN / EXT_5V_EN / DCDC/LDO/BOOST 各使能 + 5VIN/VBAT/EXT_5V 多路
ADC 检测 + 唤醒源(KEY1/2、IMU_INT、CHG_STAT、IRQ)+ 电源键长按硬关机 + 状态 LED。
**用 MCU 当 PMIC 的价值** = 可编程电源策略 + 超低待机(L1 52µA);硬件可仿,固件私有。

## 吸收的电路块(11 个,`internal/blocks/data/`,均 draft)

来源 `oshwhub-ref:M5Stack StickS3 K150 原理图 PDF V0.6 20251111`。引脚按**功能名**绑定,
deviceUuid/LCSC 全 TBD,待真机 `--probe` 刷符号脚 + `block-apply` 网表对账后升 verified。

| 块 id | 类目 | 一句话 |
|---|---|---|
| `esp32s3_pico_native_usb` | mcu | 🔥 S3-PICO SiP 最小系统 + 原生 USB 下载(无桥):22R+共模扼流+RC 复位+G0 strap |
| `es8311_codec_i2s` | audio★ | ES8311 单声道 codec(I2S+I2C),ADC↔mic / DAC↔功放,AGND 单点地 |
| `aw8737_classd_spk` | audio★ | AW8737A D/K 类功放,电荷泵,SHDN 脉冲增益,输出磁珠+EMI 电容 |
| `mems_mic_analog` | audio★ | MSM381 模拟 MEMS mic,单端→伪差分给 codec,干净音频轨 |
| `st7789_spi_lcd_btb` | display★ | ST7789P3 SPI 屏 BTB 接口 + 背光 MOSFET(驱动 IC 在 FPC 上) |
| `bmi270_imu_i2c` | sensing | BMI270 6 轴 IMU,CSB→VDDIO 选 I2C,INT 唤醒 |
| `ir_txrx_remote` | comms | VSOP38338 接收 + IR928 发射(NMOS 驱动),两半独立可 DNF,RX 须走 RMT |
| `sy7088_boost_5v` | power | SY7088 升压 VBUS→5V(1A),FB 分压,配负载开关 |
| `lgs4056_liion_charge_path` | power | LGS4056 充电 + CH213K 分立电源路径 + 电池 ADC 门控(bq24074 分立替代) |
| `i2c_isolation_2n7002dw` | mcu-support | 2N7002DW 双向 I2C 电平/域隔离(NXP AN10441),外设断电不拉挂主总线 |
| `usbc_dual_orientation_data` | usb | USB-C 双取向 tie(A6+B6/A7+B7)+ 5.1K CC + ESD5311 |

★ = 本次为块库新增的 `audio`/`display` 类目(已同步 `_block.schema.json` enum + `validate.go`)。

## PCB / conventions 增量(从原理图注释反推,无 gerber)

- **USB-C 双取向 tie 再实锤**:DP1(A6)+DP2(B6)、DM1(A7)+DM2(B7) 都 tie → 正反插都通。
  第 4 块官方板一致用双取向,进一步推翻"省 B6/B7"权宜。见 `design-decisions.md §接口取向`。
- **纯原生 USB 无桥芯片**:S3 内置 USB-Serial/JTAG,D+/D- 直连 C 口,**无 CH340、无 DTR/RTS
  自动下载管**(原生 USB CDC 软复位进下载)。这是 USB 架构的**第三选项**(现有文档只列
  单通道-CH340 / 双通道-HUB)——极密集/成本敏感的 S3 消费品可整颗省掉桥芯片。
- **AGND 单点接地**:模拟地经 0R 单点桥到数字地(R35),回流走 LDO 芯片衬底
  ("AGND回流LDO芯片衬底单点接地")。印证 `§7.9` 实战派 S3 的 R38 0402 单点桥模式。
- **去耦/复位就近**:"电容靠近管脚"、"RC 靠近芯片,电容优先靠近"、"ADC 电容靠近引脚放置"。
- **USB 差分**:D+/D- 各 22R 串阻 + 过 SDMM0806 共模扼流圈再出连接器,90Ω 差分等长。
- **D 类功放输出 EMI**:SPK_P/SPK_N 各串 120R@100MHz 磁珠 + 100pF 对地,喇叭线长时必备。
- **天线**:PICO-1 SiP 内置 PCB 天线,本体一侧禁铺铜 keepout;外置天线才需 ANT_PAD π 网络 50Ω。
- **电池 ADC 门控**:P-FET(CJ3439)在睡眠断开分压,省 µA 级常态漏电。
- **密集双板架构**:主板 48×24mm + BTB 子板(IR/SPK 外置),Hat2-Bus 座传电源 + GPIO。

## 验证 TODO(第二轮,真机)

离线草稿已落盘。升 verified 需在实时编辑器(ceshi)逐块跑:
1. `easyeda lib search` 解析每个新料的 libraryUuid/deviceUuid/LCSC C 号,写回 `standard-parts.json`
   (当前全 TBD):ES8311 / AW8737A / BMI270 / MSM381 / ST7789-BTB / VSOP38338 / IR928 /
   SY7088 / LGS4056 / CH213K / CJ3439KDW / 2N7002DW / SDMM0806 / ESP32-S3-PICO-1 等。
2. `blocks-pin-audit.py --probe` 刷 `symbol-pins.json` 快照(把新料真实符号脚读回)。
3. `blocks-pin-audit.py` 离线判定 → 修 fanout/missing(功能名 vs 真实脚名对不上处)。
4. `sch block apply` 孤立单放 + **netlist 逐网对账**(唯一可信判据)→ 升 verified,
   写回 `verification` 与 `validated`。见 `references/standard-blocks-contributing.md`。
