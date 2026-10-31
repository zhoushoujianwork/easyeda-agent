/// <reference types="@jlceda/pro-api-types" />
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { projectDelete, projectDeletePreview } from './project-delete';

const target = '1234567890abcdef', reference = 'abcdef1234567890';
function fixture(t: import('node:test').TestContext) {
	const old = (globalThis as any).eda;
	const rows = new Map<string, any>([[target, { uuid: target, friendlyName: 'disposable test', teamUuid: 'team', folderUuid: null }],
		[reference, { uuid: reference, friendlyName: 'reference', teamUuid: 'team' }], ['other', { uuid: 'other', friendlyName: 'untouched', teamUuid: 'team' }]]);
	let calls = 0;
	const api = { getCurrentProjectInfo: async () => rows.get(reference), getProjectInfo: async (id: string) => rows.get(id),
		getAllProjectsUuid: async () => [...rows.keys()], deleteProject: (id: string) => { calls++; rows.delete(id); return true; } };
	(globalThis as any).eda = { dmt_Project: api };
	t.after(() => { (globalThis as any).eda = old; });
	return { api, rows, calls: () => calls };
}
const payload = { projectUuid: target, expectedFriendlyName: 'disposable test', expectedTeamUuid: 'team', confirmDelete: true };
test('project deletion preview is read-only and exact deletion retains reference and unrelated projects', async t => {
	const f = fixture(t);
	const preview: any = await projectDeletePreview(payload);
	assert.equal(preview.result.preflightVerified, true); assert.equal(preview.result.supported, true); assert.equal(f.calls(), 0);
	const result: any = await projectDelete(payload);
	assert.equal(result.result.deleted, true); assert.equal(result.result.verified, true); assert.equal(result.result.partial, false);
	assert.deepEqual(result.result.removed, [target]); assert.equal(f.calls(), 1);
	assert.ok(f.rows.has(reference)); assert.ok(f.rows.has('other'));
});
test('identity, active-target, missing-confirmation and unreadable inventory all refuse before mutation', async t => {
	const f = fixture(t);
	for (const patch of [{ expectedFriendlyName: 'wrong' }, { expectedTeamUuid: 'wrong' }, { confirmDelete: false }, { projectUuid: reference }])
		await assert.rejects(projectDelete({ ...payload, ...patch }));
	f.api.getAllProjectsUuid = async () => [];
	await assert.rejects(projectDelete(payload)); assert.equal(f.calls(), 0);
});
test('partial deletion and verification failure never return verified or retry the delete', async t => {
	const f = fixture(t);
	f.api.deleteProject = () => false;
	const result: any = await projectDelete(payload);
	assert.equal(result.result.deleted, null); assert.equal(result.result.verified, false); assert.equal(result.result.partial, true);
	let attempts = 0;
	f.api.deleteProject = () => { attempts++; f.rows.delete(target); f.rows.delete('other'); return true; };
	const collateral: any = await projectDelete(payload);
	assert.equal(collateral.result.verified, false); assert.equal(attempts, 1);
});
