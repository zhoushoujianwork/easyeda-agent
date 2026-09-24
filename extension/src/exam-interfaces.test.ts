/// <reference types="@jlceda/pro-api-types" />

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { runAction } from './actions';

function withEda(mock: Record<string, unknown>, run: () => Promise<void>): Promise<void> {
	(globalThis as any).eda = mock;
	return run().finally(() => { delete (globalThis as any).eda; });
}

function footprintDocumentControl(uuid: string, libraryUuid: string, tabId: string): Record<string, unknown> {
	let current = { uuid: 'previous', parentLibraryUuid: 'previous-library', documentType: 0, tabId: 'previous-tab' };
	return {
		lib_LibrariesList: { getSystemLibraryUuid: async () => 'system-library' },
		dmt_SelectControl: { getCurrentDocumentInfo: async () => current },
		dmt_EditorControl: {
			getSplitScreenIdByTabId: async () => 'split-1',
			openLibraryDocument: async () => {
				current = { uuid, parentLibraryUuid: libraryUuid, documentType: 4, tabId };
				return tabId;
			},
			activateDocument: async () => true,
		},
	};
}

test('library.footprint.region_create refuses the immutable system library before opening or mutating', async () => {
	let opened = 0;
	let created = 0;
	await withEda({
		lib_LibrariesList: { getSystemLibraryUuid: async () => 'system-library' },
		dmt_EditorControl: { openLibraryDocument: async () => { opened++; return 'tab'; } },
		pcb_MathPolygon: { createPolygon: () => ({ getSource: () => [0, 0, 'L', 10, 0, 10, 10, 0, 10, 0, 0] }) },
		pcb_PrimitiveRegion: { create: async () => { created++; return undefined; } },
	}, async () => {
		await assert.rejects(
			() => runAction('library.footprint.region_create', {
				uuid: 'system-footprint', libraryUuid: 'system-library',
				points: [[0, 0], [10, 0], [10, 10], [0, 10]],
			}),
			/immutable system library.*no geometry was created/i,
		);
		assert.equal(opened, 0);
		assert.equal(created, 0);
	});
});

test('project.create forwards official fields and reports a created-but-not-opened partial outcome', async () => {
	const calls: unknown[][] = [];
	await withEda({
		dmt_Project: {
			createProject: async (...args: unknown[]) => { calls.push(args); return 'project-1'; },
			openProject: async () => { throw new Error('open failed'); },
			getCurrentProjectInfo: async () => undefined,
		},
	}, async () => {
		const res: any = await runAction('project.create', {
			friendlyName: 'AT32F415 demo', projectName: 'at32-demo', teamUuid: 'team-1',
			folderUuid: 'folder-1', description: 'exam', open: true,
		});
		assert.deepEqual(calls, [['AT32F415 demo', 'at32-demo', 'team-1', 'folder-1', 'exam']]);
		assert.deepEqual(res.result, {
			partial: true,
			uuid: 'project-1', friendlyName: 'AT32F415 demo', projectName: 'at32-demo',
			created: true, opened: false,
		});
		assert.match(res.warnings[0], /created.*opening.*failed/i);
	});
});

test('project.create reports openProject=false as a created partial outcome', async () => {
	await withEda({
		dmt_Project: {
			createProject: async () => 'project-2',
			openProject: async () => false,
		},
	}, async () => {
		const res: any = await runAction('project.create', { friendlyName: 'demo', open: true });
		assert.equal(res.result.created, true);
		assert.equal(res.result.opened, false);
		assert.equal(res.result.partial, true);
		assert.match(res.warnings[0], /returned false/i);
	});
});

test('project.find matches exact friendlyName and team in the team root inventory', async () => {
	const calls: unknown[][] = [];
	await withEda({
		dmt_Project: {
			getAllProjectsUuid: async (...args: unknown[]) => { calls.push(args); return ['p1', 'p2', 'p3']; },
			getProjectInfo: async (uuid: string) => ({
				uuid, friendlyName: uuid === 'p1' ? 'Exact' : uuid === 'p2' ? 'exact' : 'Exact',
				teamUuid: uuid === 'p3' ? 'other-team' : 'team-1', folderUuid: 'root',
			}),
		},
	}, async () => {
		const res: any = await runAction('project.find', { friendlyName: 'Exact', teamUuid: 'team-1' });
		assert.deepEqual(calls, [['team-1']]);
		assert.equal(res.result.presence, 'found');
		assert.deepEqual(res.result.matches, [{ uuid: 'p1', friendlyName: 'Exact', teamUuid: 'team-1', folderUuid: 'root' }]);
		assert.deepEqual(res.result.enumeration, {
			scope: 'team-root', complete: false, reason: 'team_scope_mismatch',
			uuidCount: 3, readableCount: 3, unreadableUuids: [],
		});
	});
});

