import {readFileSync} from 'node:fs';
import {confirmReceipt} from '../../../../../contracts/shared-dsh/ports.mjs';
const data=JSON.parse(readFileSync(new URL('shared-pool-receipts-v1.json',import.meta.url)));
for(const {intent,receipt} of data.cases){if(confirmReceipt(intent,receipt)!=='confirmed')throw new Error(intent.action);}
console.log('K v1: five receipt goldens confirmed');
