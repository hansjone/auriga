import { open, constants } from 'node:fs/promises';
import { isAbsolute } from 'node:path';
import { parseStrictJson } from '../network/trust/jcs.js';
import { createManagedModels } from '../managed/models.js';

/** Read only the instance's platform-supplied identity material. Its model list
 * is a bootstrap catalog, never the server's current authorization allowlist.
 * @param {{tenantId:string,ownerId:string,instanceId:string,file?:string}} binding */
export async function loadManagedModelMaterial(binding) {
  const file = binding.file ?? '/run/auriga/model-credential.json';
  if (!isAbsolute(file)) throw new Error('private model file required');
  const handle = await open(
    file,
    constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0),
  );
  try {
    const stat = await handle.stat();
    if (
      !stat.isFile() ||
      stat.size > 1048576 ||
      (process.platform !== 'win32' && (stat.mode & 0o077) !== 0)
    )
      throw new Error('private model file required');
    const value =
      /** @type {import('../generated/managed-model.js').ManagedModelCredential} */ (
        parseStrictJson(Buffer.from(await handle.readFile('utf8')))
      );
    const credentialPrefix = `amc.${binding.tenantId}.${binding.ownerId}.${binding.instanceId}.`;
    if (
      value.tenantId !== binding.tenantId ||
      value.ownerId !== binding.ownerId ||
      value.instanceId !== binding.instanceId ||
      !Number.isSafeInteger(value.credentialVersion) ||
      value.credentialVersion < 1 ||
      typeof value.credential !== 'string' ||
      !value.credential.startsWith(credentialPrefix) ||
      !/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}\.[A-Za-z0-9_-]{43}$/.test(
        value.credential.slice(credentialPrefix.length),
      ) ||
      !Array.isArray(value.models) ||
      !value.models.length ||
      value.models.length > 256 ||
      value.models.some(
        (m) =>
          typeof m.id !== 'string' ||
          !m.id.trim() ||
          m.id.length > 200 ||
          !Number.isSafeInteger(m.contextWindow) ||
          m.contextWindow < 2 ||
          m.contextWindow > 1048576 ||
          !Number.isSafeInteger(m.maxOutputTokens) ||
          m.maxOutputTokens < 1 ||
          m.maxOutputTokens > 32768 ||
          m.maxOutputTokens >= m.contextWindow,
      )
    )
      throw new Error('invalid model binding');
    const base = new URL(value.baseUrl);
    if (
      !['http:', 'https:'].includes(base.protocol) ||
      base.username ||
      base.password ||
      base.pathname !== '/ai/v1' ||
      base.search ||
      base.hash ||
      base.href !== value.baseUrl
    )
      throw new Error('invalid model origin');
    return { value, origin: base.origin };
  } finally {
    await handle.close();
  }
}

/** @param {{tenantId:string,ownerId:string,instanceId:string,file?:string}} binding */
export function createManagedModelStatus(binding) {
  return createManagedModels({
    loadMaterial: () => loadManagedModelMaterial(binding),
  });
}
