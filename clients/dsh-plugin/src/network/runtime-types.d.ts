import type { LedgerIdentity } from './host/ledger.js';
import type { HostPort, NetworkPort } from './host/adapter.js';
import type { Options as ConsentOptions } from './consent/types.js';
import type { TransportOptions } from './transport/nats-transport.js';
import type {
  SignedKeyset,
  PublicKey,
} from '../generated/network/keyset-types.js';
import type { ConnectNetwork } from '../generated/connect-config.js';

/** Validated, detached installation pins; never a browser/SDK preference. */
export type NetworkInstallation = Readonly<Omit<ConnectNetwork, 'roots'>> & {
  readonly roots: ReadonlyArray<Readonly<ConnectNetwork['roots'][number]>>;
};
export type NetworkProvider = FullNetworkProvider | DeviceNetworkProvider;
export interface FullNetworkProvider {
  kind?: undefined;
  ports: NetworkPorts;
  prepare(input: {
    accessToken: string;
    signal: AbortSignal;
    installation?: NetworkInstallation;
  }): Promise<PreparedNetwork>;
}

/** Installation-owned ports, never accepted from HTTP bodies, model parameters or downlink hints.
 * The complete Cordis aurigaNetwork provider supplies these business/host ports. */
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
    intakes: Record<string, ConsentPublication>,
    signal: AbortSignal,
  ): Promise<void>;
}
/** Minimal, batch-related metadata only. Never include previews, source proofs or raw frames. */
export interface ConsentPublication {
  requestId: string;
  digest: string;
  recipientIds: string[];
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
export interface FullRuntimeOptions {
  kind?: undefined;
  directory?: string;
  prepared: PreparedNetwork;
  ports: NetworkPorts;
  ctx: any;
  getAccess(): Promise<string>;
  authorizeLocal(context: unknown): Promise<boolean>;
  now?: () => number;
  onRevoked?: () => void;
  onClosed?: (reason: string) => void;
  stores?: Record<string, any>;
  transportFactory?: (options: TransportOptions) => any;
  consentTiming?: Pick<
    ConsentOptions,
    'responseDelayMs' | 'callbackTimeoutMs' | 'actionTimeoutMs'
  >;
}
export interface DeviceRegistration {
  tenant: string;
  realm: string;
  node: string;
  device: string;
  systemAddress: string;
  created: boolean;
}
export interface PreparedConnection {
  kind: 'device-connection';
  url: string;
  roots: NetworkInstallation['roots'];
  submit?(
    request: import('../generated/network/SubmitRequest.js').SubmitRequest,
    signal: AbortSignal,
  ): Promise<Uint8Array>;
  register(signal: AbortSignal): Promise<DeviceRegistration>;
  getTicket(
    signal: AbortSignal,
  ): Promise<import('./transport/nats-transport.js').Ticket>;
  refresh(
    trust: Awaited<
      ReturnType<typeof import('./trust/store.js').openTrustStore>
    >,
    signal: AbortSignal,
  ): Promise<void>;
}
export interface DeviceNetworkProvider {
  kind: 'device-connection';
  prepare(input: {
    accessToken?: string;
    signal: AbortSignal;
    installation?: NetworkInstallation;
  }): Promise<PreparedConnection>;
}
export type ConnectionRuntimeOptions = Omit<
  FullRuntimeOptions,
  'kind' | 'prepared' | 'ports' | 'consentTiming'
> & {
  kind: 'device-connection';
  prepared: PreparedConnection;
  signal?: AbortSignal;
};
export type RuntimeOptions = FullRuntimeOptions | ConnectionRuntimeOptions;