test('project.find establishes absence only after every nonempty team-root entry is readable', async () => {
	await withEda({
		dmt_Project: {
			getAllProjectsUuid: async () => ['p1', 'p2'],
			getProjectInfo: async (uuid: string) => ({ uuid, friendlyName: 'Other', teamUuid: 'team-1' }),
		},
	}, async () => {
		const res: any = await runAction('project.find', { friendlyName: 'Exact', teamUuid: 'team-1' });
		assert.equal(res.result.presence, 'absent');
		assert.equal(res.result.enumeration.complete, true);
		assert.equal(res.result.enumeration.scope, 'team-root');
	});
});

test('project.find treats empty, unreadable, and unscoped inventories as unknown', async () => {
	for (const tc of [
		{ uuids: [], info: async () => undefined, teamUuid: 'team-1', reason: 'empty_uuid_list' },
		{ uuids: ['p1'], info: async () => undefined, teamUuid: 'team-1', reason: 'project_info_incomplete' },
		{ uuids: ['p1'], info: async (uuid: string) => ({ uuid, friendlyName: 'Other', teamUuid: 'team-1' }), teamUuid: undefined, reason: 'team_not_specified' },
	]) {
		await withEda({
			dmt_Project: { getAllProjectsUuid: async () => tc.uuids, getProjectInfo: tc.info },
		}, async () => {
			const res: any = await runAction('project.find', { friendlyName: 'Exact', teamUuid: tc.teamUuid });
			assert.equal(res.result.presence, 'unknown');
			assert.equal(res.result.enumeration.complete, false);
			assert.equal(res.result.enumeration.reason, tc.reason);
		});
	}
});

test('project.find reports enumeration failure as unknown without retrying or creating', async () => {
	let count = 0;
	await withEda({
		dmt_Project: {
			getAllProjectsUuid: async () => { count++; throw new Error('unavailable'); },
			getProjectInfo: async () => { throw new Error('must not read'); },
			createProject: async () => { throw new Error('must not create'); },
		},
	}, async () => {
		const res: any = await runAction('project.find', { friendlyName: 'Exact', teamUuid: 'team-1' });
		assert.equal(count, 1);
		assert.equal(res.result.presence, 'unknown');
		assert.equal(res.result.enumeration.reason, 'uuid_list_failed');
		assert.match(res.warnings[0], /unavailable/);
	});
});

test('pcb.component.delete rejects a stale ID before the official delete call', async () => {
	let deleteCalls = 0;
	await withEda({
		pcb_PrimitiveComponent: {
			getAll: async () => [{ getState_PrimitiveId: () => 'live-1' }],
			delete: async () => { deleteCalls++; return true; },
		},
	}, async () => {
		await assert.rejects(
			() => runAction('pcb.component.delete', { primitiveIds: ['live-1', 'stale-1'] }),
			(err: any) => err.code === 'PRECONDITION_REFUSED' && /stale-1.*no deletion was dispatched/.test(err.message),
		);
		assert.equal(deleteCalls, 0);
		const res: any = await runAction('pcb.component.delete', { primitiveIds: 'live-1' });
		assert.equal(res.result.deleted, true);
		assert.equal(deleteCalls, 1);
	});
});

test('pcb.net_class.create validates live nets, creates once, and verifies membership', async () => {
	let classes: any[] = [];
	let createCalls = 0;
	await withEda({
		pcb_Net: { getAllNetsName: async () => ['+5V', '+3V3', 'GND'] },
		pcb_Drc: {
			getAllNetClasses: async () => classes,
			getNetRules: async () => [],
			createNetClass: async (name: string, nets: string[], color: unknown) => {
				createCalls++;
				classes = [{ name, nets, color }];
				return true;
			},
		},
	}, async () => {
		const payload = { name: 'PWR_Class', nets: ['+5V', '+3V3', 'GND'] };
		const first: any = await runAction('pcb.net_class.create', payload);
		assert.equal(first.result.created, true);
		assert.equal(first.result.verified, true);
		const second: any = await runAction('pcb.net_class.create', payload);
		assert.equal(second.result.alreadyExists, true);
		assert.equal(createCalls, 1);
	});
});

