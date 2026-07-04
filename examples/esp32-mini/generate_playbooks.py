#!/usr/bin/env python3
"""Generate the esp32-mini full-flow playbooks (schematic + PCB).

Sources:
  - hand-verified session data (part list, schematic coords, final PCB floorplan)
  - the golden board's copper geometry (track/via dumps passed via --tracks/--vias)

Regenerate:
  easyeda pcb track-list --project ceshi > /tmp/gt.json
  easyeda pcb via-list  --project ceshi > /tmp/gv.json
  python3 examples/esp32-mini/generate_playbooks.py --tracks /tmp/gt.json --vias /tmp/gv.json
"""
import argparse
import json
import math
import os

HERE = os.path.dirname(os.path.abspath(__file__))
LIB = "0819f05c4eef4c71ace90d822a990e87"

# designator: (deviceUuid, schematic x, y)  — coords passed sch layout-lint (0 overlap)
SCH_PARTS = [
    ("U1",   "ebc5227ec05f4bcbbb5581e49a5f7cc6", 760, 430),
    ("U2",   "9f9c6cb41c7449fd8acf96aceed2661a", 120, 700),
    ("U3",   "4951b3094e3e48a7b4c8c708b64932e4", 480, 700),
    ("J1",   "fafc5d2de09d4595b0cc7abedc0530e6", 120, 470),
    ("J2",   "41ddecaaf01b4c1aa92ec4bfe22149b7", 120, 250),
    ("SW1",  "cff07d923b95482c818b184e98b80db7", 480, 470),
    ("SW2",  "cff07d923b95482c818b184e98b80db7", 630, 470),
    ("LED1", "06303f8c50b646d88d0dd08d2ec9692c", 480, 250),
    ("R1",   "a60160c6e65140998078961749427162", 630, 250),
    ("R2",   "c3b9baa5ef2e4070a4c0f9e9cd04fe6e", 1000, 700),
    ("R3",   "c3b9baa5ef2e4070a4c0f9e9cd04fe6e", 1080, 700),
    ("R4",   "ef1f93374e0c4079b48a2d1a3cec8f6b", 300, 250),
    ("R5",   "ef1f93374e0c4079b48a2d1a3cec8f6b", 380, 250),
    ("C1",   "6e5726223dd84f70bc3b626fc7d1f72c", 300, 700),
    ("C2",   "159c3da60be9490abf4e97599788a0d5", 380, 700),
    ("C3",   "87bb635d0b2f489a9f60e7cd225beb3c", 1000, 480),
    ("C4",   "87bb635d0b2f489a9f60e7cd225beb3c", 1080, 480),
    ("C5",   "87bb635d0b2f489a9f60e7cd225beb3c", 630, 700),
    ("C6",   "87bb635d0b2f489a9f60e7cd225beb3c", 1000, 250),
]

# final PCB floorplan (judge-panel winner, verified DRC-clean): x, y, rotation
PCB_PLACE = {
    "U1":   (700, 849.5, 0),   "U2": (1995, 510, 0),  "U3": (1500, 800, 0),
    "J1":   (1550, 207.7, 0),  "J2": (2290, 900, 90),
    "SW1":  (1050, 130, 0),    "SW2": (160, 500, 180),
    "LED1": (1340, 1380, 180), "R1": (1220, 1230, 0),
    "R2":   (240, 690, 0),     "R3": (1200, 500, 180),
    "R4":   (1330, 480, 90),   "R5": (1765, 460, 90),
    "C1":   (2050, 250, 90),   "C2": (2300, 480, 270),
    "C3":   (220, 1140, 270),  "C4": (220, 1025, 270), "C5": (220, 910, 270),
    "C6":   (240, 795, 180),
}

