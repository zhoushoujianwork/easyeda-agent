import assert from 'node:assert/strict';
import { test } from 'node:test';
import { cbbCreate, cbbDelete, cbbOpen, cbbSymbolOpen, cbbSymbolBuild, cbbPlace } from './cbb-author';
const asset = { uuid:'c',libraryUuid:'l',name:'EA_AGENT__TEST',boards:[] };
const identity = {uuid:'c',libraryUuid:'l',expectedName:asset.name};
function setup(extra: any = {}) {
 (globalThis as any).eda = {
  lib_LibrariesList:{getPersonalLibraryUuid:async()=>'l'},
  lib_Cbb:{get:async()=>asset,search:async()=>[],...extra.lib_Cbb},
  dmt_Project:{getCurrentProjectInfo:async()=>({uuid:'p',itemType:'Project'}),...extra.dmt_Project},
  dmt_SelectControl:{getCurrentDocumentInfo:async()=>({uuid:'d',parentProjectUuid:'p',documentType:1}),...extra.dmt_SelectControl},
  dmt_EditorControl:{activateDocument:async()=>true,...extra.dmt_EditorControl},
  lib_Symbol:{get:async()=>({uuid:'s',libraryUuid:'l',type:17,cbbUuid:'c'}),...extra.lib_Symbol},
  sch_PrimitiveComponent:extra.sch_PrimitiveComponent,
  dmt_Schematic:extra.dmt_Schematic,
  sch_PrimitivePin:extra.sch_PrimitivePin,
  sch_PrimitivePolygon:extra.sch_PrimitivePolygon,
  sch_PrimitiveCircle:extra.sch_PrimitiveCircle,
  sch_Document:extra.sch_Document,
 };
}
function clear(){delete (globalThis as any).eda;}

test('CBB creates one namespaced asset and verifies exact fresh identity',async()=>{
 let writes=0;
 setup({lib_Cbb:{create:async(lib:string,name:string)=>{writes++;assert.equal(lib,'l');assert.equal(name,asset.name);return 'c';}}});
 try{const r=(await cbbCreate({name:'test'})).result;assert.equal(r?.verified,true);assert.equal(writes,1);}finally{clear();}
});
test('CBB create duplicate/invalid inputs never writes',async()=>{
 let writes=0;setup({lib_Cbb:{search:async()=>[asset],create:async()=>{writes++;}}});
 try{for(const p of [{name:'test'},{name:' '},{name:'x',typo:1}])await assert.rejects(cbbCreate(p));assert.equal(writes,0);}finally{clear();}
});
test('CBB create unknown outcome preserves requested name/ID and does not retry',async()=>{
 for(const throws of [true,false]){
 let writes=0;setup({lib_Cbb:{create:async()=>{writes++;if(throws)throw Error('connection lost');return 'c';},get:async()=>undefined}});
 try{const r=(await cbbCreate({name:'test'})).result;assert.equal(r?.partial,true);assert.equal(r?.verified,false);assert.equal(r?.name,asset.name);if(!throws)assert.equal(r?.uuid,'c');assert.equal(writes,1);}finally{clear();}
 }
});
test('CBB cleanup refuses wrong ownership/name and an active module project',async()=>{
 let writes=0;setup({lib_Cbb:{delete:async()=>{writes++;return true;}}});
 try{
  for(const p of [{...identity,libraryUuid:'other'},{...identity,expectedName:'other'},{...identity,uuid:'other'}])await assert.rejects(cbbDelete(p));
  (globalThis as any).eda.dmt_Project.getCurrentProjectInfo=async()=>({uuid:'c'});await assert.rejects(cbbDelete(identity));assert.equal(writes,0);
 }finally{clear();}
});
test('CBB delete requires host acceptance plus fresh absence',async()=>{
 for(const accepted of [true,false]){
 let deleted=false,writes=0;setup({lib_Cbb:{get:async()=>deleted?undefined:asset,delete:async()=>{writes++;deleted=true;return accepted;}}});
 try{const r=(await cbbDelete(identity)).result;assert.equal(r?.verified,accepted);assert.equal(r?.partial,!accepted);assert.equal(writes,1);}finally{clear();}
 }
});
test('CBB project switch requires explicit permission and exact original project before opening',async()=>{
 let opens=0;setup({lib_Cbb:{openProjectInEditor:async()=>{opens++;return true;}}});
 try{await assert.rejects(cbbOpen({...identity,fromProjectUuid:'p'}));await assert.rejects(cbbOpen({...identity,fromProjectUuid:'other',allowProjectSwitch:true}));assert.equal(opens,0);}finally{clear();}
});
test('CBB opens once and verifies the CBB project identity',async()=>{
 let opened=false,opens=0;setup({lib_Cbb:{openProjectInEditor:async()=>{opens++;opened=true;return true;}},dmt_Project:{getCurrentProjectInfo:async()=>({uuid:opened?'c':'p',itemType:opened?'CBB Project':'Project'})}});
 try{assert.equal((await cbbOpen({...identity,fromProjectUuid:'p',allowProjectSwitch:true})).result?.verified,true);assert.equal(opens,1);}finally{clear();}
});
test('CBB rejected project open stays partial with no replay',async()=>{
 let opens=0;setup({lib_Cbb:{openProjectInEditor:async()=>{opens++;return false;}}});
 try{const r=(await cbbOpen({...identity,fromProjectUuid:'p',allowProjectSwitch:true})).result;assert.equal(r?.partial,true);assert.equal(opens,1);}finally{clear();}
});
function symbolSetup(extra:any={}){
 setup({lib_Cbb:{get:async()=>({...asset,boards:[{schematic:'schema'}]})},dmt_Project:{getCurrentProjectInfo:async()=>({uuid:'c',itemType:'CBB Project'})},dmt_Schematic:{getSchematicInfo:async()=>({uuid:'schema',parentProjectUuid:'c',cbbSymbol:{uuid:'s',type:17}})},dmt_SelectControl:{getCurrentDocumentInfo:async()=>({uuid:'s',tabId:'s@project',documentType:17})},dmt_EditorControl:{openLibraryDocument:async(lib:string,type:string,uuid:string)=>{assert.deepEqual([lib,type,uuid],['project','2','s']);return 's@project';}},lib_Symbol:{get:async()=>({uuid:'s',libraryUuid:'l',type:17})},...extra});
}
test('CBB symbol opener proves active project graph membership despite omitted record cbbUuid',async()=>{
 symbolSetup();try{const r=(await cbbSymbolOpen({...identity,symbolUuid:'s'})).result;assert.equal(r?.verified,true);assert.equal((r?.membership as any).source,'active-CBB-schematic-graph');}finally{clear();}
 symbolSetup({dmt_Schematic:{getSchematicInfo:async()=>({uuid:'schema',parentProjectUuid:'other',cbbSymbol:{uuid:'s',type:17}})}});
 try{await assert.rejects(cbbSymbolOpen({...identity,symbolUuid:'s'}));}finally{clear();}
});

