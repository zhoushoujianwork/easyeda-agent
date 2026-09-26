/// <reference types="@jlceda/pro-api-types" />
import { ActionError, ErrorCodes } from './protocol';

export class NetlistContextError extends ActionError {
	constructor(message: string) { super(ErrorCodes.INVALID_STATE, message); }
}

/**
 * Manufacture export can leave Web 4.1.60's input focus outside the editor.
 * A subsequent component delete then returns without removing its target.
 * Restore the captured tab's focus through the official API, without closing,
 * reloading, reopening, retrying a mutation, or changing project data.
 */
export async function getSchematicNetlistFile(fileName?: string, type?: ESYS_NetlistType): Promise<File | undefined> {
	let before;
	try { before = await eda.dmt_SelectControl.getCurrentDocumentInfo(); }
	catch (err) { throw new NetlistContextError(`Cannot identify schematic before netlist export: ${String(err)}`); }
	if (!before?.uuid || !before.tabId) throw new NetlistContextError('Netlist export requires an identifiable active schematic tab.');
	let file: File | undefined;
	try {
		file = await eda.sch_ManufactureData.getNetlistFile(fileName, type);
	}
	finally {
		try {
		const current = await eda.dmt_SelectControl.getCurrentDocumentInfo();
		if (current?.uuid !== before.uuid || current.tabId !== before.tabId) {
			throw new NetlistContextError('Active document changed during netlist export; refusing to refocus another document.');
		}
		if (!await eda.dmt_EditorControl.activateDocument(before.tabId)) {
			throw new NetlistContextError('Could not restore editor focus after netlist export.');
		}
		const after = await eda.dmt_SelectControl.getCurrentDocumentInfo();
		if (after?.uuid !== before.uuid || after.tabId !== before.tabId) {
			throw new NetlistContextError('Document identity changed while restoring editor focus after netlist export.');
		}
		}
		catch (err) {
			if (err instanceof NetlistContextError) throw err;
			throw new NetlistContextError(`Cannot verify editor focus restoration after netlist export: ${String(err)}`);
		}
	}
	return file;
}
