/** Local plugin types, not a wire contract. All ports are trusted host code. */
export type Layer = 'conversation' | 'execution';
export type DefaultMode = 'ask_each' | 'same_org' | 'allowlist' | 'deny_all';
export type Choice = 'allow_once' | 'allow_source' | 'deny' | 'block';
export type SourceScope = 'source_node' | 'source_user';
export type Status = 'pending' | 'allowed' | 'denied' | 'expired' | 'revoked';
export interface Source {
  tenant: string;
  realm: string;
  node: string;
}
export interface VerifiedSource extends Source {
  userId: string;
  ownershipRevision: string;
}
export interface Identity extends Source {
  nodeIncarnation: string;
}
export interface Organization {
  available: boolean;
  revision: string;
  sameOrganization: boolean;
  conversation: 'allow' | 'ask_each' | 'deny_all';
  execution: 'allow' | 'ask_each' | 'deny_all';
}
/** No preview/body; createdAt/expiresAt/digest/bytes come from verified intake. */
export interface Request {
  layer: Layer;
  requestId: string;
  source: Source;
  digest: string;
  createdAt: number;
  expiresAt: number;
  bytes: number;
}
export interface Rule {
  id: string;
  layer: Layer;
  scope: SourceScope;
  source: VerifiedSource;
  choice: 'allow_source' | 'block';
  policyRevision: number;
}
export interface RecordEntry {
  request: Request;
  source: VerifiedSource;
  status: Status;
  deadline: number;
  policyRevision: number;
  decisionId: string;
  localReason: string;
  manual: boolean;
  organizationRevision: string;
  receivedAt: number;
  terminalAt?: number;
  usedAt?: number;
}
export interface Event {
  id: string;
  kind: 'decision' | 'policy_changed';
  policyRevision: number;
  at: number;
  layer?: Layer;
  requestId?: string;
  status?: 'pending' | 'allowed' | 'denied';
}
export interface Audit {
  at: number;
  action: string;
  policyRevision: number;
  detail: unknown;
}
export interface State {
  version: 1 | 2;
  identity: Identity;
  policyRevision: number;
  lastNow: number;
  defaults: Record<Layer, DefaultMode>;
  rules: Rule[];
  requests: Record<string, RecordEntry>;
  cooldowns: Record<string, number>;
  audit: Audit[];
  outbox: Event[];
  traffic: Record<string, { since: number; count: number }>;
  auditSummary: { dropped: number; digest: string };
}
export interface Limits {
  maxPending: number;
  maxPendingBytes: number;
  maxRecords: number;
  maxSourceRecords: number;
  maxUserRecords: number;
  maxSourceTerminal: number;
  maxUserTerminal: number;
  maxRequestsPerMinute: number;
  terminalRetentionMs: number;
  maxAudit: number;
}
export interface Options {
  /** Separate store/directory from the host ledger, scoped to this installation. */
  store: { open(): Promise<import('../host/store.js').LedgerStoreHandle> };
  identity: Identity;
  resolveSource(source: Source): Promise<VerifiedSource>;
  organization(source: VerifiedSource): Promise<Organization>;
  authorizeLocal(context: unknown): Promise<boolean>;
  now?: () => number;
  callbackTimeoutMs?: number;
  actionTimeoutMs?: number;
  responseDelayMs?: number;
  limits?: Partial<Limits>;
}
