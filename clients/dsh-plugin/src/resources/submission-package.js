import { createHash } from 'node:crypto';
import { isIP } from 'node:net';
import { TextDecoder } from 'node:util';
import { validatePrivateSkill } from './private-skills.js';
import { schemaValid } from './schema.js';
import { packSkill, skillPathValid } from './skill-package.js';
import {
  excluded,
  invalid,
  openSubmissionDirectory,
  safePath,
  submissionFiles,
} from './submission-files.js';
import { packPluginDirectory } from './submission-plugin.js';

const decoder = new TextDecoder('utf-8', { fatal: true });
const defaultDshRange = '>=0.2.1-rc.3';

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
    const key = path.normalize('NFC').toLowerCase();
    if (names.has(key)) invalid(path, 'skill path collides with another entry');
    names.add(key);
    if (info.isDirectory()) {
      if (path === 'SKILL.md' || !skillPathValid(path + '/file.txt'))
        invalid(path, 'invalid skill directory or depth');
      for await (const name of source.children(path))
        if (!excluded(name)) await visit(path + '/' + name);
      return;
    }
    if (path === 'assets' || path === 'references')
      invalid(path, 'expected a skill directory');
    if (!/\.(?:md|txt|json|yaml|yml)$/.test(path)) return;
    if (!skillPathValid(path) || ++count > 512)
      invalid(path, 'skill path or entry count exceeds the package limit');
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

  await visit('SKILL.md');
  await visit('references', true);
  await visit('assets', true);
  let skill;
  try {
    skill = validatePrivateSkill(decoder.decode(files['SKILL.md']));
  } catch {
    invalid('SKILL.md', 'invalid skill front matter');
  }
  try {
    return { skill, archive: packSkill(files, skill) };
  } catch {
    invalid(
      '.',
      'skill files exceed USTAR, compressed size or compression ratio limits',
    );
  }
}

/** @param {string} host */
function hostValid(host) {
  return (
    host === host.toLowerCase() &&
    host.length <= 253 &&
    (isIP(host) === 4 ||
      (isIP(host) === 6 && /^[0-9a-f:]+$/.test(host)) ||
      (!/^[0-9.]+$/.test(host) &&
        host
          .split('.')
          .every((part) =>
            /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(part),
          )))
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
 *    name?:string, version?:string, description?:string, author:string, license:string,
 *    permissions:{tools:string[],outboundHosts:string[],dataAccess:string[]},
 *    dshVersionRange?:string, tags?:string[]
 *  }
 * }} input
 */
export async function prepareDirectory({ cwd, directory, kind, metadata }) {
  if (!metadata || (kind !== 'plugin' && kind !== 'skill'))
    invalid('metadata', 'expected plugin or skill submission metadata');

  const source = submissionFiles(await openSubmissionDirectory(cwd, directory));
  let name, version, description, archive, detail, dshVersionRange;
  if (kind === 'plugin') {
    const { pkg, archive: payload } = await packPluginDirectory(source);
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
    detail = {
      plugin: {
        packageName: pkg.name,
        dshVersionRange,
        ...(metadata.tags === undefined ? {} : { tags: metadata.tags }),
      },
    };
    archive = payload;
  } else {
    if (metadata.tags !== undefined)
      invalid('metadata.tags', 'feature tags apply to plugins only');
    const { skill, archive: payload } = await packSkillDirectory(source);
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
    dshVersionRange = metadata.dshVersionRange ?? defaultDshRange;
    detail = {
      skill: {
        entry: 'SKILL.md',
        contentProfile: 'text-only',
        dshVersionRange,
      },
    };
    archive = payload;
  }

  const manifest = {
    schemaVersion: 1,
    kind,
    name,
    version,
    description,
    author: { displayName: metadata.author },
    license: metadata.license,
    permissions: metadata.permissions,
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
    invalid(
      'metadata',
      'invalid catalog name, version, description, author, license or permissions',
    );
  if (!rangeValid(dshVersionRange))
    invalid('metadata', 'invalid dshVersionRange');
  if (!metadata.permissions.outboundHosts.every(hostValid))
    invalid(
      'metadata',
      'outboundHosts must contain canonical DNS names or literal IP addresses',
    );
  manifest.permissions = {
    tools: [...metadata.permissions.tools],
    outboundHosts: [...metadata.permissions.outboundHosts],
    dataAccess: [...metadata.permissions.dataAccess],
  };
  return { manifest, archive };
}
