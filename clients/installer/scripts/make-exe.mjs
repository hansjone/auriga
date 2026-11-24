/**
 * Wrap a built package directory into one self-extracting setup .exe.
 *
 * The stub (windows/sfx/Stub.cs) is compiled with the C# compiler that ships
 * with Windows (.NET Framework 4, no extra install, free); the package zip is
 * appended to it with a small trailer. The exe unpacks to a temp directory,
 * runs install.ps1 (arguments are forwarded) and cleans up.
 *
 *   node scripts/make-exe.mjs --stage <package dir> --out <exe path>
 */
import { spawnSync } from 'node:child_process';
import {
  appendFileSync,
  existsSync,
  mkdirSync,
  openSync,
  closeSync,
  readSync,
  rmSync,
  statSync,
} from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const windowsRoot = process.env.SystemRoot ?? 'C:\\Windows';
const TAR = join(windowsRoot, 'System32', 'tar.exe');
const CSC = join(
  windowsRoot,
  'Microsoft.NET',
  'Framework64',
  'v4.0.30319',
  'csc.exe',
);
const MAGIC = 'AURZIP01';

export function compileStub(output, icon, options = {}) {
  if (!existsSync(CSC)) throw new Error(`C# compiler not found: ${CSC}`);
  const source = join(here, '..', 'windows', 'sfx', 'Stub.cs');
  const manifest =
    options.manifest ?? join(here, '../windows/sfx/app.manifest');
  const result = spawnSync(
    CSC,
    [
      '/nologo',
      '/codepage:65001',
      '/target:winexe',
      '/optimize+',
      '/r:System.Windows.Forms.dll',
      '/r:System.Drawing.dll',
      '/r:System.IO.Compression.dll',
      '/r:System.IO.Compression.FileSystem.dll',
      '/r:System.Web.Extensions.dll',
      `/win32manifest:${manifest}`,
      ...(icon ? [`/win32icon:${icon}`] : []),
      `/out:${output}`,
      source,
    ],
    { encoding: 'utf8' },
  );
  if (result.status !== 0)
    throw new Error(`csc failed: ${result.stdout}${result.stderr}`);
}

/** The tray app (windows/tray/Tray.cs): Start menu target, DSH owner, updates, uninstall UI. */
export function compileTray(output, icon) {
  if (!existsSync(CSC)) throw new Error(`C# compiler not found: ${CSC}`);
  mkdirSync(dirname(output), { recursive: true });
  const result = spawnSync(
    CSC,
    [
      '/nologo',
      '/codepage:65001',
      '/target:winexe',
      '/optimize+',
      '/r:System.Windows.Forms.dll',
      '/r:System.Drawing.dll',
      '/r:System.Web.Extensions.dll',
      `/win32manifest:${join(here, '../windows/tray/app.manifest')}`,
      ...(icon ? [`/win32icon:${icon}`] : []),
      `/out:${output}`,
      join(here, '..', 'windows', 'tray', 'Tray.cs'),
      join(here, '..', 'windows', 'launcher', 'DesktopShortcut.cs'),
    ],
    { encoding: 'utf8' },
  );
  if (result.status !== 0)
    throw new Error(`csc failed: ${result.stdout}${result.stderr}`);
  return output;
}

export function makeExe({ stage, out, icon, archive, manifest }) {
  stage = resolve(stage);
  out = resolve(out);
  const work = `${out}.work`;
  rmSync(work, { recursive: true, force: true });
  mkdirSync(work, { recursive: true });
  const zip = archive ? resolve(archive) : join(work, 'package.zip');
  const tar = archive
    ? { status: 0 }
    : spawnSync(TAR, ['-a', '-cf', zip, '-C', stage, '.'], {
        encoding: 'utf8',
      });
  if (tar.status !== 0) throw new Error(`zip failed: ${tar.stderr}`);
  rmSync(out, { force: true });
  compileStub(out, icon, { manifest });
  const trailer = Buffer.alloc(16);
  trailer.write(MAGIC, 0, 'ascii');
  trailer.writeBigInt64LE(BigInt(statSync(zip).size), 8);
  // Keep packaging usable on 16 GiB field machines even for large DSH archives.
  const input = openSync(zip, 'r');
  try {
    const buffer = Buffer.allocUnsafe(1024 * 1024);
    for (;;) {
      const count = readSync(input, buffer, 0, buffer.length, null);
      if (!count) break;
      appendFileSync(out, buffer.subarray(0, count));
    }
  } finally {
    closeSync(input);
  }
  appendFileSync(out, trailer);
  rmSync(work, { recursive: true, force: true });
  return out;
}

if (resolve(process.argv[1] ?? '') === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  const get = (k) => args[args.indexOf(`--${k}`) + 1];
  mkdirSync(dirname(resolve(get('out'))), { recursive: true });
  console.log(makeExe({ stage: get('stage'), out: get('out') }));
}
