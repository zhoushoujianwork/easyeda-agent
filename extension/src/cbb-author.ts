/// <reference types="@jlceda/pro-api-types" />
import { ActionError, ErrorCodes, type ActionResult } from './protocol';
import { describeThrown, requireString } from './util';
import { sleep } from './deadlines';

type Payload = Record<string, unknown>;
const prefix = 'EA_AGENT__';
const fail = (message: string): never => { throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, message); };
function text(p: Payload, key: string): string {
 const value = requireString(p, key);
 if (!value.trim()) fail(`${key} must not be blank.`);
 return value;
}
function fields(p: Payload, allowed: string[]) {
 for (const key of Object.keys(p)) if (!allowed.includes(key)) fail(`Unknown CBB authoring field: ${key}`);
}
function named(name: string) {
 if (!name.startsWith(prefix)) fail('Only agent-owned EA_AGENT__ CBB assets may be authored by these commands.');
}
async function personal(): Promise<string> {
 const uuid = await eda.lib_LibrariesList.getPersonalLibraryUuid();
 if (!uuid) fail('Personal writable library is unavailable.');
 return uuid!;
}
async function owned(p: Payload) {
 const uuid = text(p, 'uuid'), libraryUuid = text(p, 'libraryUuid'), expectedName = text(p, 'expectedName');
 named(expectedName);
 if (await personal() !== libraryUuid) fail('CBB authoring is restricted to the personal writable library.');
 const cbb = await eda.lib_Cbb.get(uuid, libraryUuid);
 if (!cbb || cbb.uuid !== uuid || cbb.libraryUuid !== libraryUuid || cbb.name !== expectedName) fail('CBB identity/name does not match the requested asset.');
 return { uuid, libraryUuid, name: expectedName, cbb: cbb! };
}
function partial(result: Record<string, unknown>, err: unknown): ActionResult {
 return { result: { ...result, verified: false, partial: true, error: describeThrown(err) }, warnings: ['CBB operation was not fully verified. Preserve identities and fresh-read before any retry; no automatic rollback or write replay.'] };
}

export async function cbbCreate(p: Payload): Promise<ActionResult> {
 fields(p, ['name']);
 const requestedName = text(p, 'name');
 const suffix = requestedName.normalize('NFKC').trim().toUpperCase().replace(/[^\p{L}\p{N}_]+/gu, '_');
 const name = suffix.startsWith(prefix) ? suffix : prefix + suffix;
 const libraryUuid = await personal();
 const matches = await eda.lib_Cbb.search(name, libraryUuid, undefined, 100, 1);
 if (!Array.isArray(matches)) fail('Cannot verify the pre-create name inventory.');
 if (matches.some(x => x.name === name)) fail('CBB name already exists; read it instead of recreating.');
 let uuid: string | undefined;
 try {
  uuid = await eda.lib_Cbb.create(libraryUuid, name, undefined, 'Agent-owned controlled CBB test asset.');
  if (!uuid) return partial({ requestedName, name, libraryUuid, outcome: 'unknown' }, 'Official create returned no UUID.');
  const cbb = await eda.lib_Cbb.get(uuid, libraryUuid);
  if (!cbb || cbb.uuid !== uuid || cbb.libraryUuid !== libraryUuid || cbb.name !== name) return partial({ uuid, libraryUuid, name }, 'Created CBB could not be verified by exact readback.');
  return { result: { uuid, libraryUuid, name, requestedName, cbb, verified: true, partial: false } };
 } catch (err) { return partial({ ...(uuid ? { uuid } : {}), libraryUuid, name, outcome: 'unknown' }, err); }
}

export async function cbbDelete(p: Payload): Promise<ActionResult> {
 fields(p, ['uuid', 'libraryUuid', 'expectedName']);
 const { uuid, libraryUuid, name } = await owned(p);
 const current = await eda.dmt_Project.getCurrentProjectInfo();
 if (!current?.uuid || current.uuid === uuid) fail('Verify an active different project before deleting the CBB asset.');
 try {
  const accepted = await eda.lib_Cbb.delete(uuid, libraryUuid);
  const actual = await eda.lib_Cbb.get(uuid, libraryUuid);
  if (accepted !== true || actual !== undefined) return partial({ uuid, libraryUuid, name, accepted }, 'CBB deletion was not confirmed absent.');
  return { result: { uuid, libraryUuid, name, deleted: true, verified: true, partial: false } };
 } catch (err) { return partial({ uuid, libraryUuid, name }, err); }
}

