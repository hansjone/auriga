import { existsSync, readFileSync } from 'node:fs';
const shipped = new URL('./schemas/guardian-transitions.json', import.meta.url);
export const guardianProtocol = JSON.parse(
  readFileSync(
    existsSync(shipped)
      ? shipped
      : new URL(
          '../../../contracts/resource/guardian-transitions.json',
          import.meta.url,
        ),
    'utf8',
  ),
);
