// @ts-nocheck
import { randomUUID } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { executionSession } from './task-context.js';
import { placementBinding } from './runtime.js';
import { assertSharedProfile } from './profile-state.js';
import { createSharedMessageContext } from './message-context.js';
import { createSharedMessageTrust } from './message-trust.js';
import { assertSharedSendResult } from './message-result.js';
import { decodeJson, encodeJson } from '../network/session/frames.js';
import { u64 } from '../network/source-control/frames.js';
import { registrationSession } from '../network/host/native/host-port.js';
import { MailboxError } from '../network/durable/client.js';
import { canonical, fail, same, validate, verifyToken } from './protocol.js';
import { createTurnAdmission } from './turn-admission.js';

/** Compose the documented DSH Host APIs with the node-owned W/IAM/N relay. */
export function createProductionSharedPorts(
  ctx,
  {
    settings,
    bridge,
    Peer,
    sdk,
    options,
    profileValidator = assertSharedProfile,
    available,
  },
) {
  if (
    sdk !== undefined &&
    (!sdk || sdk.Peer !== Peer || typeof sdk.LlmAdapter !== 'function')
  )
    fail('common.validation_failed');
  if (typeof profileValidator !== 'function') fail('common.validation_failed');
  const handles = new Map();
  const peers = new Map();
  const prepared = new WeakSet();
  const tasks = new WeakMap();
  const capabilities = new Map();
  const requests = new Map();
  const toolRequests = new WeakMap();
  let closed = false;
  const checkProfile = (expected) => {
    if (closed) return false;
    const result = profileValidator(ctx, expected, settings().skillRoots);
    if (typeof result?.then === 'function') {
      // A supplied async validator never grants a profile. Drain rejection
      // only; it remains a denial, not an authentication fallback.
      void Promise.resolve(result).catch(() => {});
      return false;
    }
    return result === true;
  };
  // Model and message relays are optional node services. DSH stays usable
  // without them; only the missing capability reports unavailable on use.
  const offered =
    available === undefined ? undefined : new Set(Array.from(available));
  const supported = (operation) => !offered || offered.has(operation);
  const call = (operation, body, signal) => {
    if (closed || !supported(operation)) fail('common.unavailable');
    return bridge.call(operation, body, signal);
  };
  const messageTrust = createSharedMessageTrust({
    settings,
    tenantId: options.tenantId,
    physicalInstanceId: options.physicalInstanceId,
    store: options.messageTrustStore,
  });
  const messageContexts = createSharedMessageContext(ctx, {
    call,
    trust: messageTrust,
  });
  async function message(operation, request, acceptContext) {
    const receive = {
      fetch: ['fetch', 'fetch_result'],
      attemptBegin: ['attempt_begin', 'attempt_result'],
      executionAdmit: ['execution_admit', 'admit_result'],
      receipt: ['receipt', 'receipt_committed'],
      receiveSet: ['receive_set', 'receive_set_result'],
    };
    let frame;
    if (operation === 'send') {
      if (
        !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(
          request.messageId,
        ) ||
        !request.target ||
        Object.keys(request.target).sort().join(',') !== 'local,node,realm' ||
        !['realm', 'node', 'local'].every((key) => u64(request.target[key])) ||
        !request.body ||
        Buffer.byteLength(JSON.stringify(request.body)) > 16384
      )
        fail('common.validation_failed');
    } else {
      if (!Object.hasOwn(receive, request.operation))
        fail('common.validation_failed');
      frame = decodeJson(encodeJson(request.frame));
      if (
        frame.type !== receive[request.operation][0] ||
        typeof frame.request_id !== 'string'
      )
        fail('common.validation_failed');
    }
    const task = capabilities.get(request.capability);
    const scope = 'message.' + operation;
    const check = () => {
      if (
        !task ||
        closed ||
        request.signal?.aborted ||
        task.execution.signal?.aborted ||
        capabilities.get(request.capability) !== task ||
        task.id !== request.taskId ||
        executionSession(ctx, task.execution) !== request.sessionId
      )
        fail();
      same(task.binding, {
        tenantId: request.tenantId,
        sessionId: request.sessionId,
      });
      const claims = verifyToken(
        request.capability,
        'Capability',
        options.capabilityKeys['auriga.shared.message'],
      );
      same(claims.binding, task.binding);
      if (
        claims.taskId !== task.id ||
        claims.audience !== 'auriga.shared.message' ||
        !claims.scopes.includes(scope)
      )
        fail('shared.binding_mismatch');
    };
    check();
    const signal = globalThis.AbortSignal.any(
      [request.signal, task.execution.signal].filter(Boolean),
    );
    const context = await messageContexts.prepare(request, task, signal);
    check();
    const current = await lookup(request.sessionId);
    same(current, task.binding);
    await resolve(placementBinding(current), current.platformSessionId);
    context.check();
    check();
    acceptContext?.(context);
    if (
      frame &&
      ((frame.epoch ?? frame.connection_epoch) !== context.connection.epoch ||
        (frame.boot_id !== undefined && frame.boot_id !== context.bootId))
    )
      fail('shared.binding_mismatch');
    const result = await call(
      operation,
      {
        tenantId: task.binding.tenantId,
        sessionId: task.binding.sessionId,
        taskId: task.id,
        capability: request.capability,
        ...(operation === 'send'
          ? {
              messageId: request.messageId,
              target: request.target,
              body: request.body,
            }
          : { operation: request.operation, frame }),
        contextId: context.contextId,
        bootId: context.bootId,
        nativeWitness: context.nativeWitness,
      },
      signal,
    );
    context.check();
    check();
    if (operation === 'send')
      assertSharedSendResult(result, context.address, request.target);
    else {
      if (
        result?.type !== receive[request.operation][1] &&
        result?.type !== 'error'
      )
        fail('common.validation_failed');
      await messageTrust.verify(
        result,
        context.connection,
        frame.request_id,
        undefined,
        () => {
          context.check();
          check();
        },
        frame.max_bytes,
      );
    }
    return result;
  }
  async function lookup(sessionId) {
    const result = await call('lookup', {
      tenantId: options.tenantId,
      physicalInstanceId: options.physicalInstanceId,
      sessionId,
    });
    validate('SessionBinding', result.binding);
    same(result.binding, {
      tenantId: options.tenantId,
      physicalInstanceId: options.physicalInstanceId,
      sessionId,
    });
    return result.binding;
  }
  async function resolve(binding, platformSessionId) {
    const result = await call('resolve', { binding, platformSessionId });
    same(result.binding, binding);
    if (result.state !== 'ready') fail('shared.revoked');
    const snapshot = await call('resourceSnapshot', { binding });
    validate('ResourceView', snapshot.view);
    same(snapshot.view.binding, binding);
    return {
      active: true,
      binding: result.binding,
      platformSessionId,
      authGeneration: result.authGeneration,
      authorizationSetDigest: snapshot.view.authorizationSetDigest,
    };
  }
  async function begin(binding, execution, scopes, audience, parent) {
    if (
      execution.signal?.aborted ||
      executionSession(ctx, execution) !== binding.sessionId
    )
      fail();
    const ordinary =
      audience === 'auriga.shared.enterprise' &&
      scopes.length === 1 &&
      scopes[0] === 'task.execute';
    if (ordinary && parent) fail();
    const authority = ordinary
      ? undefined
      : await turns.parent(parent, binding);
    const parentTask = authority && tasks.get(authority.execution);
    if (!ordinary && (!parentTask || parentTask.closing)) fail();
    let task = tasks.get(execution);
    if (!task) {
      task = {
        id: randomUUID(),
        binding,
        execution,
        tokens: new Set(),
        children: new Set(),
        parent: parentTask,
        parentLeases: new Set(parent ? [parent] : []),
      };
      tasks.set(execution, task);
      parentTask?.children.add(task);
    }
    if (task.closing || task.parent !== parentTask) fail();
    same(task.binding, binding);
    try {
      const result = await call(
        'beginTask',
        {
          binding,
          taskId: task.id,
          scopes,
          audience,
          ...(authority ? { parentCapability: authority.token } : {}),
        },
        globalThis.AbortSignal.any(
          [execution.signal, authority?.execution.signal].filter(Boolean),
        ),
      );
      if (
        task.closing ||
        execution.signal?.aborted ||
        tasks.get(execution) !== task
      )
        fail();
      const claims = verifyToken(
        result.token,
        'Capability',
        options.capabilityKeys[audience],
      );
      same(claims.binding, binding);
      if (claims.taskId !== task.id || claims.audience !== audience) fail();
      capabilities.set(result.token, task);
      task.tokens.add(result.token);
      return result.token;
    } catch (error) {
      // A canceled response can still have committed a task at IAM.
      await finish(binding, execution, task);
      throw error;
    }
  }
  function fenceTask(task) {
    if (!task) return;
    task.closing = true;
    for (const token of task.tokens) capabilities.delete(token);
    for (const child of task.children) fenceTask(child);
  }
  async function finish(binding, execution, knownTask) {
    const current = tasks.get(execution);
    const task = knownTask ?? current;
    if (!task) return;
    same(task.binding, binding);
    if (task.finished) return task.finished;
    fenceTask(task);
    task.finished = (async () => {
      const children = await Promise.allSettled(
        [...task.children].map((child) =>
          finish(child.binding, child.execution, child),
        ),
      );
      const error = children.find((entry) => entry.status === 'rejected');
      if (error) throw error.reason;
      const result = await call('finishTask', { binding, taskId: task.id });
      if (!result || result.fenced !== true || result.taskId !== task.id)
        fail('common.unavailable');
      try {
        validate('SessionBinding', result.binding);
      } catch {
        fail('common.unavailable');
      }
      same(result.binding, binding);
      if (tasks.get(execution) === task) tasks.delete(execution);
      task.parent?.children.delete(task);
    })().catch((error) => {
      if (task.parent) turns.invalidate(task.parent.execution);
      throw error;
    });
    return task.finished;
  }
  const turns = createTurnAdmission(ctx, {
    lookup,
    begin: (binding, execution) =>
      begin(binding, execution, ['task.execute'], 'auriga.shared.enterprise'),
    refresh: (token, sessionId) =>
      ports.enterprise.refreshCapability(token, sessionId, 'task.execute'),
    fence: (execution) => fenceTask(tasks.get(execution)),
    fenceForeground: (execution) => {
      for (const child of tasks.get(execution)?.children ?? [])
        if (![...child.parentLeases].some((value) => turns.retained(value)))
          fenceTask(child);
    },
    finish,
  });
  const removeRequest = async (agent) => {
    turns.idle(agent);
    const request = requests.get(agent);
    if (!request) return;
    requests.delete(agent);
  };
  const disposers = [
    ctx.on('agent/request', async (payload, next) => {
      const { agent, signal, turn, step } = payload;
      if (
        ctx.agents.get(agent.id) !== agent ||
        ctx.sessions.get(agent.session.id) !== agent.session
      )
        fail();
      const root = await turns.request(payload);
      const binding = root.binding;
      const previous = requests.get(agent);
      const row =
        previous?.turn === turn
          ? previous
          : { binding, turn, tasks: new Map() };
      same(row.binding, binding);
      row.step = step;
      row.signal = signal;
      row.callId = randomUUID();
      requests.set(agent, row);
      return next();
    }),
    ctx.on('session/event', (session, event) => {
      turns.end(session, event);
      const agent = ctx.agents.get(session.id);
      if (
        event.type === 'turn/end' &&
        requests.get(agent)?.turn === event.data.turn
      )
        requests.delete(agent);
    }),
    ctx.on('agent/status', ({ agent, status }) => {
      if (status === 'idle') void removeRequest(agent).catch(() => {});
    }),
    ctx.on('agent/disposed', ({ agent }) => {
      messageContexts.retire(agent);
      turns.retire(agent);
      void removeRequest(agent).catch(() => {});
    }),
  ];
  const ports = {
    sdk,
    options,
    config: () => settings().enterprise,
    messageTrustConfig: () => settings().messageTrust,
    async openMessageInbox(binding, signal) {
      const agent = ctx.agents.get(binding.sessionId);
      const native = registrationSession(ctx, { agent });
      const controller = new AbortController();
      const operationSignal = globalThis.AbortSignal.any(
        [signal, controller.signal].filter(Boolean),
      );
      const current = await lookup(binding.sessionId);
      same(current, binding);
      await resolve(placementBinding(current), current.platformSessionId);
      let active;
      let blocked;
      let lane = Promise.resolve();
      let expected,
        latest,
        stopped = false;
      const guard = () => {
        if (
          stopped ||
          operationSignal.aborted ||
          !active ||
          tasks.get(active.execution) !== active.task ||
          active.task.closing
        )
          fail();
        native.check();
        active.parent.check();
        if (executionSession(ctx, active.execution) !== binding.sessionId)
          fail();
      };
      const accept = (context) => {
        guard();
        const stable = canonical({
          contextId: context.contextId,
          binding: context.binding,
          registration: context.registration,
          nativeBinding: context.nativeBinding,
          producerEpoch: context.producerEpoch,
          connection: Object.fromEntries(
            [
              'realm',
              'node',
              'incarnation',
              'epoch',
              'bootId',
              'channelNonce',
            ].map((key) => [key, context.connection[key]]),
          ),
        });
        if (expected !== undefined && stable !== expected)
          fail('shared.binding_mismatch');
        expected ??= stable;
        latest = context;
        return context;
      };
      const refresh = async (initial = false) => {
        guard();
        const { task, execution } = active;
        if (!initial) {
          const next = await ports.downstream.issue(
            binding,
            task.id,
            'auriga.shared.message',
            ['message.receive'],
            execution.signal,
          );
          // This private background execution has one serialized request lane.
          // Retain only its current token, without pruning live helper anchors.
          if (capabilities.get(active.token) === task)
            capabilities.delete(active.token);
          task.tokens.delete(active.token);
          active.token = next;
        }
        return accept(
          await messageContexts.prepare(
            {
              tenantId: binding.tenantId,
              sessionId: binding.sessionId,
              taskId: task.id,
              capability: active.token,
            },
            task,
            execution.signal,
          ),
        );
      };
      const endOperation = async () => {
        const prior = active;
        if (!prior) return;
        // Keep a failed close as the lane's barrier. Never open a replacement
        // ordinary task while this operation lacks explicit Finish echoes.
        try {
          try {
            await finish(binding, prior.execution, prior.task);
          } finally {
            await prior.parent.close();
          }
        } catch (error) {
          blocked = error;
          throw error;
        }
        if (active === prior) active = undefined;
      };
      const startOperation = async () => {
        if (stopped || operationSignal.aborted) fail();
        native.check();
        const parent = await turns.operation(binding, agent, operationSignal);
        const execution = { agent, signal: parent.signal };
        try {
          const token = await begin(
            binding,
            execution,
            ['message.receive'],
            'auriga.shared.message',
            parent,
          );
          active = { parent, execution, task: tasks.get(execution), token };
          await refresh(true);
        } catch (error) {
          try {
            if (tasks.has(execution)) await finish(binding, execution);
          } finally {
            await parent.close();
          }
          throw error;
        }
      };
      const run = (operation) => {
        const next = lane.then(async () => {
          if (!active) await startOperation();
          try {
            guard();
            return await operation();
          } finally {
            await endOperation();
          }
        });
        lane = next.then(
          () => undefined,
          () => {
            if (blocked) throw blocked;
          },
        );
        void lane.catch(() => {});
        return next;
      };
      const close = () => {
        stopped = true;
        controller.abort();
        return lane.then(endOperation);
      };
      try {
        // Bootstrap stays inside the first finite receiver-open operation.
        await startOperation();
        const trust = await messageTrust.open();
        return {
          get context() {
            return latest;
          },
          trust,
          run,
          refresh: () => refresh(false),
          assertCurrent() {
            guard();
            latest.check();
          },
          async request(frame, expectedType, callerSignal) {
            guard();
            const operation = {
              fetch: 'fetch',
              attempt_begin: 'attemptBegin',
              execution_admit: 'executionAdmit',
              receipt: 'receipt',
              receive_set: 'receiveSet',
            }[frame.type];
            if (!operation) fail('common.validation_failed');
            const result = await message(
              'receive',
              {
                tenantId: binding.tenantId,
                sessionId: binding.sessionId,
                taskId: active.task.id,
                capability: active.token,
                operation,
                frame,
                signal: globalThis.AbortSignal.any(
                  [active.execution.signal, callerSignal].filter(Boolean),
                ),
              },
              accept,
            );
            if (result.type === 'error') throw new MailboxError(result, 1);
            if (result.type !== expectedType) fail('common.validation_failed');
            return result;
          },
          close,
        };
      } catch (error) {
        await close().catch(() => {});
        throw error;
      }
    },
    get helperBase() {
      const port = Number(ctx.webServer.port);
      if (!Number.isInteger(port) || port < 1 || port > 65535)
        fail('common.unavailable');
      return `http://127.0.0.1:${port}/dsh-team`;
    },
    assertProfile: checkProfile,
    resolve,
    lookup,
    async register(binding, parentSessionId) {
      const result = await call('register', {
        binding,
        parentSessionId: parentSessionId ?? '',
      });
      validate('SessionBinding', result.binding);
      same(result.binding, binding);
      return result.binding;
    },
    authorizeTask: (capability) => call('authorizeCapability', { capability }),
    async resourceView(binding) {
      return (await call('resourceSnapshot', { binding })).view;
    },
    createPeer(peerOptions) {
      const peer = new Peer(ctx, peerOptions);
      peers.set(peer, peerOptions.principal);
      peer.signal.addEventListener('abort', () => peers.delete(peer), {
        once: true,
      });
      return peer;
    },
    sessions: {
      prepare(id, options) {
        if (ctx.sessions.get(id)) fail('common.conflict');
        const result = {
          id,
          meta: Object.freeze({ ...options.meta }),
          handle: undefined,
        };
        prepared.add(result);
        return result;
      },
      async publish(value) {
        if (!prepared.has(value) || value.handle || closed) fail();
        const binding = await lookup(value.id);
        await resolve(placementBinding(binding), binding.platformSessionId);
        const handle = await ctx.agents.create({
          sessionId: value.id,
          meta: value.meta,
          agentOptions: { provider: 'auriga-shared', model: settings().model },
          setup: async (agentCtx) => {
            const current = await lookup(value.id);
            same(current, binding);
            await resolve(placementBinding(current), current.platformSessionId);
            // Only a Host-validated published preset may be mounted; unknown
            // ids fail here before publication, never via client-chosen cwd.
            if (
              value.meta.agentPreset !== undefined &&
              typeof ctx.agentPresets?.mount === 'function'
            )
              await ctx.agentPresets.mount(agentCtx, value.meta.agentPreset);
            return {
              commit() {
                if (closed || !checkProfile(settings().profile))
                  fail('common.unavailable');
              },
            };
          },
        });
        value.handle = handle;
        if (closed) {
          await handle.dispose();
          fail('common.unavailable');
        }
        handles.set(value.id, { handle, binding });
      },
      async discard(value) {
        if (!prepared.has(value)) fail();
        prepared.delete(value);
        handles.delete(value.id);
        await value.handle?.dispose();
      },
    },
    async invoke(action, binding, args) {
      const parts = action.split('/');
      if (parts.length !== 2) fail('common.validation_failed');
      const peer = ports.createPeer({
        principal: { kind: 'auriga-shared-member', ...binding },
        interactions: 'none',
      });
      try {
        const parameters = { ...args };
        if (parts[0] === 'workspaceFiles')
          parameters.workspaceFileScopeId = binding.sessionId;
        else parameters.sessionId = binding.sessionId;
        return await ctx.typertGateway.invoke({
          namespace: parts[0],
          method: parts[1],
          args: parameters,
          peer,
        });
      } finally {
        await peer.dispose();
      }
    },
    acquireExecution: (binding, execution) => turns.acquire(binding, execution),
    issueTask: (binding, execution, scopes, parent) =>
      begin(binding, execution, scopes, 'auriga.shared.enterprise', parent),
    issueExecution: (binding, execution) =>
      begin(binding, execution, ['task.execute'], 'auriga.shared.enterprise'),
    finishTask: (binding, execution) => finish(binding, execution),
    enterprise: {
      taskId(token) {
        const task = capabilities.get(token);
        if (!task) fail();
        return task.id;
      },
      credentials: (_binding, capability) =>
        call('readCredentials', { capability }),
      async outbound(request, capability, binding, token) {
        const task = capabilities.get(token);
        if (
          !task ||
          task.id !== capability.taskId ||
          task.execution.signal?.aborted
        )
          fail();
        same(task.binding, binding);
        same(capability.binding, binding);
        let body = request.body;
        const headers = { ...request.headers };
        if (body !== undefined && body !== null && typeof body !== 'string') {
          body = JSON.stringify(body);
          if (!headers['content-type'])
            headers['content-type'] = 'application/json';
        }
        const response = await call(
          'enterpriseOutbound',
          {
            capability,
            url: request.url,
            method: request.method,
            headers,
            bodyBase64: Buffer.from(
              ['GET', 'HEAD'].includes(request.method) ? '' : (body ?? ''),
            ).toString('base64'),
            timeoutMs: request.timeoutMs,
          },
          globalThis.AbortSignal.any(
            [
              task.execution.signal,
              request.signal,
              globalThis.AbortSignal.timeout(request.timeoutMs),
            ].filter(Boolean),
          ),
        );
        if (
          !Number.isInteger(response.statusCode) ||
          response.statusCode < 200 ||
          response.statusCode > 599 ||
          typeof response.body !== 'string' ||
          !response.headers ||
          typeof response.headers !== 'object' ||
          Array.isArray(response.headers)
        )
          fail('common.unavailable');
        let json = null;
        try {
          json = JSON.parse(response.body);
        } catch {
          /* Plain business text is valid. */
        }
        return {
          statusCode: response.statusCode,
          headers: response.headers,
          body: response.body,
          json,
        };
      },
      async refreshCapability(token, sessionId, scope) {
        const task = capabilities.get(token);
        const check = () => {
          if (
            closed ||
            !task ||
            task.closing ||
            tasks.get(task.execution) !== task ||
            capabilities.get(token) !== task ||
            task.execution.signal?.aborted ||
            executionSession(ctx, task.execution) !== sessionId
          )
            fail();
        };
        check();
        if (task.parent) {
          await turns.refreshParent(task.parent.execution, task.parentLeases);
          check();
        }
        if (
          !task ||
          task.execution.signal?.aborted ||
          executionSession(ctx, task.execution) !== sessionId
        )
          fail();
        const claims = verifyToken(
          token,
          'Capability',
          options.capabilityKeys['auriga.shared.enterprise'],
        );
        same(claims.binding, task.binding);
        if (
          claims.audience !== 'auriga.shared.enterprise' ||
          claims.taskId !== task.id ||
          !claims.scopes.includes(scope)
        )
          fail();
        const current = await lookup(sessionId);
        check();
        same(current, task.binding);
        await resolve(placementBinding(current), current.platformSessionId);
        check();
        const result = await call(
          'issueCapability',
          {
            binding: current,
            taskId: task.id,
            audience: claims.audience,
            scopes: claims.scopes,
          },
          task.execution.signal,
        );
        const refreshed = verifyToken(
          result.token,
          'Capability',
          options.capabilityKeys[claims.audience],
        );
        check();
        same(refreshed.binding, task.binding);
        if (
          refreshed.taskId !== task.id ||
          refreshed.audience !== claims.audience
        )
          fail();
        capabilities.set(result.token, task);
        task.tokens.add(result.token);
        return result.token;
      },
    },
    downstream: {
      async taskForSession(binding, signal, purpose) {
        const agent = ctx.agents.currentInitiator();
        let current = requests.get(agent);
        const parent = purpose.execution
          ? turns.executionLease(purpose.execution)
          : await turns.requestLease(agent, purpose.nativeSignal);
        try {
          current ??= requests.get(agent);
          if (!current && purpose.execution) {
            current = toolRequests.get(purpose.execution);
            if (!current) {
              current = {
                binding,
                signal: parent.signal,
                callId: purpose.execution.callId,
                tasks: new Map(),
              };
              toolRequests.set(purpose.execution, current);
            }
          }
          if (
            !current ||
            signal?.aborted ||
            current.signal.aborted ||
            ctx.agents.get(agent.id) !== agent ||
            executionSession(ctx, { agent }) !== binding.sessionId
          )
            fail();
          same(current.binding, binding);
          const purposeKey = purpose.audience + ':' + purpose.scope;
          let execution = current.tasks.get(purposeKey);
          if (execution?.signal?.aborted) {
            current.tasks.delete(purposeKey);
            await finish(binding, execution);
            execution = undefined;
          }
          if (!execution) {
            execution = {
              agent,
              signal: globalThis.AbortSignal.any([
                current.signal,
                parent.signal,
              ]),
            };
            current.tasks.set(purposeKey, execution);
            execution.ready = begin(
              binding,
              execution,
              [purpose.scope],
              purpose.audience,
              parent,
            );
          }
          try {
            await execution.ready;
          } catch (error) {
            if (current.tasks.get(purposeKey) === execution)
              current.tasks.delete(purposeKey);
            throw error;
          }
          if (
            (purpose.execution
              ? requests.get(agent) !== current &&
                toolRequests.get(purpose.execution) !== current
              : requests.get(agent) !== current) ||
            current.tasks.get(purposeKey) !== execution ||
            execution.signal?.aborted ||
            !tasks.has(execution)
          )
            fail();
          const parentLeases = tasks.get(execution).parentLeases;
          for (const value of parentLeases)
            if (!turns.active(value)) parentLeases.delete(value);
          parentLeases.add(parent);
          return {
            taskId: tasks.get(execution).id,
            callId: current.callId,
            signal: parent.signal,
            check: () => parent.check(),
            close: () => parent.close(),
          };
        } catch (error) {
          await parent.close();
          throw error;
        }
      },
      async issue(binding, taskId, audience, scopes, signal) {
        const task = [...new Set(capabilities.values())].find(
          (value) => value.id === taskId,
        );
        if (
          !task ||
          task.closing ||
          task.execution.signal?.aborted ||
          executionSession(ctx, task.execution) !== binding.sessionId
        )
          fail();
        same(task.binding, binding);
        if (task.parent)
          await turns.refreshParent(task.parent.execution, task.parentLeases);
        const result = await call(
          'issueCapability',
          { binding, taskId, audience, scopes },
          signal,
        );
        const claims = verifyToken(
          result.token,
          'Capability',
          options.capabilityKeys[audience],
        );
        same(claims.binding, task.binding);
        if (
          claims.taskId !== task.id ||
          claims.audience !== audience ||
          claims.scopes.length !== scopes.length ||
          scopes.some((scope) => !claims.scopes.includes(scope))
        )
          fail();
        if (
          tasks.get(task.execution) !== task ||
          task.execution.signal?.aborted ||
          signal?.aborted
        )
          fail();
        capabilities.set(result.token, task);
        task.tokens.add(result.token);
        return result.token;
      },
      model: ({ signal, ...body }) => {
        if (closed || !supported('model')) fail('common.unavailable');
        return bridge.stream('model', body, signal);
      },
      send: async (request) => {
        if (!supported('send')) fail('common.unavailable');
        return message('send', request);
      },
      receive: async (request) => {
        if (!supported('receive')) fail('common.unavailable');
        return message('receive', request);
      },
    },
    disconnect(revocation) {
      turns.revoke(revocation);
      for (const [peer, binding] of peers)
        if (binding.placementId === revocation.placementId)
          void peer.dispose().catch(() => {});
      for (const [id, entry] of handles)
        if (entry.binding.placementId === revocation.placementId) {
          handles.delete(id);
          void entry.handle.dispose().catch(() => {});
        }
    },
    async close() {
      if (closed) return;
      try {
        await turns.close();
        await Promise.allSettled([...requests.keys()].map(removeRequest));
        await Promise.allSettled(
          [...new Set(capabilities.values())].map((task) =>
            finish(task.binding, task.execution),
          ),
        );
      } finally {
        closed = true;
        await messageContexts.close();
        for (const dispose of disposers) dispose();
        await Promise.allSettled([
          ...[...peers.keys()].map((peer) => peer.dispose()),
          ...[...handles.values()].map(({ handle }) => handle.dispose()),
        ]);
        handles.clear();
        capabilities.clear();
        bridge.close();
      }
    },
  };
  return ports;
}
