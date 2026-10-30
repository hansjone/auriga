// Only model responses are synthetic. Identity, native tools, transport and mail stay real.
import { createServer } from 'node:http';
import { Buffer } from 'node:buffer';
import { setImmediate } from 'node:timers';
import { randomUUID } from 'node:crypto';
import { pathToFileURL } from 'node:url';

function fail(code) {
  throw Object.assign(new Error(code), { code });
}
const text = (message) =>
  typeof message?.content === 'string'
    ? message.content
    : Array.isArray(message?.content)
      ? message.content
          .filter((part) => part.type === 'text')
          .map((part) => part.text)
          .join('\n')
      : '';

function toolResult(content) {
  let value = content;
  for (let i = 0; i < 6; i++) {
    if (typeof value === 'string') {
      try {
        value = JSON.parse(value);
      } catch {
        break;
      }
    } else if (value?.status) {
      return {
        status: value.status,
        ...(typeof value.address === 'string' &&
        /^aur1:[0-9a-f]+\.[0-9a-f]+\.[0-9a-f]+$/.test(value.address)
          ? { address: value.address }
          : {}),
        ...(typeof value.code === 'string' &&
        /^network\.[a-z_]+$/.test(value.code)
          ? { code: value.code }
          : {}),
      };
    } else if (Array.isArray(value?.content)) value = text(value);
    else if (value?.result) value = value.result;
    else break;
  }
  return { status: 'unrecognized_result' };
}

export class FixtureState {
  constructor(runId) {
    if (!/^[A-Za-z0-9-]{1,80}$/.test(runId)) fail('invalid_run_id');
    this.runId = runId;
    this.plans = [];
    this.expected = [];
    this.receipts = [];
    this.requests = 0;
    this.failures = [];
    this.modelRequests = [];
  }
  marker(label) {
    if (!/^[a-z0-9_-]{1,40}$/.test(label)) fail('invalid_label');
    return `CLOUD_DESK_${this.runId}_${label}`;
  }
  plan({
    side,
    label,
    tool,
    args,
    trigger = this.marker(label),
    external = false,
  }) {
    if (
      !['cloud', 'desktop'].includes(side) ||
      !['auriga_network_join', 'auriga_send_message'].includes(tool)
    )
      fail('invalid_plan');
    if (this.plans.some((p) => p.label === label)) fail('duplicate_plan');
    this.plans.push({
      side,
      label,
      tool,
      args,
      trigger,
      external,
      issued: false,
      result: null,
    });
    return this.marker(label);
  }
  expect(side, label) {
    if (!['cloud', 'desktop'].includes(side)) fail('invalid_side');
    const marker = this.marker(label);
    if (!this.expected.some((p) => p.side === side && p.marker === marker))
      this.expected.push({ side, marker });
    return marker;
  }
  respond(side, body) {
    if (++this.requests > 256) fail('fixture_budget_exhausted');
    if (!Array.isArray(body.messages)) fail('invalid_model_payload');
    const messages = body.messages;
    if (this.modelRequests.length < 16) {
      const user = messages.findLast((message) => message.role === 'user');
      const content = text(user);
      const at = content.indexOf('CLOUD_DESK_' + this.runId + '_');
      this.modelRequests.push({
        side,
        maxTokens: body.max_tokens ?? body.max_completion_tokens ?? null,
        marker: at < 0 ? 'absent' : at === 0 ? 'start' : 'embedded',
        contentTypes: Array.isArray(user?.content)
          ? [
              ...new Set(
                user.content
                  .map((part) => part.type)
                  .filter((type) => /^[a-z_]{1,40}$/.test(type ?? '')),
              ),
            ]
          : ['string'],
        toolNames: (body.tools ?? [])
          .map((tool) => tool.function?.name)
          .filter((name) => /^[A-Za-z0-9_-]{1,80}$/.test(name ?? '')),
      });
    }
    // This fixture's Agent model is fixed at 4096 tokens; native auxiliary
    // title calls have a separate 64-token text-only budget. Their quoted
    // conversation is never a command, a tool result, or a receipt.
    const maxTokens = body.max_tokens ?? body.max_completion_tokens;
    if (Number.isInteger(maxTokens) && maxTokens <= 64 && !body.tools?.length)
      return { content: 'Cloud desktop acceptance' };
    for (const plan of this.plans.filter(
      (p) => p.side === side && p.issued && !p.result,
    )) {
      const result = messages.findLast(
        (m) => m.role === 'tool' && m.tool_call_id === plan.callId,
      );
      if (result) plan.result = toolResult(text(result));
    }
    for (const message of messages.filter((m) => m.role === 'user')) {
      const content = text(message);
      const id = content.match(
        /^\[Auriga external message ([A-Za-z0-9-]+)\]\r?\n/,
      )?.[1];
      if (!id) continue;
      for (const expected of this.expected.filter(
        (p) => p.side === side && content.includes(p.marker),
      )) {
        const old = this.receipts.find(
          (r) => r.side === side && r.marker === expected.marker,
        );
        if (old && old.messageId !== id) fail('duplicate_network_message');
        if (!old)
          this.receipts.push({
            ...expected,
            messageId: id,
            via: 'native-model-input',
          });
      }
    }
    const last = text(
      messages.findLast(
        (m) =>
          m.role === 'user' &&
          text(m).includes('CLOUD_DESK_' + this.runId + '_'),
      ),
    );
    const isExternal = /^\[Auriga external message [A-Za-z0-9-]+\]\r?\n/.test(
      last,
    );
    const plan = this.plans.find(
      (p) =>
        p.side === side &&
        last.includes(p.trigger) &&
        p.external === isExternal,
    );
    if (!plan || plan.issued)
      return { content: 'Cloud/desktop native tool check acknowledged.' };
    const names = (body.tools ?? [])
      .map((t) => t.function?.name)
      .filter((name) => /^[A-Za-z0-9_-]{1,80}$/.test(name ?? ''))
      .slice(0, 128);
    const native = names.includes(plan.tool);
    if (!native && !names.includes('run_code')) {
      this.failures.push({
        side,
        label: plan.label,
        code: 'native_tool_unavailable',
        advertisedTools: names,
      });
      fail('native_tool_unavailable');
    }
    plan.issued = true;
    plan.callId = 'call_' + randomUUID().replaceAll('-', '');
    return {
      tool_calls: [
        {
          index: 0,
          id: plan.callId,
          type: 'function',
          function: {
            name: native ? plan.tool : 'run_code',
            arguments: JSON.stringify(
              native
                ? plan.args
                : {
                    code: `console.log(JSON.stringify(await tools.${plan.tool}(${JSON.stringify(plan.args)})))`,
                    description: '云端与桌面真实工具验收',
                  },
            ),
          },
        },
      ],
    };
  }
  snapshot() {
    return globalThis.structuredClone({
      runId: this.runId,
      requests: this.requests,
      tools: this.plans.map(({ side, label, tool, issued, result }) => ({
        side,
        label,
        tool,
        issued,
        result,
      })),
      receipts: this.receipts,
      failures: this.failures,
      modelRequests: this.modelRequests,
    });
  }
}

