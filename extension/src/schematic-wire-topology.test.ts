/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { collectWireSegments, connectivityWireSegments, planSchDeleteCascadeTrees, runAction, schematicComponentsList, schematicPinDisconnect } from './actions';
import { classifyWireContact, classifyWireSegment, isDegenerateWireSegment, parseObservedWireLine, physicalWireIslands, type WireSegment } from './schematic-wire-topology';

const H: WireSegment = [-20, 0, 20, 0];
const V: WireSegment = [0, -20, 0, 20];
const wire = (id: string, line: unknown, net = 'A') => ({ getState_PrimitiveId: () => id, getState_Line: () => line, getState_Net: () => net });

test('official dev.5 observed raw segment records preserve source without phantom connectors', () => {
	const raw = [400,650,320,650,400,350,400,650,780,350,400,350];
	const got = collectWireSegments([wire('b', raw, 'XB5')]);
	assert.deepEqual(got.map(s => s.seg), [[400,650,320,650],[400,350,400,650],[780,350,400,350]]);
	assert.deepEqual(got.map(s => s.segmentIndex), [0,1,2]);
	assert.ok(got.every(s => s.rawEncoding === 'flat-segments' && s.wirePrimitiveId === 'b'));
	assert.deepEqual(got[0].rawLine, raw);
	assert.deepEqual(parseObservedWireLine([[780,350,400,350],[400,650,320,650]]).segments, [[780,350,400,350],[400,650,320,650]]);
	assert.deepEqual(parseObservedWireLine([[0,0],[10,0],[10,20]]).segments, [[0,0,10,0],[10,0,10,20]]);
});

test('unknown or missing observed encodings fail closed, including legacy ambiguous flat6', () => {
	for (const raw of [undefined, [], [0,0,10,0,10,20], [0,0,Infinity,20], [[0,0], [1,2,3,4]], [0,0,'10',20]]) {
		assert.throws(() => parseObservedWireLine(raw));
	}
	assert.throws(() => collectWireSegments([{ ...wire('bad', H), getState_Line: () => { throw new Error('unavailable'); } }]));
	assert.throws(() => collectWireSegments([wire('', H)]));
});

test('components.list exposes raw provenance and marks malformed/throwing wire reads unavailable', async t => {
	const globals=globalThis as any, previous=globals.eda;
	t.after(()=>{globals.eda=previous;});
	let line:unknown=H;
	globals.eda={sch_PrimitiveComponent:{getAll:async()=>[]},sch_PrimitiveWire:{getAll:async()=>[wire('w',line)]}};
	const good:any=await schematicComponentsList({includeWires:true});
	assert.equal(good.result.wiresAvailable,true);
	assert.deepEqual(good.result.wires[0].rawLine,H);
	assert.equal(good.result.wires[0].segmentIndex,0);
	assert.equal(good.result.wires[0].rawEncoding,'flat-segments');
	line=[0,0,10,0,10,20];
	const bad:any=await schematicComponentsList({includeWires:true});
	assert.equal(bad.result.wiresAvailable,false);
	assert.deepEqual(bad.result.wires,[]);
	assert.match(bad.result.wiresError,/Ambiguous observed flat wire/);
});

