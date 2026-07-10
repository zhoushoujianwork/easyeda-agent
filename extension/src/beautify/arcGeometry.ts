// PCB 走线美化 — 拐角圆弧几何 (fillet + 差分/等长同心圆弧)。
//
// 移植自开源扩展 Easy_EDA_PCB_Beautify（作者 m-RNA，Apache-2.0）：
//   https://github.com/m-RNA/Easy_EDA_PCB_Beautify  ·  src/lib/arcGeometry.ts
// 完整第三方许可与署名见仓库根目录 NOTICE。此文件为原样移植（纯几何，无 eda.* 依赖）。

import type { Point } from './math';
import { dist, getAngleBetween, lerp } from './math';

export interface BeautifySettingsLike {
	cornerRadiusRatio: number;
	forceArc: boolean;
	[k: string]: unknown;
}

export interface CornerArcCandidate {
	cornerIndex: number;
	start: Point;
	end: Point;
	width: number;
	angle: number;
	radius: number;
	baseRadius: number;
	center: Point;
	centerDir: Point;
	pPrev: Point;
	pCorner: Point;
	pNext: Point;
	prevWidth: number;
	nextWidth: number;
	mag1: number;
	mag2: number;
	u1: Point;
	u2: Point;
	tangentLength: number;
}

export interface CornerArcOverride {
	start: Point;
	end: Point;
	width: number;
	angle: number;
}

interface OrderedSeg { width: number; [k: string]: unknown }

export function computeCornerArcCandidate(
	points: Point[],
	orderedSegs: OrderedSeg[],
	cornerIndex: number,
	settings: BeautifySettingsLike,
	scale?: number,
): CornerArcCandidate | null {
	if (cornerIndex <= 0 || cornerIndex >= points.length - 1)
		return null;

	const pPrev = points[cornerIndex - 1];
	const pCorner = points[cornerIndex];
	const pNext = points[cornerIndex + 1];

	const prevWidth = orderedSegs[cornerIndex - 1]?.width ?? orderedSegs[0]?.width ?? 10;
	const nextWidth = orderedSegs[cornerIndex]?.width ?? prevWidth;
	const maxLineWidth = Math.max(prevWidth, nextWidth);
	const baseRadius = maxLineWidth * settings.cornerRadiusRatio;
	const radius = baseRadius * (scale ?? 1);

	const v1 = { x: pPrev.x - pCorner.x, y: pPrev.y - pCorner.y };
	const v2 = { x: pNext.x - pCorner.x, y: pNext.y - pCorner.y };
	const mag1 = Math.sqrt(v1.x ** 2 + v1.y ** 2);
	const mag2 = Math.sqrt(v2.x ** 2 + v2.y ** 2);
	if (mag1 < 0.001 || mag2 < 0.001)
		return null;

	const u1 = { x: v1.x / mag1, y: v1.y / mag1 };
	const u2 = { x: v2.x / mag2, y: v2.y / mag2 };
	const dot = Math.max(-1, Math.min(1, u1.x * u2.x + u1.y * u2.y));
	const angleRad = Math.acos(dot);
	const tanVal = Math.tan(angleRad / 2);
	const sinHalf = Math.sin(angleRad / 2);

	if (Math.abs(tanVal) < 0.0001 || Math.abs(sinHalf) < 0.0001)
		return null;

	const idealTangentLength = radius / tanVal;
	const maxAllowedTangentLength = Math.min(mag1 * 0.45, mag2 * 0.45);
	const tangentLength = Math.min(idealTangentLength, maxAllowedTangentLength);
	if (idealTangentLength > 0.001 && tangentLength < idealTangentLength * 0.95 && !settings.forceArc)
		return null;

	const effectiveRadius = tangentLength * Math.abs(tanVal);
	if (effectiveRadius < (maxLineWidth / 2) - 0.05)
		return null;
	if (tangentLength <= 0.05)
		return null;

	const start = lerp(pCorner, pPrev, tangentLength / mag1);
	const end = lerp(pCorner, pNext, tangentLength / mag2);
	const sweptAngle = getAngleBetween({ x: -v1.x, y: -v1.y }, { x: v2.x, y: v2.y });
	const centerDirRaw = { x: u1.x + u2.x, y: u1.y + u2.y };
	const centerDirLen = Math.sqrt(centerDirRaw.x ** 2 + centerDirRaw.y ** 2);
	if (centerDirLen < 0.0001)
		return null;

	const centerDir = { x: centerDirRaw.x / centerDirLen, y: centerDirRaw.y / centerDirLen };
	const centerDistance = effectiveRadius / sinHalf;
	const center = {
		x: pCorner.x + centerDir.x * centerDistance,
		y: pCorner.y + centerDir.y * centerDistance,
	};

	return {
		cornerIndex,
		start,
		end,
		width: nextWidth,
		angle: sweptAngle,
		radius: effectiveRadius,
		baseRadius,
		center,
		centerDir,
		pPrev,
		pCorner,
		pNext,
		prevWidth,
		nextWidth,
		mag1,
		mag2,
		u1,
		u2,
		tangentLength,
	};
}

