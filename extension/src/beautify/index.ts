// PCB 走线美化 — headless 编排（拐角圆弧化 + 差分/等长同心圆弧保护 + DRC 二分修复）。
//
// 算法移植自开源扩展 Easy_EDA_PCB_Beautify（作者 m-RNA，Apache-2.0）：
//   https://github.com/m-RNA/Easy_EDA_PCB_Beautify  ·  src/lib/beautify.ts
// 完整第三方许可与署名见仓库根目录 NOTICE。
//
// 相对上游的改动（headless 化，供 daemon typed-action 驱动）：
//   • 去掉自研快照/撤销（snapshot.ts）—— 依赖 pcb.save 检查点，调用方负责落盘。
//   • 去掉 iframe 设置面板与 sys_Message/LoadingBar 弹窗 —— 参数经 payload 传入，
//     结果以结构化 summary 返回。
//   • 新增 dryRun：只计算规划、不删除/创建，用于在真实板上安全预览。
//   • 线宽平滑过渡（widthTransition.ts）暂未移植 —— 见 CHANGELOG follow-up。

import type { BeautifySettingsLike, CornerArcOverride } from './arcGeometry';
import type { Point } from './math';
import { buildConcentricOverrides, computeCornerArcCandidate } from './arcGeometry';
import { runDrcCheckAndParse } from './drc';
import { dist, getAngleBetween, getLineIntersection, lerp } from './math';

export interface BeautifyOptions {
	scope: 'all' | 'selected';
	net?: string; // 只处理该网络（单网；与 nets 二选一）
	nets?: string[]; // 只处理这些网络（多网白名单；空/缺省=全部）
	layer?: number; // 只处理该铜层
	cornerRadiusRatio: number; // 圆角半径 = max(线宽) * ratio
	forceArc: boolean; // 线段较短放不下理想半径时仍生成截断圆弧
	mergeTransitionSegments: boolean; // U 型弯合并为单个大圆弧
	protectDifferentialAndEqualLength: boolean; // 差分/等长组用同心圆弧或保守跳过
	enableDrc: boolean; // 生成后跑 DRC 二分修复违规拐角
	drcIgnoreCopperPour: boolean; // DRC 忽略覆铜相关违规
	drcRetryCount: number; // DRC 二分深度
	rebuildPour: boolean; // 全部完成后重铺覆铜
	dryRun: boolean; // 只计算规划、不落笔
}

export interface BeautifySummary {
	// Index signature so it satisfies ActionResult.result (Record<string, unknown>).
	[k: string]: unknown;
	scope: 'all' | 'selected';
	dryRun: boolean;
	tracksConsidered: number;
	paths: number; // 提取出的可美化多段线数（>=3 点）
	cornersRounded: number; // 实际生成的圆弧数（= 圆滑成功的拐角数）
	linesCreated: number;
	arcsCreated: number;
	protectedGroups: number; // 检测到的差分/等长保护组数
	drcRounds: number; // DRC 二分修复轮数（0 = 一次通过或未启用）
	pourRebuilt: number | null; // 重铺的覆铜区数；未重铺为 null
	skipped?: string; // 无可处理导线时的说明
}

interface Settings extends BeautifySettingsLike {
	cornerRadiusRatio: number;
	forceArc: boolean;
	mergeTransitionSegments: boolean;
	protectDifferentialAndEqualLength: boolean;
}

// Index signature keeps it structurally compatible with arcGeometry's OrderedSeg.
interface OrderedSeg { p1: Point; p2: Point; width: number; id: string; [k: string]: unknown }

interface PathOp {
	type: 'line' | 'arc';
	start: Point;
	end: Point;
	width: number;
	angle?: number;
	cornerIndex: number;
}

interface ProtectedNetGroup {
	key: string;
	name: string;
	type: 'differential' | 'equalLength';
	nets: string[];
}