# pad→net maps (from the schematic netlist; designator-stable, id-free)
PCB_NETS = {
    "U1": {"2": "+3V3", "3": "EN", "36": "U0RXD", "37": "U0TXD", "41": "GND",
           "40": "GND", "1": "GND", "38": "LED_CTRL", "27": "IO0"},
    "U2": {"1": "GND", "2": "+3V3", "3": "+5V", "4": "+3V3"},
    "U3": {"1": "GND", "2": "U0RXD", "3": "U0TXD", "4": "+3V3", "5": "USB_DP",
           "6": "USB_DN", "16": "+3V3"},
    "J1": {"A4B9": "+5V", "B4A9": "+5V", "A1B12": "GND", "B1A12": "GND",
           "8": "GND", "9": "GND", "10": "GND", "11": "GND",
           "A6": "USB_DP", "A7": "USB_DN", "B6": "USB_DP", "B7": "USB_DN",
           "A5": "CC1", "B5": "CC2"},
    "J2": {"1": "+5V", "2": "GND"},
    "SW1": {"2": "GND", "1": "IO0"},
    "SW2": {"2": "GND", "1": "EN"},
    "LED1": {"2": "GND", "1": "LED_A"},
    "R1": {"2": "LED_A", "1": "LED_CTRL"},
    "R2": {"2": "EN", "1": "+3V3"},
    "R3": {"2": "IO0", "1": "+3V3"},
    "R4": {"2": "GND", "1": "CC1"},
    "R5": {"2": "GND", "1": "CC2"},
    "C1": {"1": "+5V", "2": "GND"},
    "C2": {"1": "+3V3", "2": "GND"},
    "C3": {"2": "GND", "1": "+3V3"},
    "C4": {"2": "GND", "1": "+3V3"},
    "C5": {"2": "GND", "1": "+3V3"},
    "C6": {"2": "GND", "1": "EN"},
}

BOARD = (0, 0, 2600, 1500)
M3 = [(150, 150), (2450, 150), (150, 1350), (2450, 1350)]
ANTENNA = "306,1200,1094,1500"
FILLS = [("1443,380,1463,400", 1), ("1636.9,380,1656.9,400", 1),
         ("1443,380,1463,400", 2), ("1636.9,380,1656.9,400", 2)]


def circle_points(cx, cy, r=63, n=24):
    return [[round(cx + r * math.cos(2 * math.pi * k / n), 1),
             round(cy + r * math.sin(2 * math.pi * k / n), 1)] for k in range(n)]


def gen_schematic():
    steps = [
        {"id": "clear-page", "run": "sch clear",
         "name": "清空当前页(保留图框)——确认门控,--yes 放行", "confirm": True},
    ]
    for des, uuid, x, y in SCH_PARTS:
        steps.append({"id": f"place-{des}", "run": "sch place",
                      "flags": {"lib": LIB, "uuid": uuid, "x": x, "y": y},
                      "capture": {f"P_{des}": "$.primitiveId"}})
        steps.append({"id": f"desig-{des}", "run": "sch modify",
                      "flags": {"id": "${P_" + des + "}",
                                "patch": json.dumps({"designator": des})}})
    steps += [
        {"id": "save-placed", "action": "schematic.save", "checkpoint": True},
        {"id": "autoconnect", "run": "sch autoconnect",
         "name": "64 个网络标志批量落地(约 3-5 分钟)",
         "flags": {"spec": "examples/esp32-mini/sch-connect.spec.json"}},
        {"id": "save-wired", "action": "schematic.save", "checkpoint": True},
        {"id": "gate-lint", "run": "sch layout-lint",
         "name": "布局门:overlap 即非零退出"},
        {"id": "gate-drc", "run": "sch drc",
         "name": "电气门:fatal 即非零退出(3 条悬空 warn = 模组备用脚,预期内)"},
        {"id": "check-info", "run": "sch check", "onFail": "continue",
         "name": "重建式检查(信息性;悬空备用脚会列出)"},
        {"id": "save-final", "action": "schematic.save", "checkpoint": True},
        {"id": "done", "notify": "✅ esp32-mini 原理图回放完成"},
    ]
    return {
        "version": 1,
        "meta": {"name": "esp32-mini-schematic",
                 "description": "esp32MiniRequire 原理图从零全流程(19 器件 + 13 网络);"
                                "先切到目标空白页(meta.doc),换页名用 --window/编辑本文件",
                 "project": "ceshi", "doc": "P1"},
        "defaults": {"timeoutSec": 30, "retry": 1},
        "vars": {"LIB": LIB},
        "steps": steps,
    }


