import { createHash } from 'node:crypto';
import { TextDecoder } from 'node:util';
import { validatePrivateSkill } from './private-skills.js';
import { schemaValid } from './schema.js';
import {
  packSkill,
  skillDirectoryValid,
  skillPathValid,
  skillRootDependencyFiles,
} from './skill-package.js';
import {
  invalid,
  openSubmissionDirectory,
  safePath,
  submissionFiles,
} from './submission-files.js';
import { packPluginDirectory } from './submission-plugin.js';

const decoder = new TextDecoder('utf-8', { fatal: true });
const defaultDshRange = '>=0.2.1-rc.3';

/** Skill-specific ignore filter: ignores VCS/cache dirs and secret files without silently dropping valid script configs like scripts/config.json.
 * @param {string} name */
function skillExcluded(name) {
  return (
    name.startsWith('.') ||
    /^(?:node_modules|__pycache__|venv|coverage|tests?|__tests__|fixtures|secrets?|credentials|private[-_]?keys?|Dockerfile|Makefile)$/i.test(
      name,
    ) ||
    /\.(?:pem|key|p12|pfx|jks|keystore|crt|cer|kdbx|sqlite3?|db|log|bak|tgz|zip)$/i.test(
      name,
    ) ||
    /^(?:env|secrets?|credentials|tokens?|private[-_]?keys?|id_(?:rsa|ed25519|ecdsa))(?:\.|$)/i.test(
      name,
    ) ||
    /^(?:config|settings|application|appsettings)\.(?:local|dev|development|prod|production|test)\.(?:json|toml|ini|ya?ml|properties)$/i.test(
      name,
    )
  );
}

/** @param {ReturnType<typeof submissionFiles>} source */
async function packSkillDirectory(source) {
  /** @type {Record<string, Buffer>} */
  const files = Object.create(null);
  const names = new Set();
  let count = 0;
  let total = 1024;

  /** @param {string} path @param {boolean} [optional] @returns {Promise<void>} */
  async function visit(path, optional = false) {
    if (!safePath(path)) invalid(path, 'unsafe skill path');
    const info = await source.stat(path, optional);
    if (!info) return;
    const key = path
      .normalize('NFC')
      .toUpperCase()
      .toLowerCase()
      .normalize('NFC');
    if (names.has(key)) invalid(path, 'skill path collides with another entry');
    names.add(key);
    if (info.isDirectory()) {
      if (!skillDirectoryValid(path))
        invalid(
          path,
          'unsupported skill directory (only references/, assets/, scripts/ are allowed)',
        );
      for await (const name of source.children(path))
        if (!skillExcluded(name)) await visit(path + '/' + name);
      return;
    }
    if (path === 'assets' || path === 'references' || path === 'scripts')
      invalid(path, 'expected a skill directory');
    if (!skillPathValid(path))
      invalid(
        path,
        'unsupported skill file path or extension (allowed: SKILL.md, root requirements.txt/pyproject.toml/package.json/uv.lock/poetry.lock, references/assets text files, and scripts/ UTF-8 scripts)',
      );
    if (++count > 512)
      invalid(path, 'skill entry count exceeds the package limit (512)');
    const padded = 512 + Math.ceil(Number(info.size) / 512) * 512;
    if ((total += padded) > 20971520)
      invalid(path, 'skill archive size limit exceeded');
    const bytes = await source.read(
      path,
      info,
      path === 'SKILL.md' ? 262144 : 2097152,
    );
    try {
      if (decoder.decode(bytes).includes('\0')) throw new Error();
    } catch {
      invalid(path, 'skill files must be UTF-8 text without NUL bytes');
    }
    files[path] = bytes;
  }

  for await (const name of source.children('')) {
    if (!skillExcluded(name)) await visit(name);
  }
  if (!files['SKILL.md'])
    invalid('SKILL.md', 'missing required SKILL.md at skill root');
  let skill;
  try {
    skill = validatePrivateSkill(decoder.decode(files['SKILL.md']));
  } catch {
    invalid('SKILL.md', 'invalid skill front matter');
  }
  const packedFiles = Object.keys(files);
  const hasScripts = packedFiles.some(
    (p) => p.startsWith('scripts/') || skillRootDependencyFiles.has(p),
  );
  try {
    return {
      skill,
      archive: packSkill(files, skill),
      files: packedFiles,
      hasScripts,
    };
  } catch {
    invalid(
      '.',
      'skill files exceed USTAR, compressed size or compression ratio limits',
    );
  }
}