test('pcb.drc.rules.set supports dry-run and exact write/readback', async () => {
	let current: Record<string, unknown> = { old: true };
	let netRules: Array<Record<string, unknown>> = [{ old: true }];
	await withEda({
		pcb_Drc: {
			getCurrentRuleConfiguration: async () => current,
			getNetRules: async () => netRules,
			overwriteCurrentRuleConfiguration: async (next: Record<string, unknown>) => { current = next; return true; },
			overwriteNetRules: async (next: Array<Record<string, unknown>>) => { netRules = next; return true; },
		},
	}, async () => {
		const requested = { ruleConfiguration: { clearance: 6 }, netRules: [{ class: 'PWR_Class', rule: 'PWR' }] };
		const preview: any = await runAction('pcb.drc.rules.set', { ...requested, dryRun: true });
		assert.equal(preview.result.dryRun, true);
		assert.deepEqual(current, { old: true });
		const applied: any = await runAction('pcb.drc.rules.set', requested);
		assert.equal(applied.result.verified, true);
		assert.deepEqual(applied.result.actual, requested);
	});
});

test('pcb.drc.rules.set unwraps the live {name, config} read shape and writes only bare config', async () => {
	let currentConfig: Record<string, unknown> = { old: true };
	let written: Record<string, unknown> | null = null;
	await withEda({
		pcb_Drc: {
			getCurrentRuleConfiguration: async () => ({
				name: written ? '自定义配置' : 'JLCPCB Capability(Two Layers Board)',
				config: currentConfig,
			}),
			overwriteCurrentRuleConfiguration: async (next: Record<string, unknown>) => {
				written = next;
				currentConfig = next;
				return true;
			},
		},
	}, async () => {
		const requested = { ruleConfiguration: { clearance: 6 } };
		const preview: any = await runAction('pcb.drc.rules.set', { ...requested, dryRun: true });
		assert.deepEqual(preview.result.before.ruleConfiguration, { old: true });
		const applied: any = await runAction('pcb.drc.rules.set', requested);
		assert.equal(applied.result.verified, true);
		assert.deepEqual(written, { clearance: 6 });
		assert.deepEqual(applied.result.actual.ruleConfiguration, { clearance: 6 });
	});
});

test('pcb.drc.rules.set accepts the exported {name, config} wrapper as input', async () => {
	let currentConfig: Record<string, unknown> = { old: true };
	let written: Record<string, unknown> | null = null;
	await withEda({
		pcb_Drc: {
			getCurrentRuleConfiguration: async () => ({ name: '当前配置', config: currentConfig }),
			overwriteCurrentRuleConfiguration: async (next: Record<string, unknown>) => {
				written = next;
				currentConfig = next;
				return true;
			},
		},
	}, async () => {
		const exported = { name: '自定义配置', config: { clearance: 6 } };
		const preview: any = await runAction('pcb.drc.rules.set', {
			ruleConfiguration: exported, dryRun: true,
		});
		assert.deepEqual(preview.result.requested.ruleConfiguration, { clearance: 6 });
		const applied: any = await runAction('pcb.drc.rules.set', { ruleConfiguration: exported });
		assert.equal(applied.result.verified, true);
		assert.deepEqual(written, { clearance: 6 });
		assert.deepEqual(applied.result.actual.ruleConfiguration, { clearance: 6 });
	});
});

test('pcb.drc.rules.set dry-run omits netRules when no net-rule write was requested', async () => {
	await withEda({
		pcb_Drc: {
			getCurrentRuleConfiguration: async () => ({ old: true }),
			getNetRules: async () => { throw new Error('must not read net rules'); },
		},
	}, async () => {
		const preview: any = await runAction('pcb.drc.rules.set', {
			ruleConfiguration: { clearance: 6 }, dryRun: true,
		});
		assert.equal(Object.hasOwn(preview.result.before, 'netRules'), false);
		assert.equal(Object.hasOwn(preview.result.requested, 'netRules'), false);
	});
});