test('contact matrix is invariant under reversal, rotation and translation; names and primitive IDs never join X', () => {
	const cases: Array<[WireSegment, WireSegment, string, number]> = [
		[H,V,'proper-cross',2], [H,[0,0,0,20],'endpoint-touch',1],
		[[-20,0,0,0],[0,0,0,20],'endpoint-touch',1],
		[H,[-5,0,30,0],'collinear-overlap',1], [H,[25,0,40,0],'disjoint',2],
	];
	for (const [a,b,relation,count] of cases) for (let turns=0; turns<4; turns++) for (const reverse of [false,true]) {
		const transform = (s: WireSegment): WireSegment => {
			let points = [[s[0],s[1]],[s[2],s[3]]];
			for (let i=0; i<turns; i++) points = points.map(([x,y]) => [-y,x]);
			if (reverse) points.reverse();
			return points.flatMap(([x,y]) => [x+135,y-70]) as WireSegment;
		};
		const pair = [transform(a),transform(b)];
		assert.equal(classifyWireContact(pair[0],pair[1]), relation);
		assert.equal(physicalWireIslands(pair.map(seg => ({seg}))).length, count);
	}
	assert.equal(physicalWireIslands([{seg:H},{seg:V}], [{x:0,y:0}]).length,1);
	assert.equal(classifyWireContact([0,0,1,1],H),'unknown');
	assert.throws(() => physicalWireIslands([{seg:[0,0,1,1]}]));
});

// ─── Zero-length wire regression (live B04_2_1, 2026-09-20) ───────────────
//
// A single zero-length wire primitive anywhere on a page made
// `physicalWireIslands` throw `Unknown wire contact.`, so `schematic.check` AND
// `schematic.bridgeCheck` failed for the WHOLE page — and with them the `check`
// stage of `sch gate`, which in turn skipped `bridge-check` and `drc`. Reproduced
// on 4 pages of one real project (31 / 15 / 4 / 2 zero-length primitives).
//
// These primitives are contact-neutral by geometry, so the fix drops them from
// contact reasoning and lets the pre-existing `zero-length-wire` WARN rule report
// them. The non-orthogonal refusal is deliberately NOT relaxed here.

test('zero-length segments are contact-neutral: classified, never "unknown"', () => {
	const zero: WireSegment = [1105,870,1105,870];
	assert.equal(classifyWireSegment(zero),'degenerate');
	assert.ok(isDegenerateWireSegment(zero));
	assert.equal(classifyWireSegment(H),'horizontal');
	assert.equal(classifyWireSegment(V),'vertical');
	assert.equal(classifyWireSegment([0,0,1,1]),'non-orthogonal');
	// A non-finite coordinate must never masquerade as a harmless degenerate.
	assert.equal(classifyWireSegment([0,0,NaN,0]),'non-orthogonal');

	// Every pairing with a real segment is disjoint — this is what used to return
	// 'unknown' and abort the run.
	for (const other of [H, V, [0,0,1,1] as WireSegment]) {
		assert.equal(classifyWireContact(zero, other),'disjoint');
		assert.equal(classifyWireContact(other, zero),'disjoint');
	}
	assert.equal(classifyWireContact(zero, [1105,870,1105,870]),'disjoint');

	// A page that contains a zero-length wire still partitions instead of throwing.
	assert.equal(physicalWireIslands([{seg:H},{seg:zero}]).length,2);
});

test('real B04_2_1 shape: one zero-length primitive among real stubs no longer aborts the island model', () => {
	// The exact observed encodings that killed the run: three orthogonal segment
	// records plus a single-segment zero-length primitive at its own coordinate.
	// `e1894` @ (1105,870) is the first trigger named by the differential replay of
	// the stored artifacts (scripts/replay-zero-length-wires-diff.ts) — where the
	// PRE-FIX code throws on all three real pages (31/15/2 zero-length primitives)
	// and the fixed code partitions them.
	const raw = [400,650,320,650,400,350,400,650,780,350,400,350];
	const real = collectWireSegments([wire('b', raw, 'XB5')]);
	const zero = collectWireSegments([wire('e1894', [1105,870,1105,870], 'PGND')]);
	assert.equal(zero.length,1);

	// Pre-fix this threw `Unknown wire contact.` and took check + bridge-check down.
	assert.equal(physicalWireIslands([...real, ...zero]).length,2);

	// connectivityWireSegments removes the degenerate primitive and keeps real
	// segment indices aligned for the reporting that follows.
	const kept = connectivityWireSegments([...real, ...zero]);
	assert.deepEqual(kept.map(s => s.seg), real.map(s => s.seg));
	assert.equal(kept.length,3);

	// Sub-pixel drift is what the platform actually emits; it must classify the same
	// way as an exactly-zero record (observed: [329.9999999999999,69.99999999999987]).
	const drift = collectWireSegments([wire(
		'drift',
		[329.9999999999999,69.99999999999987,329.9999999999999,69.99999999999987],
		'A',
	)]);
	assert.ok(isDegenerateWireSegment(drift[0].seg));
	assert.equal(physicalWireIslands([...real, ...drift]).length,2);

	// The conservative half of the rule: a primitive that MIXES a degenerate record
	// with a real one is left completely alone rather than silently shortened.
	const mixed = collectWireSegments([wire('mixed', [...H, ...([0,0,0,0] as number[])], 'A')]);
	assert.equal(mixed.length,2);
	assert.equal(connectivityWireSegments(mixed).length,2);
	assert.deepEqual(connectivityWireSegments([]),[]);
});

