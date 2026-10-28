import { runAssembly } from './source-assembly.mjs';
if (process.argv.slice(2).join(' ') !== '--run')
  throw new Error(
    'explicit --run required; starts isolated real PG/Broker/HTTPS/native services',
  );
process.env.AURIGA_GATEWAY_SEND = '1';
process.env.AURIGA_PLUGIN_SEND = '1';
console.log(JSON.stringify(await runAssembly()));
