/** Exact observed instance data, separate from movable schematic geometry. */
export const PRESERVED_INSTANCE_FIELDS = [
 'primitiveId', 'designator', 'uniqueId', 'name', 'subPartName', 'addIntoBom', 'addIntoPcb',
 'manufacturer', 'manufacturerId', 'supplier', 'supplierId', 'otherProperty', 'component', 'symbol', 'footprint',
] as const;

export function exactJSON(value: unknown): string {
 if (Array.isArray(value)) return '[' + value.map(exactJSON).join(',') + ']';
 if (value !== null && typeof value === 'object') return '{' + Object.keys(value).sort().map(k => JSON.stringify(k) + ':' + exactJSON((value as Record<string, unknown>)[k])).join(',') + '}';
 return JSON.stringify(value);
}

export function preservedInstance(record: Record<string, unknown>, pose = false): Record<string, unknown> {
 const out: Record<string, unknown> = {};
 for (const key of [...PRESERVED_INSTANCE_FIELDS, ...(pose ? ['x', 'y', 'rotation', 'mirror'] : [])]) {
  if (!(key in record) || record[key] === undefined) throw new Error(`instance ${String(record.primitiveId)} field ${key} unavailable`);
  out[key] = record[key];
 }
 for (const key of ['primitiveId', 'designator']) {
  if (typeof out[key] !== 'string' || !String(out[key]).trim()) throw new Error(`instance ${key} must be nonempty`);
 }
 // Some official V4 library parts have a native empty uniqueId. Preserve that
 // exact value; primitiveId and the full instance record still bind the part.
 if (typeof out.uniqueId !== 'string') throw new Error('instance uniqueId must be a string');
 for (const key of ['name', 'subPartName', 'manufacturer', 'manufacturerId', 'supplier', 'supplierId']) {
  if (out[key] !== null && typeof out[key] !== 'string') throw new Error(`instance ${key} must be string or explicit null`);
 }
 for (const key of ['addIntoBom', 'addIntoPcb']) if (typeof out[key] !== 'boolean') throw new Error(`instance ${key} must be boolean`);
 for (const key of ['otherProperty', 'component', 'symbol', 'footprint']) {
  if (!out[key] || typeof out[key] !== 'object' || Array.isArray(out[key])) throw new Error(`instance ${key} must be an object`);
 }
 for (const [key, value] of Object.entries(out.otherProperty as Record<string, unknown>)) {
  if (!['string', 'number', 'boolean'].includes(typeof value) || (typeof value === 'number' && !Number.isFinite(value))) throw new Error(`instance otherProperty.${key} has unsupported type`);
 }
 if (pose && (['x', 'y', 'rotation'].some(k => typeof out[k] !== 'number' || !Number.isFinite(out[k])) || typeof out.mirror !== 'boolean')) throw new Error('instance pose unavailable');
 // Detach SDK-owned mutable maps before an action can change them.
 return JSON.parse(JSON.stringify(out));
}