test('pcb.drc.rules.set stops after a false first write and reports actual state', async () => {
	let netWriteCalls = 0;
	await withEda({
		pcb_Drc: {
			getCurrentRuleConfiguration: async () => ({ old: true }),
			getNetRules: async () => [{ old: true }],
			overwriteCurrentRuleConfiguration: async () => false,
			overwriteNetRules: async () => { netWriteCalls++; return true; },
		},
	}, async () => {
		const res: any = await runAction('pcb.drc.rules.set', {
			ruleConfiguration: { clearance: 6 }, netRules: [{ class: 'PWR' }],
		});
		assert.equal(res.result.verified, false);
		assert.equal(res.result.writeFailed, true);
		assert.equal(res.result.partial, false);
		assert.equal(netWriteCalls, 0);
		assert.deepEqual(res.result.actual, { ruleConfiguration: { old: true }, netRules: [{ old: true }] });
	});
});

test('pcb.drc.rules.set rolls back an uncertain first-stage throw before returning', async () => {
	const beforeRules = { old: true };
	const beforeNetRules = [{ old: true }];
	let currentRules: Record<string, unknown> = beforeRules;
	let currentNetRules: Array<Record<string, unknown>> = beforeNetRules;
	let ruleWriteCalls = 0;
	let netWriteCalls = 0;
	await withEda({
		pcb_Drc: {
			getCurrentRuleConfiguration: async () => currentRules,
			getNetRules: async () => currentNetRules,
			overwriteCurrentRuleConfiguration: async (next: Record<string, unknown>) => {
				ruleWriteCalls++;
				currentRules = next;
				if (ruleWriteCalls === 1) throw new Error('uncertain first write');
				return true;
			},
			overwriteNetRules: async (next: Array<Record<string, unknown>>) => {
				netWriteCalls++;
				currentNetRules = next;
				return true;
			},
		},
	}, async () => {
		const res: any = await runAction('pcb.drc.rules.set', {
			ruleConfiguration: { clearance: 6 }, netRules: [{ class: 'PWR' }],
		});
		assert.equal(res.result.partial, true);
		assert.equal(res.result.rulesWritten, null);
		assert.equal(res.result.rollbackAttempted, true);
		assert.equal(res.result.rolledBack, true);
		assert.equal(netWriteCalls, 1, 'only the rollback net-rule write runs');
		assert.deepEqual(currentRules, beforeRules);
		assert.deepEqual(currentNetRules, beforeNetRules);
	});
});

test('pcb.drc.rules.set rolls both stages back when the second write throws', async () => {
	const beforeRules = { old: true };
	const beforeNetRules = [{ old: true }];
	let currentRules: Record<string, unknown> = beforeRules;
	let currentNetRules: Array<Record<string, unknown>> = beforeNetRules;
	let netWriteCalls = 0;
	await withEda({
		pcb_Drc: {
			getCurrentRuleConfiguration: async () => currentRules,
			getNetRules: async () => currentNetRules,
			overwriteCurrentRuleConfiguration: async (next: Record<string, unknown>) => { currentRules = next; return true; },
			overwriteNetRules: async (next: Array<Record<string, unknown>>) => {
				netWriteCalls++;
				if (netWriteCalls === 1) throw new Error('net write failed');
				currentNetRules = next;
				return true;
			},
		},
	}, async () => {
		const res: any = await runAction('pcb.drc.rules.set', {
			ruleConfiguration: { clearance: 6 }, netRules: [{ class: 'PWR' }],
		});
		assert.equal(res.result.partial, true);
		assert.equal(res.result.rulesWritten, true);
		assert.equal(res.result.netRulesWritten, null);
		assert.equal(res.result.rollbackAttempted, true);
		assert.equal(res.result.rolledBack, true);
		assert.deepEqual(currentRules, beforeRules);
		assert.deepEqual(currentNetRules, beforeNetRules);
	});
});