interface PathContext {
	pathId: number;
	points: Point[];
	orderedSegs: OrderedSeg[];
	net: string;
	layer: number;
	deleteIds: string[]; // 原始线段 ID（提取时收集，非 dryRun 时删除）
	createdIds: string[];
	idToCornerMap: Map<string, number>;
	badCorners: Set<number>;
	cornerScales: Map<number, number>;
	protectedGroupKeys: string[];
	protectedCornerKeys: Map<number, string>;
	cornerOverrides: Map<number, CornerArcOverride | null>;
	arcN: number; // 本路径最终几何的圆弧数（每次 re-commit 重置，反映最终态而非累计）
	lineN: number; // 本路径最终几何的直线数
}

// COPPER layers only: TOP=1, BOTTOM=2, INNER_1..30 = 15..44 —— 与 pcb.route.rip_up 一致，
// 绝不动丝印/板框/机械层的线。
function onCopper(layer: unknown): boolean {
	const n = Number(layer);
	return n === 1 || n === 2 || (n >= 15 && n <= 44);
}

// ─── 核心几何：路径点 → 绘图指令 (line/arc) ───────────────────────────────
function generatePathOps(
	path: { points: Point[]; orderedSegs: OrderedSeg[] },
	settings: Settings,
	badCorners: Set<number>,
	cornerScales: Map<number, number>,
	cornerOverrides: Map<number, CornerArcOverride | null>,
): PathOp[] {
	const { points, orderedSegs } = path;
	const ops: PathOp[] = [];
	if (points.length < 3)
		return ops;

	const ratio = settings.cornerRadiusRatio;
	let currentStart = points[0];

	for (let i = 1; i < points.length - 1; i++) {
		const pPrev = points[i - 1];
		const pCorner = points[i];
		const pNext = points[i + 1];

		const prevSegWidth = orderedSegs[i - 1]?.width ?? orderedSegs[0].width;
		const nextSegWidth = orderedSegs[i]?.width ?? prevSegWidth;
		const maxLineWidth = Math.max(prevSegWidth, nextSegWidth);
		const baseRadius = maxLineWidth * ratio;

		// DRC 判定的坏拐角：强制保持直角。
		if (badCorners.has(i)) {
			ops.push({ type: 'line', start: currentStart, end: pCorner, width: prevSegWidth, cornerIndex: i });
			currentStart = pCorner;
			continue;
		}

		let radius = baseRadius;
		const scale = cornerScales.get(i);
		if (scale !== undefined)
			radius = baseRadius * scale;

		// 差分/等长保护组的覆盖指令。
		const override = cornerOverrides.get(i);
		if (override === null) {
			ops.push({ type: 'line', start: currentStart, end: pCorner, width: prevSegWidth, cornerIndex: i });
			currentStart = pCorner;
			continue;
		}
		if (override) {
			ops.push({ type: 'line', start: currentStart, end: override.start, width: prevSegWidth, cornerIndex: i });
			ops.push({ type: 'arc', start: override.start, end: override.end, width: override.width, angle: override.angle, cornerIndex: i });
			currentStart = override.end;
			continue;
		}

		let isMerged = false;

		// U 型弯合并：相邻两个短拐角合成一个大圆弧。
		try {
			if (settings.mergeTransitionSegments && i < points.length - 2 && scale === undefined && !badCorners.has(i + 1)) {
				const pAfter = points[i + 2];
				if (pAfter) {
					const segLen = dist(pCorner, pNext);
					if (segLen < radius * 1.5) {
						const vIn = { x: pPrev.x - pCorner.x, y: pPrev.y - pCorner.y };
						const vMid = { x: pNext.x - pCorner.x, y: pNext.y - pCorner.y };
						const vOut = { x: pAfter.x - pNext.x, y: pAfter.y - pNext.y };
						const angle1 = getAngleBetween({ x: -vIn.x, y: -vIn.y }, { x: vMid.x, y: vMid.y });
						const angle2 = getAngleBetween({ x: vMid.x, y: vMid.y }, { x: vOut.x, y: vOut.y });

						if (angle1 * angle2 > 0 && Math.abs(angle1) > 1 && Math.abs(angle2) > 1) {
							const intersection = getLineIntersection(pPrev, pCorner, pNext, pAfter);
							if (intersection) {
								const dInt1 = dist(intersection, pCorner);
								const dInt2 = dist(intersection, pNext);
								if (dInt1 < segLen * 10 && dInt2 < segLen * 10) {
									const tV1 = { x: pPrev.x - intersection.x, y: pPrev.y - intersection.y };
									const tV2 = { x: pAfter.x - intersection.x, y: pAfter.y - intersection.y };
									const tMag1 = Math.sqrt(tV1.x ** 2 + tV1.y ** 2);
									const tMag2 = Math.sqrt(tV2.x ** 2 + tV2.y ** 2);
									const tDot = (tV1.x * tV2.x + tV1.y * tV2.y) / (tMag1 * tMag2);
									const tAngleRad = Math.acos(Math.max(-1, Math.min(1, tDot)));
									const tTanVal = Math.tan(tAngleRad / 2);
									let tD = 0;
									if (Math.abs(tTanVal) > 0.0001)
										tD = radius / tTanVal;

									const tMaxAllowedRadius = Math.min(tMag1 * 0.95, tMag2 * 0.95);
									const tActualD = Math.min(tD, tMaxAllowedRadius);
									const tEffectiveRadius = tActualD * Math.abs(tTanVal);

									if (tActualD > 0.05 && tEffectiveRadius >= (maxLineWidth / 2) - 0.05) {
										const pStart = lerp(intersection, pPrev, tActualD / tMag1);
										const pEnd = lerp(intersection, pAfter, tActualD / tMag2);
										if (dist(currentStart, pStart) > 0.001)
											ops.push({ type: 'line', start: currentStart, end: pStart, width: prevSegWidth, cornerIndex: i });

										const tSweptAngle = getAngleBetween({ x: -tV1.x, y: -tV1.y }, { x: tV2.x, y: tV2.y });
										const afterSegWidth = orderedSegs[i + 1]?.width ?? nextSegWidth;
										ops.push({ type: 'arc', start: pStart, end: pEnd, width: afterSegWidth, angle: tSweptAngle, cornerIndex: i });

										currentStart = pEnd;
										i++; // 跳过下一个点
										isMerged = true;
									}
								}
							}
						}
					}
				}
			}
		}
		catch { /* 合并失败退化到普通圆角 */ }

		// 普通圆角。
		if (!isMerged) {
			const v1 = { x: pPrev.x - pCorner.x, y: pPrev.y - pCorner.y };
			const v2 = { x: pNext.x - pCorner.x, y: pNext.y - pCorner.y };
			const mag1 = Math.sqrt(v1.x ** 2 + v1.y ** 2);
			const mag2 = Math.sqrt(v2.x ** 2 + v2.y ** 2);
			const dot = (v1.x * v2.x + v1.y * v2.y) / (mag1 * mag2);
			const safeDot = Math.max(-1, Math.min(1, dot));
			const angleRad = Math.acos(safeDot);
			const tanVal = Math.tan(angleRad / 2);

			let d = 0;
			if (Math.abs(tanVal) > 0.0001)
				d = radius / tanVal;

			const maxAllowedRadius = Math.min(mag1 * 0.45, mag2 * 0.45);
			const actualD = Math.min(d, maxAllowedRadius);
			let isSkipped = false;

			if (d > 0.001 && actualD < d * 0.95 && !settings.forceArc)
				isSkipped = true;
			if (!isSkipped) {
				const effectiveRadius = actualD * Math.abs(tanVal);
				if (effectiveRadius < (maxLineWidth / 2) - 0.05)
					isSkipped = true;
			}

			if (actualD > 0.05 && !isSkipped) {
				const pStart = lerp(pCorner, pPrev, actualD / mag1);
				const pEnd = lerp(pCorner, pNext, actualD / mag2);
				ops.push({ type: 'line', start: currentStart, end: pStart, width: prevSegWidth, cornerIndex: i });
				const sweptAngle = getAngleBetween({ x: -v1.x, y: -v1.y }, { x: v2.x, y: v2.y });
				ops.push({ type: 'arc', start: pStart, end: pEnd, width: nextSegWidth, angle: sweptAngle, cornerIndex: i });
				currentStart = pEnd;
			}
			else {
				ops.push({ type: 'line', start: currentStart, end: pCorner, width: prevSegWidth, cornerIndex: i });
				currentStart = pCorner;
			}
		}
	}

	const lastSegWidth = orderedSegs[orderedSegs.length - 1]?.width ?? orderedSegs[0].width;
	ops.push({ type: 'line', start: currentStart, end: points[points.length - 1], width: lastSegWidth, cornerIndex: points.length - 1 });
	return ops;
}