def gen_pcb(tracks, vias):
    steps = []
    # 1) 放置(直接落最终坐标+旋转;uniqueId 用变量,fresh 工程默认 gge1..19)
    uid_vars = {}
    for i, (des, uuid, _, _) in enumerate(SCH_PARTS, 1):
        uid_vars[f"UID_{des}"] = f"gge{i}"
    for des, uuid, _, _ in SCH_PARTS:
        x, y, rot = PCB_PLACE[des]
        flags = {"library": LIB, "uuid": uuid, "x": x, "y": y, "layer": 1,
                 "designator": des, "unique-id": "${UID_" + des + "}",
                 "nets": json.dumps(PCB_NETS[des], separators=(",", ":"))}
        if rot:
            flags["rotation"] = rot
        steps.append({"id": f"add-{des}", "run": "pcb add-component", "flags": flags})
    steps.append({"id": "stackup-4layer", "run": "pcb stackup set",
                  "name": "先升 4 层(内层铺铜的前提)", "flags": {"layers": 4}})
    steps.append({"id": "save-placed", "action": "pcb.save", "checkpoint": True})

    # 2) 板框 + M3 挖槽/禁铜环 + 天线禁布(每层独立)
    steps.append({"id": "outline", "run": "pcb outline-round",
                  "flags": {"rect": "%d,%d,%d,%d" % BOARD, "radius": 120}})
    for i, (cx, cy) in enumerate(M3, 1):
        steps.append({"id": f"m3-slot-{i}", "run": "pcb slot",
                      "flags": {"points": json.dumps(circle_points(cx, cy))}})
        x0, y0 = max(cx - 130, 0), max(cy - 130, 0)
        x1, y1 = min(cx + 130, BOARD[2]), min(cy + 130, BOARD[3])
        steps.append({"id": f"m3-ring-{i}", "run": "pcb region create",
                      "flags": {"rect": f"{x0},{y0},{x1},{y1}",
                                "rule": ["no-pours", "no-wires", "no-fills"]}})
    steps.append({"id": "antenna-l1", "run": "pcb region create",
                  "name": "天线禁布 L1(含内层 no-inner-electrical)",
                  "flags": {"rect": ANTENNA, "name": "antenna_keepout",
                            "rule": ["no-pours", "no-wires", "no-fills", "no-inner-electrical"]}})
    steps.append({"id": "antenna-l2", "run": "pcb region create",
                  "name": "天线禁布 L2(pcb check 要求每层独立)",
                  "flags": {"rect": ANTENNA, "layer": 2, "name": "antenna_keepout_bottom",
                            "rule": ["no-pours", "no-wires", "no-fills"]}})
    steps.append({"id": "gate-lint-placed", "run": "pcb layout-lint",
                  "name": "可布性门(overlap/off-board 即非零退出)",
                  "assert": {"$.overlaps": "len==0"}, "flags": {"json": True}})
    steps.append({"id": "silk-align", "run": "pcb silk-align"})
    steps.append({"id": "save-infra", "action": "pcb.save", "checkpoint": True})

    # 3) 铜:tracks + vias(金板逐条导出;全部先于铺铜/PLANE 翻转 → 避开 #31/#32 坑)
    for i, t in enumerate(tracks, 1):
        steps.append({"id": f"t{i}", "run": "pcb track",
                      "flags": {"x1": t["startX"], "y1": t["startY"],
                                "x2": t["endX"], "y2": t["endY"],
                                "layer": t["layer"], "width": t["lineWidth"],
                                "net": t["net"]}})
    for i, vv in enumerate(vias, 1):
        steps.append({"id": f"v{i}", "run": "pcb via",
                      "flags": {"x": vv["x"], "y": vv["y"], "net": vv["net"],
                                "hole": vv["holeDiameter"], "diameter": vv["diameter"]}})
    steps.append({"id": "save-copper", "action": "pcb.save", "checkpoint": True})

    # 4) +5V via 桥键合 fill(#31 workaround:track↔via 不注册,fill 面键合)
    for i, (rect, layer) in enumerate(FILLS, 1):
        steps.append({"id": f"bondfill-{i}", "run": "pcb fill create",
                      "flags": {"rect": rect, "layer": layer, "net": "+5V"}})

    # 5) 铺铜序列(pour-while-SIGNAL → flip PLANE → rebuild 配方)
    # 5.0) 先给铺铜间距加余量(10→12mil,raise-only):新建 PCB 的 reflow 会对配置
    # 间距打 ~3% 折且不生成热焊盘(平台疑点,见 README 已知问题),余量让打折后的
    # reflow 依旧 ≥10mil DRC 下限;写入同时把系统预设变自定义配置,热焊盘也随之恢复。
    steps += [
        {"id": "rules-pour-margin", "run": "pcb drc-rules-set",
         "flags": {"pour-clearance": 12},
         "name": "铺铜间距余量 10→12mil(新板 reflow 打折 workaround)"},
    ]
    steps += [
        {"id": "pour-3v3-l16", "run": "pcb pour-fit",
         "flags": {"net": "+3V3", "layer": 16, "inset": 30}},
        {"id": "pour-thermal", "run": "pcb pour", "name": "U2 散热岛(顶层 +3V3)",
         "flags": {"net": "+3V3", "layer": 1,
                   "points": "[[1727,503],[2027,503],[2027,1100],[1727,1100]]"}},
        {"id": "pour-gnd-l15", "run": "pcb pour-fit",
         "flags": {"net": "GND", "layer": 15, "inset": 30}},
        {"id": "pour-gnd-l1", "run": "pcb pour-fit",
         "flags": {"net": "GND", "layer": 1, "inset": 15, "replace": False}},
        {"id": "pour-gnd-l2", "run": "pcb pour-fit",
         "flags": {"net": "GND", "layer": 2, "inset": 15, "replace": False}},
        {"id": "plane-flip", "run": "pcb stackup set", "flags": {"plane": 15},
         "name": "L15 → GND 内电层(必须在铺铜之后翻,#32)"},
        {"id": "pour-rebuild", "run": "pcb pour-rebuild"},
        {"id": "save-pours", "action": "pcb.save", "checkpoint": True},
        # 5.1) 新建 PCB 的 reflow 用创建时规则快照:写规则/重灌都不生效,必须真正
        # 关闭+重开文档后再 rebuild 一次,reflow 才读当前规则(间距+热焊盘同时恢复)。
        # doc reload 内部先 save,不丢编辑;tab 切换不等于重载。
        {"id": "reload-pcb", "run": "doc reload",
         "name": "重载文档:刷新 reflow 规则快照(新建 PCB 平台疑点)"},
        {"id": "pour-rebuild-2", "run": "pcb pour-rebuild",
         "name": "重载后二次重灌:reflow 此时才按 12mil 余量 + 生成热焊盘"},
        {"id": "save-pours-2", "action": "pcb.save", "checkpoint": True},
    ]

    # 6) 丝印:LED 极性 + 板注
    steps += [
        {"id": "silk-plus", "run": "pcb silk-add",
         "flags": {"x": 1220, "y": 1360, "text": "+", "font-size": 40, "line-width": 6}},
        {"id": "silk-minus", "run": "pcb silk-add",
         "flags": {"x": 1440, "y": 1360, "text": "-", "font-size": 40, "line-width": 6}},
        {"id": "silk-credit", "run": "pcb silk-add",
         "flags": {"x": 1150, "y": 1430, "font-size": 45, "line-width": 6,
                   "text": "ESP32-S3 mini  github:zhoushoujianwork/easyeda-agent"}},
    ]

    # 7) 终门
    steps += [
        {"id": "gate-lint-final", "run": "pcb layout-lint", "flags": {"json": True},
         "assert": {"$.score": ">=95"}},
        {"id": "gate-check", "run": "pcb check", "flags": {"strict": True},
         "name": "DFM 门:任何 finding 即非零退出"},
        {"id": "save-final", "action": "pcb.save", "checkpoint": True},
        {"id": "drc-final", "run": "pcb drc", "onFail": "continue",
         "name": "官方 DRC(需 EasyEDA 前台;剩 1 条 Netlist Error = 平台 #33,预期内)"},
        {"id": "done", "notify": "✅ esp32-mini PCB 回放完成"},
    ]

    return {
        "version": 1,
        "meta": {"name": "esp32-mini-pcb",
                 "description": "esp32MiniRequire 四层板从零全流程(放置→板框/M3/天线→铜→"
                                "铺铜/内电层→丝印→门禁);uniqueId 用 --var UID_U1=… 对齐 "
                                "sch read 读到的值(fresh 工程默认 gge1..gge19)",
                 "project": "ceshi", "doc": "PCB1"},
        "defaults": {"timeoutSec": 60, "retry": 1},
        "vars": uid_vars,
        "steps": steps,
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--tracks", required=True, help="pcb track-list dump (golden board)")
    ap.add_argument("--vias", required=True, help="pcb via-list dump (golden board)")
    args = ap.parse_args()

    tr = json.load(open(args.tracks))["result"]
    tracks = tr.get("tracks") or tr.get("lines") or []
    vr = json.load(open(args.vias))["result"]
    vias = vr.get("vias") or []
    # stable order: net → layer → coords(可读 + diff 稳定)
    tracks.sort(key=lambda t: (t["net"], t["layer"], t["startX"], t["startY"]))
    vias.sort(key=lambda v: (v["net"], v["x"], v["y"]))

    sch = gen_schematic()
    pcb = gen_pcb(tracks, vias)
    for name, pb in (("schematic", sch), ("pcb", pcb)):
        path = os.path.join(HERE, f"{name}.playbook.json")
        with open(path, "w") as f:
            json.dump(pb, f, ensure_ascii=False, indent=1)
            f.write("\n")
        print(f"{path}: {len(pb['steps'])} steps")


if __name__ == "__main__":
    main()