/** @param {string} host */
function hostValid(host) {
  if (host !== host.toLowerCase() || host.length > 253) return false;
  if (host.includes(':')) {
    if (!/^[0-9a-f:]+$/.test(host)) return false;
    // Brackets restrict the URL parser to an IPv6 literal. Parsing does no I/O.
    try {
      return new URL('http://[' + host + ']').hostname.startsWith('[');
    } catch {
      return false;
    }
  }
  const parts = host.split('.');
  if (/^[0-9.]+$/.test(host))
    return (
      parts.length === 4 &&
      parts.every(
        (part) => /^(?:0|[1-9][0-9]{0,2})$/.test(part) && Number(part) <= 255,
      )
    );
  return parts.every((part) =>
    /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(part),
  );
}

/** @param {string} range */
function rangeValid(range) {
  return (
    range === '*' ||
    range.split('||').every((branch) =>
      branch
        .trim()
        .split(/\s+/)
        .every((term) =>
          schemaValid(
            'package',
            'Version',
            term.replace(/^(?:>=|<=|>|<|=|\^|~)/, ''),
          ),
        ),
    )
  );
}

/**
 * Freeze a local source directory without executing package code or resolving dependencies.
 * @param {{
 *  cwd:string, directory:string, kind:'plugin'|'skill',
 *  metadata:{
 *    name?:string, version?:string, description?:string, author?:string, license?:string,
 *    permissions:{tools:string[],outboundHosts:string[],dataAccess:string[]},
 *    dshVersionRange?:string, tags?:string[], changeNotes?:string
 *  }
 * }} input
 */
