import { randomUUID } from 'node:crypto';
import { isAbsolute } from 'node:path';
import { registrationSession } from '../network/host/native/host-port.js';
import { catalogIdentity } from './catalog-api.js';
import { diagnoseResource } from './diagnostics.js';
import { exact, refuse, uuid } from './trust.js';
import documents from './schema-data.js';
import { schemaValid } from './schema.js';

const maxArchiveBytes = 64 * 1024 * 1024;
const handleTtl = 15 * 60 * 1000;
const maxHandles = 8;
const maxReviewReason = 1024;
const str = { type: 'string', minLength: 1 };
const tags = documents.package.$defs.Plugin.properties.tags;
const permissionsSchema = documents.package.$defs.Permissions;
const metadataFields = {
  name: str,
  version: str,
  description: str,
  author: str,
  license: str,
  dshVersionRange: str,
};
const validationFailed = () => refuse('common.validation_failed');
const unauthorized = () => refuse('common.unauthenticated');

/** @param {unknown} value @param {number} [max] */
function text(value, max = 4096) {
  return (
    typeof value === 'string' &&
    !!value.trim() &&
    value.length <= max &&
    !value.includes('\0')
  );
}

const hints = {
  'common.validation_failed': '检查目录、参数和清单后重新预检。',
  'common.unauthenticated': '请用户在桌面 Auriga 重新登录，再预检。',
  'common.forbidden':
    '请检查租户成员身份及目标资源归属，并在桌面主会话中操作。',
  'common.not_found': '检查目标资源 ID，或在当前会话重新预检取得有效句柄。',
  'common.conflict': '检查目标资源和版本。若更改提交参数，重新预检。',
  'common.precondition_failed':
    '目标资源有并发更新，确认最新状态后用原参数重试。',
  'common.rate_limited': '等待当前工作结束或旧句柄到期后重试。',
  'common.unavailable': '待连接恢复后重试。重试送审时使用原句柄和参数。',
  'resource.digest_mismatch': '检查源文件并重新预检，随后提交新的句柄。',
  'resource.storage_unavailable': '等待当前账号资源状态恢复，随后重试。',
};
const uploadRules = {
  bundled_dependency_required: '将声明的运行时依赖纳入归档，再重新预检。',
  lifecycle_scripts_disallowed: '移除归档中的包生命周期脚本，再重新预检。',
  bundle_patch_missing: '补入 package.json 声明的 DSH patch 文件，再重新预检。',
  native_build_disallowed: '移除包的原生构建文件和声明，再重新预检。',
};

/** @param {unknown} value @param {number} size */
function diagnosticText(value, size) {
  return typeof value === 'string'
    ? value
        .replace(/[\p{C}\r\n]/gu, ' ')
        .trim()
        .slice(0, size)
    : '';
}

/** The SDK displays Error.message. Use local preflight context and fixed platform replies.
 * @param {any} error @param {'prepare'|'submit'} action */
function toolFailure(error, action) {
  const { code } = diagnoseResource(error, 'catalog').body;
  const stage =
    action === 'submit' &&
    ['package', 'upload', 'submit'].includes(error?.stage)
      ? error.stage
      : action;
  let detail = '';
  if (action === 'prepare' && code === 'common.validation_failed') {
    const path = diagnosticText(error.path, 240);
    const reason = diagnosticText(error.reason, 240);
    if (path && !/^[/\\]|^[a-z]:/i.test(path) && reason)
      detail = path + ': ' + reason + ' ';
  }
  if (stage === 'upload' && Object.hasOwn(uploadRules, error?.rule))
    detail =
      error.rule +
      ' ' +
      uploadRules[/** @type {keyof typeof uploadRules} */ (error.rule)];
  const hint =
    detail && stage === 'upload'
      ? ''
      : (hints[/** @type {keyof typeof hints} */ (code)] ??
        hints['common.unavailable']);
  return Object.assign(new Error(`${code} [${stage}] ${detail}${hint}`), {
    code,
  });
}

/** @param {any} args */
function checkPrepare(args) {
  if (
    !exact(args, ['kind', 'directory', 'metadata']) ||
    !['plugin', 'skill'].includes(args.kind) ||
    !text(args.directory) ||
    !args.metadata ||
    typeof args.metadata !== 'object' ||
    Array.isArray(args.metadata)
  )
    validationFailed();
  const { permissions, tags: pluginTags, ...fields } = args.metadata;
  if (
    Object.entries(fields).some(
      ([key, value]) => !Object.hasOwn(metadataFields, key) || !text(value),
    ) ||
    !text(fields.author) ||
    !text(fields.license) ||
    (args.kind === 'skill' && !text(fields.version)) ||
    (pluginTags !== undefined &&
      (args.kind !== 'plugin' ||
        !Array.isArray(pluginTags) ||
        pluginTags.length > 5 ||
        new Set(pluginTags).size !== pluginTags.length ||
        pluginTags.some(
          (tag) =>
            typeof tag !== 'string' ||
            tag.length > 32 ||
            !/^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/.test(tag),
        ))) ||
    !schemaValid('package', 'Permissions', permissions)
  )
    validationFailed();
}

