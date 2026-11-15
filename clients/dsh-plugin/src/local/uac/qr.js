import { qrcodegen } from '../../generated/qrcodegen.js';

/** Render public scan coordinates as an SVG path (one unit per module, 4-module quiet zone).
 * @param {string} content @returns {{size:number,path:string}} */
export function qrPath(content) {
  const code = qrcodegen.QrCode.encodeText(
    content,
    qrcodegen.QrCode.Ecc.MEDIUM,
  );
  const border = 4;
  const parts = [];
  for (let y = 0; y < code.size; y++)
    for (let x = 0; x < code.size; x++)
      if (code.getModule(x, y))
        parts.push(`M${x + border} ${y + border}h1v1h-1z`);
  return { size: code.size + border * 2, path: parts.join('') };
}