test('pcb.drc.rules.set returns an unverified partial result when final readback throws', async () => {
	let reads = 0;
	await withEda({
		pcb_Drc: {
			getCurrentRuleConfiguration: async () => {
				reads++;
				if (reads > 1) throw new Error('readback unavailable');
				return { old: true };
			},
			overwriteCurrentRuleConfiguration: async () => true,
		},
	}, async () => {
		const res: any = await runAction('pcb.drc.rules.set', { ruleConfiguration: { clearance: 6 } });
		assert.equal(res.result.partial, true);
		assert.equal(res.result.verified, false);
		assert.match(res.result.readbackError, /unavailable/i);
	});
});

test('pcb.silk.add loads a requested font and returns the actual family', async () => {
	let added = '';
	let createArgs: unknown[] = [];
	await withEda({
		sys_FontManager: {
			getFontsList: async () => ['default'],
			addFont: async (font: string) => { added = font; return true; },
		},
		pcb_PrimitiveString: {
			create: async (...args: unknown[]) => {
				createArgs = args;
				return { getState_PrimitiveId: () => 'silk-1', getState_FontFamily: () => 'Arial' };
			},
		},
		pcb_Primitive: { getPrimitivesBBox: async () => ({ minX: 0, minY: 0, maxX: 20, maxY: 10 }) },
	}, async () => {
		const res: any = await runAction('pcb.silk.add', { text: 'CAN', x: 10, y: 20, fontFamily: 'Arial' });
		assert.equal(added, 'Arial');
		assert.equal(createArgs[4], 'Arial');
		assert.equal(res.result.fontFamily, 'Arial');
	});
});

test('pcb.silk.set requires a modify result, a readable primitive, and matching font readback', async () => {
	await withEda({
		sys_FontManager: { getFontsList: async () => ['Arial'], addFont: async () => true },
		pcb_PrimitiveAttribute: { getAll: async () => [] },
		pcb_PrimitiveString: {
			modify: async (id: string) => id === 'no-result' ? undefined : { id },
			get: async (id: string) => {
				if (id === 'missing') return undefined;
				const font = id === 'wrong-font' ? 'default' : 'Arial';
				return { getState_FontFamily: () => font, getState_X: () => 10, getState_Y: () => 20 };
			},
		},
	}, async () => {
		const res: any = await runAction('pcb.silk.set', {
			primitiveIds: ['no-result', 'missing', 'wrong-font', 'ok'], fontFamily: 'Arial',
		});
		assert.deepEqual(res.result.results.map((item: any) => item.ok), [false, false, false, true]);
		assert.match(res.result.results[0].error, /modify returned no primitive/i);
		assert.match(res.result.results[1].error, /not found by readback/i);
		assert.match(res.result.results[2].error, /readback differs/i);
		assert.equal(res.result.results[3].actual.fontFamily, 'Arial');
	});
});

test('library.footprint.region_create saves and verifies a no-components region', async () => {
	let saveCalls = 0;
	let createArgs: unknown[] = [];
	const source: Array<string | number> = [0, 0, 'L', 1200, 0, 1200, 900, 0, 900, 0, 0];
	const region = {
		getState_PrimitiveId: () => 'region-1', getState_Layer: () => 12,
		getState_RuleType: () => [2], getState_RegionName: () => 'LCD_BODY',
		getState_LineWidth: () => 6, getState_PrimitiveLock: () => true,
		getState_ComplexPolygon: () => ({ getSource: () => source }),
	};
	await withEda({
		...footprintDocumentControl('fp-copy', 'lib-1', 'tab-1'),
		pcb_MathPolygon: { createPolygon: () => ({ getSource: () => source }) },
		pcb_PrimitiveRegion: {
			create: async (...args: unknown[]) => { createArgs = args; return region; },
			get: async () => region,
			delete: async () => true,
		},
		pcb_Document: { save: async () => { saveCalls++; return true; } },
	}, async () => {
		const res: any = await runAction('library.footprint.region_create', {
			uuid: 'fp-copy', libraryUuid: 'lib-1',
			points: [[0, 0], [1200, 0], [1200, 900], [0, 900]],
			ruleType: '2', name: 'LCD_BODY', lineWidth: 6, locked: true,
		});
		assert.equal(saveCalls, 1);
		assert.deepEqual(createArgs[2], [2]);
		assert.equal(createArgs[3], 'LCD_BODY');
		assert.equal(createArgs[5], true);
		assert.equal(res.result.verified, true);
		assert.deepEqual(res.result.actual.source, source);
	});
});

