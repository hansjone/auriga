import { execFileSync } from 'node:child_process';
import { mkdirSync, existsSync } from 'node:fs';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
export function buildHelper(output) {
  const compiler = join(
    process.env.SystemRoot ?? 'C:\\Windows',
    'Microsoft.NET/Framework64/v4.0.30319/csc.exe',
  );
  if (process.platform !== 'win32' || !existsSync(compiler))
    throw new Error('Windows .NET Framework compiler required');
  mkdirSync(dirname(output), { recursive: true });
  execFileSync(
    compiler,
    [
      '/nologo',
      '/target:exe',
      '/optimize+',
      '/r:System.Web.Extensions.dll',
      '/r:System.Security.dll',
      `/out:${output}`,
      fileURLToPath(new URL('./Helper.cs', import.meta.url)),
    ],
    { windowsHide: true, stdio: 'pipe' },
  );
  return output;
}
if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
)
  buildHelper(resolve(process.argv[2]));
