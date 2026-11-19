// @ts-nocheck
import { randomUUID } from 'node:crypto';
import {
  mkdirSync,
  openSync,
  fsyncSync,
  closeSync,
  writeFileSync,
  renameSync,
  existsSync,
} from 'node:fs';
import { dirname } from 'node:path';
import { canonical, fail } from '../shared/protocol.js';
import { parseStrictJson } from '../resources/json.js';
import {
  readIntent,
  readOwnedFile,
  normalizeView,
  loadResources,
  sameBinding,
  exact,
} from './intent.js';

const clone = (value) => JSON.parse(JSON.stringify(value));
const hostInstanceId = randomUUID();
const live = (row) => row?.status === 'running' || row?.status === 'stopping';

/** Public native registries, rather than a member-supplied 'idle' claim, fence
 * package mutations. Persisted sessions cover jobs whose agent is not warm. */
export async function dedicatedQuiescent(ctx, activeExecutions) {
  if (
    activeExecutions.size ||
    typeof ctx.agents?.list !== 'function' ||
    typeof ctx.sessionPersistence?.list !== 'function'
  )
    return false;
  const agents = ctx.agents.list();
  if (!Array.isArray(agents) || agents.some((agent) => agent.status !== 'idle'))
    return false;
  if (!ctx.jobs) return true;
  if (typeof ctx.jobs.list !== 'function') return false;
  const stored = await ctx.sessionPersistence.list();
  if (!Array.isArray(stored)) return false;
  const ids = new Set([
    ...agents.map((agent) => agent.session.id),
    ...stored.map((row) => row.header?.id),
  ]);
  if (ctx.jobs.list(undefined).some(live)) return false;
  return [...ids].filter(Boolean).every((id) => !ctx.jobs.list(id).some(live));
}

function durable(file, value) {
  mkdirSync(dirname(file), { recursive: true, mode: 0o700 });
  const temporary = file + '.' + randomUUID() + '.tmp';
  const fd = openSync(temporary, 'wx', 0o600);
  try {
    writeFileSync(fd, canonical(value) + '\n');
    fsyncSync(fd);
  } finally {
    closeSync(fd);
  }
  renameSync(temporary, file);
  if (process.platform !== 'win32') {
    const parent = openSync(dirname(file), 'r');
    try {
      fsyncSync(parent);
    } finally {
      closeSync(parent);
    }
  }
}
function operationResult(value, name) {
  if (
    !value ||
    value.target !== name ||
    value.error ||
    !['applied', 'restart-required'].includes(value.application)
  )
    fail('resource.apply_failed');
  return value.application === 'restart-required';
}
async function inventory(manager) {
  const [bundles, plugins] = await Promise.all([
    manager.listBundles(),
    manager.listPlugins(),
  ]);
  if (!Array.isArray(bundles) || !Array.isArray(plugins))
    fail('resource.apply_failed');
  return { bundles, plugins };
}
function observed(inventory, name) {
  const matches = inventory.bundles.filter((row) => row.name === name);
  if (matches.length > 1) fail('resource.apply_failed');
  const bundle = matches[0];
  if (!bundle) return undefined;
  const rows = Array.isArray(bundle.rows) ? bundle.rows : [];
  const states = rows.map((row) =>
    inventory.plugins.filter((item) => item.entryId === row.entryId),
  );
  const active =
    bundle.enabled &&
    !bundle.error &&
    rows.length > 0 &&
    states.every(
      (matches) =>
        matches.length === 1 &&
        (!matches[0].enabled || matches[0].fiberPhase === 'active'),
    ) &&
    states.some(
      (matches) => matches[0]?.enabled && matches[0].fiberPhase === 'active',
    );
  return {
    ...bundle,
    active,
    entryIds: rows.map((row) => row.entryId).filter(Boolean),
  };
}

/** Trusted Node intent -> public PluginManager/SkillRegistry lifecycle. The
 * signed Resource authority remains in Node; this layer proves actual use. */