test('library.footprint.region_create accepts rotated/reversed linear source encoding and repeated closure', async () => {
	const requestedSource: Array<string | number> = [0, 0, 'L', 10, 0, 10, 10, 0, 10, 0, 0];
	const equivalentReadback: Array<string | number> = [
		10, 10, 'L', 10, 0, 'L', 0, 0, 'L', 0, 10, 'L', 10, 10, 10, 10,
	];
	let deletes = 0;
	const region = {
		getState_PrimitiveId: () => 'region-equivalent', getState_Layer: () => 12,
		getState_RuleType: () => [2], getState_RegionName: () => 'LCD_BODY',
		getState_LineWidth: () => 6, getState_PrimitiveLock: () => true,
		getState_ComplexPolygon: () => ({ getSource: () => equivalentReadback }),
	};
	await withEda({
		...footprintDocumentControl('fp-copy', 'lib-1', 'tab-equivalent'),
		pcb_MathPolygon: { createPolygon: () => ({ getSource: () => requestedSource }) },
		pcb_PrimitiveRegion: {
			create: async () => region,
			get: async () => region,
			delete: async () => { deletes++; return true; },
		},
		pcb_Document: { save: async () => true },
	}, async () => {
		const res: any = await runAction('library.footprint.region_create', {
			uuid: 'fp-copy', libraryUuid: 'lib-1',
			points: [[0, 0], [10, 0], [10, 10], [0, 10]],
			ruleType: 'no-components', name: 'LCD_BODY', lineWidth: 6, locked: true,
		});
		assert.equal(res.result.verified, true);
		assert.equal(deletes, 0);
		assert.deepEqual(res.result.actual.source, equivalentReadback);
	});
});

test('library.footprint.region_create keeps a verified region when the host drops its optional name', async () => {
	const source: Array<string | number> = [0, 0, 'L', 10, 0, 10, 10, 0, 10, 0, 0];
	let deletes = 0;
	let saves = 0;
	const region = {
		getState_PrimitiveId: () => 'region-no-name', getState_Layer: () => 12,
		getState_RuleType: () => [2], getState_RegionName: () => '',
		getState_LineWidth: () => 6, getState_PrimitiveLock: () => true,
		getState_ComplexPolygon: () => ({ getSource: () => source }),
	};
	await withEda({
		...footprintDocumentControl('fp-copy', 'lib-1', 'tab-no-name'),
		pcb_MathPolygon: { createPolygon: () => ({ getSource: () => source }) },
		pcb_PrimitiveRegion: {
			create: async () => region,
			get: async () => region,
			delete: async () => { deletes++; return true; },
		},
		pcb_Document: { save: async () => { saves++; return true; } },
	}, async () => {
		const res: any = await runAction('library.footprint.region_create', {
			uuid: 'fp-copy', libraryUuid: 'lib-1',
			points: [[0, 0], [10, 0], [10, 10], [0, 10]],
			ruleType: 'no-components', name: 'LCD_BODY', lineWidth: 6, locked: true,
		});
		assert.equal(res.result.verified, true);
		assert.equal(res.result.namePersisted, false);
		assert.deepEqual(res.result.readbackDifferences, ['name']);
		assert.equal(res.result.requested.name, 'LCD_BODY');
		assert.equal(res.result.actual.name, '');
		assert.match(res.warnings[0], /name.*requested.*host readback/i);
		assert.equal(deletes, 0);
		assert.equal(saves, 1);
	});
});

