import { readFile, writeFile, mkdir, cp } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import {
  packSkill,
  unpackSkill,
} from '../clients/dsh-plugin/src/resources/skill-package.js';
import { validateArchive } from '../clients/dsh-plugin/src/resources/archive.js';
import { schemaValid } from '../clients/dsh-plugin/src/resources/schema.js';
import assert from 'node:assert/strict';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
const req = createRequire(new URL('../package.json', import.meta.url));
const { build, version: esbuildVersion } = createRequire(
  req.resolve('vite/package.json'),
)('esbuild');
const source = process.argv[2];
if (!source)
  throw Error('usage: node seed-resources/prepare.mjs <dsh-memwiki source>');
const root = new URL('./', import.meta.url);
const out = new URL('memwiki/package/', root);
await mkdir(new URL('lib/', out), { recursive: true });
// Read only generic distribution inputs. Never walk the user vault or config.
for (const entry of ['index', 'cli', 'dsh-host']) {
  const result = await build({
    metafile: true,
    entryPoints: [`${source}/lib/${entry}.js`],
    outfile: new URL(`lib/${entry}.js`, out).pathname.replace(/^\/(.:)/, '$1'),
    bundle: true,
    platform: 'node',
    format: 'esm',
    target: 'node22',
    banner: {
      js: "import { createRequire as __seedRequire } from 'node:module'; const require = __seedRequire(import.meta.url);",
    },
    sourcemap: false,
  });
  for (const input of Object.keys(result.metafile.inputs)) {
    if (!input.includes('node_modules/')) continue;
    let location = pathToFileURL(resolve(input));
    while (location.pathname.includes('node_modules/')) {
      try {
        const dependency = JSON.parse(
          await readFile(new URL('package.json', location), 'utf8'),
        );
        for (const filename of [
          'LICENSE',
          'LICENSE.md',
          'LICENSE.txt',
          'LICENCE',
        ]) {
          try {
            await mkdir(new URL('licenses/', out), { recursive: true });
            await cp(
              new URL(filename, location),
              new URL(
                'licenses/' +
                  dependency.name.replaceAll('/', '-') +
                  '-' +
                  filename,
                out,
              ),
            );
          } catch {}
        }
        break;
      } catch {
        location = new URL('../', location);
      }
    }
  }
}
for (const entry of ['index', 'cli', 'dsh-host']) {
  const file = new URL(`lib/${entry}.js`, out);
  await writeFile(
    file,
    (await readFile(file, 'utf8')).replaceAll(
      '../../templates/vault/',
      '../templates/vault/',
    ),
  );
}
for (const file of ['lib/client.js', 'cordis.patch.yml', 'LICENSE'])
  await cp(`${source}/${file}`, new URL(file, out));
