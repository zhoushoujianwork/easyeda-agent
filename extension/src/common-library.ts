/// <reference types="@jlceda/pro-api-types" />
import { ActionError, type ActionResult, ErrorCodes } from './protocol';
import { describeThrown, optionalString } from './util';

export interface CommonLibraryModel {
	category: string;
	group: string;
	deviceUuid: string;
	libraryUuid: string;
	name: string | null;
	comment: string | null;
	symbolUuid: string | null;
	footprintUuid: string | null;
	schematic: boolean | null;
	pcb: boolean | null;
	identityVerified: false;
}

function invalid(message: string): never {
	throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Unsupported Common Library source: ${message}`);
}

function record(value: unknown, label: string): Record<string, unknown> {
	if (!value || typeof value !== 'object' || Array.isArray(value)) invalid(`${label} must be an object.`);
	return value as Record<string, unknown>;
}

function textField(row: Record<string, unknown>, key: string): string | null {
	const value = row[key];
	if (value === undefined || value === null) return null;
	if (typeof value !== 'string') invalid(`${key} must be a string.`);
	return value;
}

function enabledField(row: Record<string, unknown>, key: string): boolean | null {
	const value = row[key];
	if (value === undefined || value === null || value === '') return null;
	if (value === true || value === 'true') return true;
	if (value === false || value === 'false') return false;
	return invalid(`${key} must be a boolean or a true/false string.`);
}

/**
 * Source schema: the official editor's getSystemLists consumes
 * result -> commSystemSetting.BASE_LIBRARY -> category {name, data[]}.
 * Keep these configured references separate from verified lib_Device assets.
 */
export function parseSystemCommonLibrary(value: unknown, systemLibraryUuid: string): CommonLibraryModel[] {
	const envelope = record(value, 'response');
	if (envelope.success !== true) invalid('response.success is not true.');
	let result = envelope.result;
	if (typeof result === 'string') {
		try { result = JSON.parse(result); }
		catch { invalid('result is not valid JSON.'); }
	}
	const setting = record(record(result, 'result').commSystemSetting, 'commSystemSetting');
	if (!Array.isArray(setting.BASE_LIBRARY)) invalid('BASE_LIBRARY must be an array.');
	if (!systemLibraryUuid.trim()) invalid('the official System library UUID is unavailable.');
	const models: CommonLibraryModel[] = [];
	for (const categoryValue of setting.BASE_LIBRARY) {
		const category = record(categoryValue, 'category');
		const name = textField(category, 'name');
		if (name === null) invalid('category needs a name.');
		// The bundled catalogue ends with a named placeholder without data.
		if (category.data === undefined) {
			if (Object.keys(category).some(key => key !== 'name')) invalid('unknown category fields without data[].');
			continue;
		}
		if (!Array.isArray(category.data)) invalid('category data must be an array.');
		for (const modelValue of category.data) {
			const row = record(modelValue, 'model');
			const deviceUuid = textField(row, 'device');
			if (!deviceUuid?.trim()) invalid('model needs a non-empty device UUID.');
			const rowCategory = textField(row, 'category');
			if (rowCategory !== null && rowCategory !== name) invalid('model category differs from its container.');
			models.push({
				category: name,
				group: textField(row, 'group') ?? '',
				deviceUuid,
				libraryUuid: textField(row, 'devicePath') || systemLibraryUuid,
				name: textField(row, 'deviceName'),
				comment: textField(row, 'comment'),
				symbolUuid: textField(row, 'symbol'),
				footprintUuid: textField(row, 'footprint'),
				schematic: enabledField(row, 'sch'),
				pcb: enabledField(row, 'pcb'),
				identityVerified: false,
			});
		}
	}
	return models;
}

/** Read only the public System catalogue, with no search/GUI/private-cache fallback. */
export async function listSystemCommonLibrary(payload: Record<string, unknown>): Promise<ActionResult> {
	const category = optionalString(payload, 'category');
	const group = optionalString(payload, 'group');
	const query = optionalString(payload, 'query')?.normalize('NFKC').trim().toLocaleLowerCase();
	const environment = eda.sys_Environment;
	if (typeof environment?.isWeb !== 'function' || typeof environment.isProPrivateEdition !== 'function'
		|| !environment.isWeb() || environment.isProPrivateEdition()) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'Common Library source discovery supports the public Web editor only.');
	}
	const origin = environment.isJLCEDAProEdition() ? 'https://pro.lceda.cn'
		: environment.isEasyEDAProEdition() ? 'https://pro.easyeda.com' : null;
	if (!origin) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'The editor site could not be identified.');
	const url = `${origin}/api/system/preference`;
	try {
		const response = await eda.sys_ClientUrl.request(url, 'GET');
		if (!response.ok) {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Common Library source refused HTTP ${response.status}.`, url);
		}
		const source: unknown = await response.json();
		const systemLibraryUuid = await eda.lib_LibrariesList.getSystemLibraryUuid();
		const models = parseSystemCommonLibrary(source, systemLibraryUuid ?? '');
		const scoped = models.filter(model =>
			(category === undefined || model.category === category)
			&& (group === undefined || model.group === group));
		if (query && scoped.some(model => !model.name?.trim())) {
			throw new ActionError(ErrorCodes.PRECONDITION_REFUSED,
				'Catalogue model names are unavailable. List without --query and resolve explicit UUIDs with lib device get.');
		}
		const devices = scoped.filter(model => !query || [model.name, model.comment, model.group].some(value =>
			value?.normalize('NFKC').toLocaleLowerCase().includes(query)));
		return { result: {
			scope: 'system', count: devices.length, catalogueCount: models.length, devices,
			selectedModelAvailable: false,
			source: { url, readVia: 'eda.sys_ClientUrl.request', capturedAt: new Date().toISOString() },
		} };
	}
	catch (error) {
		if (error instanceof ActionError) throw error;
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Failed to read the System Common Library catalogue.', describeThrown(error));
	}
}
