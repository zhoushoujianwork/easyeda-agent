// PCB 走线美化 — DRC 结果解析 (提取违规原语 ID, 供二分修复定位坏拐角)。
//
// 移植自开源扩展 Easy_EDA_PCB_Beautify（作者 m-RNA，Apache-2.0）：
//   https://github.com/m-RNA/Easy_EDA_PCB_Beautify  ·  src/lib/drc.ts
// 完整第三方许可与署名见仓库根目录 NOTICE。改动：去掉 settings/logger 依赖，
// 以显式 opts 传入；覆铜过滤保持三层精确结构。

// eda.pcb_Drc.check(false, false, true) 的三层返回结构（Category → SubCategory → Issue）。
interface DrcIssue {
	errorObjType?: string;
	objs?: unknown[];
	explanation?: { errData?: { obj1?: unknown; obj2?: unknown } };
}
interface DrcSubCategory { name?: string; list?: DrcIssue[]; count?: number }
interface DrcCategory { name?: string; list?: DrcSubCategory[]; count?: number }

/**
 * 运行 DRC 检查并解析出涉及违规的原语 ID 集合。
 * @param ignoreCopperPour 忽略覆铜相关违规（重铺后通常自动消解）
 * @returns 违规原语 ID 集合；DRC 不可用/异常时返回空集（不抛，best-effort）
 */
export async function runDrcCheckAndParse(ignoreCopperPour: boolean): Promise<Set<string>> {
	const violatedIds = new Set<string>();
	try {
		const categories = (await eda.pcb_Drc.check(false, false, true)) as unknown as DrcCategory[];
		if (!Array.isArray(categories))
			return violatedIds;

		const filtered = ignoreCopperPour ? filterOutCopperPourIssues(categories) : categories;
		for (const category of filtered) {
			if (!Array.isArray(category.list))
				continue;
			for (const subCategory of category.list) {
				if (!Array.isArray(subCategory.list))
					continue;
				for (const issue of subCategory.list)
					extractViolatedIds(issue, violatedIds);
			}
		}
		return violatedIds;
	}
	catch {
		return violatedIds;
	}
}

/**
 * 提取单条 issue 的违规对象 ID。
 * 主路径 issue.objs[]；备用 issue.explanation.errData.obj1/obj2。
 */
function extractViolatedIds(issue: DrcIssue, ids: Set<string>): void {
	if (Array.isArray(issue.objs)) {
		for (const id of issue.objs)
			if (typeof id === 'string') ids.add(id);
	}
	const errData = issue.explanation?.errData;
	if (errData) {
		if (typeof errData.obj1 === 'string') ids.add(errData.obj1);
		if (typeof errData.obj2 === 'string') ids.add(errData.obj2);
	}
}

function isCopperPourSubCategory(sub: DrcSubCategory): boolean {
	return sub.name?.includes('Copper Region') ?? false;
}

function isCopperPourIssue(issue: DrcIssue): boolean {
	return issue.errorObjType?.includes('Copper Region') ?? false;
}

/** 过滤掉覆铜相关的 DRC issue（三层结构，空组自动剪枝）。 */
function filterOutCopperPourIssues(categories: DrcCategory[]): DrcCategory[] {
	return categories
		.map((category) => {
			const filteredSubs = (category.list ?? [])
				.filter(sub => !isCopperPourSubCategory(sub))
				.map((sub) => {
					const filteredIssues = (sub.list ?? []).filter(issue => !isCopperPourIssue(issue));
					return { ...sub, list: filteredIssues, count: filteredIssues.length };
				})
				.filter(sub => (sub.list?.length ?? 0) > 0);

			const totalCount = filteredSubs.reduce((n, s) => n + (s.count ?? 0), 0);
			return { ...category, list: filteredSubs, count: totalCount };
		})
		.filter(category => (category.list?.length ?? 0) > 0);
}