test('check and bridge survive a page whose only defect is a zero-length wire, and still report it', async t => {
	// installScene already anchors pins at the four coords below, so the degenerate
	// record at (0,0) is exactly the field shape that aborted the run.
	installScene(t,{});
	const api=(globalThis as any).eda;
	api.sch_PrimitiveWire.getAll=async()=>[wire('h',H),wire('v',V),wire('zero',[0,0,0,0],'A')];

	// bridge-check must not throw; the degenerate primitive is simply not an island.
	const bridge:any=await runAction('schematic.bridgeCheck',{});
	assert.equal(bridge.result.summary.wireTreesTotal,2);

	// check must not throw either, and must surface the degenerate primitive through
	// the rule that already existed for it.
	const check:any=await runAction('schematic.check',{});
	assert.equal(check.result.summary.zeroLengthWires,1);
	const zeroFinding=check.result.findings.find((f:any)=>f.type==='zero-length-wire');
	assert.ok(zeroFinding,'zero-length-wire must be reported instead of aborting the run');
	assert.equal(zeroFinding.level,'warn');
	assert.equal(zeroFinding.wirePrimitiveId,'zero');
});

test('a non-orthogonal segment is still refused, now with the offending segment named', () => {
	assert.throws(() => physicalWireIslands([{seg:[0,0,1,1]}]),/Non-orthogonal wire segment/);
	assert.throws(() => physicalWireIslands([{seg:[0,0,1,1]}]),/0,0,1,1/);
	assert.throws(() => physicalWireIslands([{seg:[0,0,1,1]}]),/index=0/);
});

test('cascade removes only target-exclusive physical island; same primitive crossing a survivor is retained', () => {
	const targets = [{id:'remove',pins:[{x:-20,y:0}]}], survivor = [{x:0,y:-20}];
	const marker = [{id:'a-flag',x:20,y:0},{id:'b-flag',x:0,y:20}];
	assert.deepEqual(planSchDeleteCascadeTrees(targets,[{id:'a',points:H},{id:'b',points:V}],marker,survivor),[
		{wireIds:['a'],flagIds:['a-flag'],ownerIds:['remove']},
	]);
	assert.deepEqual(planSchDeleteCascadeTrees(targets,[{id:'shared',points:[...H,...V]}],marker,survivor),[]);
	assert.deepEqual(planSchDeleteCascadeTrees(targets,[{id:'a',points:H},{id:'b',points:[0,0,0,-20]}],marker,survivor),[]);
	assert.throws(() => planSchDeleteCascadeTrees(targets,[{id:'bad',points:[0,0,10,0,10,20]}],marker,survivor));
});

