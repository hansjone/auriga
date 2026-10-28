import type { SubmitRequest } from '../../generated/network/SubmitRequest.js';

export interface Submission {
  request: SubmitRequest;
  bindingId: string;
  sourceSnapshot: {
    binding: import('../host/ledger.js').Binding;
    registration: import('../host/ledger.js').LocalRegistration;
    bindingRevision: string | null;
  } | null;
  expiresAt: number;
  result: string | null;
}
export interface Intake {
  delivery: any;
  recipientId: string;
  local: string;
  bindingId: string;
  hostSessionId: string;
  attemptId: string;
  epoch: string;
  stateVersion: string;
  stage:
    | 'received'
    | 'admit_requested'
    | 'admitted'
    | 'invoking'
    | 'accepted'
    | 'committed';
  admission: any;
  receipt: any;
}
export interface DurableState {
  producerEpoch: string;
  submissions: Record<string, Submission>;
  receive: Record<string, { enabled: boolean; version: string; pending: any }>;
  inbound: Record<string, Intake>;
  /** Full verified terminal envelopes, keyed by mail_seq; never host receipts. */
  terminal?: Record<string, any>;
  resumeCursor: string;
}
