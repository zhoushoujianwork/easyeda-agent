// PCB 走线美化 — 平面几何辅助。
//
// 移植自开源扩展 Easy_EDA_PCB_Beautify（作者 m-RNA，Apache-2.0）：
//   https://github.com/m-RNA/Easy_EDA_PCB_Beautify  ·  src/lib/math.ts
// 完整第三方许可与署名见仓库根目录 NOTICE。此文件为原样移植，仅保留 headless
// 用到的纯函数。

export interface Point {
	x: number;
	y: number;
}

/** 比较两个浮点数是否足够接近 (默认误差 0.001)。 */
export function isClose(a: number, b: number, eps = 0.001): boolean {
	return Math.abs(a - b) < eps;
}

export function dist(p1: Point, p2: Point): number {
	return Math.sqrt((p1.x - p2.x) ** 2 + (p1.y - p2.y) ** 2);
}

export function lerp(p1: Point, p2: Point, t: number): Point {
	return {
		x: p1.x + (p2.x - p1.x) * t,
		y: p1.y + (p2.y - p1.y) * t,
	};
}

/** 计算角度 (0-360)。 */
export function getAngle(p1: Point, p2: Point): number {
	return (Math.atan2(p2.y - p1.y, p2.x - p1.x) * 180) / Math.PI;
}

/** 计算两个向量之间的夹角 (有符号, -180..180)。 */
export function getAngleBetween(v1: Point, v2: Point): number {
	let angle = getAngle({ x: 0, y: 0 }, v2) - getAngle({ x: 0, y: 0 }, v1);
	while (angle <= -180) angle += 360;
	while (angle > 180) angle -= 360;
	return angle;
}

/** 计算两条线的交点 (平行返回 null)。 */
export function getLineIntersection(p1: Point, p2: Point, p3: Point, p4: Point): Point | null {
	if (!p1 || !p2 || !p3 || !p4)
		return null;
	const d = (p1.x - p2.x) * (p3.y - p4.y) - (p1.y - p2.y) * (p3.x - p4.x);
	if (Math.abs(d) < 1e-6)
		return null; // 平行

	const t = ((p1.x - p3.x) * (p3.y - p4.y) - (p1.y - p3.y) * (p3.x - p4.x)) / d;
	return {
		x: p1.x + t * (p2.x - p1.x),
		y: p1.y + t * (p2.y - p1.y),
	};
}
