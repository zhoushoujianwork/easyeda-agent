/// <reference types="@jlceda/pro-api-types" />
import { ActionError, ErrorCodes, type ActionResult } from './protocol';
import { describeThrown, requireString } from './util';
import { sleep } from './deadlines';

type Payload = Record<string, unknown>;
const prefix = 'EA_AGENT__';
function fail(message: string): never { throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, message); }
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
 return { result: { ...result, verified: false, partial: true, error: describeThrown(err), errorProperties: err && typeof err === 'object' ? Object.fromEntries(Object.getOwnPropertyNames(err).slice(0, 20).map(key => { try { return [key, describeThrown((err as Record<string, unknown>)[key], 2000)]; } catch { return [key, '<unreadable>']; } })) : undefined }, warnings: ['CBB operation was not fully verified. Preserve identities and fresh-read before any retry; no automatic rollback or write replay.'] };
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
 fields(p, ['uuid', 'libraryUuid', 'expectedName', 'symbolUuid', 'targetProjectUuid', 'importedSchematicUuid']);
 const symbolUuid = text(p, 'symbolUuid');
 const identity = await owned(p);
 const project = await eda.dmt_Project.getCurrentProjectInfo();
 let expectedProjectUuid=identity.uuid;
 const evidence = [];
 if(p.targetProjectUuid!==undefined||p.importedSchematicUuid!==undefined){
  expectedProjectUuid=text(p,'targetProjectUuid');
  const schematicUuid=text(p,'importedSchematicUuid');
  const current=await eda.dmt_SelectControl.getCurrentDocumentInfo();
  if(project?.uuid!==expectedProjectUuid||!current?.uuid) fail('Exact imported target project must be active.');
  const preview=await cbbProjectPlace({projectUuid:expectedProjectUuid,documentUuid:current.uuid,schematicUuid,expectedName:identity.name,symbolUuid,x:0,y:0,dryRun:true});
  const membership=preview.result?.membership as Record<string,unknown>|undefined;
  if(preview.result?.verified!==true) return preview;
  if(membership?.symbolUuid!==symbolUuid||membership.sourceCbbUuid!==identity.uuid) fail('Imported symbol does not bind the exact owned source CBB.');
  evidence.push(schematicUuid);
 }else{
 if (project?.uuid !== identity.uuid) fail('Open the owned CBB project before opening its project-library symbol.');
 for (const board of identity.cbb.boards) {
  const ref: unknown = board.schematic;
  const schematic = typeof ref === 'string' ? await eda.dmt_Schematic.getSchematicInfo(ref) : ref as IDMT_SchematicItem | undefined;
  if (schematic?.parentProjectUuid === identity.uuid && schematic.cbbSymbol?.uuid === symbolUuid && schematic.cbbSymbol.type === 17) evidence.push(schematic.uuid);
 }
 }
 if (evidence.length !== 1) fail('Exact CBB symbol membership is missing or ambiguous in the active project graph.');
 let tabId: string | undefined;
 try {
  // CBB symbols are PROJECT-library assets. Web 4.1.78 does not return them
  // from the enclosing personal library, and its CBB-wide opener can throw.
  if ((await eda.dmt_Project.getCurrentProjectInfo())?.uuid !== expectedProjectUuid) fail('CBB project changed before symbol navigation.');
  let before: Awaited<ReturnType<typeof eda.dmt_SelectControl.getCurrentDocumentInfo>>;
  try { before = await eda.dmt_SelectControl.getCurrentDocumentInfo(); } catch { before = undefined; }
  const exactTab = (id: string | undefined) => id === `${symbolUuid}@project` || id === `${symbolUuid}@${expectedProjectUuid}`;
  if (before?.uuid === symbolUuid && before.documentType === 17 && exactTab(before.tabId)) tabId = before.tabId;
  else {
   tabId = await eda.dmt_EditorControl.openLibraryDocument('project', '2' as ELIB_LibraryType.SYMBOL, symbolUuid);
   if (!tabId) return partial(identity, 'Official project-library symbol open returned no tab.');
   await eda.dmt_EditorControl.activateDocument(tabId);
  }
  for (let i = 0; i < 20; i++) {
   const actual = await eda.dmt_SelectControl.getCurrentDocumentInfo();
   const current = await eda.dmt_Project.getCurrentProjectInfo();
   if (current?.uuid === expectedProjectUuid && actual?.uuid === symbolUuid && actual.documentType === 17 && exactTab(actual.tabId) && (actual.tabId === tabId || (tabId === `${symbolUuid}@project` && actual.tabId === `${symbolUuid}@${expectedProjectUuid}`))) {
    const symbol = await eda.lib_Symbol.get(symbolUuid, 'project');
    if (!symbol || symbol.uuid !== symbolUuid || symbol.type !== 17) return partial({ ...identity, tabId, actual }, 'Exact project-library CBB symbol is unreadable.');
    return { result: { ...identity, expectedProjectUuid, tabId: actual.tabId, symbol, actual, extraGraphics:await symbolExtraGraphics(), membership: { source: 'active-CBB-schematic-graph', schematicUuid: evidence[0], cbbUuid: identity.uuid, symbolUuid }, verified: true, partial: false } };
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
 fields(p, ['uuid', 'libraryUuid', 'expectedName', 'symbolUuid', 'projectUuid', 'documentUuid', 'x', 'y', 'rotation', 'mirror', 'allowSourceProjectProbe']);
 const projectUuid = text(p, 'projectUuid'), documentUuid = text(p, 'documentUuid'), symbolUuid = text(p, 'symbolUuid');
 const x = p.x, y = p.y, rotation = p.rotation ?? 0, mirror = p.mirror ?? false;
 if (typeof x !== 'number' || !Number.isFinite(x) || typeof y !== 'number' || !Number.isFinite(y) ||
     typeof rotation !== 'number' || ![0, 90, 180, 270].includes(rotation) || typeof mirror !== 'boolean') fail('CBB placement requires finite x/y, quarter-turn rotation and boolean mirror.');
 const identity = await owned(p);
 const symbol = await eda.lib_Symbol.get(symbolUuid, identity.libraryUuid);
 const symbolDirect = !!symbol && symbol.uuid === symbolUuid && symbol.libraryUuid === identity.libraryUuid && symbol.type === 17 && symbol.cbbUuid === identity.uuid;
 if (p.allowSourceProjectProbe !== undefined && typeof p.allowSourceProjectProbe !== 'boolean') fail('allowSourceProjectProbe must be boolean.');
 if (!symbolDirect && p.allowSourceProjectProbe !== true) fail('Exact CBB symbol ownership is unavailable. Save ALL open documents and explicitly allow a source-project probe.');
 if (symbol && (symbol.uuid !== symbolUuid || symbol.type !== 17 || (symbol.cbbUuid && symbol.cbbUuid !== identity.uuid))) fail('CBB symbol metadata contradicts the requested variant.');
 await page(projectUuid, documentUuid);
 const before = (await eda.sch_PrimitiveComponent.getAll()).map(geometry);
 if (before.some(c => !c.id || ![c.x, c.y, c.rotation].every(Number.isFinite) || typeof c.mirror !== 'boolean') || new Set(before.map(c => c.id)).size !== before.length) fail('Incomplete/duplicate geometry in pre-placement inventory.');
 await page(projectUuid, documentUuid);
 let primitiveId: string | undefined;
 let sourceProbe: Record<string, unknown> | undefined;
 let instanceCreateAttempted = false;
 try {
  if (!symbolDirect) {
   const savedTarget = await eda.sch_Document.save();
   if (savedTarget !== true) throw Error('Target page save failed; source project was not opened.');
   await page(projectUuid, documentUuid);
   if (await eda.lib_Cbb.openProjectInEditor(identity.uuid, identity.libraryUuid) !== true) throw Error('CBB source project open was not accepted.');
   let found: { schematicUuid: string; symbolUuid: string } | undefined;
   for (let attempt=0;attempt<30&&!found;attempt++) {
    const current = await eda.dmt_Project.getCurrentProjectInfo();
    if (current?.uuid === identity.uuid && current.itemType === 'CBB Project') {
     for (const board of identity.cbb.boards) {
      const ref: unknown = board.schematic;
      const schematic = typeof ref === 'string' ? await eda.dmt_Schematic.getSchematicInfo(ref) : ref as IDMT_SchematicItem | undefined;
      if (schematic?.parentProjectUuid === identity.uuid && schematic.cbbSymbol?.uuid === symbolUuid && schematic.cbbSymbol.type === 17) found = { schematicUuid: schematic.uuid, symbolUuid };
     }
    }
    if (!found) await sleep(100);
   }
   if (!found) throw Error('Exact source CBB symbol membership is unavailable; no instance was created.');
   const sourceSymbol = await eda.lib_Symbol.get(symbolUuid, 'project');
   if (!sourceSymbol || sourceSymbol.uuid !== symbolUuid || sourceSymbol.type !== 17) throw Error('Graph-selected CBB source symbol could not be read.');
   if (await eda.dmt_Project.openProject(projectUuid) !== true) throw Error('Return to target project was not accepted.');
   let returned=false;
   for(let attempt=0;attempt<30;attempt++) {
    if ((await eda.dmt_Project.getCurrentProjectInfo())?.uuid === projectUuid) { returned=true;break; }
    await sleep(100);
   }
   if (!returned) throw Error('Target project identity did not settle after source probe.');
   if (!await eda.dmt_EditorControl.openDocument(documentUuid)) throw Error('Target page could not be reopened after source probe.');
   returned=false;
   for(let attempt=0;attempt<30;attempt++) {
    try { await page(projectUuid,documentUuid);returned=true;break; } catch { await sleep(100); }
   }
   if (!returned) throw Error('Target page identity did not settle after source probe.');
   const actualBefore = (await eda.sch_PrimitiveComponent.getAll()).map(geometry);
   if (actualBefore.length !== before.length || !before.every(old=>actualBefore.some(actual=>JSON.stringify(actual)===JSON.stringify(old)))) throw Error('Target component geometry changed across the source probe; no instance was created.');
   sourceProbe = { savedTarget, sourceProjectUuid: identity.uuid, ...found, returnedProjectUuid: projectUuid, returnedDocumentUuid: documentUuid, targetGeometryPreserved:true };
  }
  await page(projectUuid, documentUuid);
  instanceCreateAttempted = true;
  const created = await eda.sch_PrimitiveComponent.createCbbSymbol({ libraryUuid: identity.libraryUuid, cbbUuid: identity.uuid, uuid: symbolUuid }, x as number, y as number, rotation as number, mirror as boolean);
  primitiveId = created?.getState_PrimitiveId();
  if (!primitiveId || before.some(c => c.id === primitiveId)) return partial({ ...identity, symbolUuid, sourceProbe, primitiveId: primitiveId ?? null, outcome: 'unknown' }, 'Official placement returned no unique new instance.');
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
  return { result: { ...identity, symbolUuid, primitiveId, state, actualCbb, actualSymbol, preserved, sourceProbe, verified: true, partial: false } };
 } catch (err) { return partial({ ...identity, symbolUuid, sourceProbe, instanceCreateAttempted, primitiveId: primitiveId ?? null, outcome: 'unknown' }, err); }
}

type Pin = NonNullable<Awaited<ReturnType<typeof eda.sch_PrimitivePin.getAll>>>[number];
async function symbolExtraGraphics(){
 const raw=await eda.sch_PrimitiveRectangle.getAll();
 if(!Array.isArray(raw)) fail('CBB rectangle inventory is unavailable.');
 const rectangles=raw.map(r=>({id:r.getState_PrimitiveId(),x:r.getState_TopLeftX(),y:r.getState_TopLeftY(),width:r.getState_Width(),height:r.getState_Height(),rotation:r.getState_Rotation()}));
 if(rectangles.some(r=>!r.id||![r.x,r.y,r.width,r.height,r.rotation].every(Number.isFinite))) fail('CBB rectangle geometry is incomplete.');
 const arcs=await eda.sch_PrimitiveArc.getAllPrimitiveId(),texts=await eda.sch_PrimitiveText.getAllPrimitiveId(),objects=await eda.sch_PrimitiveObject.getAllPrimitiveId();
 if(![arcs,texts,objects].every(Array.isArray)) fail('CBB graphic inventory is unavailable.');
 return {rectangles,arcs,texts,objects};
}

/** Place an already-imported project CBB; never invokes the cross-project importer. */
export async function cbbProjectPlace(p: Payload): Promise<ActionResult> {
 fields(p, ['projectUuid','documentUuid','schematicUuid','expectedName','symbolUuid','x','y','dryRun','viaSymbolApi']);
 const projectUuid=text(p,'projectUuid'), documentUuid=text(p,'documentUuid');
 const schematicUuid=text(p,'schematicUuid'), expectedName=text(p,'expectedName');
 named(expectedName);
 if (p.dryRun !== undefined && typeof p.dryRun !== 'boolean') fail('dryRun must be boolean.');
 if(p.viaSymbolApi!==undefined&&typeof p.viaSymbolApi!=='boolean') fail('viaSymbolApi must be boolean.');
 const x=p.x,y=p.y;
 if(typeof x!=='number'||!Number.isFinite(x)||typeof y!=='number'||!Number.isFinite(y)) fail('Finite placement x/y required.');
 await page(projectUuid,documentUuid);
 const graph=await eda.dmt_Schematic.getSchematicInfo(schematicUuid);
 const sourceSymbolUuid=graph?.cbbSymbol?.uuid;
 if(!graph || graph.uuid!==schematicUuid || graph.parentProjectUuid!==projectUuid || graph.name!==expectedName || graph.itemType!=='CBB Schematic' || !sourceSymbolUuid || graph.cbbSymbol?.type!==17 || graph.cbbSymbol.name!==expectedName) fail('Exact active-project native CBB schematic and symbol membership required.');
 // DMT buildCbbSymbol projects source.split('|')[0], not the local UUID.
 // The official project CBB catalogue exposes the local record instead.
 const catalogue=await eda.lib_Cbb.search(expectedName,'project',undefined,100,1);
 if(!Array.isArray(catalogue)||catalogue.length>=100) fail('Project CBB catalogue is unavailable or truncated.');
 // Catalogue name can be the internal lowercase title. Resolve every entry
 // by its local UUID and compare the authoritative display name instead.
 const matches:Array<{symbolUuid:string;symbol:NonNullable<Awaited<ReturnType<typeof eda.lib_Symbol.get>>>}>=[];
 for(const entry of catalogue){
  if(!entry?.uuid) return partial({graph,catalogue},'Project CBB catalogue contains an unreadable identity.');
  const record=await eda.lib_Symbol.get(entry.uuid,'project');
  if(!record || record.uuid!==entry.uuid || record.type!==17) return partial({graph,catalogue,unreadableSymbolUuid:entry.uuid},'Project CBB catalogue symbol is unreadable.');
  if(record.name===expectedName) matches.push({symbolUuid:entry.uuid,symbol:record});
 }
 if(matches.length!==1) return partial({graph,catalogue,matches},'Project CBB catalogue name is missing or ambiguous.');
 const {symbolUuid,symbol}=matches[0];
 const membership={source:'active-project-CBB-graph-and-unique-catalogue',projectUuid,schematicUuid,symbolUuid,sourceSymbolUuid,sourceCbbUuid:graph.cbbSymbol.cbbUuid??'',name:expectedName};
 if(!symbol || symbol.uuid!==symbolUuid || symbol.type!==17 || symbol.name!==expectedName) return partial({membership,graph,symbol:symbol??null},'Project CBB symbol metadata differs from the selected schematic.');
 if(p.dryRun===true) return {result:{membership,symbol,dryRun:true,verified:true,partial:false}};
 if(text(p,'symbolUuid')!==symbolUuid) fail('Explicit project symbol differs from the graph-selected variant.');
 await page(projectUuid,documentUuid);
 const before=(await eda.sch_PrimitiveComponent.getAll()).map(geometry);
 if(new Set(before.map(c=>c.id)).size!==before.length || before.some(c=>!c.id||![c.x,c.y,c.rotation].every(Number.isFinite)||typeof c.mirror!=='boolean')) fail('Project CBB baseline inventory is incomplete.');
 let primitiveId:string|undefined;
 try {
  // Official SDK uses cbbUuid="" for an existing CURRENT-project symbol.
  // An explicit graph proof above replaces cross-project library assumptions.
  // SDK 0.3.53 passes CBB y unchanged to native (y-down) placeInfo. Our CLI,
  // and getState_Y(), use schematic y-up coordinates.
  const created=p.viaSymbolApi!==false
   ? await eda.sch_PrimitiveComponent.create({libraryType:'2' as ELIB_LibraryType.SYMBOL,libraryUuid:projectUuid,uuid:symbolUuid},x,y,'',0,false,true,true)
   : await eda.sch_PrimitiveComponent.createCbbSymbol({libraryUuid:'project',cbbUuid:'',uuid:symbolUuid},x,-y,0,false);
  primitiveId=created?.getState_PrimitiveId();
  await page(projectUuid,documentUuid);
  const after=await eda.sch_PrimitiveComponent.getAll();
  const instance=after.find(c=>c.getState_PrimitiveId()===primitiveId);
  const preserved=before.every(old=>after.some(c=>JSON.stringify(geometry(c))===JSON.stringify(old)));
  if(!primitiveId||before.some(c=>c.id===primitiveId)||!instance||after.length!==before.length+1||!preserved) return partial({membership,primitiveId:primitiveId??null,preserved},'Project CBB placement did not produce one isolated fresh instance.');
  const state=geometry(instance);
  const binding=(instance as unknown as {getState_Symbol:()=>{uuid:string}}).getState_Symbol();
  if(state.type!=='block_symbol'||state.x!==x||state.y!==y||state.rotation!==0||state.mirror!==false||binding?.uuid!==symbolUuid) return partial({membership,primitiveId,state,binding},'Project CBB geometry or local symbol binding differs.');
  return {result:{membership,primitiveId,state,binding,preserved,verified:true,partial:false}};
 } catch(err){return partial({membership,primitiveId:primitiveId??null,outcome:'unknown'},err);}
}

function pinState(pin: Pin) {
 return { id: pin.getState_PrimitiveId(), number: pin.getState_PinNumber(), name: pin.getState_PinName(), x: pin.getState_X(), y: pin.getState_Y(), rotation: pin.getState_Rotation(), length: pin.getState_PinLength(), shape: pin.getState_PinShape(), pinType: pin.getState_pinType() };
}

export async function cbbSymbolBuild(p: Payload): Promise<ActionResult> {
 fields(p, ['uuid', 'libraryUuid', 'expectedName', 'symbolUuid', 'pins', 'outline', 'dryRun']);
 const outline = p.outline;
 if (!Array.isArray(outline) || outline.length < 8 || outline.length % 2 || outline.some(x => typeof x !== 'number' || !Number.isFinite(x)) || outline[0] !== outline[outline.length-2] || outline[1] !== outline[outline.length-1]) fail('CBB outline must be a closed finite coordinate polyline.');
 if (!Array.isArray(p.pins) || !p.pins.length || p.pins.length > 256) fail('CBB pins must be a non-empty bounded array.');
 const pins = (p.pins as unknown[]).map((raw, i) => {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) fail(`pins[${i}] must be an object.`);
  const v = raw as Payload; fields(v, ['number', 'name', 'x', 'y', 'rotation', 'length']);
  const number = text(v, 'number'), name = text(v, 'name'), x = v.x, y = v.y, rotation = v.rotation ?? 0, length = v.length ?? 20;
  if (typeof x !== 'number' || !Number.isFinite(x) || typeof y !== 'number' || !Number.isFinite(y) || ![0,90,180,270].includes(rotation as number) || typeof length !== 'number' || !Number.isFinite(length) || length <= 0) fail('CBB pins require finite geometry and positive length.');
  return { number, name, x: x as number, y: y as number, rotation: rotation as number, length: length as number };
 });
 if (new Set(pins.map(x => x.number)).size !== pins.length || new Set(pins.map(x => x.name)).size !== pins.length) fail('CBB pin numbers and port names must be unique.');
 if (p.dryRun !== undefined && typeof p.dryRun !== 'boolean') fail('dryRun must be boolean.');
 const opened = await cbbSymbolOpen(Object.fromEntries(['uuid','libraryUuid','expectedName','symbolUuid'].map(k => [k,p[k]])));
 if (opened.result?.verified !== true) return opened;
 const tabId = opened.result.tabId, symbolUuid = text(p, 'symbolUuid'), cbbUuid = text(p, 'uuid');
 const active = async () => {
  const current = await eda.dmt_Project.getCurrentProjectInfo(), doc = await eda.dmt_SelectControl.getCurrentDocumentInfo();
  if (current?.uuid !== cbbUuid || doc?.uuid !== symbolUuid || doc.tabId !== tabId || doc.documentType !== 17) fail('CBB symbol target changed; stop authoring.');
 };
 await active();
 const beforePins = (await eda.sch_PrimitivePin.getAll()).map(pinState);
 const beforePolygons = await eda.sch_PrimitivePolygon.getAllPrimitiveId();
 const beforeCircles = await eda.sch_PrimitiveCircle.getAllPrimitiveId();
 const beforeExtraGraphics=await symbolExtraGraphics();
 if (!Array.isArray(beforePolygons) || !Array.isArray(beforeCircles)) fail('CBB symbol inventory is unavailable.');
 if (p.dryRun === true) return { result: { ...opened.result, dryRun: true, scope: 'symbol-inventory-preview-only', beforePins, beforePolygons, beforeCircles, beforeExtraGraphics, requested: { pins, outline }, verified: true, partial: false } };
 if (beforePins.length || beforePolygons.length || beforeCircles.length||Object.values(beforeExtraGraphics).some(a=>a.length)) fail('CBB symbol build only supports a proven-empty owned target; no overwrite.');
 const createdPins: string[] = []; let outlineId: string | undefined;
 try {
  await active();
  outlineId = (await eda.sch_PrimitivePolygon.create(outline as number[], null, 'none', 1, null))?.getState_PrimitiveId();
  if (!outlineId) throw Error('CBB outline creation returned no ID.');
  for (const pin of pins) {
   await active();
   const id = (await eda.sch_PrimitivePin.create(pin.x,pin.y,pin.number,pin.name,pin.rotation,pin.length,null,'None' as ESCH_PrimitivePinShape,'Passive' as ESCH_PrimitivePinType))?.getState_PrimitiveId();
   if (!id) throw Error(`CBB pin ${pin.number} creation returned no ID.`);
   createdPins.push(id);
  }
  await active();
  const saved = await eda.sch_Document.save();
  if (saved !== true) throw Error('CBB symbol save did not return true.');
  await active();
  const actualPins = (await eda.sch_PrimitivePin.getAll()).map(pinState);
  const polygon = await eda.sch_PrimitivePolygon.get(outlineId);
  const polygonIds = await eda.sch_PrimitivePolygon.getAllPrimitiveId(), circleIds = await eda.sch_PrimitiveCircle.getAllPrimitiveId();
  const matches = actualPins.length === pins.length && pins.every((pin,i) => actualPins.some(actual => actual.id === createdPins[i] && actual.number === pin.number && actual.name === pin.name && actual.x === pin.x && actual.y === pin.y && actual.rotation === pin.rotation && actual.length === pin.length && actual.shape === 'None' && actual.pinType === 'Passive'));
  const extra=await symbolExtraGraphics();
  if (!matches || JSON.stringify(polygon?.getState_Line()) !== JSON.stringify(outline) || polygonIds.length !== 1 || polygonIds[0] !== outlineId || circleIds.length||Object.values(extra).some(a=>a.length)) throw Error('CBB symbol fresh geometry differs from the requested spec.');
  await active();
  return { result: { ...opened.result, createdPins, outlineId, saved, actualPins, verified: true, partial: false } };
 } catch (err) { return partial({ ...opened.result, createdPins, outlineId: outlineId ?? null }, err); }
}