// ─── 差分/等长保护组 ──────────────────────────────────────────────────────
async function loadProtectedNetGroups(settings: Settings): Promise<ProtectedNetGroup[]> {
	if (!settings.protectDifferentialAndEqualLength)
		return [];

	const groups: ProtectedNetGroup[] = [];
	try {
		// 这两个 API 在部分 pro-api 版本尚不存在 —— feature-detect，缺失即无保护组（退化为独立圆角）。
		const drcApi = eda.pcb_Drc as unknown as {
			getAllDifferentialPairs?: () => Promise<unknown>;
			getAllEqualLengthNetGroups?: () => Promise<unknown>;
		};
		const differentialRaw = typeof drcApi.getAllDifferentialPairs === 'function'
			? await drcApi.getAllDifferentialPairs()
			: [];
		for (const pair of normalizeDifferentialPairs(differentialRaw)) {
			const nets = uniqueNets([pair.positiveNet, pair.negativeNet]);
			if (nets.length >= 2)
				groups.push({ key: `diff:${pair.name || nets.join('/')}`, name: pair.name || nets.join('/'), type: 'differential', nets });
		}

		const equalLengthGroups = typeof drcApi.getAllEqualLengthNetGroups === 'function'
			? await drcApi.getAllEqualLengthNetGroups()
			: [];
		if (Array.isArray(equalLengthGroups)) {
			for (const group of equalLengthGroups) {
				const nets = uniqueNets((group as { nets?: unknown[] })?.nets || []);
				if (nets.length >= 2) {
					const name = (group as { name?: string })?.name || nets.join('/');
					groups.push({ key: `eq:${name}`, name, type: 'equalLength', nets });
				}
			}
		}
	}
	catch { /* 读取失败即跳过保护 */ }

	return groups;
}