export function buildConcentricOverrides(candidates: CornerArcCandidate[]): CornerArcOverride[] | null {
	if (candidates.length < 2)
		return null;

	const commonCenter = findCommonCenter(candidates);
	if (!commonCenter)
		return null;

	const overrides: CornerArcOverride[] = [];
	for (const candidate of candidates) {
		const fromCenter = {
			x: candidate.pCorner.x - commonCenter.x,
			y: candidate.pCorner.y - commonCenter.y,
		};
		const forward = fromCenter.x * candidate.centerDir.x + fromCenter.y * candidate.centerDir.y;
		if (forward >= -0.05)
			return null;

		const start = projectPointToLine(commonCenter, candidate.pCorner, candidate.pPrev);
		const end = projectPointToLine(commonCenter, candidate.pCorner, candidate.pNext);
		const radiusToPrev = dist(commonCenter, start);
		const radiusToNext = dist(commonCenter, end);
		const radius = (radiusToPrev + radiusToNext) / 2;

		if (radius < Math.max(candidate.prevWidth, candidate.nextWidth) / 2 - 0.05)
			return null;
		if (Math.abs(radiusToPrev - radiusToNext) > Math.max(0.2, radius * 0.03))
			return null;

		const startDistance = dist(candidate.pCorner, start);
		const endDistance = dist(candidate.pCorner, end);
		if (startDistance < 0.05 || endDistance < 0.05)
			return null;
		if (startDistance > candidate.mag1 * 0.45 || endDistance > candidate.mag2 * 0.45)
			return null;
		if (!isPointBetween(start, candidate.pCorner, candidate.pPrev) || !isPointBetween(end, candidate.pCorner, candidate.pNext))
			return null;

		overrides.push({
			start,
			end,
			width: candidate.nextWidth,
			angle: candidate.angle,
		});
	}

	return overrides;
}

function findCommonCenter(candidates: CornerArcCandidate[]): Point | null {
	const centers: Point[] = [];
	for (let i = 0; i < candidates.length; i++) {
		for (let j = i + 1; j < candidates.length; j++) {
			const center = intersectRays(
				candidates[i].pCorner,
				candidates[i].centerDir,
				candidates[j].pCorner,
				candidates[j].centerDir,
			);
			if (center)
				centers.push(center);
		}
	}

	if (centers.length === 0)
		return findCollinearCommonCenter(candidates);

	const average = centers.reduce((acc, p) => ({ x: acc.x + p.x, y: acc.y + p.y }), { x: 0, y: 0 });
	average.x /= centers.length;
	average.y /= centers.length;

	for (const candidate of candidates) {
		const centerVec = { x: average.x - candidate.pCorner.x, y: average.y - candidate.pCorner.y };
		if (centerVec.x * candidate.centerDir.x + centerVec.y * candidate.centerDir.y <= 0.05)
			return null;
	}

	return average;
}

function findCollinearCommonCenter(candidates: CornerArcCandidate[]): Point | null {
	const validCenters = candidates
		.map(candidate => candidate.center)
		.filter(center => candidates.every(candidate => isCenterOnRay(center, candidate)));
	if (validCenters.length === 0)
		return null;

	return validCenters.reduce((best, center) => {
		const bestScore = sumDistanceToCorners(best, candidates);
		const centerScore = sumDistanceToCorners(center, candidates);
		return centerScore > bestScore ? center : best;
	});
}

function isCenterOnRay(center: Point, candidate: CornerArcCandidate): boolean {
	const toCenter = {
		x: center.x - candidate.pCorner.x,
		y: center.y - candidate.pCorner.y,
	};
	const forward = toCenter.x * candidate.centerDir.x + toCenter.y * candidate.centerDir.y;
	if (forward <= 0.05)
		return false;

	const cross = Math.abs(toCenter.x * candidate.centerDir.y - toCenter.y * candidate.centerDir.x);
	return cross <= 0.2;
}

function sumDistanceToCorners(center: Point, candidates: CornerArcCandidate[]): number {
	return candidates.reduce((sum, candidate) => sum + dist(center, candidate.pCorner), 0);
}

function intersectRays(p1: Point, d1: Point, p2: Point, d2: Point): Point | null {
	const cross = d1.x * d2.y - d1.y * d2.x;
	if (Math.abs(cross) < 0.0001)
		return null;

	const delta = { x: p2.x - p1.x, y: p2.y - p1.y };
	const t = (delta.x * d2.y - delta.y * d2.x) / cross;
	const u = (delta.x * d1.y - delta.y * d1.x) / cross;
	if (t <= 0.05 || u <= 0.05)
		return null;

	return {
		x: p1.x + d1.x * t,
		y: p1.y + d1.y * t,
	};
}

function projectPointToLine(point: Point, lineA: Point, lineB: Point): Point {
	const vx = lineB.x - lineA.x;
	const vy = lineB.y - lineA.y;
	const lenSq = vx ** 2 + vy ** 2;
	if (lenSq < 0.000001)
		return lineA;

	const t = ((point.x - lineA.x) * vx + (point.y - lineA.y) * vy) / lenSq;
	return {
		x: lineA.x + vx * t,
		y: lineA.y + vy * t,
	};
}

function isPointBetween(point: Point, a: Point, b: Point): boolean {
	const ab = dist(a, b);
	return dist(a, point) <= ab + 0.05 && dist(b, point) <= ab + 0.05;
}