export async function cbbOpen(p: Payload): Promise<ActionResult> {
 fields(p, ['uuid', 'libraryUuid', 'expectedName', 'fromProjectUuid', 'allowProjectSwitch']);
 if (p.allowProjectSwitch !== true) fail('Save ALL open documents and explicitly pass allowProjectSwitch:true before switching projects.');
 const fromProjectUuid = text(p, 'fromProjectUuid');
 const identity = await owned(p);
 const before = await eda.dmt_Project.getCurrentProjectInfo();
 if (before?.uuid !== fromProjectUuid) fail('Active project differs from fromProjectUuid; no CBB project was opened.');
 if (before?.uuid === identity.uuid) return { result: { ...identity, opened: true, alreadyOpen: true, verified: true, partial: false } };
 try {
  const accepted = await eda.lib_Cbb.openProjectInEditor(identity.uuid, identity.libraryUuid);
  if (accepted !== true) return partial({ ...identity, fromProjectUuid, accepted }, 'Official CBB project open was not accepted.');
  for (let i = 0; i < 20; i++) {
   const actual = await eda.dmt_Project.getCurrentProjectInfo();
   if (actual?.uuid === identity.uuid && actual.itemType === 'CBB Project') return { result: { ...identity, fromProjectUuid, opened: true, actual, verified: true, partial: false } };
   await sleep(100);
  }
  return partial({ ...identity, fromProjectUuid, accepted }, 'CBB project identity did not settle.');
 } catch (err) { return partial({ ...identity, fromProjectUuid, outcome: 'unknown' }, err); }
}

export async function cbbSymbolOpen(p: Payload): Promise<ActionResult> {
 fields(p, ['uuid', 'libraryUuid', 'expectedName']);
 const identity = await owned(p);
 let tabId: string | undefined;
 try {
  tabId = await eda.lib_Cbb.openSymbolInEditor(identity.uuid, identity.libraryUuid);
  if (!tabId) return partial(identity, 'Official CBB symbol open returned no tab.');
  await eda.dmt_EditorControl.activateDocument(tabId);
  for (let i = 0; i < 20; i++) {
   const actual = await eda.dmt_SelectControl.getCurrentDocumentInfo();
   if (actual?.tabId === tabId && actual.documentType === 17) {
    const symbol = await eda.lib_Symbol.get(actual.uuid, identity.libraryUuid);
    if (!symbol || symbol.libraryUuid !== identity.libraryUuid || symbol.uuid !== actual.uuid || symbol.type !== 17 || symbol.cbbUuid !== identity.uuid) return partial({ ...identity, tabId, actual }, 'Opened symbol does not prove exact CBB ownership.');
    return { result: { ...identity, tabId, symbol, actual, verified: true, partial: false } };
   }
   await sleep(100);
  }
  return partial({ ...identity, tabId }, 'CBB symbol focus did not settle.');
 } catch (err) { return partial({ ...identity, tabId: tabId ?? null }, err); }
}