export async function prepareDirectory({ cwd, directory, kind, metadata }) {
  if (!metadata || (kind !== 'plugin' && kind !== 'skill'))
    invalid('metadata', 'expected plugin or skill submission metadata');

  const source = submissionFiles(await openSubmissionDirectory(cwd, directory));
  let name,
    version,
    description,
    license,
    archive,
    files,
    hasScripts = false,
    detail,
    dshVersionRange;
  if (kind === 'plugin') {
    const {
      pkg,
      archive: payload,
      files: packedFiles,
    } = await packPluginDirectory(source);
    dshVersionRange =
      metadata.dshVersionRange ?? pkg.engines?.dsh ?? defaultDshRange;
    if (
      (metadata.version !== undefined && metadata.version !== pkg.version) ||
      (pkg.engines?.dsh !== undefined && dshVersionRange !== pkg.engines.dsh)
    )
      invalid(
        'package.json',
        'version and engines.dsh must match submission metadata',
      );
    name = metadata.name ?? pkg.name.split('/').at(-1);
    version = pkg.version;
    description = metadata.description ?? pkg.description;
    license =
      metadata.license ??
      (typeof pkg.license === 'string' && pkg.license.trim()
        ? pkg.license.trim()
        : undefined);
    detail = {
      plugin: {
        packageName: pkg.name,
        dshVersionRange,
        ...(metadata.tags === undefined ? {} : { tags: metadata.tags }),
      },
    };
    archive = payload;
    files = packedFiles;
  } else {
    if (metadata.tags !== undefined)
      invalid('metadata.tags', 'feature tags apply to plugins only');
    const {
      skill,
      archive: payload,
      files: packedFiles,
      hasScripts: skillHasScripts,
    } = await packSkillDirectory(source);
    hasScripts = skillHasScripts;
    if (
      (metadata.name !== undefined && metadata.name !== skill.name) ||
      (metadata.description !== undefined &&
        metadata.description !== skill.description)
    )
      invalid(
        'SKILL.md',
        'name and description must match submission metadata',
      );
    name = skill.name;
    version = metadata.version;
    description = skill.description;
    license = metadata.license;
    dshVersionRange = metadata.dshVersionRange ?? defaultDshRange;
    detail = {
      skill: {
        entry: 'SKILL.md',
        contentProfile: 'text-only',
        dshVersionRange,
      },
    };
    archive = payload;
    files = packedFiles;
  }

  if (
    typeof name !== 'string' ||
    name.length < 1 ||
    name.length > 64 ||
    !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(name)
  )
    invalid(
      'metadata.name',
      `catalog name must match ^[a-z0-9]+(-[a-z0-9]+)*$ without npm @scope (got ${JSON.stringify(name)}); omit metadata.name to derive automatically`,
    );
  if (
    typeof version !== 'string' ||
    !schemaValid('package', 'Version', version)
  )
    invalid(
      'metadata.version',
      `version must be strict SemVer such as 1.0.0 (got ${JSON.stringify(version)})`,
    );
  if (
    typeof description !== 'string' ||
    !description.trim() ||
    description.length > 1024
  )
    invalid(
      'metadata.description',
      'description must be 1..1024 non-empty characters (provide in package.json, SKILL.md or metadata.description)',
    );
  if (typeof license !== 'string' || !license.trim() || license.length > 128)
    invalid(
      'metadata.license',
      'license must be 1..128 characters (e.g. MIT, Apache-2.0, Proprietary)',
    );
  if (
    metadata.changeNotes !== undefined &&
    (typeof metadata.changeNotes !== 'string' ||
      !metadata.changeNotes.trim() ||
      metadata.changeNotes.length > 4096)
  )
    invalid(
      'metadata.changeNotes',
      'changeNotes must be 1..4096 non-empty characters when provided',
    );
  if (!schemaValid('package', 'Permissions', metadata.permissions))
    invalid(
      'metadata.permissions',
      'permissions must contain tools[], outboundHosts[] and dataAccess[]',
    );
  if (!rangeValid(dshVersionRange))
    invalid('metadata.dshVersionRange', 'invalid dshVersionRange');
  if (!metadata.permissions.outboundHosts.every(hostValid))
    invalid(
      'metadata.permissions.outboundHosts',
      'outboundHosts must contain canonical DNS names or literal IP addresses',
    );

  // Author identity is resolved authoritatively by the server from the authenticated member.
  const authorDisplayName =
    typeof metadata.author === 'string' && metadata.author.trim()
      ? metadata.author.trim().slice(0, 128)
      : 'Authenticated Member';

  const manifest = {
    schemaVersion: 1,
    kind,
    name,
    version,
    description: description.trim(),
    author: { displayName: authorDisplayName },
    license: license.trim(),
    permissions: {
      tools: [...metadata.permissions.tools],
      outboundHosts: [...metadata.permissions.outboundHosts],
      dataAccess: [...metadata.permissions.dataAccess],
    },
    runtimes: [],
    artifacts: [
      {
        id: 'payload',
        role: 'payload',
        digest: 'sha256:' + createHash('sha256').update(archive).digest('hex'),
        sizeBytes: archive.length,
        mediaType: 'application/gzip',
        os: 'any',
        arch: 'any',
      },
    ],
    signature: { mode: 'detached', algorithm: 'Ed25519' },
    ...detail,
  };
  if (!schemaValid('package', 'Manifest', manifest))
    invalid('metadata', 'invalid catalog manifest fields after normalization');
  return {
    manifest,
    archive,
    files,
    hasScripts,
    ...(metadata.changeNotes
      ? { changeNotes: metadata.changeNotes.trim() }
      : {}),
  };
}
