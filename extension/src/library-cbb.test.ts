import assert from 'node:assert/strict';
import { test } from 'node:test';
import { libraryCbbGet, libraryCbbSearch } from './library-cbb';

test('CBB search passes explicit pagination and preserves exact library identity', async () => {
 const calls: unknown[][] = [];
 const items = [{uuid:'cbb',libraryUuid:'lib',name:'iso',ordinal:1}];
 (globalThis as any).eda={lib_Cbb:{search:async(...args:unknown[])=>{calls.push(args);return items;}}};
 try {
  const r = await libraryCbbSearch({query:'iso',libraryUuid:'lib',limit:5,page:2});
  assert.deepEqual(calls,[['iso','lib',undefined,5,2]]);
  assert.deepEqual(r.result?.items,items);assert.equal(r.result?.count,1);
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
