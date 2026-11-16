import { execFileSync } from 'node:child_process';
import { readFileSync, writeFileSync } from 'node:fs';
import { createHash } from 'node:crypto';

// Static discovery deliberately does not claim a resolved Maven/plugin closure.
const files = execFileSync('git', ['ls-files', '-z'], { encoding: 'utf8' })
  .split('\0')
  .filter(Boolean);
const entries = [];
const inputs = [];
const add = (file, kind, name, version, digest = null, source = null) =>
  entries.push({
    file,
    kind,
    name,
    version: version ?? null,
    digest,
    source,
    forgejo: { status: 'missing-or-unverified', location: null },
  });
const properties = Object.fromEntries(
  [
    ...readFileSync('pom.xml', 'utf8').matchAll(/<([\w.-]+)>([^<>]+)<\/\1>/g),
  ].map((m) => [m[1], m[2]]),
);
const expand = (value) =>
  value?.replace(/\$\{([^}]+)\}/g, (all, name) => properties[name] ?? all);
for (const file of files) {
  if (file.startsWith('deploy/forgejo/')) continue;
  if (
    !/(Dockerfile[^/]*$|\.(?:sh|ps1|mjs|yml|yaml|mod|sum|xml|properties)$|(?:package(?:-lock)?|versions)\.json$|go\.work$|requirements\.txt$)/.test(
      file,
    )
  )
    continue;
  const text = readFileSync(file, 'utf8');
  let relevant = false;
  const record = (...args) => {
    relevant = true;
    add(file, ...args);
  };
  if (/package-lock\.json$/.test(file)) {
    const lock = JSON.parse(text);
    for (const [key, p] of Object.entries(lock.packages ?? {})) {
      if (key && !p.link)
        record(
          'npm',
          p.name ?? key.split('node_modules/').at(-1),
          p.version,
          p.integrity ?? null,
          p.resolved ?? null,
        );
    }
  } else if (file.endsWith('pnpm-lock.yaml')) {
    const section =
      text.split(/^packages:\s*$/m)[1]?.split(/^snapshots:\s*$/m)[0] ?? '';
    for (const match of section.matchAll(
      /^  (\S[^\n]*):\r?\n([\s\S]*?)(?=^  \S|$(?![\s\S]))/gm,
    )) {
      const id = match[1].replace(/^'|'$/g, '');
      const split = id.lastIndexOf('@');
      record(
        'pnpm',
        id.slice(0, split),
        id.slice(split + 1),
        match[2].match(/integrity: ([^\s,}]+)/)?.[1] ?? null,
        'https://registry.npmjs.org',
      );
    }
  } else if (file.endsWith('go.sum')) {
    for (const line of text.split(/\r?\n/)) {
      const [name, version, digest] = line.split(' ');
      if (digest)
        record('go-sum', name, version, digest, 'https://proxy.golang.org');
    }
  } else if (/go\.(mod|work)$/.test(file)) {
    for (const m of text.matchAll(/^(?:go|toolchain)\s+(\S+)/gm))
      record('go-toolchain', 'go', m[1]);
    for (const m of text.matchAll(/^\s*(?:require\s+)?(\S+)\s+(v\S+)/gm))
      record('go-module', m[1], m[2], null, 'https://proxy.golang.org');
    for (const m of text.matchAll(/^replace\s+(.+)$/gm))
      record('go-replace', m[1], null);
  } else if (file.endsWith('pom.xml')) {
    for (const m of text.matchAll(
      /<(dependency|plugin|extension)>([\s\S]*?)<\/\1>/g,
    )) {
      const field = (name) =>
        m[2].match(new RegExp('<' + name + '>([^<]+)</' + name + '>'))?.[1];
      record(
        'maven-' + m[1],
        (field('groupId') ?? 'inherited') + ':' + field('artifactId'),
        expand(field('version')),
        null,
        'https://repo.maven.apache.org/maven2',
      );
    }
    for (const m of text.matchAll(
      /<(nodeVersion|protocArtifact|pluginArtifact)>([^<]+)</g,
    ))
      record('maven-tool', m[1], expand(m[2]));
  }
  for (const [i, line] of text.split(/\r?\n/).entries()) {
    const from =
      /(?:^|\/)Dockerfile(?:\.[^.]+)?$/.test(file) &&
      line.match(/^FROM\s+(?:--platform=\S+\s+)?(\S+)/i);
    if (from && from[1] !== 'scratch')
      record(
        'container-from',
        from[1],
        from[1].split('@')[0],
        from[1].match(/sha256:[a-f0-9]{64}/)?.[0] ?? null,
      );
    const image = line.match(/^\s*image:\s*(.+)/);
    if (image)
      record(
        'compose-image',
        image[1],
        null,
        image[1].match(/sha256:[a-f0-9]{64}/)?.[0] ?? null,
      );
    if (file.endsWith('requirements.txt')) {
      const requirement = line.match(/^([\w.-]+)==([^\s]+)/);
      if (requirement)
        record(
          'python',
          requirement[1],
          requirement[2],
          null,
          'https://pypi.org',
        );
    }
    if (
      file.endsWith('maven-wrapper.properties') &&
      /^(distributionUrl|wrapperUrl)=/.test(line)
    ) {
      const [name, url] = line.split('=');
      const digest = text
        .match(
          new RegExp(
            '^' +
              (name === 'wrapperUrl' ? 'wrapper' : 'distribution') +
              'Sha256Sum=(.+)$',
            'm',
          ),
        )?.[1]
        ?.trim();
      record('maven-wrapper', name, url.split('/').at(-2), digest ?? null, url);
    }
    if (
      /(curl|wget|Invoke-WebRequest|https?:\/\/|uses:|apt-get install|apk add|pip install|go install|npm install|corepack prepare)/.test(
        line,
      ) &&
      (/^(scripts|deploy|\.github|\.mvn|infra)\//.test(file) ||
        file.endsWith('maven-wrapper.properties'))
    ) {
      record('download-reference', 'line:' + (i + 1), null, null, line.trim());
    }
  }
  if (file.endsWith('versions.json')) {
    const p = JSON.parse(text);
    for (const name of [
      'nodeImage',
      'dshVersion',
      'nodeVersion',
      'npmVersion',
    ]) {
      if (p[name])
        record(
          'dsh-tool',
          name,
          p[name],
          p[name].match(/sha256:[a-f0-9]{64}/)?.[0] ?? null,
        );
    }
  }
  if (file.endsWith('package.json')) {
    const p = JSON.parse(text);
    if (p.packageManager)
      record(
        'package-manager',
        p.packageManager,
        p.packageManager.split('@').at(-1),
      );
  }
  if (relevant)
    inputs.push({
      file,
      sha256: createHash('sha256').update(text).digest('hex'),
    });
}
const artifacts = JSON.parse(
  readFileSync('deploy/forgejo/artifacts.json', 'utf8'),
).artifacts;
for (const entry of entries) {
  let prefix;
  if (entry.kind === 'go-sum' || entry.kind === 'go-module')
    prefix = 'auriga-go-inputs-';
  if (entry.kind === 'pnpm' || entry.kind === 'package-manager')
    prefix = 'auriga-build-web-inputs-';
  if (entry.kind === 'npm' && entry.file.startsWith('services/contracts/'))
    prefix = 'auriga-build-java-contracts-npm-inputs-';
  if (
    entry.kind === 'npm' &&
    /(?:dsh-runtime|real-dsh|installer\/locks)/.test(entry.file)
  )
    prefix = 'dsh-npm-cache-';
  if (entry.kind === 'maven-wrapper') prefix = 'auriga-build-java-tools-';
  if (entry.kind === 'python') prefix = 'auriga-build-operator-wheels-';
  if (prefix) {
    entry.forgejo.candidateLocations = artifacts
      .filter((a) => a.file.startsWith(prefix) && a.file.endsWith('.part000'))
      .map((a) => a.path);
  }
}
const result = {
  schemaVersion: 1,
  sourceCommit: execFileSync('git', ['rev-parse', 'HEAD'], {
    encoding: 'utf8',
  }).trim(),
  forgejoArtifactIndex: 'artifacts.json',
  completeness: 'static-discovery-only',
  limitations: [
    'Static discovery records Maven declarations; resolve inherited versions and transitive inputs against a verified reactor closure',
    'download-reference includes conditional and test-only downloads; dynamic URL/version/digest require resolution',
    'go.sum includes historical and platform-specific modules; entries are not a minimal Linux build closure',
    'Forgejo archive availability is recorded separately; per-package membership is not inferred from archive names',
  ],
  inputs,
  entries,
};
// Keep individual records on one line so large lock inventories remain reviewable.
const header = JSON.stringify({ ...result, entries: undefined }, null, 2).slice(
  0,
  -2,
);
writeFileSync(
  process.argv[2] ?? 'deploy/forgejo/forgejo-deps-inventory.json',
  header +
    ',\n  "entries": [\n' +
    entries.map((entry) => '    ' + JSON.stringify(entry)).join(',\n') +
    '\n  ]\n}\n',
);
console.log(
  JSON.stringify({
    entries: entries.length,
    inputs: inputs.length,
    kinds: Object.fromEntries(
      [...new Set(entries.map((e) => e.kind))].map((k) => [
        k,
        entries.filter((e) => e.kind === k).length,
      ]),
    ),
  }),
);
