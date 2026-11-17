// Local visual QA only: real admin routes with isolated, in-memory API fixtures.
// Run from the worktree root: node web/admin/test/resource-upload-visual.mjs
// Rework-only QA: set AURIGA_UPLOAD_REWORK=1 to capture notices and draft recovery.
/* global localStorage */
import { Buffer } from 'node:buffer';
import { createServer } from 'vite';
import { chromium } from '../../../e2e/node_modules/playwright/index.mjs';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { gzipSync } from 'node:zlib';
import assert from 'node:assert/strict';
import net from 'node:net';
const output = resolve(
  'web/admin/node_modules/.cache/resource-upload-evidence',
);
const rework = process.env.AURIGA_UPLOAD_REWORK === '1';
const prefix = rework ? 'rework-' : '';
await mkdir(output, { recursive: true });
const skillFolder = resolve(output, 'example-skill');
await mkdir(skillFolder, { recursive: true });
await writeFile(
  resolve(skillFolder, 'SKILL.md'),
  '---\nname: example-skill\ndescription: Summarize team documentation with clear next steps.\nversion: 1.2.0\n---\nUse the selected documents to prepare a concise summary.',
);
function tar(entries) {
  const blocks = [];
  for (const [path, body] of Object.entries(entries)) {
    const bytes = Buffer.from(body),
      h = Buffer.alloc(512);
    h.write(path);
    h.write('0000644\0', 100);
    h.write('0000000\0', 108);
    h.write('0000000\0', 116);
    h.write(bytes.length.toString(8).padStart(11, '0') + '\0', 124);
    h.write('00000000000\0', 136);
    h.fill(32, 148, 156);
    h.write('0', 156);
    h.write('ustar\0', 257);
    h.write('00', 263);
    h.write(
      h
        .reduce((s, b) => s + b, 0)
        .toString(8)
        .padStart(6, '0') + '\0 ',
      148,
    );
    blocks.push(h, bytes, Buffer.alloc((512 - (bytes.length % 512)) % 512));
  }
  return gzipSync(Buffer.concat([...blocks, Buffer.alloc(1024)]));
}
const pluginPath = resolve(output, 'example-plugin.tgz');
await writeFile(
  pluginPath,
  tar({
    'package/package.json': JSON.stringify({
      name: '@example/team-summary',
      version: '1.2.0',
      description: 'Team documentation summaries for DSH.',
      author: rework
        ? 'Alice <author@example.com> (https://example.com/alice)'
        : 'Alice',
      license: 'MIT',
      peerDependencies: {
        '@deepseek-ai/dsh': rework ? '0.2.x' : '^0.2.1-rc.3',
      },
      dsh: { bundle: { patch: 'index.js' } },
    }),
    'package/index.js': 'export default {}',
  }),
);
const reservation = net.createServer();
await new Promise((r) => reservation.listen(0, '127.0.0.1', r));
const port = reservation.address().port;
await new Promise((r) => reservation.close(r));
const server = await createServer({
  root: resolve('web/admin'),
  server: { host: '127.0.0.1', port, strictPort: true },
});
let browser;
try {
  await server.listen();
  const origin = server.resolvedUrls.local[0].replace(/\/$/, '');
  browser = await chromium.launch({ headless: true, channel: 'chrome' });
  const errors = [],
    requests = [];
  for (const width of rework ? [1280] : [1280, 1600])
    for (const theme of ['light', 'dark'])
      for (const kind of rework ? ['plugin'] : ['plugin', 'skill'])
        for (const phase of rework
          ? ['directory']
          : ['directory', 'review', 'published', 'distribution']) {
          const context = await browser.newContext({
            viewport: { width, height: 1050 },
            deviceScaleFactor: 1,
          });
          await context.addCookies([
            { name: 'auriga_csrf', value: 'visual-only', url: origin },
          ]);
          await context.addInitScript((theme) => {
            localStorage.setItem('auriga.admin.theme', theme);
          }, theme);
          const page = await context.newPage();
          page.on('pageerror', (e) => {
            errors.push(e.message);
            console.error('PAGE', e.message);
          });
          const manifest = {
            schemaVersion: 1,
            kind,
            name: kind === 'plugin' ? 'team-summary' : 'example-skill',
            version: '1.2.0',
            description: 'Team documentation summaries.',
            author: { displayName: 'Alice' },
            license: 'MIT',
            permissions: { tools: [], outboundHosts: [], dataAccess: [] },
            runtimes: [],
            artifacts: [
              {
                id: 'payload',
                role: 'payload',
                digest: 'sha256:' + 'a'.repeat(64),
                sizeBytes: 512,
                mediaType: 'application/gzip',
                os: 'any',
                arch: 'any',
              },
            ],
            signature: { mode: 'detached', algorithm: 'Ed25519' },
            [kind]:
              kind === 'plugin'
                ? {
                    packageName: '@example/team-summary',
                    dshVersionRange: '^0.2.1-rc.3',
                  }
                : {
                    entry: 'SKILL.md',
                    contentProfile: 'text-only',
                    dshVersionRange: '^0.2.1-rc.3',
                  },
          };
          const pack = {
            id: 'p1',
            tenantId: 'tenant',
            name: manifest.name,
            kind,
            ownerId: 'author',
            visibility: 'tenant',
            groupIds: [],
            revision: 1,
            createdAt: '2026-11-18T00:00:00Z',
            enabled: true,
            defaultVersionId: null,
          };
          const version = {
            id: 'v1',
            packageId: 'p1',
            kind,
            version: '1.2.0',
            digest: 'sha256:' + 'b'.repeat(64),
            manifest,
            state: phase === 'published' ? 'published' : 'draft',
            revision: 2,
            createdAt: '2026-11-18T00:00:00Z',
            risk: {
              level: 'r2',
              minimumLevel: 'r2',
              requiredReviewers: 2,
              requiredSecurityReviewers: 1,
            },
          };
          const review = {
            reviewId: 'r1',
            authorId: 'author',
            version: { ...version, state: 'pending_review' },
            status: 'pending',
            revision: 1,
            decisions: [],
          };
          let rejected = false,
            uploadCount = 0,
            versionReads = 0;
          await page.route('**/api/**', async (route) => {
            const req = route.request(),
              u = new URL(req.url());
            if (!u.pathname.startsWith('/api/')) return route.continue();
            requests.push([req.method(), u.pathname]);
            let body;
            if (u.pathname === '/api/iam/v1/me')
              body = {
                account: { id: 'reviewer', displayName: 'Alice' },
                tenant: { id: 'tenant' },
                session: { id: 'session' },
              };
            else if (u.pathname === '/api/org/v1/me')
              body = {
                member: { id: 'reviewer', displayName: 'Alice' },
                tenant: { id: 'tenant' },
                permissions: [
                  'resource.packages.read',
                  'resource.packages.upload',
                  'resource.versions.submit',
                  'resource.versions.review',
                  'resource.versions.publish',
                  'resource.distributions.manage',
                ],
              };
            else if (
              u.pathname.endsWith('/catalog/packages') &&
              req.method() === 'GET'
            )
              body = { items: [pack] };
            else if (
              u.pathname.endsWith('/catalog/packages') &&
              req.method() === 'POST'
            )
              body = pack;
            else if (u.pathname.endsWith('/catalog/packages/p1')) body = pack;
            else if (u.pathname.endsWith('/versions') && req.method() === 'GET')
              body = { items: [] };
            else if (
              u.pathname.endsWith('/versions') &&
              req.method() === 'POST'
            ) {
              uploadCount++;
              assert.ok(
                req.headers()['content-type'].includes('multipart/form-data'),
              );
              assert.ok(
                req
                  .postDataBuffer()
                  .includes(Buffer.from('auriga-resource.json')),
              );
              body = version;
            } else if (u.pathname.endsWith('/submissions')) {
              if (rework && !rejected) {
                rejected = true;
                return route.fulfill({
                  status: 403,
                  json: {
                    status: 403,
                    code: 'common.forbidden',
                    title: 'Denied',
                  },
                });
              }
              body = review;
            } else if (u.pathname.endsWith('/catalog/review'))
              body = { items: [review] };
            else if (u.pathname.endsWith('/catalog/reviews/r1')) body = review;
            else if (u.pathname.endsWith('/catalog/published'))
              body = { items: [version] };
            else if (u.pathname.endsWith('/versions/v1')) {
              versionReads++;
              body = version;
            } else if (u.pathname.endsWith('/catalog/assignments'))
              body = { items: [] };
            else if (u.pathname.endsWith('/catalog/distributions'))
              body = {
                items: [
                  {
                    distributionId: 'd1',
                    instanceId: 'team-workspace',
                    state: 'applying',
                    selectionRevision: 1,
                    generation: 2,
                    selections: [
                      {
                        identity: {
                          kind,
                          packageId: 'p1',
                          versionId: 'v1',
                          version: '1.2.0',
                        },
                        action: 'install',
                      },
                    ],
                    reports: [],
                  },
                ],
              };
            else
              return route.fulfill({
                status: 404,
                json: {
                  status: 404,
                  code: 'common.not_found',
                  title: u.pathname,
                },
              });
            await route.fulfill({
              status: 200,
              headers: { ETag: '"2"' },
              json: body,
            });
          });
          await page.route('**/resources/**', async (route) => {
            if (route.request().resourceType() !== 'document')
              return route.continue();
            const response = await route.fetch();
            await route.fulfill({
              response,
              body: (await response.text()).replace(
                '<head>',
                '<head><meta name="auriga-access-mode" content="domain">',
              ),
            });
          });
          const path =
            '/resources/' +
            (kind === 'plugin' ? 'plugins' : 'skills') +
            (phase === 'directory' ? '' : '/' + phase);
          await page.goto(origin + path);
          await page.locator('.catalog-list-toolbar').waitFor();
          await page
            .getByRole('status')
            .filter({ hasText: '加载中' })
            .waitFor({ state: 'hidden' });
          if (phase === 'directory') {
            await page
              .locator(
                kind === 'plugin'
                  ? '[data-testid="catalog-artifacts"]'
                  : '[data-testid="catalog-folder"]',
              )
              .setInputFiles(kind === 'plugin' ? pluginPath : skillFolder);
            await page
              .locator('[data-testid="catalog-upload-preview"]')
              .waitFor();
            if (rework) {
              await page
                .locator('[data-testid="upload-preview-notices"]')
                .waitFor();
              assert.equal(
                await page.locator('[data-testid="upload-range"]').inputValue(),
                '^0.2.1-rc.3',
              );
              assert.equal(
                await page
                  .locator('[data-testid="upload-author"]')
                  .inputValue(),
                'Alice',
              );
            }
          }
          assert.equal(
            await page.locator('.admin-shell').getAttribute('data-admin-theme'),
            theme,
          );
          assert.equal(
            await page
              .locator('.resource-center nav [aria-current="page"]')
              .count(),
            1,
          );
          assert.ok(
            !(await page
              .locator('.catalog-page')
              .innerText()
              .then((t) => /parse_\w+|uploadTitle/.test(t))),
          );
          const overflow = await page
            .locator('.catalog-page')
            .evaluate((el) => el.scrollWidth > el.clientWidth);
          assert.equal(overflow, false);
          await page.screenshot({
            path: resolve(
              output,
              `${prefix}${kind}-${phase}-${width}-${theme}.png`,
            ),
            fullPage: true,
          });
          if (phase === 'directory') {
            await page.locator('[data-testid="permissions-confirm"]').check();
            await page
              .locator('[data-testid="upload-submit"]')
              .scrollIntoViewIfNeeded();
            await page.screenshot({
              path: resolve(
                output,
                `${prefix}${kind}-permissions-${width}-${theme}.png`,
              ),
              fullPage: true,
            });
            await page.locator('[data-testid="upload-submit"]').click();
            if (rework) {
              await page.locator('[data-testid="upload-open-draft"]').waitFor();
              assert.equal(
                await page.locator('[data-testid="catalog-upload"]').count(),
                0,
              );
              await page.screenshot({
                path: resolve(output, `rework-draft-rejected-${theme}.png`),
                fullPage: true,
              });
              await page.locator('[data-testid="upload-open-draft"]').click();
              await page.locator('[data-testid="catalog-reason"]').waitFor();
              await page
                .locator('[data-testid="catalog-reason"]')
                .fill(
                  'Continue the saved draft after permissions are restored',
                );
              assert.equal(
                await page
                  .locator('.catalog-detail')
                  .getByRole('button', { name: '送审', exact: true })
                  .isEnabled(),
                true,
              );
              assert.equal(uploadCount, 1);
              assert.equal(versionReads, 1);
              await page.screenshot({
                path: resolve(output, `rework-draft-details-${theme}.png`),
                fullPage: true,
              });
            } else
              await page
                .getByRole('status')
                .filter({ hasText: '已上传并送审' })
                .waitFor();
          }
          await context.close();
        }
  assert.deepEqual(errors, []);
  await writeFile(
    resolve(output, rework ? 'rework-result.json' : 'visual-result.json'),
    JSON.stringify(
      {
        screenshots: rework ? 8 : 32,
        errors,
        writeRequests: requests.filter((r) => r[0] === 'POST'),
        output,
      },
      null,
      2,
    ),
  );
  console.log(JSON.stringify({ screenshots: rework ? 8 : 32, errors, output }));
} finally {
  await browser?.close();
  await server.close();
}