function instance(overrides:any={}){
 return {getState_PrimitiveId:()=>'new',getState_ComponentType:()=>'block_symbol',getState_X:()=>100,getState_Y:()=>200,getState_Rotation:()=>0,getState_Mirror:()=>false,getState_Cbb:()=>({uuid:'c',libraryUuid:'l'}),getState_CbbSymbol:()=>({uuid:'s',cbbUuid:'c',libraryUuid:'l'}),...overrides};
}
const placement={...identity,symbolUuid:'s',projectUuid:'p',documentUuid:'d',x:100,y:200};
test('CBB places an explicitly verified native block once and fresh-reads its identity',async()=>{
 let written=false,writes=0;const c=instance();setup({sch_PrimitiveComponent:{getAll:async()=>written?[c]:[],createCbbSymbol:async(...args:any[])=>{assert.deepEqual(args,[{libraryUuid:'l',cbbUuid:'c',uuid:'s'},100,200,0,false]);writes++;written=true;return c;}}});
 try{const r=(await cbbPlace(placement)).result;assert.equal(r?.verified,true);assert.equal(r?.primitiveId,'new');assert.equal(writes,1);}finally{clear();}
});
test('CBB placement rejects wrong variants/page and invalid geometry before writing',async()=>{
 let writes=0;setup({sch_PrimitiveComponent:{getAll:async()=>[],createCbbSymbol:async()=>{writes++;}}});
 try{
 for(const p of [{...placement,x:NaN},{...placement,rotation:45},{...placement,documentUuid:'other'},{...placement,symbolUuid:'other'},{...placement,mirror:'yes'}])await assert.rejects(cbbPlace(p));
 assert.equal(writes,0);
 }finally{clear();}
});
test('CBB fresh read mismatch preserves new ID and never retries/deletes',async()=>{
 let written=false,writes=0;const c=instance();setup({sch_PrimitiveComponent:{getAll:async()=>written?[instance({getState_ComponentType:()=>'part'})]:[],createCbbSymbol:async()=>{writes++;written=true;return c;},delete:async()=>assert.fail('no guessed cleanup')}});
 try{const r=(await cbbPlace(placement)).result;assert.equal(r?.verified,false);assert.equal(r?.partial,true);assert.equal(r?.primitiveId,'new');assert.equal(writes,1);}finally{clear();}
});
test('CBB detects post-write context drift as partial and retains identity',async()=>{
 let written=false,writes=0;setup({dmt_SelectControl:{getCurrentDocumentInfo:async()=>({uuid:written?'changed':'d',parentProjectUuid:'p',documentType:1})},sch_PrimitiveComponent:{getAll:async()=>[],createCbbSymbol:async()=>{writes++;written=true;return instance();}}});
 try{const r=(await cbbPlace(placement)).result;assert.equal(r?.partial,true);assert.equal(r?.primitiveId,'new');assert.equal(writes,1);}finally{clear();}
});