async function jsonBody(req) {
  const chunks = [];
  let size = 0;
  for await (const chunk of req) {
    if ((size += chunk.length) > 8 * 1024 * 1024) fail('body_too_large');
    chunks.push(chunk);
  }
  return JSON.parse(Buffer.concat(chunks).toString('utf8'));
}

export async function startFixture({
  host = '127.0.0.1',
  runId = randomUUID(),
} = {}) {
  const state = new FixtureState(runId);
  const keys = {
    cloud: randomUUID(),
    desktop: randomUUID(),
    control: randomUUID(),
  };
  const server = createServer(async (req, res) => {
    const send = (status, body) => {
      res.writeHead(status, {
        'content-type': 'application/json',
        'cache-control': 'no-store',
      });
      res.end(JSON.stringify(body));
    };
    try {
      if (req.url === `/control/${keys.control}` && req.method === 'POST') {
        const { op, value } = await jsonBody(req);
        if (op === 'plan') return send(200, { marker: state.plan(value) });
        if (op === 'expect')
          return send(200, { marker: state.expect(value.side, value.label) });
        if (op === 'state') return send(200, state.snapshot());
        if (op === 'stop') {
          send(200, { stopped: true });
          setImmediate(() => server.close());
          return;
        }
        return send(400, { code: 'invalid_control_operation' });
      }
      const side = ['cloud', 'desktop'].find((s) =>
        req.url?.startsWith(`/${s}/${keys[s]}/v1/`),
      );
      if (!side) return send(404, { code: 'not_found' });
      if (req.method === 'GET' && req.url.endsWith('/models'))
        return send(200, {
          object: 'list',
          data: [
            {
              id: 'cloud-desk-fixture',
              object: 'model',
              owned_by: 'local-test-only',
            },
          ],
        });
      if (req.method !== 'POST' || !req.url.endsWith('/chat/completions'))
        return send(404, { code: 'not_found' });
      const body = await jsonBody(req);
      const choice = state.respond(side, body);
      const base = {
        id: 'chatcmpl-cloud-desk',
        created: Math.floor(Date.now() / 1000),
        model: body.model,
      };
      const finish = choice.tool_calls ? 'tool_calls' : 'stop';
      if (body.stream) {
        res.writeHead(200, {
          'content-type': 'text/event-stream',
          'cache-control': 'no-store',
        });
        res.write(
          'data: ' +
            JSON.stringify({
              ...base,
              object: 'chat.completion.chunk',
              choices: [
                {
                  index: 0,
                  delta: { role: 'assistant', ...choice },
                  finish_reason: null,
                },
              ],
            }) +
            '\n\n',
        );
        res.write(
          'data: ' +
            JSON.stringify({
              ...base,
              object: 'chat.completion.chunk',
              choices: [{ index: 0, delta: {}, finish_reason: finish }],
            }) +
            '\n\n',
        );
        res.end('data: [DONE]\n\n');
      } else
        send(200, {
          ...base,
          object: 'chat.completion',
          choices: [
            {
              index: 0,
              message: { role: 'assistant', ...choice },
              finish_reason: finish,
            },
          ],
        });
    } catch (error) {
      send(500, {
        error: {
          type: 'local_fixture_error',
          code: /^[a-z_]+$/.test(error.code ?? '')
            ? error.code
            : 'invalid_request',
        },
      });
    }
  });
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, host, resolve);
  });
  const port = server.address().port;
  const origin = `http://127.0.0.1:${port}`;
  return {
    state,
    server,
    port,
    origin,
    keys,
    urls: {
      cloud: `${origin}/cloud/${keys.cloud}/v1`,
      desktop: `${origin}/desktop/${keys.desktop}/v1`,
    },
    async close() {
      server.closeAllConnections();
      await new Promise((resolve) => server.close(resolve));
    },
  };
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  if (process.argv.slice(2).join(' ') !== '--serve')
    throw new Error('Use --serve');
  const fixture = await startFixture({
    host: process.env.AURIGA_CLOUD_DESK_FIXTURE_BIND ?? '127.0.0.1',
    runId: process.env.AURIGA_CLOUD_DESK_RUN,
  });
  // Captured only by the owning runner; endpoint capabilities never enter its report.
  process.stdout.write(
    JSON.stringify({ port: fixture.port, keys: fixture.keys }) + '\n',
  );
}
