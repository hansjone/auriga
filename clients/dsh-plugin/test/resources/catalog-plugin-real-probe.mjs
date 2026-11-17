import { writeFileSync, readFileSync, existsSync } from 'node:fs';
import { join } from 'node:path';
import { setImmediate } from 'node:timers';
import { setTimeout as delay } from 'node:timers/promises';
import { catalogFixture } from './catalog-fixture.js';
import { catalogService } from './broker-fixture.js';
import { resourceHost } from '../../src/resources/host.js';

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
        const f = catalogFixture({ after() {} }, undefined, {
          home: process.env.DSH_HOME,
          kind: 'plugin',
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
