import { runAssembly } from './source-assembly.mjs';

if (process.argv.slice(2).join(' ') !== '--run')
  throw new Error(
    'explicit --run required; isolated real PG/Broker/HTTPS are started',
  );
process.env.AURIGA_GATEWAY_SEND = '1';
console.log(JSON.stringify(await runAssembly()));
