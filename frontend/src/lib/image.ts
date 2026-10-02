// Client-side receipt-file preparation for the scan screen: browsers can
// decode far more formats than the AI connectors read well, and a photo taken
// with the receipt text sideways makes vision models loop over the text until
// their context is gone (they never reach the JSON answer). Rotating it before
// upload — and re-encoding through the canvas, which also bakes EXIF
// orientation into the pixels — gives the connector a photo it can read.

/** Decodes a file to a bitmap (EXIF orientation already applied by the
 * browser), or null when the format cannot be decoded (PDF, exotic HEIC). */
export async function decodeImage(file: File): Promise<ImageBitmap | null> {
  try {
    return await createImageBitmap(file);
  } catch {
    return null;
  }
}

/**
 * Re-encodes a file as JPEG rotated by quarterTurns × 90° (clockwise).
 * Throws when the browser cannot decode the file — callers keep the original
 * in that case, so unsupported uploads (PDFs) simply skip rotation.
 */
export async function reencodeFileRotated(
  file: File,
  quarterTurns: number,
): Promise<File> {
  const bitmap = await decodeImage(file);
  if (bitmap === null) throw new TypeError('format not decodable');
  const turns = ((quarterTurns % 4) + 4) % 4;
  // A quarter-turn swap of width/height; 0 turns keeps them (still re-encodes,
  // which strips progressive JPEGs and EXIF blocks the connectors choke on).
  const [width, height] =
    turns % 2 === 1 ? [bitmap.height, bitmap.width] : [bitmap.width, bitmap.height];
  const canvas = document.createElement('canvas');
  canvas.width = width;
  canvas.height = height;
  const ctx = canvas.getContext('2d');
  if (ctx === null) throw new TypeError('canvas unavailable');
  ctx.translate(width / 2, height / 2);
  ctx.rotate((turns * Math.PI) / 2);
  // Centered because createImageBitmap already applied any EXIF rotation.
  ctx.drawImage(bitmap, -bitmap.width / 2, -bitmap.height / 2);
  const blob = await canvasToBlob(canvas);
  bitmap.close();
  // The staged entry keeps its identity (name, size display) except for the
  // pixels — one blob name per rotation state keeps the dedup key stable.
  return new File([blob], file.name, { type: 'image/jpeg' });
}

function canvasToBlob(canvas: HTMLCanvasElement): Promise<Blob> {
  return new Promise((resolve, reject) =>
    canvas.toBlob(
      (b) => (b ? resolve(b) : reject(new TypeError('re-encode failed'))),
      'image/jpeg',
      0.9,
    ),
  );
}