await cp(`${source}/templates`, new URL('templates/', out), {
  recursive: true,
});
await cp(`${source}/skills`, new URL('skills/', out), { recursive: true });
const original = JSON.parse(await readFile(`${source}/package.json`, 'utf8'));
for (const dependency of Object.keys(original.dependencies ?? {})) {
  await cp(
    `${source}/node_modules/${dependency}/LICENSE`,
    new URL('licenses/' + dependency.replaceAll('/', '-') + '-LICENSE', out),
  );
}
const pkg = {
  name: original.name,
  version: original.version,
  type: 'module',
  license: original.license,
  main: 'lib/index.js',
  exports: {
    '.': './lib/index.js',
    './client': './lib/client.js',
    './dsh-host': './lib/dsh-host.js',
    './package.json': './package.json',
    './cordis.patch.yml': './cordis.patch.yml',
  },
  dsh: original.dsh,
};
await writeFile(
  new URL('package.json', out),
  JSON.stringify(pkg, null, 2) + '\n',
);
execFileSync('python', [
  '-c',
  `import tarfile, pathlib, gzip
p=pathlib.Path('seed-resources/memwiki/package')
with open('seed-resources/dsh-memwiki-0.4.0.tgz','wb') as raw, gzip.GzipFile(filename='',mode='wb',fileobj=raw,mtime=0) as gz, tarfile.open(fileobj=gz,mode='w',format=tarfile.USTAR_FORMAT) as t:
 for f in sorted(p.rglob('*')):
  if f.is_file():
   i=t.gettarinfo(str(f),'package/'+f.relative_to(p).as_posix());i.mode=0o644;i.uid=i.gid=0;i.uname=i.gname='';i.mtime=0
   with f.open('rb') as b:t.addfile(i,b)`,
]);
const pluginBytes = await readFile(new URL('dsh-memwiki-0.4.0.tgz', root));
await validateArchive(pluginBytes, { name: pkg.name, version: pkg.version });
const meta = {
  name: 'clear-task-summary',
  description: 'Summarize a task using supplied facts only',
};
const skill = packSkill(
  {
    'SKILL.md': `---\nname: ${meta.name}\ndescription: "${meta.description}"\n---\n# Clear task summary\n\nUse when the user asks to summarize supplied task notes.\nState the result first. List completed work, evidence and remaining questions.\nUse only facts supplied in the conversation. Label uncertainty explicitly.\nDo not access files, call tools or external services, or include secrets.\n`,
  },
  meta,
);
unpackSkill(skill, meta);
await writeFile(new URL('clear-task-summary-1.0.0.tgz', root), skill);
const digest = (b) => 'sha256:' + createHash('sha256').update(b).digest('hex');
const summary = [];
for (const [kind, name, version, bytes] of [
  ['plugin', pkg.name, pkg.version, pluginBytes],
  ['skill', meta.name, '1.0.0', skill],
]) {
  const m = {
    schemaVersion: 1,
    name,
    version,
    description:
      kind === 'plugin'
        ? 'Shared Markdown memory wiki DSH plugin'
        : meta.description,
    author: { displayName: 'Resource seed maintainers' },
    license: 'MIT',
    permissions: {
      tools: [],
      outboundHosts: [],
      dataAccess: kind === 'plugin' ? ['user_selected_files'] : [],
    },
    runtimes: kind === 'plugin' ? [{ kind: 'node', versionRange: '>=22' }] : [],
    artifacts: [
      {
        id: 'payload',
        role: 'payload',
        digest: digest(bytes),
        sizeBytes: bytes.length,
        mediaType: 'application/gzip',
        os: 'any',
        arch: 'any',
      },
    ],
    signature: { mode: 'detached', algorithm: 'Ed25519' },
    kind,
    ...(kind === 'plugin'
      ? { plugin: { packageName: name, dshVersionRange: '>=0.2.1-rc.3' } }
      : {
          skill: {
            entry: 'SKILL.md',
            contentProfile: 'text-only',
            dshVersionRange: '>=0.2.1-rc.3',
          },
        }),
  };
  assert(
    schemaValid(
      'package',
      kind === 'plugin' ? 'PluginManifest' : 'SkillManifest',
      m,
    ),
  );
  const raw = JSON.stringify(m, null, 2) + '\n';
  await writeFile(new URL(name + '.auriga-resource.json', root), raw);
  summary.push({
    name,
    version,
    kind,
    artifactDigest: digest(bytes),
    sizeBytes: bytes.length,
    manifestDigest: digest(raw),
    signed: false,
  });
}
await writeFile(
  new URL('checksums.json', root),
  JSON.stringify(summary, null, 2) + '\n',
);
console.log(
  JSON.stringify(
    {
      archiveValidation: true,
      manifestSchema: true,
      skillRoundTrip: true,
      resources: summary,
    },
    null,
    2,
  ),
);

await writeFile(
  new URL('build-source.json', root),
  JSON.stringify(
    {
      name: original.name,
      version: original.version,
      esbuildVersion,
      sourceLockDigest: digest(await readFile(`${source}/package-lock.json`)),
      sourceEntries: await Promise.all(
        ['index', 'cli', 'dsh-host'].map(async (name) => ({
          path: `lib/${name}.js`,
          digest: digest(await readFile(`${source}/lib/${name}.js`)),
        })),
      ),
    },
    null,
    2,
  ) + '\n',
);
