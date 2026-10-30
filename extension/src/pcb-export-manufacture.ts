/// <reference types="@jlceda/pro-api-types" />
import { readResponseContext, mutationContextError } from './eda-context';
import { ActionError, ErrorCodes, type ActionResult } from './protocol';
import { blobToBase64, newArtifactId } from './util';

export const MAX_MANUFACTURE_BYTES = 20 * 1024 * 1024;

/** Read-only official beta exports. Never reinterpret or rewrite the host file. */
async function exportManufacture(payload: Record<string, unknown>, kind: 'gerber' | 'placement'): Promise<ActionResult> {
	const allowed = kind === 'gerber' ? ['fileName', 'unit', 'colorSilkscreen', 'digitalFormat'] : ['fileName', 'unit', 'fileType'];
	const unit = payload.unit ?? 'mm';
	const fileType = kind === 'gerber' ? 'zip' : (payload.fileType ?? 'csv');
	const fileName = payload.fileName ?? `board.${fileType}`;
	if (Object.keys(payload).some(k => !allowed.includes(k))
		|| typeof fileName !== 'string' || !fileName.trim() || fileName === '.' || fileName === '..' || fileName.length > 128 || /[\\/\x00-\x1f]/.test(fileName)
		|| !(kind === 'gerber' ? ['mm', 'inch'] : ['mm', 'mil']).includes(unit as string)
		|| (kind === 'placement' && !['csv', 'xlsx'].includes(fileType as string))) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Use a basename, a supported manufacture unit and csv|xlsx placement format. Unknown options are refused.');
	}
	const color = payload.colorSilkscreen ?? false;
	const digital = payload.digitalFormat ?? (unit === 'inch' ? { integerNumber: 3, decimalNumber: 6 } : { integerNumber: 4, decimalNumber: 5 });
	if (kind === 'gerber' && (typeof color !== 'boolean' || !digital || typeof digital !== 'object'
		|| Object.keys(digital).sort().join(',') !== 'decimalNumber,integerNumber'
		|| !Object.values(digital).every(v => Number.isInteger(v) && (v as number) >= 1 && (v as number) <= 6))) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Gerber needs boolean colorSilkscreen and digitalFormat={integerNumber:1..6,decimalNumber:1..6}.');
	}
	const before = await readResponseContext();
	if (before.documentType !== 'pcb' || !before.projectUuid || !before.documentUuid || !before.tabId) {
		throw new ActionError(ErrorCodes.INVALID_STATE, 'Activate a readable target PCB tab before manufacture export.');
	}
	const api = eda.pcb_ManufactureData;
	if (typeof (kind === 'gerber' ? api?.getGerberFile : api?.getPickAndPlaceFile) !== 'function') {
		throw new ActionError(ErrorCodes.EDA_API_UNAVAILABLE, `unsupported: official ${kind} getter is unavailable on this host.`);
	}
	const components = await eda.pcb_PrimitiveComponent.getAll();
	if (!Array.isArray(components) || components.length === 0) {
		throw new ActionError(ErrorCodes.INVALID_STATE, 'PCB has no readable components; manufacture export was not attempted.');
	}
	let file: File | undefined;
	try {
		file = kind === 'gerber'
			? await api.getGerberFile(fileName, color as boolean, unit as ESYS_Unit.MILLIMETER | ESYS_Unit.INCH, digital as { integerNumber: number; decimalNumber: number })
			: await api.getPickAndPlaceFile(fileName, fileType as 'csv' | 'xlsx', unit as ESYS_Unit.MILLIMETER | ESYS_Unit.MIL);
	} catch (err) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Official ${kind} export failed.`, String(err));
	}
	if (!file || !file.size) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Official ${kind} export returned no non-empty file; inspect the active PCB, save state, nets and copper. No artifact accepted.`);
	}
	if (file.size > MAX_MANUFACTURE_BYTES) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Official ${kind} file exceeds the 20 MiB transport limit.`);
	}
	const inlineBase64 = await blobToBase64(file);
	const drift = mutationContextError(before, await readResponseContext());
	if (drift) throw new ActionError(ErrorCodes.INVALID_STATE, `Manufacture export target changed: ${drift}. No artifact accepted.`);
	const id = newArtifactId();
	return {
		result: { artifactId: id, fileType, fileName: file.name || fileName, size: file.size, unit,
			exportContext: before, ...(kind === 'gerber' ? { colorSilkscreen: color, digitalFormat: digital } : {}) },
		artifacts: [{ id, kind: `pcb_${kind}`, fileName: file.name || fileName,
			mimeType: file.type || (fileType === 'csv' ? 'text/csv' : fileType === 'xlsx' ? 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet' : 'application/zip'), inlineBase64 }],
	};
}

export const pcbExportGerber = (payload: Record<string, unknown>) => exportManufacture(payload, 'gerber');
export const pcbExportPlacement = (payload: Record<string, unknown>) => exportManufacture(payload, 'placement');
