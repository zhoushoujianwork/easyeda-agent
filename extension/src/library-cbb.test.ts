import assert from 'node:assert/strict';
import { test } from 'node:test';
import { libraryCbbGet, libraryCbbSearch, libraryCbbExportSource } from './library-cbb';

test('CBB search passes explicit pagination and preserves exact library identity', async () => {
 const calls: unknown[][] = [];
 const items = [{uuid:'cbb',libraryUuid:'lib',name:'iso',ordinal:1}];
 (globalThis as any).eda={lib_Cbb:{search:async(...args:unknown[])=>{calls.push(args);return items;},get:async()=>({uuid:'cbb',libraryUuid:'lib',boards:[]})}};
 try {
  const r = await libraryCbbSearch({query:'iso',libraryUuid:'lib',limit:5,page:2});
  assert.deepEqual(calls,[['iso','lib',undefined,5,2]]);
  assert.deepEqual(r.result?.items,items.map(x=>({...x,libraryIdentitySource:'lib_Cbb.get'})));assert.equal(r.result?.count,1);
 }finally{delete (globalThis as any).eda;}
});
test('CBB search rejects invalid requests before accessing SDK',async()=>{
 (globalThis as any).eda={};
 try {for(const p of [{query:''},{query:' '},{query:'x',limit:0},{query:'x',limit:101},{query:'x',limit:1.5},{query:'x',page:0},{query:'x',page:1.5},{query:'x',typo:1}]) await assert.rejects(libraryCbbSearch(p));}
 finally{delete (globalThis as any).eda;}
});
test('CBB unavailable/error/malformed search never masquerades as empty',async()=>{
 for(const api of [{}, {search:async()=>{throw Error('offline');}}, {search:async()=>undefined}, {search:async()=>[{}]}, {search:async()=>[{uuid:'x',libraryUuid:'',name:'x'}]}]) {
  (globalThis as any).eda={lib_Cbb:api};
  try{await assert.rejects(libraryCbbSearch({query:'iso'}));}finally{delete (globalThis as any).eda;}
 }
 (globalThis as any).eda={lib_Cbb:{search:async()=>[]}};
 try{assert.equal((await libraryCbbSearch({query:'iso'})).result?.count,0);}finally{delete (globalThis as any).eda;}
});
test('CBB get preserves all boards and symbols without choosing/opening any',async()=>{
 const cbb={uuid:'cbb',libraryUuid:'lib',name:'iso',boards:[{name:'A',schematic:{cbbSymbol:{uuid:'a'}}},{name:'B',schematic:{cbbSymbol:{uuid:'b'}}}]};
 (globalThis as any).eda={lib_Cbb:{get:async(id:string,lib:string)=>{assert.equal(id,'cbb');assert.equal(lib,'lib');return cbb;}}};
 try{assert.deepEqual((await libraryCbbGet({uuid:'cbb',libraryUuid:'lib'})).result?.cbb,cbb);}finally{delete (globalThis as any).eda;}
});
test('CBB get rejects missing/wrong identity and unsupported boards',async()=>{
 for(const cbb of [undefined,{uuid:'other',libraryUuid:'lib',boards:[]},{uuid:'cbb',libraryUuid:'other',boards:[]},{uuid:'cbb',libraryUuid:'lib'}]){
  (globalThis as any).eda={lib_Cbb:{get:async()=>cbb}};
  try{await assert.rejects(libraryCbbGet({uuid:'cbb',libraryUuid:'lib'}));}finally{delete (globalThis as any).eda;}
 }
});

