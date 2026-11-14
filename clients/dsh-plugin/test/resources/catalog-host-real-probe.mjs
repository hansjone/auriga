import { writeFile } from 'node:fs/promises';
import { setImmediate } from 'node:timers';
import { catalogHost } from '../../src/resources/catalog-host.js';
export const name = 'auriga-catalog-host-real-probe';
export const inject = ['skills', 'sessionController'];
export function apply(ctx) {
  setImmediate(
    () =>
      void (async () => {
        const host = catalogHost(ctx),
          results = [];
        for (const preset of ['cordis', 'standard', 'ptc'])
          results.push({
            preset,
            ...(await host.observe('p1c-collision', preset)),
          });
        return { ok: true, results };
      })().then(
        (value) =>
          writeFile(process.env.P1C_RESULT, JSON.stringify(value, null, 2)),
        (error) =>
          writeFile(
            process.env.P1C_RESULT,
            JSON.stringify({
              ok: false,
              error: String(error),
              stack: error.stack,
            }),
          ),
      ),
  );
}
