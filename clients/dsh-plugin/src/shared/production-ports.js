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
import { fail, same, validate, verifyToken } from './protocol.js';

/** Compose the documented DSH Host APIs with the node-owned W/IAM/N relay. */
export function createProductionSharedPorts(
  ctx,
  { settings, bridge, Peer, options },
) {
  const handles = new Map();
  const peers = new Map();
  const prepared = new WeakSet();
  const tasks = new WeakMap();
  const capabilities = new Map();
  const requests = new Map();
  let closed = false;
  const checkProfile = (expected) =>
    !closed && assertSharedProfile(ctx, expected, settings().skillRoots);
  const call = (operation, body, signal) => {
    if (closed) fail('common.unavailable');
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
  async function message(operation, request) {
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
  async function begin(binding, execution, scopes, audience) {
    if (
      execution.signal?.aborted ||
      executionSession(ctx, execution) !== binding.sessionId
    )
      fail();
    let task = tasks.get(execution);
    if (!task) {
      task = { id: randomUUID(), binding, execution, tokens: new Set() };
      tasks.set(execution, task);
    }
    same(task.binding, binding);
    try {
      const result = await call(
        'beginTask',
        {
          binding,
          taskId: task.id,
          scopes,
          audience,
        },
        execution.signal,
      );
      if (execution.signal?.aborted) fail();
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
      await finish(binding, execution).catch(() => {});
      throw error;
    }
  }
  async function finish(binding, execution) {
    const task = tasks.get(execution);
    if (!task) return;
    same(task.binding, binding);
    tasks.delete(execution);
    for (const token of task.tokens) capabilities.delete(token);
    await call('finishTask', { binding, taskId: task.id });
  }
  const removeRequest = async (agent) => {
    const request = requests.get(agent);
    if (!request) return;
    requests.delete(agent);
    await Promise.allSettled(
      [...request.tasks.values()].map((execution) =>
        finish(request.binding, execution),
      ),
    );
  };
  const disposers = [
    ctx.on('agent/request', async (payload, next) => {
      const { agent, signal, turn, step } = payload;
      if (
        ctx.agents.get(agent.id) !== agent ||
        ctx.sessions.get(agent.session.id) !== agent.session
      )
        fail();
      const prior = requests.get(agent);
      if (prior?.turn !== turn) await removeRequest(agent);
      const binding = await lookup(agent.session.id);
      const row = requests.get(agent) ?? { binding, turn, tasks: new Map() };
      same(row.binding, binding);
      row.step = step;
      row.signal = signal;
      row.callId = randomUUID();
      requests.set(agent, row);
      return next();
    }),
    ctx.on('agent/status', ({ agent, status }) => {
      if (status === 'idle') void removeRequest(agent).catch(() => {});
    }),
    ctx.on('agent/disposed', ({ agent }) => {
      void removeRequest(agent).catch(() => {});
    }),
  ];
  const ports = {
    options,
    config: () => settings().enterprise,
    messageTrustConfig: () => settings().messageTrust,
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
          setup: async () => {
            const current = await lookup(value.id);
            same(current, binding);
            await resolve(placementBinding(current), current.platformSessionId);
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
    issueTask: (binding, execution, scopes) =>
      begin(binding, execution, scopes, 'auriga.shared.enterprise'),
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
        same(current, task.binding);
        await resolve(placementBinding(current), current.platformSessionId);
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
        const current = requests.get(agent);
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
        if (!execution) {
          execution = { agent, signal: current.signal };
          current.tasks.set(purposeKey, execution);
          await begin(binding, execution, [purpose.scope], purpose.audience);
        }
        return { taskId: tasks.get(execution).id, callId: current.callId };
      },
      async issue(binding, taskId, audience, scopes, signal) {
        const task = [...new Set(capabilities.values())].find(
          (value) => value.id === taskId,
        );
        if (
          !task ||
          task.execution.signal?.aborted ||
          executionSession(ctx, task.execution) !== binding.sessionId
        )
          fail();
        same(task.binding, binding);
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
      model: ({ signal, ...body }) => bridge.stream('model', body, signal),
      send: (request) => message('send', request),
      receive: (request) => message('receive', request),
    },
    disconnect(revocation) {
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
