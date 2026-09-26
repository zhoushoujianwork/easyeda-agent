/// <reference types="@jlceda/pro-api-types" />
import { ActionError, ErrorCodes } from './protocol';
import { captureSchematicReadiness, waitForSchematicReady } from './schematic-readiness';

export class NetlistContextError extends ActionError {
	constructor(message: string) { super(ErrorCodes.INVALID_STATE, message); }
}

/**
 * Export may finish while its annotation updates are still being synchronized.
 * Preserve identity and await the observed transaction; input focus is unrelated.
 */
export async function getSchematicNetlistFile(fileName?: string, type?: ESYS_NetlistType): Promise<File | undefined> {
	let before;
	try { before = await eda.dmt_SelectControl.getCurrentDocumentInfo(); }
	catch (err) { throw new NetlistContextError(`Cannot identify schematic before netlist export: ${String(err)}`); }
	if (!before?.uuid || !before.tabId) throw new NetlistContextError('Netlist export requires an identifiable active schematic tab.');
	let readiness;
	try { readiness = captureSchematicReadiness({ uuid: before.uuid, tabId: before.tabId }); }
	catch (err) { throw new NetlistContextError(`Cannot verify schematic readiness before netlist export: ${String(err)}`); }
	let file: File | undefined;
	try {
		file = await eda.sch_ManufactureData.getNetlistFile(fileName, type);
	}
	finally {
		try {
			await waitForSchematicReady({ uuid: before.uuid, tabId: before.tabId }, 5000, readiness);
		}
		catch (err) {
			if (err instanceof NetlistContextError) throw err;
			throw new NetlistContextError(`Cannot verify schematic readiness after netlist export: ${String(err)}`);
		}
	}
	return file;
}
