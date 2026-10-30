/// <reference types="@jlceda/pro-api-types" />
import { ActionError, type ActionResult, ErrorCodes } from './protocol';
import { describeThrown, requireString, optionalString, optionalNumber } from './util';

function fields(payload: Record<string, unknown>, allowed: string[]) {
 for (const key of Object.keys(payload)) if (!allowed.includes(key)) throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, `Unknown CBB field: ${key}`);
}
function nonempty(payload: Record<string, unknown>, key: string): string {
 const value = requireString(payload, key);
 if (!value.trim()) throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, `${key} must not be blank.`);
 return value;
}
function failure(err: unknown): never {
 if (err instanceof ActionError) throw err;
 throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Official CBB library read failed.', describeThrown(err));
}
export async function libraryCbbSearch(payload: Record<string, unknown>): Promise<ActionResult> {
 fields(payload, ['query', 'libraryUuid', 'limit', 'page']);
 const query = nonempty(payload, 'query');
 const libraryUuid = optionalString(payload, 'libraryUuid');
 const limit = optionalNumber(payload, 'limit') ?? 20, page = optionalNumber(payload, 'page') ?? 1;
 if (!Number.isInteger(limit) || limit < 1 || limit > 100 || !Number.isInteger(page) || page < 1) throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'CBB limit must be 1..100 and page a positive integer.');
 if (typeof eda.lib_Cbb?.search !== 'function') throw new ActionError(ErrorCodes.INVALID_STATE, 'Official beta lib_Cbb.search is unavailable.');
 try {
  const items = await eda.lib_Cbb.search(query, libraryUuid, undefined, limit, page);
  if (!Array.isArray(items) || items.length > limit || items.some(item => !item || typeof item.uuid !== 'string' || !item.uuid || typeof item.libraryUuid !== 'string' || !item.libraryUuid || typeof item.name !== 'string')) throw new ActionError(ErrorCodes.INVALID_STATE, 'CBB search returned an unsupported inventory; cannot treat it as empty.');
  return { result: { query, libraryUuid: libraryUuid ?? null, limit, page, count: items.length, items, source: 'lib_Cbb.search', apiStatus: 'beta' } };
 } catch (err) { return failure(err); }
}
export async function libraryCbbGet(payload: Record<string, unknown>): Promise<ActionResult> {
 fields(payload, ['uuid', 'libraryUuid']);
 const uuid = nonempty(payload, 'uuid'), libraryUuid = optionalString(payload, 'libraryUuid');
 if (typeof eda.lib_Cbb?.get !== 'function') throw new ActionError(ErrorCodes.INVALID_STATE, 'Official beta lib_Cbb.get is unavailable.');
 try {
  const cbb = await eda.lib_Cbb.get(uuid, libraryUuid);
  if (!cbb) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `CBB "${uuid}" was not found.`);
  if (cbb.uuid !== uuid || (libraryUuid !== undefined && cbb.libraryUuid !== libraryUuid) || !Array.isArray(cbb.boards)) throw new ActionError(ErrorCodes.INVALID_STATE, 'CBB identity or boards inventory does not match the request.');
  return { result: { cbb, source: 'lib_Cbb.get', apiStatus: 'beta' } };
 } catch (err) { return failure(err); }
}