test('search resolves a missing runtime library UUID via get, never guesses it',async()=>{
 (globalThis as any).eda={lib_Cbb:{search:async()=>[{uuid:'c',name:'Buck'}],get:async()=>({uuid:'c',libraryUuid:'actual',boards:[]})}};
 try{assert.equal(((await libraryCbbSearch({query:'电源'})).result?.items as any[])[0].libraryUuid,'actual');}
 finally{delete (globalThis as any).eda;}
});
test('runtime schematic references are resolved read-only with strict identity',async()=>{
 (globalThis as any).eda={lib_Cbb:{get:async()=>({uuid:'c',libraryUuid:'l',boards:[{schematic:'s'}]})},dmt_Schematic:{getSchematicInfo:async()=>({uuid:'s',parentProjectUuid:'other',cbbSymbol:{uuid:'do-not-use'}})}};
 try{const r=(await libraryCbbGet({uuid:'c',libraryUuid:'l'})).result;
 assert.equal((r?.schematicDetails as any[])[0].available,false);assert.equal((r?.schematicDetails as any[])[0].schematic,undefined);}
 finally{delete (globalThis as any).eda;}
});

test('runtime schematic references expose only exact CBB-owned symbol metadata',async()=>{
 const schematic={uuid:'s',parentProjectUuid:'c',cbbSymbol:{uuid:'symbol',libraryUuid:'l'}};
 (globalThis as any).eda={lib_Cbb:{get:async()=>({uuid:'c',libraryUuid:'l',boards:[{schematic:'s'}]})},dmt_Schematic:{getSchematicInfo:async()=>schematic}};
 try{const r=(await libraryCbbGet({uuid:'c',libraryUuid:'l'})).result;assert.deepEqual((r?.schematicDetails as any[])[0],{uuid:'s',available:true,schematic});}
 finally{delete (globalThis as any).eda;}
});
test('search/get identity disagreement fails the inventory',async()=>{
 (globalThis as any).eda={lib_Cbb:{search:async()=>[{uuid:'c',name:'Buck',libraryUuid:'search-lib'}],get:async()=>({uuid:'c',libraryUuid:'actual-lib',boards:[]})}};
 try{await assert.rejects(libraryCbbSearch({query:'Buck'}));}finally{delete (globalThis as any).eda;}
});

test('CBB export uses exact official identity and format without opening a project',async()=>{
 const calls:unknown[][]=[];
 (globalThis as any).eda={lib_Cbb:{get:async()=>({uuid:'c',libraryUuid:'l',boards:[]})},sys_FileManager:{getCbbFileByCbbUuid:async(...args:unknown[])=>{calls.push(args);return new Blob(['archive']);}}};
 try{const r=(await libraryCbbExportSource({uuid:'c',libraryUuid:'l'})).result;assert.deepEqual(calls,[['c','l',{fileName:'cbb-source.epro2',fileType:'epro2'}]]);assert.equal(r?.size,7);assert.equal(r?.base64,Buffer.from('archive').toString('base64'));}
 finally{delete (globalThis as any).eda;}
});
test('CBB export rejects absent, empty, oversized and changing files',async()=>{
 for(const file of [undefined,{size:0},{size:16*1024*1024+1},{size:1,arrayBuffer:async()=>new ArrayBuffer(2)}]){
 (globalThis as any).eda={lib_Cbb:{get:async()=>({uuid:'c',libraryUuid:'l',boards:[]})},sys_FileManager:{getCbbFileByCbbUuid:async()=>file}};
 try{await assert.rejects(libraryCbbExportSource({uuid:'c',libraryUuid:'l'}));}finally{delete (globalThis as any).eda;}
 }
});

test('CBB export preserves module-download permission refusal and never opens the asset',async()=>{
 let exports=0;
 (globalThis as any).eda={lib_Cbb:{get:async()=>({uuid:'c',libraryUuid:'l',boards:[]}),openProjectInEditor:async()=>assert.fail('must not open')},sys_FileManager:{getCbbFileByCbbUuid:async()=>{exports++;throw Error('没有下载库权限');}}};
 try{await assert.rejects(libraryCbbExportSource({uuid:'c',libraryUuid:'l'}),(err:any)=>{assert.match(err.detail,/没有下载库权限/);return true;});assert.equal(exports,1);}
 finally{delete (globalThis as any).eda;}
});