function installScene(t: { after: (f: () => void) => void }, opts: { sameNet?: boolean; emptyWireNet?: boolean; missingPinNet?: boolean; missingNetlist?: boolean; markerAtCross?: boolean; markerConflict?: boolean; malformed?: boolean; pinReadFails?: boolean; tee?: boolean } = {}) {
	const globals = globalThis as any, previous = globals.eda;
	t.after(() => { globals.eda = previous; });
	const names = ['A','A',opts.sameNet ? 'A' : 'B',opts.sameNet ? 'A' : 'B'];
	const coords = [[-20,0],[20,0],[0,-20],[0,20]];
	const parts = coords.map(([x,y],i) => ({
		getState_PrimitiveId: () => `p${i}`, getState_ComponentType: () => 'part',
		getState_Designator: () => `R${i+1}`, getState_X: () => x, getState_Y: () => y,
	}));
	const markers = [
		...(opts.markerAtCross ? [{getState_PrimitiveId:()=> 'marker',getState_ComponentType:()=> 'netflag',getState_X:()=>0,getState_Y:()=>0,getState_Net:()=> 'A'}] : []),
		...(opts.markerConflict ? [{getState_PrimitiveId:()=> 'conflict',getState_ComponentType:()=> 'netflag',getState_X:()=>-10,getState_Y:()=>0,getState_Net:()=> 'B'}] : []),
	];
	const netlist = { components: Object.fromEntries(coords.map((_,i) => [`p${i}`, {props:{Designator:`R${i+1}`},pinInfoMap:{one:{number:'1',net:opts.missingPinNet && i === 1 ? '' : names[i]}}}])) };
	globals.eda = {
		dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ uuid: 'page', tabId: 'page@project' }) },
		dmt_EditorControl: { activateDocument: async () => true },
		sch_PrimitiveComponent: {
			getAll: async () => [...parts,...markers],
			getAllPinsByPrimitiveId: async (id: string) => {
				if (opts.pinReadFails) throw new Error('pins unavailable');
				const i=parts.findIndex(p=>p.getState_PrimitiveId()===id);
				return i<0 ? [] : [{getState_PinNumber:()=> '1',getState_PinName:()=> '1',getState_NoConnected:()=>false,getState_X:()=>coords[i][0],getState_Y:()=>coords[i][1]}];
			},
		},
		sch_PrimitiveWire: { getAll: async () => [wire('h',opts.malformed ? [0,0,10,0,10,20] : H,opts.emptyWireNet ? '' : 'A'),wire('v',opts.tee ? [0,-20,0,0] : V,opts.emptyWireNet ? '' : names[2])] },
		sch_ManufactureData: {getNetlistFile: async () => opts.missingNetlist ? undefined : {text:async()=>JSON.stringify(netlist)}},
	};
}

test('check gives bare X INFO only with complete pin evidence; ordinary clean X passes', async t => {
	installScene(t);
	const out:any=await runAction('schematic.check',{});
	assert.equal(out.result.passed,true);
	assert.equal(out.result.findings.length,1);
	assert.equal(out.result.findings[0].type,'wire-crossing');
	assert.equal(out.result.findings[0].level,'info');
	assert.deepEqual(out.result.findings[0].segments.map((s:any)=>s.primitiveId),['h','v']);
});

test('check derives bare-X evidence from official pins when raw wire nets are empty', async t => {
	installScene(t,{emptyWireNet:true});
	const out:any=await runAction('schematic.check',{});
	assert.equal(out.result.passed,true);
	assert.equal(out.result.findings.find((f:any)=>f.type==='wire-crossing')?.level,'info');
});

for (const opts of [{emptyWireNet:true,missingPinNet:true},{emptyWireNet:true,markerConflict:true}]) test(`empty raw wire net still fails closed on incomplete/conflicting island evidence ${JSON.stringify(opts)}`,async t=>{
	installScene(t,opts);
	const out:any=await runAction('schematic.check',{});
	assert.equal(out.result.passed,false);
	assert.equal(out.result.findings.find((f:any)=>f.type==='wire-crossing')?.level,'error');
});

