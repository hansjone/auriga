import assert from 'node:assert/strict';
import { readFile, writeFile, mkdir, mkdtemp } from 'node:fs/promises';
import { join, delimiter, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { createHash } from 'node:crypto';
import { javaClasspath } from './assembly-process.mjs';

export function replaceOnce(source, before, after) {
  assert.equal(
    source.split(before).length,
    2,
    'exact test-assembly anchor required',
  );
  return source.replace(before, after);
}

// Only copies of existing TEST assembly sources change. No production source
// or existing assertion changes; provenance is saved beside the generated copy.
export async function prepareDual(root, output) {
  const run = promisify(execFile);
  const generated = join(output, 'java');
  const classes = join(output, 'classes');
  await mkdir(generated, { recursive: true });
  await mkdir(classes, { recursive: true });
  const hashes = {};
  const source = async (relative) => {
    const value = (await readFile(join(root, relative), 'utf8')).replaceAll(
      '\r\n',
      '\n',
    );
    hashes[relative] = createHash('sha256').update(value).digest('hex');
    return value;
  };
  let host = await source(
    'services/platform-app/src/test/java/auriga/iam/NetworkSourceJavaHost.java',
  );
  host = replaceOnce(
    host,
    '"https://localhost",',
    'System.getenv("AURIGA_SEND4_ORIGIN"),',
  );
  host = replaceOnce(
    host,
    'new IamGrpcDevices(devices),',
    'new IamGrpcDevices(devices), new auriga.org.OrgGrpcServices.AuthorizationGrpc(org),',
  );
  host = replaceOnce(
    host,
    'List.of(new NetworkMailboxGrpcService(wire))',
    'List.of(new NetworkMailboxGrpcService(wire), DualDshLogin.communication(this))',
  );
  host = replaceOnce(
    host,
    '"https",\n        "https://127.0.0.1:" + web.port()',
    '"https",\n        "http://127.0.0.1:" + web.port()',
  );
  await writeFile(join(generated, 'NetworkSourceJavaHost.java'), host);
  let web = await source(
    'services/platform-app/src/test/java/auriga/iam/NetworkSourceJavaWeb.java',
  );
  web = replaceOnce(
    web,
    '"server.ssl.enabled=true"',
    '"server.ssl.enabled=false"',
  );
  web = replaceOnce(
    web,
    '@EnableAutoConfiguration\n  static class App',
    '@EnableAutoConfiguration\n  @org.springframework.context.annotation.Import(DualDshLogin.class)\n  static class App',
  );
  await writeFile(join(generated, 'NetworkSourceJavaWeb.java'), web);
  const classpath = await javaClasspath(root);
  const javac = process.env.JAVA_HOME
    ? join(
        process.env.JAVA_HOME,
        'bin',
        process.platform === 'win32' ? 'javac.exe' : 'javac',
      )
    : 'javac';
  const args = [
    '-cp',
    classpath,
    '-d',
    classes,
    join(generated, 'NetworkSourceJavaHost.java'),
    join(generated, 'NetworkSourceJavaWeb.java'),
    join(root, 'e2e/network/support/DualDshLogin.java'),
  ];
  const compiled = await run(javac, args, {
    cwd: root,
    windowsHide: true,
    maxBuffer: 8 * 1024 * 1024,
  });
  await writeFile(
    join(output, 'javac.log'),
    JSON.stringify({ command: javac, args, exit: 0 }) +
      '\n' +
      compiled.stdout +
      compiled.stderr,
  );
  const gatewaySource = 'infra/ai-gateway/cmd/network-source-assembly/main.go';
  let gateway = await source(gatewaySource);
  gateway = replaceOnce(
    gateway,
    'defer gateway.Close()',
    'defer gateway.Close()\n closeTeam, err := startDualTeam(ctx, ready, b)\n if err != nil { return err }\n defer func(){ if e := closeTeam(); e != nil { result = e } }()',
  );
  const file = join(output, 'gateway-main.go');
  await writeFile(file, gateway);
  const overlay = join(output, 'go-overlay.json');
  await writeFile(
    overlay,
    JSON.stringify({
      Replace: {
        [join(root, gatewaySource)]: file,
        [join(
          root,
          'infra/ai-gateway/cmd/network-source-assembly/dual_team.go',
        )]: join(root, 'e2e/network/support/dual-team.go'),
      },
    }),
  );
  const executable = join(
    output,
    process.platform === 'win32' ? 'dual-gateway.exe' : 'dual-gateway',
  );
  const built = await run(
    'go',
    [
      'build',
      '-overlay',
      overlay,
      '-o',
      executable,
      './cmd/network-source-assembly',
    ],
    {
      cwd: join(root, 'infra/ai-gateway'),
      windowsHide: true,
      maxBuffer: 8 * 1024 * 1024,
    },
  );
  await writeFile(join(output, 'go-build.log'), built.stdout + built.stderr);
  await writeFile(
    join(output, 'test-assembly-provenance.json'),
    JSON.stringify(
      {
        hashes,
        generatedJava: host,
        generatedWeb: web,
        generatedGateway: gateway,
      },
      null,
      2,
    ),
  );
  return { classpath: classes + delimiter + classpath, executable };
}
if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  assert.equal(process.argv.slice(2).join(' '), '--compile');
  const root = process.cwd(),
    target = join(root, 'e2e/network/target');
  await mkdir(target, { recursive: true });
  const output = await mkdtemp(join(target, 'dual-compile-'));
  await prepareDual(root, output);
  console.log(
    JSON.stringify({
      output,
      scope: 'test assembly compile only; no PG or business acceptance',
    }),
  );
}
