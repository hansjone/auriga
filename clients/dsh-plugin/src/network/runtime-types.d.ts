import type { LedgerIdentity } from './host/ledger.js';
import type { HostPort, NetworkPort } from './host/adapter.js';
import type { Options as ConsentOptions } from './consent/types.js';
import type { TransportOptions } from './transport/nats-transport.js';
import type {
  SignedKeyset,
  PublicKey,
} from '../generated/network/keyset-types.js';

/** Installation-owned ports, never accepted from HTTP bodies, model parameters or downlink hints.
 * IAM register/prepare and private HTTPS submission have no public route yet.
 * The Cordis aurigaNetwork provider supplies these ports using native credentials. */
export interface NetworkPorts {
  host: HostPort;
  network: NetworkPort;
  prepareTicket(input: {
    accessToken: string;
    identity: LedgerIdentity;
    signal: AbortSignal;
    reason: string;
    attempt: number;
  }): Promise<{
    tenant: string;
    node: string;
    device: string;
    ticket: string;
    expiresAtMs: number;
  }>;
  refreshKeys?(
    hint: { realm: string; minimumVersion: string; kid: string },
    signal: AbortSignal,
  ): Promise<SignedKeyset>;
  resolveSource: ConsentOptions['resolveSource'];
  organization: ConsentOptions['organization'];
  organizationPolicy(): Promise<import('../consent-ui/adapter.js').Policy>;
  sourceFor: Parameters<
    typeof import('./tools/send.js').createAgentSendTool
  >[0]['sourceFor'];
  resolveTarget: Parameters<
    typeof import('./tools/send.js').createAgentSendTool
  >[0]['resolveTarget'];
  releaseResult(startedAt: number): Promise<void>;
  submit(
    request: import('../generated/network/SubmitRequest.js').SubmitRequest,
    signal: AbortSignal,
  ): Promise<unknown>;
  reconcileSubmission?(
    request: import('../generated/network/SubmitRequest.js').SubmitRequest,
    signal: AbortSignal,
  ): Promise<boolean>;
  publishConsent(
    events: import('./consent/types.js').Event[],
    intakes: Record<string, unknown>,
    signal: AbortSignal,
  ): Promise<void>;
}
export interface PreparedNetwork {
  identity: LedgerIdentity;
  url: string;
  roots: Pick<PublicKey, 'kid' | 'public_key_spki'>[];
  keyset: SignedKeyset;
  /** Only an authoritative first-install registration may initialize a missing ledger. */
  initialize?: boolean;
  tls?: { ca?: string | Buffer | Array<string | Buffer> };
  sendRateLimit?: {
    messages: number;
    localMessages?: number;
    windowMs: number;
  };
}
export interface RuntimeOptions {
  directory?: string;
  prepared: PreparedNetwork;
  ports: NetworkPorts;
  ctx: any;
  getAccess(): Promise<string>;
  authorizeLocal(context: unknown): Promise<boolean>;
  now?: () => number;
  onRevoked?: () => void;
  stores?: Record<string, any>;
  transportFactory?: (options: TransportOptions) => any;
  consentTiming?: Pick<
    ConsentOptions,
    'responseDelayMs' | 'callbackTimeoutMs' | 'actionTimeoutMs'
  >;
}