type Component = NonNullable<Awaited<ReturnType<typeof eda.sch_PrimitiveComponent.getAll>>>[number];
function geometry(c: Component) {
 return { id: c.getState_PrimitiveId(), type: c.getState_ComponentType(), x: c.getState_X(), y: c.getState_Y(), rotation: c.getState_Rotation(), mirror: c.getState_Mirror() };
}
async function page(projectUuid: string, documentUuid: string) {
 const project = await eda.dmt_Project.getCurrentProjectInfo();
 const doc = await eda.dmt_SelectControl.getCurrentDocumentInfo();
 if (project?.uuid !== projectUuid || doc?.uuid !== documentUuid || doc.parentProjectUuid !== projectUuid || doc.documentType !== 1) fail('Active schematic page identity changed; no further CBB writes are allowed.');
 return doc;
}
export async function cbbPlace(p: Payload): Promise<ActionResult> {
 fields(p, ['uuid', 'libraryUuid', 'expectedName', 'symbolUuid', 'projectUuid', 'documentUuid', 'x', 'y', 'rotation', 'mirror']);
 const projectUuid = text(p, 'projectUuid'), documentUuid = text(p, 'documentUuid'), symbolUuid = text(p, 'symbolUuid');
 const x = p.x, y = p.y, rotation = p.rotation ?? 0, mirror = p.mirror ?? false;
 if (typeof x !== 'number' || !Number.isFinite(x) || typeof y !== 'number' || !Number.isFinite(y) ||
     typeof rotation !== 'number' || ![0, 90, 180, 270].includes(rotation) || typeof mirror !== 'boolean') fail('CBB placement requires finite x/y, quarter-turn rotation and boolean mirror.');
 const identity = await owned(p);
 const symbol = await eda.lib_Symbol.get(symbolUuid, identity.libraryUuid);
 if (!symbol || symbol.libraryUuid !== identity.libraryUuid || symbol.uuid !== symbolUuid || symbol.type !== 17 || symbol.cbbUuid !== identity.uuid) fail('Exact CBB symbol ownership is unavailable or mismatched; no instance was placed.');
 await page(projectUuid, documentUuid);
 const before = (await eda.sch_PrimitiveComponent.getAll()).map(geometry);
 if (before.some(c => !c.id || ![c.x, c.y, c.rotation].every(Number.isFinite) || typeof c.mirror !== 'boolean') || new Set(before.map(c => c.id)).size !== before.length) fail('Incomplete/duplicate geometry in pre-placement inventory.');
 await page(projectUuid, documentUuid);
 let primitiveId: string | undefined;
 try {
  const created = await eda.sch_PrimitiveComponent.createCbbSymbol({ libraryUuid: identity.libraryUuid, cbbUuid: identity.uuid, uuid: symbolUuid }, x as number, y as number, rotation as number, mirror as boolean);
  primitiveId = created?.getState_PrimitiveId();
  if (!primitiveId || before.some(c => c.id === primitiveId)) return partial({ ...identity, symbolUuid, primitiveId: primitiveId ?? null, outcome: 'unknown' }, 'Official placement returned no unique new instance.');
  await page(projectUuid, documentUuid);
  const after = await eda.sch_PrimitiveComponent.getAll();
  const instance = after.find(c => c.getState_PrimitiveId() === primitiveId);
  const preserved = before.every(old => after.some(c => JSON.stringify(geometry(c)) === JSON.stringify(old)));
  if (!instance || after.length !== before.length + 1 || !preserved) return partial({ ...identity, symbolUuid, primitiveId, preserved }, 'Fresh component inventory did not confirm a single isolated placement.');
  const state = geometry(instance);
  const cbb = instance as unknown as { getState_Cbb?: () => { libraryUuid: string; uuid: string }; getState_CbbSymbol?: () => { libraryUuid: string; cbbUuid: string; uuid?: string } };
  const actualCbb = cbb.getState_Cbb?.(), actualSymbol = cbb.getState_CbbSymbol?.();
  const matches = state.type === 'block_symbol' && state.x === x && state.y === y && state.rotation === rotation && state.mirror === mirror &&
   actualCbb?.uuid === identity.uuid && actualCbb.libraryUuid === identity.libraryUuid && actualSymbol?.uuid === symbolUuid && actualSymbol.cbbUuid === identity.uuid && actualSymbol.libraryUuid === identity.libraryUuid;
  if (!matches) return partial({ ...identity, symbolUuid, primitiveId, state, actualCbb, actualSymbol }, 'Placed CBB identity or geometry differs from the request.');
  await page(projectUuid, documentUuid);
  return { result: { ...identity, symbolUuid, primitiveId, state, actualCbb, actualSymbol, preserved, verified: true, partial: false } };
 } catch (err) { return partial({ ...identity, symbolUuid, primitiveId: primitiveId ?? null, outcome: 'unknown' }, err); }
}
