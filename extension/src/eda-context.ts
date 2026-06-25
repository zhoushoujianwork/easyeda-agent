/**
 * Helpers that read EasyEDA project/document context via the official `eda`
 * object. Used both for the `context` frame sent on connect and for the
 * `context` block attached to each action response.
 */

import type { ContextFrame, ResponseContext } from './protocol';

/**
 * Map a numeric EDMT_EditorDocumentType to a stable string label used in the
 * protocol. Only schematic is meaningful for Phase 1; others are labelled for
 * completeness.
 *
 * @param documentType - numeric document type from getCurrentDocumentInfo
 * @returns a lowercase string label
 */
export function documentTypeLabel(documentType: number | undefined): string | undefined {
	if (documentType === undefined) {
		return undefined;
	}
	switch (documentType) {
		case EDMT_EditorDocumentType.HOME:
			return 'home';
		case EDMT_EditorDocumentType.BLANK:
			return 'blank';
		case EDMT_EditorDocumentType.SCHEMATIC_PAGE:
			return 'schematic';
		case EDMT_EditorDocumentType.PCB:
			return 'pcb';
		case EDMT_EditorDocumentType.SYMBOL_COMPONENT:
			return 'symbol';
		case EDMT_EditorDocumentType.FOOTPRINT:
			return 'footprint';
		case EDMT_EditorDocumentType.PANEL:
			return 'panel';
		default:
			return `type_${documentType}`;
	}
}

/**
 * Read the best-effort current context (project + active document). Every field
 * is optional; failures are swallowed so context never blocks an action.
 *
 * @returns a partial response context
 */
export async function readResponseContext(): Promise<ResponseContext> {
	const context: ResponseContext = {};

	try {
		const project = await eda.dmt_Project.getCurrentProjectInfo();
		if (project) {
			context.projectUuid = project.uuid;
			context.projectName = project.friendlyName || project.name;
		}
	}
	catch { /* best-effort */ }

	try {
		const doc = await eda.dmt_SelectControl.getCurrentDocumentInfo();
		if (doc) {
			context.documentUuid = doc.uuid;
			context.tabId = doc.tabId;
			const label = documentTypeLabel(doc.documentType);
			if (label) {
				context.documentType = label;
			}
		}
	}
	catch { /* best-effort */ }

	return context;
}

/**
 * Read the EasyEDA client version for the `register` frame. Best-effort; falls
 * back to an empty string.
 *
 * @returns the EasyEDA version string, or '' if unavailable
 */
export function readEasyEdaVersion(): string {
	try {
		const version = eda.sys_Environment.getEditorCurrentVersion();
		if (typeof version === 'string') {
			return version;
		}
	}
	catch { /* best-effort */ }
	return '';
}

/**
 * Build the `context` frame sent to the daemon after a successful register.
 * Empty fields are omitted.
 *
 * @param windowId - the registered window id
 * @returns a context frame
 */
export async function buildContextFrame(windowId: string): Promise<ContextFrame> {
	const ctx = await readResponseContext();
	const frame: ContextFrame = {
		type: 'context',
		windowId,
	};
	if (ctx.projectUuid) {
		frame.projectUuid = ctx.projectUuid;
	}
	if (ctx.projectName) {
		frame.projectName = ctx.projectName;
	}
	if (ctx.documentUuid) {
		frame.documentUuid = ctx.documentUuid;
	}
	if (ctx.documentType) {
		frame.documentType = ctx.documentType;
	}
	if (ctx.tabId) {
		frame.tabId = ctx.tabId;
	}
	return frame;
}