for (const opts of [{missingNetlist:true},{markerAtCross:true}]) test(`check does not waive ambiguous/anchored X ${JSON.stringify(opts)}`,async t=>{
	installScene(t,opts);
	const out:any=await runAction('schematic.check',{});
	assert.equal(out.result.passed,false);
	assert.equal(out.result.findings.find((f:any)=>f.type==='wire-crossing')?.level,'error');
});

test('check preserves foreign T contact error; unreadable anchors fail instead of proving empty',async t=>{
	installScene(t,{tee:true});
	const out:any=await runAction('schematic.check',{});
	assert.ok(out.result.findings.some((f:any)=>f.type==='wire-contact'&&f.level==='error'));
});

for (const sameNet of [false,true]) test(`bridge keeps X as two real islands, sameNet=${sameNet}`,async t=>{
	installScene(t,{sameNet});
	const out:any=await runAction('schematic.bridgeCheck',{});
	assert.equal(out.result.summary.wireTreesTotal,2);
	assert.equal(out.result.summary.bridges,0);
});

test('bridge joins at T and catches two names; X marker also prevents noncontact exemption',async t=>{
	installScene(t,{tee:true});
	const out:any=await runAction('schematic.bridgeCheck',{});
	assert.equal(out.result.summary.wireTreesTotal,1);
	assert.equal(out.result.summary.bridges,1);
});

test('bridge explicit marker at X joins both arms and reports the foreign net',async t=>{
	installScene(t,{markerAtCross:true});
	const out:any=await runAction('schematic.bridgeCheck',{});
	assert.equal(out.result.summary.wireTreesTotal,1);
	assert.equal(out.result.summary.bridges,1);
});

test('an unrelated floating-pin WARN remains blocking beside verified X INFO',async t=>{
	installScene(t);
	const api=(globalThis as any).eda;
	const original=api.sch_PrimitiveComponent.getAllPinsByPrimitiveId;
	api.sch_PrimitiveComponent.getAllPinsByPrimitiveId=async(id:string)=>{
		const pins=await original(id);
		if(id==='p0') pins.push({getState_PinNumber:()=> 'unused',getState_PinName:()=> 'unused',getState_NoConnected:()=>false,getState_X:()=>-30,getState_Y:()=>10});
		return pins;
	};
	const out:any=await runAction('schematic.check',{});
	assert.equal(out.result.findings.find((f:any)=>f.type==='wire-crossing').level,'info');
	assert.ok(out.result.findings.some((f:any)=>f.type==='floating-pin'&&f.level==='warn'));
	assert.equal(out.result.passed,false);
});

test('third wire endpoint at X is explicit contact and never receives bare-X INFO',async t=>{
	installScene(t,{sameNet:true});
	const api=(globalThis as any).eda;
	api.sch_PrimitiveWire.getAll=async()=>[wire('h',H),wire('v',V),wire('branch',[0,0,10,0])];
	const out:any=await runAction('schematic.check',{});
	assert.equal(out.result.findings.find((f:any)=>f.type==='wire-crossing').level,'error');
});

test('one primitive containing noncontact arms remains two bridge islands',async t=>{
	installScene(t,{sameNet:true});
	const api=(globalThis as any).eda;
	api.sch_PrimitiveWire.getAll=async()=>[wire('merged-id',[...V,...H])];
	const out:any=await runAction('schematic.bridgeCheck',{});
	assert.equal(out.result.summary.wireTreesTotal,2);
	assert.equal(out.result.passed,true);
});

test('check and bridge fail on unknown raw line and missing pin geometry',async t=>{
	installScene(t,{malformed:true});
	await assert.rejects(runAction('schematic.check',{}),/Ambiguous observed flat wire/);
	await assert.rejects(runAction('schematic.bridgeCheck',{}),/Ambiguous observed flat wire/);
});

test('bridge missing pin geometry cannot suppress an occupied crossing',async t=>{
	installScene(t,{pinReadFails:true});
	await assert.rejects(runAction('schematic.bridgeCheck',{}),/pin geometry unavailable/);
	await assert.rejects(runAction('schematic.check',{}),/pin geometry unavailable/);
});