function normalizeDifferentialPairs(input: unknown): Array<{ name?: string; positiveNet?: string; negativeNet?: string }> {
	const pairs: Array<{ name?: string; positiveNet?: string; negativeNet?: string }> = [];
	const visit = (value: unknown) => {
		if (!value)
			return;
		if (Array.isArray(value)) {
			for (const item of value) visit(item);
			return;
		}
		if (typeof value !== 'object')
			return;
		const v = value as { positiveNet?: unknown; negativeNet?: unknown };
		if (typeof v.positiveNet === 'string' && typeof v.negativeNet === 'string') {
			pairs.push(value as { name?: string; positiveNet?: string; negativeNet?: string });
			return;
		}
		for (const child of Object.values(value)) visit(child);
	};
	visit(input);
	return pairs;
}

function uniqueNets(nets: unknown[]): string[] {
	const result: string[] = [];
	for (const net of nets) {
		if (typeof net !== 'string' || !net)
			continue;
		if (!result.includes(net))
			result.push(net);
	}
	return result;
}

function buildNetToProtectedGroups(groups: ProtectedNetGroup[]): Map<string, ProtectedNetGroup[]> {
	const map = new Map<string, ProtectedNetGroup[]>();
	for (const group of groups) {
		for (const net of group.nets) {
			if (!map.has(net))
				map.set(net, []);
			map.get(net)!.push(group);
		}
	}
	return map;
}