test('CBB cleanup refuses unreadable active-project identity before deletion',async()=>{
 let writes=0;setup({dmt_Project:{getCurrentProjectInfo:async()=>undefined},lib_Cbb:{delete:async()=>{writes++;return true;}}});
 try{await assert.rejects(cbbDelete(identity));assert.equal(writes,0);}finally{clear();}
});
test('CBB placement detects unexpected movement of an existing component',async()=>{
 let written=false,writes=0;
 const old=instance({getState_PrimitiveId:()=>'old',getState_ComponentType:()=>'part',getState_X:()=>500});
 const moved=instance({getState_PrimitiveId:()=>'old',getState_ComponentType:()=>'part',getState_X:()=>501});
 setup({sch_PrimitiveComponent:{getAll:async()=>written?[moved,instance()]:[old],createCbbSymbol:async()=>{writes++;written=true;return instance();}}});
 try{const r=(await cbbPlace(placement)).result;assert.equal(r?.partial,true);assert.equal(r?.primitiveId,'new');assert.equal(r?.preserved,false);assert.equal(writes,1);}finally{clear();}
});

const symbolSpec={...identity,symbolUuid:'s',outline:[-20,20,20,20,20,-20,-20,-20,-20,20],pins:[{number:'1',name:'IN',x:-40,y:0,rotation:180,length:20}]};
function buildSetup(saved=true){
 const pins:any[]=[];let line:number[]|undefined,creates=0;
 symbolSetup({sch_PrimitivePin:{getAll:async()=>pins,create:async(x:number,y:number,number:string,name:string,rotation:number,length:number)=>{creates++;const pin={getState_PrimitiveId:()=>`pin${creates}`,getState_PinNumber:()=>number,getState_PinName:()=>name,getState_X:()=>x,getState_Y:()=>y,getState_Rotation:()=>rotation,getState_PinLength:()=>length,getState_PinShape:()=>'None',getState_pinType:()=>'Passive'};pins.push(pin);return pin;}},sch_PrimitivePolygon:{getAllPrimitiveId:async()=>line?['outline']:[],create:async(value:number[])=>{line=value;return {getState_PrimitiveId:()=>'outline'};},get:async()=>({getState_Line:()=>line})},sch_PrimitiveCircle:{getAllPrimitiveId:async()=>[]},sch_Document:{save:async()=>saved}});
 return ()=>({pins,creates,line});
}
test('CBB symbol preview is read-only and exact empty-target build verifies geometry plus save',async()=>{
 const state=buildSetup();
 try{const preview=(await cbbSymbolBuild({...symbolSpec,dryRun:true})).result;assert.equal(preview?.dryRun,true);assert.equal(state().creates,0);assert.equal(state().line,undefined);
 const built=(await cbbSymbolBuild(symbolSpec)).result;assert.equal(built?.verified,true);assert.equal(built?.saved,true);assert.deepEqual(built?.createdPins,['pin1']);
 await assert.rejects(cbbSymbolBuild(symbolSpec));assert.equal(state().creates,1);
 }finally{clear();}
});
test('CBB symbol save refusal preserves created identities and never retries or deletes them',async()=>{
 const state=buildSetup(false);
 try{const r=(await cbbSymbolBuild(symbolSpec)).result;assert.equal(r?.partial,true);assert.equal(r?.verified,false);assert.deepEqual(r?.createdPins,['pin1']);assert.equal(r?.outlineId,'outline');assert.equal(state().creates,1);}finally{clear();}
});
test('CBB symbol invalid specs fail before opening any editor',async()=>{
 setup();try{for(const p of [{...symbolSpec,outline:[0,0,1,0,1,1,0,1]},{...symbolSpec,pins:[symbolSpec.pins[0],symbolSpec.pins[0]]},{...symbolSpec,pins:[{...symbolSpec.pins[0],x:Infinity}]}])await assert.rejects(cbbSymbolBuild(p));}finally{clear();}
});
