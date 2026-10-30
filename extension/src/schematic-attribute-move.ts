/// <reference types="@jlceda/pro-api-types" />
import { readResponseContext, mutationContextError } from './eda-context';
import { exactJSON } from './preserve-instance';
import { ActionError, ErrorCodes, type ActionResult } from './protocol';

type Attribute = Awaited<ReturnType<typeof eda.sch_PrimitiveAttribute.getAll>>[number];
type State = Record<string, unknown>;

// Official @jlceda/pro-api-types 0.4.25 SCH_PrimitiveAttribute.modify supports
// partial {x,y}. The returned object is an acknowledgement, never verification.
export async function moveSchematicAttribute(payload: State, readState: (attribute: Attribute) => Promise<State>): Promise<ActionResult> {
	const refuse = (message: string): never => { throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, message); };
	const keys = new Set(['projectUuid', 'documentUuid', 'primitiveId', 'expected', 'x', 'y', 'ownerType']);
	if (Object.keys(payload).some(key => !keys.has(key))) refuse('Attribute move accepts only bound identity, expected state, x and y.');
	for (const key of ['projectUuid', 'documentUuid', 'primitiveId']) {
		if (typeof payload[key] !== 'string' || !(payload[key] as string).trim()) refuse(`Attribute move requires ${key}.`);
	}
	for (const key of ['x', 'y']) if (typeof payload[key] !== 'number' || !Number.isFinite(payload[key])) refuse(`Attribute move ${key} must be finite.`);
	const ownerType = payload.ownerType ?? 'part';
	if (ownerType !== 'part' && ownerType !== 'sheet') refuse('Attribute move ownerType must be part or sheet.');
	const allowedKeys = ownerType === 'sheet' ? ['Name', 'Description', 'Drawed'] : ['Name', 'Designator'];
	const expected = payload.expected as State;
	if (!expected || typeof expected !== 'object' || Array.isArray(expected) || expected.primitiveId !== payload.primitiveId
		|| typeof expected.ParentPrimitiveId !== 'string' || !expected.ParentPrimitiveId.trim()
		|| !allowedKeys.includes(expected.Key as string)) refuse('Attribute move requires a complete supported attribute state for the requested ownerType.');
	const identityBefore = await readResponseContext();
	if (identityBefore.projectUuid !== payload.projectUuid || identityBefore.documentUuid !== payload.documentUuid
		|| identityBefore.documentType !== 'schematic' || !identityBefore.tabId) refuse('Attribute move requires the exact active schematic context.');
	const id = payload.primitiveId as string;
	const attribute = await eda.sch_PrimitiveAttribute.get(id);
	if (!attribute || attribute.getState_PrimitiveId() !== id) refuse('Attribute move target is missing or has a different ID.');
	const before = await readState(attribute!);
	if (exactJSON(before) !== exactJSON(expected)) refuse('Attribute move original state changed or expected state is incomplete.');
	const parent = await eda.sch_PrimitiveComponent.get(expected.ParentPrimitiveId as string);
	if (!parent || parent.getState_PrimitiveId() !== expected.ParentPrimitiveId || String(parent.getState_ComponentType()) !== ownerType) refuse('Attribute move parent differs from the requested ownerType.');
	const prewrite = await readResponseContext();
	if (mutationContextError(identityBefore, prewrite) || !prewrite.tabId) refuse('Attribute move context changed before write.');
	const requested = { x: payload.x as number, y: payload.y as number };
	const moved = before.X !== requested.x || before.Y !== requested.y;
	const errors: string[] = [];
	let attempted = false;
	if (moved) {
		attempted = true;
		try {
			const ack = await eda.sch_PrimitiveAttribute.modify(id, requested);
			if (!ack) errors.push('modify returned no attribute; the write may have applied');
		} catch (error) { errors.push(`modify failed; the write may have applied: ${String(error)}`); }
	}
	let after: State | undefined;
	try {
		const fresh = await eda.sch_PrimitiveAttribute.get(id);
		if (!fresh || fresh.getState_PrimitiveId() !== id) throw new Error('fresh attribute is missing or has a different ID');
		after = await readState(fresh);
		if (exactJSON(after) !== exactJSON({ ...before, X: requested.x, Y: requested.y })) errors.push('fresh attribute coordinates or preserved state differ from requested result');
	} catch (error) { errors.push(`fresh read failed: ${String(error)}`); }
	const identityAfter = await readResponseContext();
	const drift = mutationContextError(identityBefore, identityAfter);
	if (drift || !identityAfter.tabId) errors.push(`attribute move context changed: ${drift ?? 'missing tabId'}`);
	const verified = errors.length === 0;
	return { result: { scope: 'attribute-position-only', ownerType, primitiveId: id, requested, before, after: after ?? null,
		identityBefore, identityAfter, attempted, moved, preserved: verified, verified, partial: !verified, errors },
		...(verified ? {} : { warnings: ['Attribute position is not fully verified; retain this response and fresh state. Do not retry automatically.'] }) };
}