test('library.footprint.region_create still rolls back material metadata mismatches', async (t) => {
	const source: Array<string | number> = [0, 0, 'L', 10, 0, 10, 10, 0, 10, 0, 0];
	const cases = [
		{ label: 'layer', field: 'layer', actual: { layer: 1 } },
		{ label: 'rule type', field: 'ruleType', actual: { ruleType: [5] } },
		{ label: 'lock', field: 'locked', actual: { locked: false } },
		{ label: 'different non-empty name', field: 'name', actual: { name: 'OTHER' } },
	];
	for (const mismatch of cases) {
		await t.test(mismatch.label, async () => {
			const state = { layer: 12, ruleType: [2], locked: true, name: 'LCD_BODY', ...mismatch.actual };
			let present = true;
			let saves = 0;
			const region = {
				getState_PrimitiveId: () => `region-wrong-${mismatch.field}`,
				getState_Layer: () => state.layer,
				getState_RuleType: () => state.ruleType,
				getState_RegionName: () => state.name,
				getState_LineWidth: () => 6,
				getState_PrimitiveLock: () => state.locked,
				getState_ComplexPolygon: () => ({ getSource: () => source }),
			};
			await withEda({
				...footprintDocumentControl('fp-copy', 'lib-1', `tab-wrong-${mismatch.field}`),
				pcb_MathPolygon: { createPolygon: () => ({ getSource: () => source }) },
				pcb_PrimitiveRegion: {
					create: async () => region,
					get: async () => present ? region : undefined,
					delete: async () => { present = false; return true; },
				},
				pcb_Document: { save: async () => { saves++; return true; } },
			}, async () => {
				const res: any = await runAction('library.footprint.region_create', {
					uuid: 'fp-copy', libraryUuid: 'lib-1',
					points: [[0, 0], [10, 0], [10, 10], [0, 10]],
					ruleType: 'no-components', name: 'LCD_BODY', lineWidth: 6, locked: true,
				});
				assert.equal(res.result.verified, false);
				assert.equal(res.result.rolledBack, true);
				assert.match(res.result.error, new RegExp(mismatch.field, 'i'));
				assert.equal(saves, 2);
			});
		});
	}
});

test('library.footprint.region_create rolls back and re-saves when initial save returns false', async () => {
	const source: Array<string | number> = [0, 0, 'L', 10, 0, 10, 10, 0, 10, 0, 0];
	let present = true;
	let saves = 0;
	const region = { getState_PrimitiveId: () => 'region-2' };
	await withEda({
		...footprintDocumentControl('fp-copy', 'lib-1', 'tab-2'),
		pcb_MathPolygon: { createPolygon: () => ({ getSource: () => source }) },
		pcb_PrimitiveRegion: {
			create: async () => region,
			delete: async () => { present = false; return true; },
			get: async () => present ? region : undefined,
		},
		pcb_Document: { save: async () => { saves++; return saves > 1; } },
	}, async () => {
		const res: any = await runAction('library.footprint.region_create', {
			uuid: 'fp-copy', libraryUuid: 'lib-1', points: [[0, 0], [10, 0], [10, 10], [0, 10]],
		});
		assert.equal(res.result.partial, true);
		assert.equal(res.result.deleteRolledBack, true);
		assert.equal(res.result.saveRolledBack, true);
		assert.equal(res.result.absentAfterRollback, true);
		assert.equal(res.result.rolledBack, true);
		assert.equal(saves, 2);
	});
});

test('library.footprint.region_create rolls back a geometrically mismatched readback', async () => {
	const requestedSource: Array<string | number> = [0, 0, 'L', 10, 0, 10, 10, 0, 10, 0, 0];
	const wrongSource: Array<string | number> = [0, 0, 'L', 20, 0, 20, 10, 0, 10, 0, 0];
	let present = true;
	let saves = 0;
	const region = {
		getState_PrimitiveId: () => 'region-3', getState_Layer: () => 12,
		getState_RuleType: () => [2], getState_RegionName: () => undefined,
		getState_LineWidth: () => 6, getState_PrimitiveLock: () => true,
		getState_ComplexPolygon: () => ({ getSource: () => wrongSource }),
	};
	await withEda({
		...footprintDocumentControl('fp-copy', 'lib-1', 'tab-3'),
		pcb_MathPolygon: { createPolygon: () => ({ getSource: () => requestedSource }) },
		pcb_PrimitiveRegion: {
			create: async () => region,
			delete: async () => { present = false; return true; },
			get: async () => present ? region : undefined,
		},
		pcb_Document: { save: async () => { saves++; return true; } },
	}, async () => {
		const res: any = await runAction('library.footprint.region_create', {
			uuid: 'fp-copy', libraryUuid: 'lib-1', points: [[0, 0], [10, 0], [10, 10], [0, 10]],
		});
		assert.equal(res.result.verified, false);
		assert.equal(res.result.rolledBack, true);
		assert.deepEqual(res.result.actual.source, wrongSource);
		assert.match(res.result.error, /source/i);
		assert.equal(saves, 2);
	});
});
