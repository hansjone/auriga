// Real public rc.3 schema/Loader/settings round-trip; fake profile and upstream.
import { runNativeModels } from '../../../../../clients/dsh-plugin/test/manual-models-host.mjs';
await runNativeModels(...process.argv.slice(2, 6));
