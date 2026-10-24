// Opt-in subprocess probe: only the caller's fresh private fixture is opened.
import { pathToFileURL } from 'node:url';
import { join } from 'node:path';
import { createOwnerAdmission } from '../src/identity/owner-admission.js';

const [packageDir, fixtureHome, operation, rawCounter] = process.argv.slice(2);
const { Context, Service } = await import(
  pathToFileURL(
    join(packageDir, 'node_modules/@deepseek-ai/cordis/lib/index.js'),
  ).href
);
const { LocalCredentialProvider } = await import(
  pathToFileURL(
    join(
      packageDir,
      'node_modules/@deepseek-ai/dsh-credentials-local/lib/index.js',
    ),
  ).href
);
const provider = new LocalCredentialProvider(new Context(), {
  dshHome: fixtureHome,
  watch: false,
});
const lifecycle = provider[Service.init]();
const cleanup = (await lifecycle.next()).value;
try {
  await lifecycle.next();
  process.send({ ready: true });
  await new Promise((resolve) => process.once('message', resolve));
  const admission = await createOwnerAdmission({
    credentials: provider,
    owner: { providerId: 'emergency', subject: 'alice' },
    initialize: operation === 'initialize',
  });
  const accepted =
    operation === 'commit'
      ? await admission.commitAdmission({
          providerId: 'emergency',
          profile: { subject: 'alice', mfaPerformed: true },
          emergencyCounter: Number(rawCounter),
        })
      : undefined;
  process.send({
    accepted,
    lastCounter: (await admission.readState()).lastCounter,
  });
} catch {
  // No provider exception or fixture path crosses the subprocess boundary.
  process.send({ failed: true });
  process.exitCode = 1;
} finally {
  await cleanup();
  process.disconnect();
}
