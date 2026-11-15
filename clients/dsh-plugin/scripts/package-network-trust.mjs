import { readFile, mkdir, writeFile } from 'node:fs/promises';

/** Copy the canonical contract as a release resource. This does not modify K's
 * generated models; published plugins can validate without a repository tree. */
export async function packageNetworkTrust(
  output = new URL('../ui/', import.meta.url),
) {
  const source = await readFile(
    new URL(
      '../../../contracts/network/auto-renewal-v2.schema.json',
      import.meta.url,
    ),
  );
  await mkdir(output, { recursive: true });
  await writeFile(
    new URL('network-auto-renewal-v2.schema.json', output),
    source,
  );
}
