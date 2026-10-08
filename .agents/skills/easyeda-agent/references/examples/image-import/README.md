# 原理图与 PCB 参考图片

来源：本例用 Python 标准库生成 6×4 RGBA PNG，不含客户资料或外部图片。
状态：`offline-verified` 仅素材生成、本地解码、尺寸比例与 CLI dry-run；本轮未访问 EDA，
未执行创建、保存、重载或图元回读，不声明现场导入通过。

开始状态不需要工程、元器件或网表。在独立临时目录生成 `logo.png`，离线报告也留在该
目录。原理图参考图片用 `sch image`，PCB 参考图片用 `pcb image`（DOCUMENT 13）；
它们不产生电气网络，也不等于制造丝印。PNG/JPEG 转制造丝印需要人工调节图稿，不提供
自动转换。已有 SVG 丝印导入仍按 [PCB 图片说明](../../pcb-layout.md) 的原契约使用。

## 自生成素材

```bash
python3 - <<'PY'
from pathlib import Path
import struct
import zlib

rows = ("###...", "#.#...", "###...", "....##")
width, height = len(rows[0]), len(rows)
raw = b"".join(b"\x00" + b"".join(
    bytes((0, 0, 0, 255 if cell == "#" else 0)) for cell in row
) for row in rows)

def chunk(kind, data):
    return (struct.pack(">I", len(data)) + kind + data
            + struct.pack(">I", zlib.crc32(kind + data) & 0xffffffff))

Path("logo.png").write_bytes(
    b"\x89PNG\r\n\x1a\n"
    + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0))
    + chunk(b"IDAT", zlib.compress(raw))
    + chunk(b"IEND", b"")
)
PY
```

`#` 是不透明黑色，`.` 是完全透明。左侧 3×3 区域中心留透明孔，右下方有独立的
2×1 黑块；共 10 个不透明像素，适合检查源图片尺寸、透明区域和非对称朝向。此处只生成
参考图片文件，不把像素转换为制造轮廓。

## 参数与离线步骤

```bash
easyeda sch image create --file logo.png --x 0 --y 0 --width 60 --dry-run > sch-reference.json
easyeda pcb image create --file logo.png --x 1000 --y -1000 --width 600 --layer 13 --dry-run > pcb-reference.json
```

两条命令只读本地图片，不连接或写入 EDA。原理图的坐标和尺寸为 raw（0.01 inch），
`width=60` 应按 6:4 的源比例得到 `height=40`；省略两个尺寸时 CLI 会解析真实源尺寸并
显式准备 6×4 raw。PCB 使用 mil，必须给宽或高；`width=600` 应得到 `height=400`。
两域都是 y-UP、源左上角 anchor，不能把原理图 raw 值直接当 PCB mil。

观测：两个报告均为 `dryRun:true`、`fileName:logo.png`、`mirror:false`，分别得到
60×40 raw 与 600×400 mil。PCB 未旋转、未镜像时，本地计算的 `bbox` 应为
`{minX:1000,maxX:1600,minY:-1400,maxY:-1000}`，层应为 13；这只是本地预测，不能当
宿主回读。dry-run 不证明宿主保留图片内容、透明度或持久化，也不证明电气状态不变。

修改自由参数后重新运行本地命令：PCB 宽度改为 300 应等比得到高 200；只给高 200
也应得到宽 300；原理图的同类计算仍使用 raw 单位。旋转/镜像另按对应命令契约读取，
实际锚点和 bbox 必须由现场新回读确认，不能靠本地预览补签。

## 错误与验证边界

坏图、PNG/JPEG 扩展名与内容不符、非法尺寸应在本地失败，修正源图片或参数后重算。
PCB 参考图片缺宽/高，或要求丝印层 3/4，也应拒绝；允许的参考层仅 DOCUMENT 13。
原理图不提供独立 `sch image delete`，已有对象按 ID 用 `sch prim-delete --ids` 删除。

历史参考图片现场证据见 [原理图](../../schematic.md) 与 [PCB 图片说明](../../pcb-layout.md)。
实际导入仍须绑定精确工程/文档，创建后显式保存、真实重载、新鲜 list 回读并核对电气与
非目标对象；本例没有执行这些步骤，不能代替其证据。制造图稿的人工调节与 DFM 验证
不在本例的自动化范围内。
