/** Offscreen smoke: only temporary/staged payloads, no DSH server or desktop UI. */
import { spawnSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { performance } from 'node:perf_hooks';
import { launchEnv, nodeBin } from '../runtime/core.mjs';
import { sha256File } from '../runtime/verify.mjs';
import { PYTHON_LOCK, readPythonLock } from './python-runtime.mjs';

export function smokePython(payload) {
  const lock = readPythonLock();
  const directory = join(payload, 'runtime/python');
  const manifest = JSON.parse(
    readFileSync(join(directory, 'runtime.json'), 'utf8'),
  );
  if (
    manifest.lockSha256 !== sha256File(PYTHON_LOCK) ||
    JSON.stringify(manifest.packages) !== JSON.stringify(lock.wheels) ||
    manifest.installed.length !== lock.wheels.length
  )
    throw new Error('Python runtime manifest differs from lock');
  for (const file of [
    ...manifest.python.licenses,
    ...manifest.installed.flatMap((row) => row.licenses),
  ])
    if (!existsSync(join(directory, file)))
      throw new Error(`Python license missing: ${file}`);
  const env = launchEnv(payload, join(payload, 'smoke-home'));
  const imports =
    'import docx, pptx, openpyxl, pandas, PIL, lxml, xlsxwriter; print("ok")';
  const versions = `import sys, pathlib, importlib.metadata as m; assert pathlib.Path(sys.executable).resolve().parent == pathlib.Path(${JSON.stringify(directory)}).resolve(); assert sys.version_info[:3] == (${lock.version.split('.').join(',')}); expected=${JSON.stringify(Object.fromEntries(lock.wheels.map((w) => [w.name.toLowerCase().replace(/[-_.]+/g, '-'), w.version])))}; actual={d.metadata['Name'].lower().replace('_','-'):d.version for d in m.distributions()}; assert actual == expected, actual; print('versions ok')`;
  const importMilliseconds = {};
  for (const alias of ['python', 'python3']) {
    for (const source of [imports, versions]) {
      const started = performance.now();
      // Resolve through PATH just as DSH's subprocess provider does.
      const result = spawnSync(alias, ['-c', source], {
        cwd: payload,
        env,
        windowsHide: true,
        encoding: 'utf8',
        timeout: 120000,
      });
      if (result.status !== 0)
        throw new Error(
          `${alias} smoke failed (${result.status}): ${result.stderr || result.error}`,
        );
      if (source === imports)
        importMilliseconds[alias] = Math.round(performance.now() - started);
    }
  }
  for (const module of ['ensurepip', 'pip']) {
    const result = spawnSync(
      join(directory, 'python.exe'),
      ['-I', '-m', module],
      {
        cwd: payload,
        env,
        windowsHide: true,
        encoding: 'utf8',
        timeout: 120000,
      },
    );
    if (
      result.status === 0 ||
      !result.stderr?.includes(`No module named ${module}`)
    )
      throw new Error(`Bundled Python unexpectedly provides ${module}`);
  }
  if (existsSync(join(directory, 'py.exe')))
    throw new Error('Bundled Python must not shadow the system py launcher');
  return {
    version: manifest.version,
    packages: manifest.packages.length,
    aliases: ['python', 'python3'],
    importMilliseconds,
    result: 'ok',
  };
}

/** Exercise the public environment contract from the actual pinned npm closure. */
export function smokeDshPython(payload) {
  const code = `
    import { createRequire } from 'node:module';
    import { pathToFileURL } from 'node:url';
    import { spawnSync } from 'node:child_process';
    import assert from 'node:assert/strict';
    const require = createRequire(${JSON.stringify(join(payload, 'upstream/app/package.json'))});
    const { scrubbedParentEnv } = await import(pathToFileURL(require.resolve('@deepseek-ai/dsh-subprocess')).href);
    const env = scrubbedParentEnv();
    for (const name of ['python', 'python3']) {
      const r = spawnSync(name, ['-c', 'import sys, docx, pptx, openpyxl, pandas, PIL, lxml, xlsxwriter; print(sys.version.split()[0])'], { env, windowsHide: true, encoding: 'utf8', timeout: 120000 });
      assert.equal(r.status, 0, r.stderr || String(r.error));
      assert.equal(r.stdout.trim(), ${JSON.stringify(readPythonLock().version)});
    }
    console.log('DSH subprocess Python ok');
  `;
  const result = spawnSync(
    nodeBin(payload),
    ['--input-type=module', '-e', code],
    {
      env: launchEnv(payload, join(payload, 'smoke-home')),
      cwd: payload,
      windowsHide: true,
      encoding: 'utf8',
      timeout: 180000,
    },
  );
  if (result.status !== 0)
    throw new Error(
      `DSH Python smoke failed (${result.status}): ${result.stderr || result.error}`,
    );
  return result.stdout.trim();
}

if (resolve(process.argv[1] ?? '') === fileURLToPath(import.meta.url)) {
  if (!process.argv[2]) throw new Error('staged payload directory required');
  console.log(JSON.stringify(smokePython(resolve(process.argv[2]))));
  if (process.argv[3] === '--dsh')
    console.log(smokeDshPython(resolve(process.argv[2])));
}