function installLegacyScene(t: {after:(f:()=>void)=>void}, inputs: Array<{id:string;line:unknown}>) {
	const globals=globalThis as any, previous=globals.eda;
	t.after(()=>{globals.eda=previous;});
	let live=[...inputs], mutations=0;
	const created:number[][]=[];
	const mock=(w:{id:string;line:unknown}):any=>({...wire(w.id,w.line),getState_Color:()=>null,getState_LineWidth:()=>null,getState_LineType:()=>null});
	const part:any={getState_PrimitiveId:()=> 'part',getState_ComponentType:()=> 'part',getState_Designator:()=> 'R1',getState_X:()=>-20,getState_Y:()=>0};
	globals.eda={
		dmt_SelectControl:{getCurrentDocumentInfo:async()=>({uuid:'page',tabId:'page@project'})},
		sch_PrimitiveComponent:{getAll:async()=>[part],getAllPinsByPrimitiveId:async()=>[{getState_PinNumber:()=> '1',getState_X:()=>-20,getState_Y:()=>0}],modify:async()=>{mutations++;throw new Error('component must not move before refusal');},delete:async()=>{mutations++;return true;}},
		sch_PrimitiveWire:{getAll:async()=>live.map(mock),delete:async(ids:string[])=>{mutations++;live=live.filter(w=>!ids.includes(w.id));return true;},create:async(points:number[])=>{mutations++;created.push(points);const w={id:'new',line:points};live.push(w);return mock(w);}},
	};
	return {mutations:()=>mutations,created};
}

test('legacy group-move preserves simple single-segment translation',async t=>{
	const fx=installLegacyScene(t,[{id:'h',line:H}]);
	const out:any=await runAction('schematic.group.move',{primitiveIds:['h'],dx:10,dy:20});
	assert.deepEqual(fx.created,[[-10,20,30,20]]);
	assert.equal(out.result.movedWires.length,1);
	assert.equal(fx.mutations(),2);
});

for(const [name,line,extra] of [
	['branched',[...H,0,0,0,20],[]],
	['proper-X',H,[{id:'v',line:V}]],
	['ambiguous-flat6',[0,0,10,0,10,20],[]],
] as Array<[string,unknown,Array<{id:string;line:unknown}>]>) {
	test(`legacy group-move refuses ${name} before even moving selected component`,async t=>{
		const fx=installLegacyScene(t,[{id:'h',line},...extra]);
		await assert.rejects(runAction('schematic.group.move',{primitiveIds:['part','h'],dx:10,dy:20}),(e:any)=>e.code==='PRECONDITION_REFUSED');
		assert.equal(fx.mutations(),0);
	});
	test(`legacy disconnect refuses ${name} without deleting another island`,async t=>{
		const fx=installLegacyScene(t,[{id:'h',line},...extra]);
		await assert.rejects(schematicPinDisconnect({wirePrimitiveId:'h'}),(e:any)=>e.code==='PRECONDITION_REFUSED');
		assert.equal(fx.mutations(),0);
	});
}

test('legacy group-move refuses a new crossing at translated destination before writes',async t=>{
	const fx=installLegacyScene(t,[{id:'h',line:[-20,40,20,40]},{id:'v',line:V}]);
	await assert.rejects(runAction('schematic.group.move',{primitiveIds:['part','h'],dx:0,dy:-40}),/fresh raw snapshot.*compose/);
	assert.equal(fx.mutations(),0);
});

test('legacy disconnect still deletes a verified simple straight stub',async t=>{
	const fx=installLegacyScene(t,[{id:'h',line:H}]);
	const out:any=await schematicPinDisconnect({pinX:-20,pinY:0});
	assert.equal(out.result.disconnected,true);
	assert.deepEqual(out.result.deletedWires,['h']);
	assert.equal(fx.mutations(),1);
});
