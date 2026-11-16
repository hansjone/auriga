import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

export function mapInventory(inventory, closure) {
  const index = new Map();
  for (const item of closure.entries) {
    if (
      !/^[a-f0-9]{64}$/.test(item.sha256) ||
      !item.archive ||
      !item.member ||
      !item.version
    ) {
      throw Error(
        'Closure entry requires an archive, member, exact version and SHA-256',
      );
    }
    const key = `${item.kind}:${item.name}@${item.version}`;
    const items = index.get(key) ?? [];
    items.push(item);
    index.set(key, items);
  }
  for (const group of Object.values(closure.inputGroups ?? {})) {
    if (
      !group.closureMembers?.length ||
      group.closureMembers.some(
        (member) =>
          !(
            index.get(`${member.kind}:${member.name}@${member.version}`) ?? []
          ).some(
            (item) =>
              item.archive === member.archive && item.member === member.member,
          ),
      )
    ) {
      throw Error('Input group member is absent from the verified closure');
    }
  }
  const entries = inventory.entries.map((entry, position) => {
    let kind = entry.kind;
    let name = entry.name;
    let version = entry.version;
    if (kind === 'go-toolchain') {
      const required = version.replace(/^go/, '').split('.').map(Number);
      const toolchain = closure.entries.find((item) => {
        if (item.kind !== kind || item.name !== name) return false;
        const available = item.version.split('.').map(Number);
        const differing = available.findIndex(
          (part, i) => part !== (required[i] ?? 0),
        );
        return (
          differing === -1 || available[differing] > (required[differing] ?? 0)
        );
      });
      if (toolchain) version = toolchain.version;
    }
    if (kind === 'go-sum') {
      kind = version.endsWith('/go.mod') ? 'go-mod' : 'go-module';
      version = version.replace(/\/go.mod$/, '');
    }
    if (kind === 'pnpm') kind = 'npm';
    if (kind === 'dsh-tool' && name === 'dshVersion') {
      kind = 'npm';
      name = '@deepseek-ai/dsh';
    }
    if (kind === 'maven-tool' && version) {
      if (name === 'nodeVersion') {
        kind = 'maven';
        name = 'com.github.eirslett:node';
        version = version.replace(/^v/, '');
      } else {
        const coordinate = version.split(':');
        kind = 'maven';
        name = coordinate.slice(0, 2).join(':');
        version = coordinate[2];
      }
    }
    if (
      ['maven-plugin', 'maven-extension', 'maven-dependency'].includes(kind)
    ) {
      const resolved = closure.resolvedMavenVersions?.find(
        (item) =>
          item.file === entry.file &&
          item.kind === kind &&
          item.declaredName === name,
      );
      if (resolved) {
        name = resolved.name;
        version = resolved.version;
      }
      kind = 'maven';
    }
    if (kind === 'maven' && (!version || name.startsWith('inherited:'))) {
      const artifactId = name.split(':').at(-1);
      const candidates = closure.entries.filter(
        (item) =>
          item.kind === kind &&
          (name.startsWith('inherited:')
            ? item.name.endsWith(':' + artifactId)
            : item.name === name) &&
          (!version || item.version === version),
      );
      const coordinates = [
        ...new Set(candidates.map((item) => item.name + '@' + item.version)),
      ];
      if (coordinates.length === 1) {
        name = candidates[0].name;
        version = candidates[0].version;
      }
    }
    const matches = (index.get(`${kind}:${name}@${version}`) ?? []).filter(
      (item) =>
        !/^(sha512-|h1:)/.test(entry.digest ?? '') ||
        item.integrity === entry.digest,
    );
    let forgejo = matches.length
      ? {
          status: 'present',
          evidence: matches.map((item) => ({
            archive: item.archive,
            member: item.member,
            sha256: item.sha256,
            version: item.version,
            packagePath: item.packagePath,
            integrity: item.integrity,
            evidenceType: item.evidenceType,
            imageIdentity: item.imageIdentity,
          })),
        }
      : {
          status: 'missing',
          reason: version
            ? 'No exact member in the verified closure index'
            : 'Exact version remains unresolved',
        };
    if (entry.kind === 'go-replace' && /=>\s+\.\.?\//.test(entry.name)) {
      Object.assign(forgejo, {
        status: 'source',
        reason: 'Local replacement is supplied by the pinned source repository',
      });
    }
    if (entry.kind === 'maven-dependency' && entry.name.startsWith('auriga:')) {
      Object.assign(forgejo, {
        status: 'source',
        reason:
          'Auriga reactor module is built from the pinned source repository',
      });
    }
    if (
      ['go-module', 'go-sum'].includes(entry.kind) &&
      closure.sourceModules?.includes(entry.name)
    ) {
      Object.assign(forgejo, {
        status: 'source',
        reason: 'The pinned workspace supplies this local Go module',
        sourceCommit: closure.sourceCommit,
      });
    }
    if (
      entry.kind === 'maven-wrapper' &&
      entry.name === 'wrapperUrl' &&
      entry.digest === closure.wrapperJar?.sha256
    ) {
      Object.assign(forgejo, {
        status: 'source',
        reason:
          'The checked Maven Wrapper JAR is committed in the source repository',
        member: '.mvn/wrapper/maven-wrapper.jar',
        sha256: entry.digest,
        sourceCommit: closure.sourceCommit,
      });
    }
    if (['compose-image', 'container-from'].includes(entry.kind)) {
      let reference = entry.name.replace(/^['"]|['"]$/g, '');
      const fallback = reference.match(/^\$\{[^:]+:-(.+)\}$/)?.[1];
      if (fallback && !fallback.includes('$')) reference = fallback;
      const variables = {
        AURIGA_IMAGE_POSTGRES: 'postgres:17',
        AURIGA_IMAGE_NATS: 'nats:2.10-alpine',
        AURIGA_IMAGE_REGISTRY: 'registry:2',
      };
      const variable = reference.match(/^\$\{([^}]+)\}$/)?.[1];
      if (variables[variable]) reference = variables[variable];
      if (closure.buildArguments?.[variable])
        reference = closure.buildArguments[variable];
      if (
        closure.targetPlatform === 'linux/amd64' &&
        reference ===
          "${{ matrix.os == 'ubuntu-latest' && 'postgres:17' || '' }}"
      )
        reference = 'postgres:17';
      const image = closure.images?.find(
        (item) =>
          item.reference === reference || item.aliases?.includes(reference),
      );
      if (image) {
        name = image.reference;
        version = image.version ?? image.reference;
        Object.assign(forgejo, {
          status: 'present',
          reason: 'Pinned image archive',
          evidence: [image],
        });
      }
      const role = variable
        ?.match(/^AURIGA_IMAGE_(PLATFORM|EDGE|AGENT|BIFROST|OPERATOR)$/)?.[1]
        .toLowerCase();
      const built =
        role && closure.builtImages?.find((item) => item.role === role);
      if (built)
        Object.assign(forgejo, {
          status: 'source',
          reason: 'Application image built from the pinned repository',
          ...built,
        });
      const sourceRole =
        reference === '${COMPOSE_PROJECT_NAME:-auriga-m2}-edge:latest'
          ? 'edge'
          : reference === '${AURIGA_INSTALLER_IMAGE:-${AURIGA_IMAGE_PLATFORM}}'
            ? 'platform'
            : null;
      const sourceImage = closure.builtImages?.find(
        (item) => item.role === sourceRole,
      );
      if (sourceImage)
        Object.assign(forgejo, {
          status: 'source',
          reason:
            'The default resolves to an application image built from this repository',
          ...sourceImage,
        });
      if (reference === '${BASE_RUNTIME_IMAGE}')
        Object.assign(forgejo, {
          status: 'source',
          reason:
            'Incremental DSH builds require the signed result of a prior full DSH build; the full build inputs are indexed separately',
          sourceCommit: closure.sourceCommit,
          member: 'deploy/compose/real-dsh/Dockerfile',
        });
    }
    if (entry.kind === 'compose-image' && !/\.ya?ml$/.test(entry.file)) {
      Object.assign(forgejo, {
        status: 'non-input-reference',
        reason:
          'The discovery matched an image property in source code rather than a Compose image declaration',
      });
    }
    if (entry.kind === 'compose-image' && entry.name.startsWith('{ type:')) {
      Object.assign(forgejo, {
        status: 'non-input-reference',
        reason: 'The discovery matched a schema field declaration',
      });
    }
    if (entry.kind === 'download-reference') {
      const line = entry.source ?? '';
      const urls = [...line.matchAll(/https?:\/\/[^\s"'`<>]+/g)].map(
        (match) => match[0],
      );
      const localOrExample =
        urls.length > 0 &&
        urls.every((url) => {
          try {
            const host = new URL(url).hostname;
            return (
              host === 'localhost' ||
              host === '127.0.0.1' ||
              host === '0.0.0.0' ||
              host === '[::1]' ||
              /(^|\.)example\.(com|net|org)$/.test(host) ||
              host.endsWith('.invalid')
            );
          } catch {
            return false;
          }
        });
      if (/^(#|\/\/)/.test(line) || localOrExample) {
        Object.assign(forgejo, {
          status: 'non-input-reference',
          reason: localOrExample
            ? 'The URL is a loopback endpoint or a documentation/test placeholder'
            : 'The reference occurs in a comment',
        });
      }
      if (
        entry.file.endsWith('package-lock.json') &&
        /^"url":\s*"/.test(line)
      ) {
        Object.assign(forgejo, {
          status: 'non-input-reference',
          reason:
            'npm lock funding/repository metadata; package downloads use the resolved property',
        });
      }
    }
    const resolution = closure.referenceResolutions?.find(
      (item) =>
        item.file === entry.file &&
        item.source === entry.source &&
        item.kind === entry.kind &&
        item.name === entry.name,
    );
    if (resolution) {
      const group = resolution.forgejo.inputGroup;
      if (group && !closure.inputGroups?.[group]?.closureMembers?.length)
        throw Error('Missing input group evidence');
      forgejo = { ...resolution.forgejo };
    }
    if (forgejo.status === 'missing') {
      const excluded = closure.platformExclusions?.find(
        (item) =>
          item.kind === kind && item.name === name && item.version === version,
      );
      if (excluded)
        Object.assign(forgejo, {
          status: 'platform-excluded',
          reason: 'Lockfile excludes this package from Linux x64 glibc',
          constraints: excluded,
        });
    }
    return {
      ...entry,
      discoveryPosition: position,
      resolvedName: name,
      resolvedVersion: version,
      forgejo,
    };
  });
  for (const entry of entries.filter(
    (item) => item.kind === 'download-reference',
  )) {
    const url = entry.source?.match(/^"resolved": "([^"]+)",?$/)?.[1];
    if (!url) continue;
    const dependency = entries.find(
      (item) =>
        item.kind === 'npm' && item.file === entry.file && item.source === url,
    );
    if (dependency)
      entry.forgejo = {
        ...dependency.forgejo,
        duplicateOfDiscoveryPosition: dependency.discoveryPosition,
      };
  }
  const supplemental = new Set(closure.supplementalArchives ?? []);
  const present = entries.filter((entry) => entry.forgejo.status === 'present');
  for (const entry of present) {
    const evidence =
      entry.forgejo.evidence ??
      closure.inputGroups?.[entry.forgejo.inputGroup]?.evidence ??
      [];
    entry.forgejo.supply = evidence.some((item) =>
      supplemental.has(item.archive),
    )
      ? 'includes-supplement'
      : 'existing-only';
  }
  const counts = Object.fromEntries(
    [
      'present',
      'source',
      'platform-excluded',
      'non-input-reference',
      'missing',
    ].map((status) => [
      status,
      entries.filter((e) => e.forgejo.status === status).length,
    ]),
  );
  return {
    ...inventory,
    completeness: counts.missing
      ? 'mapped-discovery-with-open-items'
      : 'mapped-linux-amd64-discovery',
    closureIndex: 'closure-index.json',
    acceptanceSourceCommit: closure.sourceCommit,
    supplyCounts: {
      existingOnly: present.filter(
        (entry) => entry.forgejo.supply === 'existing-only',
      ).length,
      includesSupplement: present.filter(
        (entry) => entry.forgejo.supply === 'includes-supplement',
      ).length,
      missing: counts.missing,
    },
    limitations: [
      'The original discoveries are retained, including historical locks, metadata, conditional inputs and test fixtures',
      'Platform exclusions apply to Linux amd64 glibc; other platform closures require separate verification',
      'Mirrored GitHub action sources do not supply a hosted CI runner or the GitHub service APIs',
      'Cold-build and stack evidence is pinned to acceptanceSourceCommit; later source and lock changes require revalidation',
    ],
    counts,
    entries,
  };
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(resolve(process.argv[1])).href
) {
  const [inventoryFile, closureFile, output] = process.argv.slice(2);
  if (!inventoryFile || !closureFile || !output)
    throw Error('Usage: node map-inventory.mjs INVENTORY CLOSURE OUTPUT');
  const mapped = mapInventory(
    JSON.parse(readFileSync(inventoryFile)),
    JSON.parse(readFileSync(closureFile)),
  );
  const header = JSON.stringify(
    { ...mapped, entries: undefined },
    null,
    2,
  ).slice(0, -2);
  writeFileSync(
    output,
    header +
      ',\n  "entries": [\n' +
      mapped.entries.map((e) => '    ' + JSON.stringify(e)).join(',\n') +
      '\n  ]\n}\n',
  );
  console.log(JSON.stringify(mapped.counts));
}
