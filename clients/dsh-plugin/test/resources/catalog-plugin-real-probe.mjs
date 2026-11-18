import { writeFileSync, readFileSync, existsSync } from 'node:fs';
import { join } from 'node:path';
import { setImmediate } from 'node:timers';
import { setTimeout as delay } from 'node:timers/promises';
import { catalogFixture } from './catalog-fixture.js';
import { catalogService, resourceBroker } from './broker-fixture.js';
import { resourceHost } from '../../src/resources/host.js';
import { hostTime } from '../../src/resources/host-lock.js';

export const name = 'auriga-catalog-plugin-real-probe';
export const inject = ['pluginManager'];
export function apply(ctx) {
  const evidence = process.env.P2B_EVIDENCE;
  const put = (name, value) =>
    writeFileSync(
      join(evidence, name + '.json'),
      JSON.stringify(value, null, 2),
    );
  setImmediate(
    () =>
      void (async () => {
        if (process.env.P2B_PHASE === 'report') {
          const prepared = JSON.parse(
            readFileSync(join(evidence, 'prepared.json'), 'utf8'),
          );
          let row = JSON.parse(
            readFileSync(join(evidence, 'installed.json'), 'utf8'),
          ).installation;
          const identity = {
            tenantId: row.resource.identity.tenantId,
            ownerId: '019a0000-0000-7000-8000-000000000012',
            clientId: row.clientId,
          };
          const runtime = {
            config: { platformOrigin: 'http://platform.example.test' },
            vault: {
              snapshot: () => ({
                credential: {
                  claims: {
                    tid: identity.tenantId,
                    sub: identity.ownerId,
                    did: identity.clientId,
                  },
                },
              }),
            },
            getAccess: async () => 'fixture-token',
          };
          resourceBroker(runtime, async (url, options) => {
            if (url.pathname.endsWith('/installations'))
              return Response.json({ items: [row] });
            if (url.pathname.endsWith('/installation-reports')) {
              const report = JSON.parse(options.body);
              put('terminal-report', report);
              row = { ...row, state: report.state, report };
              return Response.json(row);
            }
            throw new Error('unexpected recovery request');
          });
          const service = catalogService({
            runtime,
            current: () => runtime,
            home: process.env.DSH_HOME,
            privateSkills: {},
            host: {},
            pluginHost: resourceHost(ctx),
            roots: async () => prepared.trust,
          });
          await service.list();
          return;
        }
        const f = catalogFixture({ after() {} }, undefined, {
          home: process.env.DSH_HOME,
          kind: 'plugin',
          time: hostTime,
          bundleFiles: {
            'package/cordis.patch.yml':
              '- insert:\n    - id: catalog-managed-example\n      name: "@example/catalog-plugin"\n',
            'package/index.js':
              'exports.name="catalog-managed-example";exports.apply=(ctx)=>{ctx.on("dispose",()=>{});};\n',
          },
        });
        const roots = await f.roots();
        const trust = Object.fromEntries(
          [
            ['release', roots.releaseKeys],
            ['revocation', roots.revocationKeys],
          ].map(([role, keys]) => {
            const [kid, key] = [...keys][0];
            return [
              role,
              {
                kid,
                publicKeySpkiBase64: key
                  .export({ type: 'spki', format: 'der' })
                  .toString('base64'),
              },
            ];
          }),
        );
        const host = resourceHost(ctx);
        const service = catalogService({
          ...f,
          privateSkills: {},
          host: {},
          pluginHost: host,
        });
        const accepted = await service.apply({
          packageId: f.packageId,
          versionId: f.versionId,
          action: 'install',
          revision: 0,
          key: 'real-plugin-install',
        });
        put('prepared', { trust, accepted, home: f.home });
        let installation;
        const deadline = Date.now() + 90000;
        while (Date.now() < deadline) {
          try {
            installation = (await service.list()).items[0];
          } catch (error) {
            if (error.code !== 'EEXIST') throw error;
          }
          if (installation?.state === 'applied') break;
          await delay(500);
        }
        if (installation?.state !== 'applied')
          throw new Error('real plugin never reached applied');
        put('installed', {
          installation,
          observed: await host.observe(f.manifest.plugin.packageName),
        });
        while (!existsSync(join(evidence, 'revoke-now'))) await delay(100);
        f.desktop.acceptRevocations(f.who, [
          Buffer.from(JSON.stringify(f.revoke())),
        ]);
        put('revoked', {
          persisted: true,
          ledger: JSON.parse(
            readFileSync(join(f.directory, 'ledger.json'), 'utf8'),
          ),
        });
      })().catch((error) =>
        put('error', {
          error: String(error),
          stack: error.stack,
          cause: String(error.cause),
          detail: String(error.cause?.cause),
        }),
      ),
  );
}
