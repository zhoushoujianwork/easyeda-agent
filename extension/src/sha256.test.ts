import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { test } from 'node:test';
import { sha256BytesHex } from './sha256';

test('sandbox SHA-256 matches FIPS vectors and Node across padding/block boundaries', () => {
	for (const value of ['', 'abc', 'abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq', 'a'.repeat(1000000)]) {
		const bytes = new TextEncoder().encode(value);
		assert.equal(sha256BytesHex(bytes), createHash('sha256').update(bytes).digest('hex'));
	}
	for (const size of [1,55,56,57,63,64,65,119,120,127,128,129,1024,65537]) {
		const bytes = Uint8Array.from({length:size}, (_,i)=>(i*131+17)&255);
		assert.equal(sha256BytesHex(bytes), createHash('sha256').update(bytes).digest('hex'));
	}
});