function refreshProtectedCornerOverrides(activePaths: PathContext[], protectedGroups: ProtectedNetGroup[], settings: Settings) {
	for (const ctx of activePaths) {
		ctx.cornerOverrides.clear();
		ctx.protectedCornerKeys.clear();
	}
	if (!settings.protectDifferentialAndEqualLength || protectedGroups.length === 0)
		return;

	for (const group of protectedGroups) {
		const contexts = activePaths.filter(ctx => ctx.protectedGroupKeys.includes(group.key));
		const contextsByLayer = new Map<number, PathContext[]>();
		for (const ctx of contexts) {
			if (!contextsByLayer.has(ctx.layer))
				contextsByLayer.set(ctx.layer, []);
			contextsByLayer.get(ctx.layer)!.push(ctx);
		}

		for (const [layer, layerContexts] of contextsByLayer) {
			const groupNets = new Set(group.nets);
			const presentNets = new Set(layerContexts.map(ctx => ctx.net));
			const hasFullGroup = Array.from(groupNets).every(net => presentNets.has(net));
			if (!hasFullGroup) {
				for (const ctx of layerContexts)
					markAllProtectedCornersStraight(ctx, group.key, layer);
				continue;
			}

			const maxCornerCount = Math.max(...layerContexts.map(ctx => ctx.points.length - 2));
			for (let cornerIndex = 1; cornerIndex <= maxCornerCount; cornerIndex++) {
				const cornerKey = `${group.key}#${layer}#${cornerIndex}`;
				const candidates = [];
				let canProtect = true;
				for (const ctx of layerContexts) {
					if (cornerIndex >= ctx.points.length - 1) {
						canProtect = false;
						break;
					}
					ctx.protectedCornerKeys.set(cornerIndex, cornerKey);
					if (ctx.badCorners.has(cornerIndex)) {
						canProtect = false;
						break;
					}
					const candidate = computeCornerArcCandidate(
						ctx.points, ctx.orderedSegs, cornerIndex,
						{ ...settings, forceArc: false }, ctx.cornerScales.get(cornerIndex),
					);
					if (!candidate) {
						canProtect = false;
						break;
					}
					candidates.push({ ctx, candidate });
				}

				if (!canProtect) {
					for (const ctx of layerContexts) {
						if (cornerIndex < ctx.points.length - 1)
							ctx.cornerOverrides.set(cornerIndex, null);
					}
					continue;
				}

				const overrides = buildConcentricOverrides(candidates.map(item => item.candidate));
				if (!overrides) {
					for (const { ctx } of candidates)
						ctx.cornerOverrides.set(cornerIndex, null);
					continue;
				}
				for (let i = 0; i < candidates.length; i++)
					candidates[i].ctx.cornerOverrides.set(cornerIndex, overrides[i]);
			}
		}
	}
}

function markAllProtectedCornersStraight(ctx: PathContext, groupKey: string, layer: number) {
	for (let cornerIndex = 1; cornerIndex < ctx.points.length - 1; cornerIndex++) {
		ctx.protectedCornerKeys.set(cornerIndex, `${groupKey}#${layer}#${cornerIndex}`);
		ctx.cornerOverrides.set(cornerIndex, null);
	}
}

function syncProtectedCornerRepair(
	activePaths: PathContext[],
	sourceCtx: PathContext,
	cornerIndex: number,
	apply: (ctx: PathContext, idx: number) => void,
): PathContext[] {
	const cornerKey = sourceCtx.protectedCornerKeys.get(cornerIndex);
	if (!cornerKey) {
		apply(sourceCtx, cornerIndex);
		return [sourceCtx];
	}
	const changed: PathContext[] = [];
	for (const ctx of activePaths) {
		for (const [idx, key] of ctx.protectedCornerKeys) {
			if (key === cornerKey) {
				apply(ctx, idx);
				changed.push(ctx);
			}
		}
	}
	return changed.length > 0 ? changed : [sourceCtx];
}

// ─── 读入待美化的导线 ─────────────────────────────────────────────────────
interface RawTrack { id: string; net: string; layer: number; width: number; p1: Point; p2: Point }

