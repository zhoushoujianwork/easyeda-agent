/// <reference types="@jlceda/pro-api-types" />
import { readResponseContext, mutationContextError } from './eda-context';
import { ActionError, ErrorCodes, type ActionResult } from './protocol';

type State = Record<string, unknown>;
type Text = Awaited<ReturnType<typeof eda.sch_PrimitiveText.getAll>>[number];

// A create acknowledgement is never readback evidence. Never retry create or
// delete uncertain results, including an SDK throw after a possible write.
export async function createSchematicText(payload: State): Promise<ActionResult> {
	const refuse = (message: string): never => { throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, message); };
	const keys = new Set(['content', 'x', 'y', 'rotation', 'fontSize']);
	if (Object.keys(payload).some(key => !keys.has(key))) refuse('Text create accepts only content, x, y, rotation and fontSize.');
	const content = payload.content;
	if (typeof content !== 'string' || /^\p{White_Space}*$/u.test(content) || content.includes('\0') || [...content].length > 16000) {
		refuse('content must be nonblank, contain no NUL, and have at most 16000 Unicode characters.');
	}
	for (const key of ['x', 'y']) if (typeof payload[key] !== 'number' || !Number.isFinite(payload[key])) refuse(`${key} must be explicitly finite.`);
	const rotation = Object.hasOwn(payload, 'rotation') ? payload.rotation : 0;
	if (typeof rotation !== 'number' || ![0, 90, 180, 270].includes(rotation)) refuse('rotation must be 0, 90, 180 or 270.');
	const hasFontSize = Object.hasOwn(payload, 'fontSize');
	if (hasFontSize && (typeof payload.fontSize !== 'number' || !Number.isFinite(payload.fontSize) || payload.fontSize <= 0)) refuse('fontSize must be positive and finite.');
	const requested = { content: content as string, x: payload.x as number, y: payload.y as number, rotation: rotation as 0 | 90 | 180 | 270,
		...(hasFontSize ? { fontSize: payload.fontSize as number } : {}) };
	const identityBefore = await readResponseContext();
	if (!identityBefore.projectUuid || !identityBefore.documentUuid || identityBefore.documentType !== 'schematic' || !identityBefore.tabId) refuse('Text create requires a real active schematic page with complete identity.');
	const before = await eda.sch_PrimitiveText.getAll();
	if (!Array.isArray(before)) refuse('Text inventory unavailable before create.');
	const beforeIDs = new Set(before.map(text => text.getState_PrimitiveId()));
	const prewrite = await readResponseContext();
	if (mutationContextError(identityBefore, prewrite) || !prewrite.tabId) refuse('Text create document identity changed before write.');
	const errors: string[] = [];
	let knownID: string | null = null;
	try {
		const ack = await eda.sch_PrimitiveText.create(requested.x, requested.y, requested.content, requested.rotation, undefined, undefined, requested.fontSize);
		if (!ack) throw new Error('create returned no primitive; write may have applied');
		const id = ack.getState_PrimitiveId();
		if (typeof id !== 'string' || !id.trim()) throw new Error('create returned no stable primitive ID; write may have applied');
		knownID = id;
		if (beforeIDs.has(id)) errors.push('create returned an existing primitive ID');
	} catch (error) { errors.push(`create not confirmed; write may have applied: ${String(error)}`); }
	let after: State | null = null;
	try {
		const fresh = await eda.sch_PrimitiveText.getAll();
		if (!Array.isArray(fresh)) throw new Error('fresh text inventory unavailable');
		const matches = fresh.filter(text => text.getState_PrimitiveId() === knownID);
		if (!knownID || matches.length !== 1) throw new Error('known ID is missing or ambiguous in fresh inventory');
		const text: Text = matches[0];
		after = { primitiveId: text.getState_PrimitiveId(), content: text.getState_Content(), x: text.getState_X(), y: text.getState_Y(),
			rotation: text.getState_Rotation(), fontSize: text.getState_FontSize() };
		for (const [key, value] of Object.entries(requested)) if (after[key] !== value) errors.push(`fresh text ${key} differs from requested value`);
	} catch (error) { errors.push(`fresh read failed: ${String(error)}`); }
	const identityAfter = await readResponseContext();
	const drift = mutationContextError(identityBefore, identityAfter);
	if (drift || !identityAfter.tabId) errors.push(`text create context changed: ${drift ?? 'missing tabId'}`);
	const verified = errors.length === 0;
	return { result: { primitiveId: knownID, knownID, requested, after, identityBefore, identityAfter, attempted: true, verified, partial: !verified, errors },
		...(verified ? {} : { warnings: ['Text create is uncertain. Retain this response and inspect fresh target-page texts; never retry create or automatically delete uncertain results.'] }) };
}