/** @typedef {{manifest:any,archive:Buffer}} Prepared */
/** @typedef {{cwd:string,directory:string,kind:'plugin'|'skill',metadata:any}} PrepareInput */
/** @typedef {{packageId:string,versionId:string,state:'pending_review'}} Receipt */
/** @typedef {{reason:string,packageId?:string,key:string}} Operation */
/** @typedef {{prepared:Prepared, session:any, cwd:string, expires:number, operation?:Operation, pending?:Promise<Receipt>, receipt?:Receipt}} Snapshot */

/** Register with the desktop host. Scope changes retire every handle created under the previous login.
 * @param {any} ctx
 * @param {{current:()=>any,service:(runtime:any)=>any,prepare:(input:PrepareInput)=>Promise<Prepared>,now?:()=>number}} options */
export function installSubmissionTools(
  ctx,
  { current, service, prepare, now = Date.now },
) {
  ctx.inject(
    [
      'tools',
      'sessions',
      'sessionController',
      'agents',
      'sessionPersistence',
      'workspaceRegistry',
    ],
    (/** @type {any} */ host) => {
      /** @type {Map<string, Snapshot>} */
      const handles = new Map();
      /** @type {{runtime:any, stamp:string}|undefined} */
      let scope;
      let closed = false;
      let preparing = false;
      function active() {
        if (closed) return unauthorized();
        try {
          const runtime = current();
          const generation = runtime?.catalogGeneration?.();
          if (!generation || runtime.resourceIdentity) return unauthorized();
          const { tenantId, ownerId, hostId } = catalogIdentity(runtime);
          const stamp = JSON.stringify([generation, tenantId, ownerId, hostId]);
          if (scope?.runtime !== runtime || scope?.stamp !== stamp) {
            handles.clear();
            scope = { runtime, stamp };
          }
          return scope;
        } catch {
          scope = undefined;
          handles.clear();
          return unauthorized();
        }
      }

      /** @param {any} execution */
      async function admitted(execution) {
        const source = active();
        let native;
        try {
          native = registrationSession(host, execution);
        } catch {
          return refuse('common.forbidden');
        }
        const check = () => {
          if (active() !== source) return unauthorized();
          if (source.runtime.vault?.snapshot?.().resourceIsolationPending)
            return refuse('resource.storage_unavailable');
          try {
            native.check();
            if (execution.signal?.aborted) return refuse('common.forbidden');
          } catch {
            return refuse('common.forbidden');
          }
        };
        async function verify() {
          check();
          try {
            await native.verify();
          } catch {
            check();
            return refuse('common.forbidden');
          }
          check();
        }
        await verify();
        for (const [handle, row] of handles)
          if (row.expires <= now()) handles.delete(handle);
        return {
          native,
          source,
          check,
          verify,
          session: host.sessions.get(native.id),
        };
      }

      /** @param {any} args @param {any} execution */
      async function prepareTool(args, execution) {
        checkPrepare(args);
        const { native, session, check } = await admitted(execution);
        check();
        if (!isAbsolute(native.cwd)) return refuse('common.forbidden');
        if (preparing || handles.size >= maxHandles)
          return refuse('common.rate_limited');
        preparing = true;
        try {
          const { directory, kind, metadata } = args;
          /** @type {Prepared} */
          const { manifest, archive } = await prepare({
            cwd: native.cwd,
            directory,
            kind,
            metadata,
          });
          check();
          if (
            !Buffer.isBuffer(archive) ||
            !archive.length ||
            archive.length > maxArchiveBytes ||
            manifest?.kind !== kind ||
            !text(manifest.name) ||
            !text(manifest.version)
          )
            return validationFailed();
          const retained = [...handles.values()].reduce(
            (bytes, row) => bytes + row.prepared.archive.length,
            archive.length,
          );
          if (retained > maxArchiveBytes) return refuse('common.rate_limited');
          const handle = randomUUID();
          handles.set(handle, {
            prepared: {
              manifest: globalThis.structuredClone(manifest),
              archive: Buffer.from(archive),
            },
            session,
            cwd: native.cwd,
            expires: now() + handleTtl,
          });
          return {
            handle,
            kind,
            name: manifest.name,
            version: manifest.version,
            archiveBytes: archive.length,
          };
        } catch (error) {
          check();
          throw error;
        } finally {
          preparing = false;
        }
      }

      /** @param {any} args @param {any} execution */
      async function submitTool(args, execution) {
        if (
          !(
            exact(args, ['handle', 'reason']) ||
            exact(args, ['handle', 'reason', 'packageId'])
          ) ||
          !uuid(args.handle) ||
          !text(args.reason, maxReviewReason) ||
          (Object.hasOwn(args, 'packageId') && !uuid(args.packageId))
        )
          return validationFailed();
        const { handle, reason, packageId } = args;
        const { native, session, source, check, verify } =
          await admitted(execution);
        check();
        const row = handles.get(handle);
        if (
          !row ||
          row.session !== session ||
          row.cwd !== native.cwd ||
          row.expires <= now()
        )
          return refuse('common.not_found');
        const operation = (row.operation ??= {
          reason,
          key: randomUUID(),
          ...(packageId ? { packageId } : {}),
        });
        if (operation.reason !== reason || operation.packageId !== packageId)
          return refuse('common.conflict');
        if (row.receipt) return row.receipt;
        const pending = (row.pending ??= (async () => {
          const { packageId, versionId, state } = await service(
            source.runtime,
          ).submitPrepared(
            {
              manifest: globalThis.structuredClone(row.prepared.manifest),
              archive: Buffer.from(row.prepared.archive),
              ...operation,
            },
            verify,
          );
          if (
            !uuid(packageId) ||
            !uuid(versionId) ||
            state !== 'pending_review'
          )
            return refuse('common.unavailable');
          return Object.freeze({ packageId, versionId, state });
        })());
        try {
          const receipt = await pending;
          await verify();
          row.receipt = receipt;
          return receipt;
        } catch (error) {
          check();
          throw error;
        } finally {
          if (row.pending === pending) row.pending = undefined;
        }
      }

      /** @param {any} tool */
      function register(tool, /** @type {'prepare'|'submit'} */ action) {
        return host.tools.register({
          ...tool,
          output: {
            schema: { type: 'object', additionalProperties: true },
            render: (/** @type {any} */ _args, /** @type {any} */ value) => [
              { type: 'text', text: JSON.stringify(value) },
            ],
          },
          async execute(/** @type {any} */ args, /** @type {any} */ execution) {
            if (closed) return unauthorized();
            try {
              return await tool.execute(args, execution);
            } catch (error) {
              throw toolFailure(error, action);
            }
          },
        });
      }
      host.effect(() => {
        /** @type {(()=>void)[]} */
        const disposers = [];
        const dispose = () => {
          closed = true;
          scope = undefined;
          handles.clear();
          for (const unregister of disposers.reverse()) unregister();
        };
        try {
          disposers.push(
            register(
              {
                name: 'auriga_resource_prepare',
                description:
                  '预检当前会话工作目录内的 DSH 插件或技能，返回短期送审句柄。技能目录需有 SKILL.md，metadata.version 必填。插件目录需有 package.json 及其声明的 DSH patch。/ Preflight a plugin or skill directory relative to this session and return a short-lived submission handle.',
                parameters: {
                  type: 'object',
                  properties: {
                    kind: { type: 'string', enum: ['plugin', 'skill'] },
                    directory: {
                      ...str,
                      description:
                        '相对会话工作目录，例如 skills/example / Relative directory, e.g. skills/example',
                    },
                    metadata: {
                      type: 'object',
                      properties: {
                        ...metadataFields,
                        tags,
                        permissions: permissionsSchema,
                      },
                      required: ['author', 'license', 'permissions'],
                      additionalProperties: false,
                    },
                  },
                  required: ['kind', 'directory', 'metadata'],
                  additionalProperties: false,
                },
                execute: prepareTool,
              },
              'prepare',
            ),
          );
          disposers.push(
            register(
              {
                name: 'auriga_resource_submit',
                description:
                  '使用同一会话的预检句柄提交成员资源审核。按用户授权提交并说明理由，追加版本时传入已有资源的 packageId。/ Submit the prepared resource for member review when the user requests it.',
                parameters: {
                  type: 'object',
                  properties: {
                    handle: str,
                    reason: { ...str, maxLength: maxReviewReason },
                    packageId: str,
                  },
                  required: ['handle', 'reason'],
                  additionalProperties: false,
                },
                execute: submitTool,
              },
              'submit',
            ),
          );
        } catch (error) {
          dispose();
          throw error;
        }
        return dispose;
      }, 'auriga-dsh: desktop resource submission tools');
    },
  );
}