async function readRawTracks(opts: BeautifyOptions): Promise<RawTrack[]> {
	let lines: Array<{
		getState_PrimitiveId: () => string;
		getState_Net: () => string;
		getState_Layer: () => number;
		getState_StartX: () => number;
		getState_StartY: () => number;
		getState_EndX: () => number;
		getState_EndY: () => number;
		getState_LineWidth: () => number;
		getState_PrimitiveLock: () => boolean;
	}>;

	// net 白名单：合并单网 opts.net 与多网 opts.nets（空=不过滤）。
	const wantNets = new Set<string>([
		...(opts.net ? [opts.net] : []),
		...(opts.nets ?? []),
	]);
	const netFilter = wantNets.size > 0 ? wantNets : null;

	if (opts.scope === 'selected') {
		const selectedIds = await eda.pcb_SelectControl.getAllSelectedPrimitives_PrimitiveId();
		if (!selectedIds || selectedIds.length === 0)
			return [];
		const want = new Set(selectedIds);
		const all = (await eda.pcb_PrimitiveLine.getAll()) ?? [];
		lines = all.filter(l => want.has(l.getState_PrimitiveId()));
	}
	else {
		// 单网时下推到 getAll 加速；多网/全网时取全部，在下方按 netFilter 过滤
		// （getAll 的 layer 形参类型收窄，层过滤统一在循环里做）。
		const single = netFilter && netFilter.size === 1 ? [...netFilter][0] : undefined;
		lines = (await eda.pcb_PrimitiveLine.getAll(single)) ?? [];
	}

	const out: RawTrack[] = [];
	for (const l of lines) {
		if (l.getState_PrimitiveLock())
			continue;
		const layer = Number(l.getState_Layer());
		if (!onCopper(layer))
			continue;
		if (netFilter && !netFilter.has(l.getState_Net()))
			continue;
		if (opts.layer != null && layer !== opts.layer)
			continue;
		out.push({
			id: l.getState_PrimitiveId(),
			net: l.getState_Net(),
			layer,
			width: l.getState_LineWidth(),
			p1: { x: l.getState_StartX(), y: l.getState_StartY() },
			p2: { x: l.getState_EndX(), y: l.getState_EndY() },
		});
	}
	return out;
}

