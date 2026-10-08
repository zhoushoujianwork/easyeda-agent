import { ActionError, ErrorCodes, type ActionResult } from './protocol';

export const BITMAP_SILK_UNSUPPORTED = 'unsupported: bitmap manufacturing silk is offline-verified only; host creation, geometry readback, DFM and save/reload are not live-verified; no write attempted';

// Same schema as internal/protocol/bitmap_silk.go. This validates plans only;
// neither API presence nor caller-supplied validation fields enable writing.
export function validateBitmapSilk(payload: Record<string, unknown>): void {
	const refuse = (message: string): never => { throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, message); };
	const object = (value: unknown): Record<string, unknown> => {
		if (!value || typeof value !== 'object' || Array.isArray(value)) return refuse('Bitmap silk metadata must be objects.');
		return value as Record<string, unknown>;
	};
	const finite = (value: unknown): value is number => typeof value === 'number' && Number.isFinite(value);
	const only = (value: Record<string, unknown>, keys: Array<string>) => {
		if (Object.keys(value).some(key => !keys.includes(key))) refuse('Unknown bitmap silk field.');
	};
	only(payload, ['schemaVersion','source','conversion','polygons','x','y','width','height','rotation','mirror','layer','units','anchor']);
	if (payload.schemaVersion !== 1 || payload.units !== 'mil' || payload.anchor !== 'top-left') refuse('Bitmap silk requires schemaVersion 1, units mil and anchor top-left.');
	for (const key of ['x','y','width','height','rotation']) if (!finite(payload[key])) refuse('Bitmap silk geometry must be finite.');
	const width = payload.width as number, height = payload.height as number;
	if (width <= 0 || height <= 0 || ![3,4].includes(payload.layer as number) || typeof payload.mirror !== 'boolean') refuse('Bitmap silk requires positive dimensions, boolean mirror and layer 3 or 4.');
	const source = object(payload.source), conversion = object(payload.conversion);
	only(source, ['fileName','format','sha256','pixelWidth','pixelHeight']);
	only(conversion, ['threshold','background','invert','simplify']);
	const ext = typeof source.fileName === 'string' ? source.fileName.toLowerCase().match(/\.[^.]+$/)?.[0] : undefined;
	if (typeof source.fileName !== 'string' || /[/\\]/.test(source.fileName) || typeof source.sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(source.sha256)
		|| !((source.format === 'png' && ext === '.png') || (source.format === 'jpeg' && ['.jpg','.jpeg'].includes(ext ?? '')))
		|| !Number.isSafeInteger(source.pixelWidth) || !Number.isSafeInteger(source.pixelHeight)
		|| (source.pixelWidth as number) <= 0 || (source.pixelHeight as number) <= 0 || (source.pixelWidth as number) > 1_000_000 / (source.pixelHeight as number)) refuse('Invalid bitmap source metadata.');
	if (!Number.isInteger(conversion.threshold) || (conversion.threshold as number) < 0 || (conversion.threshold as number) > 255
		|| !['white','black'].includes(conversion.background as string) || typeof conversion.invert !== 'boolean' || typeof conversion.simplify !== 'boolean') refuse('Invalid bitmap conversion parameters.');
	const polygons = payload.polygons;
	if (!Array.isArray(polygons) || !polygons.length || polygons.length > 25_000) refuse('Bitmap silk requires non-empty bounded contours.');
	let vertices = 0;
	for (const contour of polygons as Array<unknown>) {
		if (!Array.isArray(contour) || contour.length < 11 || contour.length % 2 !== 1 || contour[2] !== 'L') refuse('Bitmap contour must be closed [x0,y0,L,x1,y1,...,x0,y0].');
		const c = contour as Array<unknown>;
		const numbers = c.filter((_, i) => i !== 2);
		if (numbers.some((n, i) => !finite(n) || n < 0 || n > (i % 2 === 0 ? width : height))) refuse('Bitmap contour coordinates must be finite and within the canvas.');
		if (numbers[0] !== numbers[numbers.length-2] || numbers[1] !== numbers[numbers.length-1]) refuse('Bitmap contour is not closed.');
		vertices += numbers.length / 2;
		if (vertices > 100_000) refuse('Bitmap contours exceed 100000 vertices.');
	}
}

export async function pcbSilkImportBitmap(payload: Record<string, unknown>): Promise<ActionResult> {
	validateBitmapSilk(payload);
	throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, BITMAP_SILK_UNSUPPORTED);
}
