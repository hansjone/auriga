// @ts-nocheck
import { canonical } from '../shared/protocol.js';
import { readOwnedFile } from './intent.js';
import { accessSync, constants } from 'node:fs';

export const DEDICATED_PROFILE = Object.freeze({
  pluginInstall: true,
  privateSkills: true,
  includeDefaultRoots: true,
  toolPresentation: 'native',
});
export const DEDICATED_PROFILE_PATH = '/_auriga/dedicated-profile';
export const DEDICATED_RESOURCE_PATH = '/_auriga/dedicated-resources';
export const DEDICATED_PROFILE_DIR =
  '/home/dsh/.host/.dsh/profiles/auriga-dedicated';

/** Attest the actual public services/configuration of this dedicated process.
 * A selected resource file or an always-true injected callback is not proof. */
export function assertDedicatedProfile(ctx, expected, resources) {
  if (
    canonical(expected) !== canonical(DEDICATED_PROFILE) ||
    !resources?.isCurrent()
  )
    return false;
  if (!ctx.registry?.entries || !ctx.reflect?.get) return false;
  const manager = ctx.reflect.get('pluginManager', true);
  const skills = ctx.reflect.get('skills', true);
  const env = ctx.reflect.get('shellEnv', true);
  const profile = ctx.reflect.get('profileContext', true);
  if (
    !manager ||
    ![
      'installBundle',
      'setBundleEnabled',
      'removeBundle',
      'listBundles',
      'listPlugins',
    ].every((key) => typeof manager[key] === 'function') ||
    !skills ||
    !['registerProvider', 'get', 'snapshot'].every(
      (key) => typeof skills[key] === 'function',
    ) ||
    !env ||
    typeof env.collect !== 'function' ||
    typeof env.register !== 'function' ||
    profile?.name !== 'auriga-dedicated' ||
    profile.dir !== DEDICATED_PROFILE_DIR ||
    profile.installAnchor !== '/opt/dsh/package.json'
  )
    return false;
  try {
    accessSync(profile.dir, constants.W_OK);
    accessSync(profile.dir + '/package.json', constants.W_OK);
    readOwnedFile(profile.dir + '/package.json', 1048576);
  } catch {
    return false;
  }
  let tools = false,
    localSkills = false;
  for (const [, runtime] of ctx.registry.entries()) {
    const fibers = [...runtime.fibers].filter((fiber) => fiber.uid !== null);
    if (!fibers.length) continue;
    const name = runtime.name?.replace(/^@deepseek-ai\/dsh-/, '');
    if (
      [
        'auriga-dsh',
        'auriga-dsh-shared',
        'auriga-dsh-shared-ports',
        'auriga-dsh-shared-browser',
      ].includes(name)
    )
      return false;
    if (name === 'tools' || name === 'ToolRuntime') {
      if (
        fibers.length !== 1 ||
        fibers[0].state !== 2 ||
        fibers[0].config?.mode !== 'native'
      )
        return false;
      tools = true;
    }
    if (name === 'skill-filesystem') {
      if (fibers.length !== 1 || fibers[0].state !== 2) return false;
      const config = fibers[0].config;
      if (
        config?.includeDefaultRoots !== true ||
        config.providerName !== 'auriga-dedicated-local' ||
        config.bundledSkillDir !== undefined ||
        !Array.isArray(config.customSkillDirs) ||
        config.customSkillDirs.length !== 0
      )
        return false;
      localSkills = true;
    }
  }
  return tools && localSkills;
}

export function createDedicatedProfileHandler({
  ctx,
  resources,
  physicalInstanceId,
}) {
  return async (req, res) => {
    if (
      req.method !== 'GET' ||
      ![DEDICATED_PROFILE_PATH, DEDICATED_RESOURCE_PATH].includes(req.url)
    ) {
      res.writeHead(404, { 'cache-control': 'no-store' });
      res.end();
      return;
    }
    let valid = false;
    try {
      await resources.verify();
      valid = assertDedicatedProfile(ctx, DEDICATED_PROFILE, resources);
    } catch {
      /* failed closed */
    }
    res.writeHead(valid ? 200 : 503, {
      'content-type': 'application/json',
      'cache-control': 'no-store',
    });
    res.end(
      canonical(
        req.url === DEDICATED_RESOURCE_PATH
          ? resources.receipt()
          : valid
            ? {
                schemaVersion: 1,
                physicalInstanceId,
                profile: 'auriga.dedicated.v1',
                capabilities: DEDICATED_PROFILE,
              }
            : { error: 'common.unavailable' },
      ),
    );
  };
}
