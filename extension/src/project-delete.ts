/// <reference types="@jlceda/pro-api-types" />
import { ActionError, type ActionResult, ErrorCodes } from './protocol';
import { requireString } from './util';

type Payload = Record<string, unknown>;
function refuse(message: string): never { throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, message); }
async function inventory(team: string, folder?: string): Promise<string[]> {
	const ids = await eda.dmt_Project.getAllProjectsUuid(team, folder);
	if (!Array.isArray(ids) || !ids.length || ids.some(id => typeof id !== 'string' || !id)
		|| new Set(ids).size !== ids.length) return refuse('Project inventory is empty, unreadable or duplicated; absence is unknown.');
	return ids;
}
async function prepare(payload: Payload) {
	const uuid = requireString(payload, 'projectUuid');
	const expectedName = requireString(payload, 'expectedFriendlyName');
	if (!/^[a-f0-9]{8,64}$/i.test(uuid) || !expectedName.trim()) refuse('Exact project UUID and nonempty expected friendly name are required.');
	const reference = await eda.dmt_Project.getCurrentProjectInfo();
	if (!reference?.uuid || reference.uuid === uuid) refuse('Use a different, observed reference project; deleting the active project is refused.');
	const info = await eda.dmt_Project.getProjectInfo(uuid);
	if (!info || info.uuid !== uuid || info.friendlyName !== expectedName || typeof info.teamUuid !== 'string' || !info.teamUuid)
		refuse('Project UUID/name/team readback is missing or mismatched; no deletion attempted.');
	if (payload.expectedTeamUuid !== undefined && payload.expectedTeamUuid !== info.teamUuid) refuse('Project team changed; no deletion attempted.');
	const before = await inventory(info.teamUuid, info.folderUuid || undefined);
	if (!before.includes(uuid)) refuse('Target project is not present in its observed team/folder inventory.');
	return { uuid, info, reference, before };
}

/** Read-only preflight; the SDK labels deleteProject internal, so support is explicit. */
export async function projectDeletePreview(payload: Payload): Promise<ActionResult> {
	const p = await prepare(payload);
	return { result: { dryRun: true, deleted: false, preflightVerified: true,
		supported: typeof eda.dmt_Project.deleteProject === 'function',
		target: { uuid: p.uuid, friendlyName: p.info.friendlyName, teamUuid: p.info.teamUuid, folderUuid: p.info.folderUuid ?? null },
		referenceProjectUuid: p.reference.uuid, inventoryCount: p.before.length,
		note: 'Official SDK deleteProject is internal; runtime support and a fresh post-delete inventory are required. No GUI or script fallback.' } };
}

/** Delete exactly one explicitly confirmed project; never replay an uncertain call. */
export async function projectDelete(payload: Payload): Promise<ActionResult> {
	if (payload.confirmDelete !== true || typeof payload.expectedTeamUuid !== 'string' || !payload.expectedTeamUuid)
		refuse('confirmDelete:true and the preflighted expectedTeamUuid are required.');
	if (typeof eda.dmt_Project.deleteProject !== 'function')
		throw new ActionError(ErrorCodes.EDA_API_UNAVAILABLE, 'Official internal deleteProject API is unavailable; no deletion attempted.');
	const p = await prepare(payload);
	// Recheck reference identity immediately before the only mutation.
	if ((await eda.dmt_Project.getCurrentProjectInfo())?.uuid !== p.reference.uuid) refuse('Reference project changed before deletion.');
	let returned: unknown, callError: string | null = null;
	try { returned = await eda.dmt_Project.deleteProject(p.uuid); }
	catch (error) { callError = String(error); }
	try {
		const after = await inventory(p.info.teamUuid, p.info.folderUuid || undefined);
		const targetAfter = await eda.dmt_Project.getProjectInfo(p.uuid);
		const referenceAfter = await eda.dmt_Project.getCurrentProjectInfo();
		const removed = p.before.filter(id => !after.includes(id));
		const verified = !after.includes(p.uuid) && !targetAfter && referenceAfter?.uuid === p.reference.uuid
			&& removed.length === 1 && removed[0] === p.uuid;
		return { result: { uuid: p.uuid, friendlyName: p.info.friendlyName, teamUuid: p.info.teamUuid,
			deleted: verified ? true : null, verified, partial: !verified, officialReturn: returned ?? null, callError,
			beforeCount: p.before.length, afterCount: after.length, removed, referenceProjectUuid: referenceAfter?.uuid ?? null },
		warnings: verified ? [] : ['Deletion was attempted once but readback is incomplete or mismatched. Preserve evidence; do not retry blindly.'] };
	}
	catch (error) {
		return { result: { uuid: p.uuid, deleted: null, verified: false, partial: true, officialReturn: returned ?? null, callError },
			warnings: [`Post-delete verification failed: ${String(error)}. Do not retry blindly.`] };
	}
}
