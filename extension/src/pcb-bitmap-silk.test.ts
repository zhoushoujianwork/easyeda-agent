import assert from 'node:assert/strict';
import { test } from 'node:test';
import { runAction } from './actions';
import { pcbSilkImportBitmap, validateBitmapSilk, BITMAP_SILK_UNSUPPORTED } from './pcb-bitmap-silk';
import { ActionError, ErrorCodes } from './protocol';

const plan = {
	schemaVersion: 1, source: {fileName:'标志 logo.PNG',format:'png',sha256:'a'.repeat(64),pixelWidth:2,pixelHeight:2},
	conversion:{threshold:128,background:'white',invert:false,simplify:true},
	polygons:[[0,0,'L',100,0,100,100,0,100,0,0]],
	x:10,y:-20,width:100,height:100,rotation:90,mirror:false,layer:3,units:'mil',anchor:'top-left',
};

test('bitmap typed action refuses even a valid plan with host APIs present, without any EDA call',async t=>{
	const old=(globalThis as any).eda;
	let writes=0;
	(globalThis as any).eda={pcb_PrimitiveImage:{create:async()=>{writes++;}},pcb_MathPolygon:{convertImageToComplexPolygon:async()=>{writes++;}}};
	t.after(()=>{if(old===undefined)delete(globalThis as any).eda;else(globalThis as any).eda=old;});
	validateBitmapSilk(plan);
	await assert.rejects(()=>runAction('pcb.silk.import_bitmap',plan),(e:unknown)=>{
		assert.ok(e instanceof ActionError);assert.equal(e.code,ErrorCodes.PRECONDITION_REFUSED);assert.equal(e.message,BITMAP_SILK_UNSUPPORTED);return true;
	});
	assert.equal(writes,0);
	await assert.rejects(()=>pcbSilkImportBitmap({...plan,force:true}),/Unknown bitmap silk field/);
});

test('bitmap plan schema rejects unsupported layers, nonfinite geometry and malformed contours',()=>{
	for(const patch of [
		{schemaVersion:2},{units:'raw'},{anchor:'center'},{width:0},{x:Infinity},{rotation:NaN},{layer:13},{mirror:'false'},
		{source:{...plan.source,pixelWidth:1000001}},{source:{...plan.source,sha256:'bad'}},{source:{...plan.source,fileName:'x.svg'}},
		{conversion:{...plan.conversion,threshold:256}},{conversion:{...plan.conversion,background:'transparent'}},
		{polygons:[]},{polygons:[[0,0,'L',100,0,100,100,0,100,1,1]]},{polygons:[[0,0,'L',101,0,100,100,0,100,0,0]]},
	]) assert.throws(()=>validateBitmapSilk({...plan,...patch}));
});