// ─── 主入口 ───────────────────────────────────────────────────────────────
export async function runBeautify(opts: BeautifyOptions): Promise<BeautifySummary> {
	const settings: Settings = {
		cornerRadiusRatio: opts.cornerRadiusRatio,
		forceArc: opts.forceArc,
		mergeTransitionSegments: opts.mergeTransitionSegments,
		protectDifferentialAndEqualLength: opts.protectDifferentialAndEqualLength,
	};

	const summary: BeautifySummary = {
		scope: opts.scope,
		dryRun: opts.dryRun,
		tracksConsidered: 0,
		paths: 0,
		cornersRounded: 0,
		linesCreated: 0,
		arcsCreated: 0,
		protectedGroups: 0,
		drcRounds: 0,
		pourRebuilt: null,
	};

	const tracks = await readRawTracks(opts);
	summary.tracksConsidered = tracks.length;
	if (tracks.length < 1) {
		summary.skipped = opts.scope === 'selected'
			? '未选中可处理的铜层导线（先在 EasyEDA 里框选走线，或用 scope=all）'
			: '未找到可处理的铜层导线';
		return summary;
	}

	const protectedGroups = await loadProtectedNetGroups(settings);
	const netToProtectedGroups = buildNetToProtectedGroups(protectedGroups);
	summary.protectedGroups = protectedGroups.length;

	// ── 路径提取：按 (net, layer) 分组，端点相接（degree<=2）串成有序多段线 ──
	const groups = new Map<string, RawTrack[]>();
	for (const t of tracks) {
		const key = `${t.net}#@#${t.layer}`;
		if (!groups.has(key))
			groups.set(key, []);
		groups.get(key)!.push(t);
	}

	const activePaths: PathContext[] = [];
	let pathIdCounter = 0;

	for (const [key, group] of groups) {
		const [net, layerStr] = key.split('#@#');
		const layer = Number(layerStr);
		const segs: OrderedSeg[] = group.map(t => ({ p1: t.p1, p2: t.p2, width: t.width, id: t.id }));

		// 3 位小数精度的坐标键，避免浮点误差导致断连。
		const pointKey = (p: Point) => `${p.x.toFixed(3)},${p.y.toFixed(3)}`;
		const connections = new Map<string, OrderedSeg[]>();
		for (const seg of segs) {
			for (const k of [pointKey(seg.p1), pointKey(seg.p2)]) {
				if (!connections.has(k))
					connections.set(k, []);
				connections.get(k)!.push(seg);
			}
		}

		const used = new Set<string>();
		for (const startSeg of segs) {
			if (used.has(startSeg.id))
				continue;
			const points: Point[] = [startSeg.p1, startSeg.p2];
			const orderedSegs: OrderedSeg[] = [startSeg];
			used.add(startSeg.id);

			let extended = true;
			while (extended) {
				extended = false;
				const lastKey = pointKey(points[points.length - 1]);
				const lastConns = connections.get(lastKey) || [];
				if (lastConns.length <= 2) {
					for (const seg of lastConns) {
						if (used.has(seg.id))
							continue;
						if (pointKey(seg.p1) === lastKey) {
							points.push(seg.p2); orderedSegs.push(seg); used.add(seg.id); extended = true; break;
						}
						else if (pointKey(seg.p2) === lastKey) {
							points.push(seg.p1); orderedSegs.push(seg); used.add(seg.id); extended = true; break;
						}
					}
				}
				if (!extended) {
					const firstKey = pointKey(points[0]);
					const firstConns = connections.get(firstKey) || [];
					if (firstConns.length <= 2) {
						for (const seg of firstConns) {
							if (used.has(seg.id))
								continue;
							if (pointKey(seg.p1) === firstKey) {
								points.unshift(seg.p2); orderedSegs.unshift(seg); used.add(seg.id); extended = true; break;
							}
							else if (pointKey(seg.p2) === firstKey) {
								points.unshift(seg.p1); orderedSegs.unshift(seg); used.add(seg.id); extended = true; break;
							}
						}
					}
				}
			}

			if (points.length >= 3) {
				activePaths.push({
					pathId: pathIdCounter++,
					points,
					orderedSegs,
					net,
					layer,
					deleteIds: orderedSegs.map(s => s.id),
					createdIds: [],
					idToCornerMap: new Map(),
					badCorners: new Set(),
					cornerScales: new Map(),
					protectedGroupKeys: (netToProtectedGroups.get(net) || []).map(g => g.key),
					protectedCornerKeys: new Map(),
					cornerOverrides: new Map(),
					arcN: 0,
					lineN: 0,
				});
			}
		}
	}

	summary.paths = activePaths.length;
	if (activePaths.length === 0) {
		summary.skipped = '没有 >=3 点的连续走线可圆滑（都是单段或分叉）';
		return summary;
	}

	refreshProtectedCornerOverrides(activePaths, protectedGroups, settings);

	// ── dryRun：只计算规划，不落笔 ──
	if (opts.dryRun) {
		for (const ctx of activePaths) {
			const ops = generatePathOps(ctx, settings, ctx.badCorners, ctx.cornerScales, ctx.cornerOverrides);
			for (const op of ops) {
				if (dist(op.start, op.end) < 0.001)
					continue;
				if (op.type === 'arc') { summary.arcsCreated++; summary.cornersRounded++; }
				else summary.linesCreated++;
			}
		}
		return summary;
	}

	const resolveNewId = (res: unknown): string | null => {
		if (typeof res === 'string')
			return res;
		const r = res as { id?: string; primitiveId?: string; getState_PrimitiveId?: () => string } | null;
		if (r && r.id)
			return r.id;
		if (r && r.primitiveId)
			return r.primitiveId;
		if (r && r.getState_PrimitiveId)
			return r.getState_PrimitiveId();
		return null;
	};

	// Counts are tracked PER PATH and reset on each (re-)commit so the summary
	// reflects the FINAL geometry, not the cumulative total across DRC-repair
	// rounds (each repair deletes then recreates a path's primitives).
	const commitOps = async (ops: PathOp[], ctx: PathContext) => {
		ctx.arcN = 0;
		ctx.lineN = 0;
		for (const item of ops) {
			if (dist(item.start, item.end) < 0.001)
				continue;
			let newId: string | null = null;
			if (item.type === 'line') {
				const res = await eda.pcb_PrimitiveLine.create(
					ctx.net, ctx.layer as unknown as TPCB_LayersOfLine,
					item.start.x, item.start.y, item.end.x, item.end.y, item.width,
				);
				newId = resolveNewId(res);
				if (newId) ctx.lineN++;
			}
			else {
				const res = await eda.pcb_PrimitiveArc.create(
					ctx.net, ctx.layer as unknown as TPCB_LayersOfLine,
					item.start.x, item.start.y, item.end.x, item.end.y, item.angle!, item.width,
				);
				newId = resolveNewId(res);
				if (newId) ctx.arcN++;
			}
			if (newId) {
				ctx.createdIds.push(newId);
				ctx.idToCornerMap.set(newId, item.cornerIndex);
			}
		}
	};

	// ── 删除原始线段（逐个删，确保成功） ──
	for (const ctx of activePaths) {
		for (const id of ctx.deleteIds) {
			try { await eda.pcb_PrimitiveLine.delete([id]); }
			catch { /* per-id best-effort */ }
		}
	}

	// ── 第一次乐观生成 ──
	for (const ctx of activePaths) {
		const ops = generatePathOps(ctx, settings, ctx.badCorners, ctx.cornerScales, ctx.cornerOverrides);
		await commitOps(ops, ctx);
	}

	// ── DRC 二分修复（缩小违规拐角半径，直到通过或退化为直角） ──
	if (opts.enableDrc) {
		let drcAttempt = 0;
		const maxDrcRetries = opts.drcRetryCount || 4;
		while (drcAttempt <= maxDrcRetries) {
			const isFinalAttempt = drcAttempt === maxDrcRetries;
			const violatedIds = await runDrcCheckAndParse(opts.drcIgnoreCopperPour);
			if (violatedIds.size === 0)
				break;

			const pathsToRepair = new Set<PathContext>();
			for (const ctx of activePaths) {
				for (const id of ctx.createdIds) {
					if (violatedIds.has(id)) {
						const idx = ctx.idToCornerMap.get(id);
						if (idx !== undefined) {
							const changed = syncProtectedCornerRepair(activePaths, ctx, idx, (repairCtx, repairIdx) => {
								const currentScale = repairCtx.cornerScales.get(repairIdx) ?? 1.0;
								const nextScale = currentScale * 0.5;
								if (isFinalAttempt || nextScale < 0.1)
									repairCtx.badCorners.add(repairIdx);
								else
									repairCtx.cornerScales.set(repairIdx, nextScale);
							});
							for (const changedCtx of changed)
								pathsToRepair.add(changedCtx);
						}
					}
				}
			}

			if (pathsToRepair.size === 0)
				break; // 违规不属于本次生成的图元

			refreshProtectedCornerOverrides(activePaths, protectedGroups, settings);
			for (const ctx of pathsToRepair) {
				if (ctx.createdIds.length > 0) {
					try {
						await eda.pcb_PrimitiveLine.delete(ctx.createdIds);
						await eda.pcb_PrimitiveArc.delete(ctx.createdIds);
					}
					catch { /* best-effort */ }
					ctx.createdIds = [];
					ctx.idToCornerMap.clear();
				}
				const ops = generatePathOps(ctx, settings, ctx.badCorners, ctx.cornerScales, ctx.cornerOverrides);
				await commitOps(ops, ctx);
			}
			drcAttempt++;
		}
		summary.drcRounds = drcAttempt;
	}

	// 从各路径最终态汇总计数（反映最终几何，避免 DRC 重试的累计虚高）。
	summary.arcsCreated = activePaths.reduce((n, c) => n + c.arcN, 0);
	summary.linesCreated = activePaths.reduce((n, c) => n + c.lineN, 0);
	summary.cornersRounded = summary.arcsCreated;

	// ── 重铺覆铜（美化删/建轨迹会让同网 GND 键合 stale，重铺恢复连通性） ──
	if (opts.rebuildPour) {
		let rebuilt = 0;
		try {
			const pours = (await eda.pcb_PrimitivePour.getAll()) ?? [];
			for (const p of pours) {
				try { if (await p.rebuildCopperRegion()) rebuilt++; }
				catch { /* per-pour best-effort */ }
			}
		}
		catch { /* pour rebuild best-effort */ }
		summary.pourRebuilt = rebuilt;
	}

	return summary;
}