export function createDedicatedResources(
  ctx,
  {
    binding,
    intentFile,
    artifactRoot,
    stateFile,
    currentView,
    quiescent,
    now = () => Math.floor(Date.now() / 1000),
    instanceId = hostInstanceId,
  },
) {
  sameBinding(binding, binding);
  if (
    typeof currentView !== 'function' ||
    typeof quiescent !== 'function' ||
    !ctx.skills?.registerProvider
  )
    fail('common.unavailable');
  const manager = ctx.get?.('pluginManager') ?? ctx.pluginManager;
  if (
    !manager ||
    ![
      'listBundles',
      'listPlugins',
      'installBundle',
      'setBundleEnabled',
      'removeBundle',
    ].every((key) => typeof manager[key] === 'function')
  )
    fail('common.unavailable');
  let closed = false,
    running,
    verified,
    control,
    prepared;
  let state = {
    schemaVersion: 1,
    binding: clone(binding),
    revisions: {},
    managed: {},
    pending: null,
  };
  if (existsSync(stateFile)) {
    state = parseStrictJson(readOwnedFile(stateFile, 1048576), true);
    exact(state, [
      'schemaVersion',
      'binding',
      'revisions',
      'managed',
      'pending',
    ]);
    sameBinding(state.binding, binding);
    if (
      state.schemaVersion !== 1 ||
      !state.revisions ||
      !state.managed ||
      typeof state.revisions !== 'object' ||
      typeof state.managed !== 'object'
    )
      fail('resource.apply_failed');
  }
  let definitions = new Map();
  let receipt = {
    schemaVersion: 1,
    binding: clone(binding),
    viewId: '',
    intentDigest: '',
    status: 'applying',
    reason: 'resource.applying',
    hostInstanceId: instanceId,
  };
  const detachProvider = ctx.skills.registerProvider((value) => {
    control = value;
    return {
      name: 'auriga-dedicated-catalog',
      async list() {
        if (!isCurrent()) return [];
        return [...definitions.values()].map((entry) => ({
          ...entry.skill,
          rank: 50,
          locator: entry.resourceId,
        }));
      },
      async get(candidate, options) {
        options.signal?.throwIfAborted();
        await verify();
        const entry = definitions.get(candidate.name);
        if (!entry || entry.resourceId !== candidate.locator) return undefined;
        options.signal?.throwIfAborted();
        return clone(entry.skill);
      },
    };
  });
  let persisted = canonical(state);
  const save = () => {
    const next = canonical(state);
    if (next === persisted && existsSync(stateFile)) return;
    durable(stateFile, state);
    persisted = next;
  };
  const setStatus = (intent, status, reason) => {
    verified = undefined;
    receipt = {
      schemaVersion: 1,
      binding: clone(binding),
      viewId: intent?.value.view.viewId ?? '',
      intentDigest: intent?.intentDigest ?? '',
      status,
      reason,
      hostInstanceId: instanceId,
    };
    control?.invalidate();
  };
  const read = () => {
    if (closed) fail('common.unavailable');
    return readIntent(intentFile, binding, now);
  };
  async function fence(intent) {
    if (read().normalized !== intent.normalized) fail('shared.revoked');
    const view = normalizeView(await currentView(clone(binding)));
    if (
      canonical(view) !== canonical(intent.value.view) ||
      read().normalized !== intent.normalized
    )
      fail('shared.revoked');
  }
  function isCurrent() {
    try {
      return (
        !closed &&
        receipt.status === 'ready' &&
        verified !== undefined &&
        verified.normalized === read().normalized &&
        now() <= verified.observedAt + 1
      );
    } catch {
      return false;
    }
  }
  async function observeAll(intent, wanted) {
    await fence(intent);
    const actual = await inventory(manager);
    await fence(intent);
    const plugins = wanted.filter((entry) => entry.kind === 'plugin');
    const expected = new Map(plugins.map((entry) => [entry.name, entry]));
    for (const [name, owned] of Object.entries(state.managed)) {
      if (expected.has(name)) continue;
      const row = observed(actual, name);
      if (
        row?.enabled ||
        row?.installed ||
        actual.plugins.some(
          (plugin) =>
            owned.entryIds?.includes(plugin.entryId) &&
            plugin.fiberPhase === 'active',
        )
      )
        return false;
    }
    for (const entry of plugins) {
      const row = observed(actual, entry.name);
      const proof = state.managed[entry.name];
      if (
        !proof ||
        proof.installed !== true ||
        proof.artifactDigest !== entry.artifactDigest ||
        proof.manifestDigest !== entry.manifestDigest ||
        !row?.installed ||
        !row.enabled ||
        !row.active ||
        row.version !== entry.version
      )
        return false;
      proof.entryIds = row.entryIds;
    }
    // Installation-provided bundles are preserved. A new unowned profile
    // dependency cannot silently join the Node's approved active catalog set.
    if (
      actual.bundles.some(
        (row) =>
          row.installed &&
          row.availability === 'profile' &&
          row.enabled &&
          !Object.hasOwn(state.managed, row.name),
      )
    )
      return false;
    await fence(intent);
    return true;
  }
  async function reconcileOnce() {
    let intent;
    try {
      intent = read();
      await fence(intent);
      const wanted =
        prepared?.digest === intent.intentDigest
          ? prepared.resources
          : await loadResources(intent, artifactRoot);
      prepared = { digest: intent.intentDigest, resources: wanted };
      await fence(intent);
      for (const entry of wanted) {
        const previous = state.revisions[entry.resourceId];
        if (
          previous &&
          (entry.installationRevision < previous.revision ||
            (entry.installationRevision === previous.revision &&
              (entry.manifestDigest !== previous.manifestDigest ||
                previous.present === false)))
        )
          fail('resource.state_conflict');
      }
      const samePending = state.pending?.intentDigest === intent.intentDigest;
      if (
        samePending &&
        (state.pending.status === 'failed' ||
          (state.pending.status === 'applying' &&
            state.pending.hostInstanceId !== instanceId))
      ) {
        setStatus(intent, 'failed', 'resource.state_conflict');
        return receipt;
      }
      if (
        samePending &&
        state.pending.hostInstanceId === instanceId &&
        state.pending.status === 'restart-required'
      ) {
        setStatus(intent, 'restart-required', 'resource.restart_required');
        return receipt;
      }
      if (!(await observeAll(intent, wanted))) {
        setStatus(intent, 'applying', 'resource.applying');
        if (!(await quiescent())) return receipt;
        await fence(intent);
        let needsRestart = false;
        state.pending = {
          intentDigest: intent.intentDigest,
          hostInstanceId: instanceId,
          status: 'applying',
        };
        save();
        const expected = new Map(
          wanted
            .filter((row) => row.kind === 'plugin')
            .map((row) => [row.name, row]),
        );
        const apply = async (name, action) => {
          await fence(intent);
          const result = await action();
          await fence(intent);
          const restart = operationResult(result, name);
          needsRestart ||= restart;
          if (restart) {
            state.pending.status = 'restart-required';
            save();
          }
        };
        for (const [name, proof] of Object.entries(state.managed)) {
          const entry = expected.get(name);
          if (
            entry?.artifactDigest === proof.artifactDigest &&
            entry.manifestDigest === proof.manifestDigest
          )
            continue;
          const actual = observed(await inventory(manager), name);
          if (actual?.enabled)
            await apply(name, () => manager.setBundleEnabled(name, false));
          if (actual?.installed)
            await apply(name, () => manager.removeBundle(name));
        }
        for (const entry of expected.values()) {
          const proof = state.managed[entry.name];
          let actual = observed(await inventory(manager), entry.name);
          if (
            !proof ||
            proof.artifactDigest !== entry.artifactDigest ||
            proof.manifestDigest !== entry.manifestDigest ||
            !actual?.installed ||
            actual.version !== entry.version
          ) {
            if (actual?.installed) fail('resource.state_conflict');
            // Persist ownership before a public install can commit without a
            // response. Recovery never treats installed metadata as byte proof.
            state.managed[entry.name] = {
              resourceId: entry.resourceId,
              artifactDigest: entry.artifactDigest,
              manifestDigest: entry.manifestDigest,
              version: entry.version,
              entryIds: [],
              installed: false,
            };
            save();
            await apply(entry.name, () =>
              manager.installBundle('file:' + entry.artifactFile, {
                enabled: false,
                approvedBuilds: [],
                saveExact: true,
              }),
            );
            state.managed[entry.name].installed = true;
            save();
          }
          actual = observed(await inventory(manager), entry.name);
          if (!actual?.enabled)
            await apply(entry.name, () =>
              manager.setBundleEnabled(entry.name, true),
            );
        }
        if (needsRestart) {
          state.pending.status = 'restart-required';
          save();
          setStatus(intent, 'restart-required', 'resource.restart_required');
          return receipt;
        }
        if (!(await observeAll(intent, wanted))) fail('resource.apply_failed');
      }
      const nextDefinitions = new Map(
        wanted
          .filter((entry) => entry.kind === 'skill')
          .map((entry) => [entry.name, entry]),
      );
      for (const key of Object.keys(state.revisions))
        state.revisions[key].present = false;
      for (const entry of wanted)
        state.revisions[entry.resourceId] = {
          revision: entry.installationRevision,
          manifestDigest: entry.manifestDigest,
          present: true,
        };
      state.pending = null;
      await fence(intent);
      save();
      definitions = nextDefinitions;
      receipt = {
        schemaVersion: 1,
        binding: clone(binding),
        viewId: intent.value.view.viewId,
        intentDigest: intent.intentDigest,
        status: 'ready',
        reason: 'verified',
        hostInstanceId: instanceId,
      };
      verified = { normalized: intent.normalized, observedAt: now() };
      control.invalidate();
      return receipt;
    } catch (error) {
      if (intent && state.pending?.intentDigest === intent.intentDigest) {
        state.pending.status = 'failed';
        try {
          save();
        } catch {
          /* persistence uncertainty remains a refusal */
        }
      }
      setStatus(
        intent,
        'failed',
        [
          'shared.revoked',
          'resource.state_conflict',
          'resource.signature_invalid',
        ].includes(error.code)
          ? error.code
          : 'resource.apply_failed',
      );
      return receipt;
    }
  }
  function reconcile() {
    if (closed)
      return Promise.reject(
        Object.assign(new Error('common.unavailable'), {
          code: 'common.unavailable',
        }),
      );
    if (!running)
      running = reconcileOnce().finally(() => {
        running = undefined;
      });
    return running;
  }
  async function verify() {
    const outcome = await reconcile();
    if (outcome.status !== 'ready' || !isCurrent())
      fail('resource.apply_failed');
    return clone(outcome);
  }
  return {
    reconcile,
    verify,
    isCurrent,
    invalidate() {
      verified = undefined;
      control.invalidate();
    },
    receipt() {
      return clone(receipt);
    },
    async close() {
      if (closed) return;
      closed = true;
      verified = undefined;
      prepared = undefined;
      definitions.clear();
      control?.invalidate();
      detachProvider();
      await running;
    },
  };
}
