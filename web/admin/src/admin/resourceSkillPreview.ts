/* global ReadableStream */
const max = 10 * 1024 * 1024;
export async function boundedBytes(stream: ReadableStream<Uint8Array>) {
  const reader = stream.getReader(),
    chunks: Uint8Array[] = [];
  let size = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.length;
      if (size > max) throw new Error('Preview budget');
      chunks.push(value);
    }
  } finally {
    await reader.cancel();
  }
  const bytes = new Uint8Array(size);
  let offset = 0;
  for (const c of chunks) {
    bytes.set(c, offset);
    offset += c.length;
  }
  return bytes;
}
export async function skillPreview(response: Response) {
  if (!response.body) throw new Error('Missing body');
  const bytes = await boundedBytes(response.body);
  const raw =
    bytes[0] === 31 && bytes[1] === 139
      ? await boundedBytes(
          new Blob([bytes])
            .stream()
            .pipeThrough(new DecompressionStream('gzip')),
        )
      : bytes;
  return tarText(raw);
}
export type PreviewFile = {
  path: string;
  text: string;
  binary?: boolean;
  sizeBytes?: number;
};

export type PreviewArchiveResult = {
  files: PreviewFile[];
  truncated: boolean;
};

export async function archivePreview(
  response: Response,
  kind: 'skill' | 'plugin',
): Promise<PreviewFile[]> {
  return (await archivePreviewWithMeta(response, kind)).files;
}

export async function archivePreviewWithMeta(
  response: Response,
  kind: 'skill' | 'plugin',
): Promise<PreviewArchiveResult> {
  if (kind === 'skill')
    return { files: await skillPreview(response), truncated: false };
  if (!response.body) throw new Error('Missing body');
  const bytes = await boundedBytes(response.body);
  const raw =
    bytes[0] === 31 && bytes[1] === 139
      ? await boundedBytes(
          new Blob([bytes])
            .stream()
            .pipeThrough(new DecompressionStream('gzip')),
        )
      : bytes;
  return tarArchiveEntriesWithMeta(raw);
}

export function tarArchiveEntries(raw: Uint8Array): PreviewFile[] {
  return tarArchiveEntriesWithMeta(raw).files;
}

export function tarArchiveEntriesWithMeta(
  raw: Uint8Array,
): PreviewArchiveResult {
  const headerDecoder = new TextDecoder('utf-8', { fatal: true });
  const textDecoder = new TextDecoder('utf-8', { fatal: true });
  const result: PreviewFile[] = [];
  const seen = new Set<string>();
  let truncated = false;
  for (let offset = 0, count = 0; offset + 512 <= raw.length; count++) {
    const header = raw.subarray(offset, offset + 512);
    if (header.every((b) => b === 0)) return { files: result, truncated };
    if (count >= 512) {
      truncated = true;
      break;
    }
    const str = (start: number, end: number) =>
      headerDecoder.decode(header.subarray(start, end)).split('\0')[0]!;
    const name = str(0, 100),
      prefix = str(345, 500),
      rawPath = prefix ? `${prefix}/${name}` : name;
    const path = rawPath.replace(/^package\//, '');
    const sizeField = str(124, 136).trim();
    if (!/^[0-7]+$/.test(sizeField)) throw new Error('Invalid size');
    const size = parseInt(sizeField, 8),
      type = str(156, 157);
    if (
      rawPath.startsWith('/') ||
      rawPath.includes('\\') ||
      rawPath.split('/').some((s) => s === '.' || s === '..') ||
      seen.has(path) ||
      !['', '0', '5'].includes(type) ||
      offset + 512 + size > raw.length
    )
      throw new Error('Unsafe preview');
    seen.add(path);
    if (type !== '5') {
      const slice = raw.subarray(offset + 512, offset + 512 + size);
      if (size > 256 * 1024) {
        result.push({
          path,
          text: `[文件过大，已省略预览 (${size} bytes)]`,
          binary: true,
          sizeBytes: size,
        });
      } else {
        try {
          const text = textDecoder.decode(slice);
          const hasControl = [...text].some(
            (c) => c.charCodeAt(0) < 32 && !['\t', '\n', '\r'].includes(c),
          );
          result.push(
            hasControl
              ? {
                  path,
                  text: `[二进制文件 (${size} bytes)]`,
                  binary: true,
                  sizeBytes: size,
                }
              : { path, text, sizeBytes: size },
          );
        } catch {
          result.push({
            path,
            text: `[二进制文件 (${size} bytes)]`,
            binary: true,
            sizeBytes: size,
          });
        }
      }
    }
    offset += 512 + Math.ceil(size / 512) * 512;
  }
  return { files: result, truncated };
}
export function tarText(raw: Uint8Array) {
  const decoder = new TextDecoder('utf-8', { fatal: true }),
    result: { path: string; text: string }[] = [];
  const seen = new Set<string>();
  for (let offset = 0, count = 0; offset + 512 <= raw.length; count++) {
    if (count > 512) throw new Error('Entry budget');
    const header = raw.subarray(offset, offset + 512);
    if (header.every((b) => b === 0)) return result;
    const str = (start: number, end: number) =>
      decoder.decode(header.subarray(start, end)).split('\0')[0]!;
    const name = str(0, 100),
      prefix = str(345, 500),
      path = prefix ? `${prefix}/${name}` : name;
    const sizeField = str(124, 136).trim();
    if (!/^[0-7]+$/.test(sizeField)) throw new Error('Invalid size');
    const size = parseInt(sizeField, 8),
      type = str(156, 157);
    if (
      path.startsWith('/') ||
      path.includes('\\') ||
      path.split('/').some((s) => s === '.' || s === '..') ||
      seen.has(path) ||
      !['', '0', '5'].includes(type) ||
      size > 1024 * 1024 ||
      offset + 512 + size > raw.length
    )
      throw new Error('Unsafe preview');
    seen.add(path);
    if (type !== '5') {
      const text = decoder.decode(
        raw.subarray(offset + 512, offset + 512 + size),
      );
      if (
        [...text].some(
          (c) => c.charCodeAt(0) < 32 && !['\t', '\n', '\r'].includes(c),
        )
      )
        throw new Error('Binary content');
      result.push({ path, text });
    }
    offset += 512 + Math.ceil(size / 512) * 512;
  }
  throw new Error('Truncated tar');
}
