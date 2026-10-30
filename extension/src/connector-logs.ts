import { ActionError, ErrorCodes } from './protocol';

// Read only our transport diagnostics. Never clear/export logs or return
// unrelated project, account or third-party extension messages.
export async function readConnectorLogs(limit: unknown): Promise<Array<{ timestamp: number; type: string; message: string }>> {
	if (!Number.isInteger(limit) || (limit as number) < 1 || (limit as number) > 100)
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'includeConnectorLogs must be an integer from 1 to 100.');
	const rows = await eda.sys_Log.sort();
	if (!Array.isArray(rows)) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Connector diagnostics are unavailable.');
	if (rows.some(row => typeof row?.message !== 'string'))
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Native log shape is unsupported (fields: ${[...new Set(rows.flatMap(row => row && typeof row === 'object' ? Object.keys(row) : []))].join(',')}).`);
	return rows.filter(row => row.message.includes('[easyeda-agent] '))
		.slice(-(limit as number)).map(row => {
			if (!Number.isFinite(row.timestamp) || typeof row.type !== 'string')
				throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Connector diagnostic metadata is incomplete.');
			return { timestamp: row.timestamp, type: row.type, message: row.message.slice(row.message.indexOf('[easyeda-agent] ')).slice(0, 2000) };
		});
}
