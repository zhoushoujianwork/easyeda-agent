# 参考图片与位图丝印候选

来源：本例用 Python 标准库生成 6×4 RGBA PNG，不含客户资料或外部图片。
状态：`offline-verified` 仅本地解码、参数、像素边界与 Apply 数据契约；本轮未访问 EDA。
`pcb.silk.import_bitmap` 的现场写入为 `unsupported`，不能把本例当已导入制造丝印。

开始状态不需要工程、元器件或网表。把 [bitmap-parameters.json](bitmap-parameters.json)
保存到独立临时目录，在同一目录生成 `logo.png`，后续报告和队列也留在该目录。
原理图参考图片用 `sch image`，PCB 参考图片用 `pcb image`（DOCUMENT 13），两者均与
本例的制造丝印候选分开；已有现场证据见 [原理图](../../schematic.md) 和
[PCB 图片边界](../../pcb-layout.md#bitmap-silk-import)，本例不重复认证。

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
2×1 黑块；10 个不透明像素可同时检查孔洞、独立区域与非对称朝向。

## 参数与离线步骤

参数文件给出 `x=1000,y=-1000`、`width=600`、`layer=3`、阈值 128、白色合成背景，
位置/尺寸单位为 mil、y-UP，anchor 为源画布左上角。只给宽度并保留比例，应得到
600×400 mil 的画布，每像素为 100×100 mil。`layer` 和物理尺寸必须明确声明，不能从
像素数猜尺寸，也不能把层 13 参考图片当顶/底制造丝印。

```bash
easyeda pcb silk-import-bitmap --from bitmap-parameters.json \
  --dry-run --out bitmap.apply.json > bitmap-report.json
```

观测：源图尺寸为 6×4，10 个前景像素形成两个着墨区域与一个透明孔；共线简化只删除
冗余顶点，不改变像素边界。未旋转、未镜像时，画布与着墨 bbox 都是
`{minX:1000,maxX:1600,minY:-1400,maxY:-1000}`。报告保留源 SHA-256、转换参数、
轮廓/顶点计数和 `payload.polygons`；`bboxKind:"planned-geometry"` 与
`hostBBoxVerified:false` 明确没有宿主回读。Apply 为 `version:1` / `meta` / `steps` 队列，
只有一个 `pcb.silk.import_bitmap` typed 候选及其 `schemaVersion:1`、源哈希、像素尺寸、
mil 与 top-left 单位约定。`--out` 只写新文件，重跑须选择新路径。

修改自由参数后重新生成，不手改已生成轮廓：宽度改为 300 应等比得到高 200；改阈值
与背景时核对着墨像素差异；底丝印 `layer=4` 默认 mirror，若参数里保留 `mirror:false`
则显式覆盖默认镜像。候选先绕源画布竖直中线镜像，再绕左上角 anchor 旋转；这不是
DOCUMENT 13 参考图片已验证的宿主镜像规律。`pixelPitch:{x,y}` 只说明分辨率，不能
代替最窄笔画或制造净距检查。

## 错误与边界

缺宽/高、缺显式层、层 13、非 PNG/JPEG、坏图或非法阈值都应在本地失败；修正源参数
后重算。完全透明像素始终是背景，`invert` 不把透明区域填成丝印；半透明颜色先合成到
选定背景后再阈值化。本例不做平滑、去噪、焊盘避让、板边检查或制造 DFM。

`--dry-run` 与 `--out` 不连接宿主；实际调用 `pcb.silk.import_bitmap` 固定返回
`PRECONDITION_REFUSED` / `unsupported`。没有现场创建、保存、重载或对象回读结果，
禁止将队列改投 SVG action 或 GUI 导入来补签通过。
