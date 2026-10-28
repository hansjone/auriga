import assert from 'node:assert/strict';
import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
const run = promisify(execFile);

// Test-only entry point and read-only observation. Production routing, TLS,
// CONNECT callout, IAM and READY remain their original implementations.
export async function buildEdgeWSS(root, output) {
  await mkdir(output, { recursive: true });
  const main = join(root, 'infra/edge/main.go');
  const source = await readFile(main, 'utf8');
  const needle = 'run(os.Args[1:], os.Stdout, os.Stderr)';
  assert.equal(source.split(needle).length, 2);
  const replacement = join(output, 'edge-main.go');
  await writeFile(replacement, source.replace(needle, 'edgeWSSRun()'));
  const overlay = join(output, 'edge-overlay.json');
  await writeFile(
    overlay,
    JSON.stringify({
      Replace: {
        [main]: replacement,
        [join(root, 'infra/edge/edge_wss_assembly.go')]: join(
          root,
          'e2e/network/support/edge-wss-entry.go',
        ),
      },
    }),
  );
  const edge = join(output, process.platform === 'win32' ? 'edge.exe' : 'edge');
  await run('go', ['build', '-overlay', overlay, '-o', edge, '.'], {
    cwd: join(root, 'infra/edge'),
    windowsHide: true,
  });
  const relay = join(
    root,
    'infra/ai-gateway/cmd/network-source-assembly/relay.go',
  );
  const relaySource = await readFile(relay, 'utf8');
  const marker = '\tif bounded {\n\t\tevent["remainingMillisAtStart"]';
  const normalized = relaySource.replaceAll('\r\n', '\n');
  assert.equal(normalized.split(marker).length, 2);
  const observed = join(output, 'relay.go');
  await writeFile(
    observed,
    normalized.replace(
      marker,
      '\tif q, ok := req.(*iam.RedeemNetworkTicketRequest); ok { event["ingress"] = q.GetIngress() }\n' +
        marker,
    ),
  );
  const runtime = join(root, 'infra/ai-gateway/internal/network/runtime.go');
  const runtimeSource = await readFile(runtime, 'utf8');
  const decision = 'OnDecision: r.redeemer.onDecision';
  assert.equal(runtimeSource.split(decision).length, 2);
  const observedRuntime = join(output, 'runtime.go');
  await writeFile(
    observedRuntime,
    runtimeSource.replace(
      decision,
      'OnDecision: func(a Attempt, c *jwt.UserClaims, e error) { r.redeemer.onDecision(a, c, e); _ = json.NewEncoder(os.Stdout).Encode(map[string]any{"stage":"callout-decision", "clientType":a.ClientType, "serverId":a.ServerID, "clientId":a.ClientID, "allowed":e == nil && c != nil}) }',
    ),
  );
  const gatewayOverlay = join(output, 'gateway-overlay.json');
  await writeFile(
    gatewayOverlay,
    JSON.stringify({
      Replace: { [relay]: observed, [runtime]: observedRuntime },
    }),
  );
  const gateway = join(
    output,
    process.platform === 'win32' ? 'gateway.exe' : 'gateway',
  );
  await run(
    'go',
    [
      'build',
      '-overlay',
      gatewayOverlay,
      '-o',
      gateway,
      './cmd/network-source-assembly',
    ],
    { cwd: join(root, 'infra/ai-gateway'), windowsHide: true },
  );
  return { edge, gateway };
}
if (process.argv[2] === '--build')
  await buildEdgeWSS(resolve('.'), resolve(process.argv[3]));
