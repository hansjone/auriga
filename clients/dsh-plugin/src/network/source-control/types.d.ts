import type {
  Registration,
  SourceFact,
  SourceGetResult,
  SourceRegister,
  SourceRegisterResult,
  SourceRevokeResult,
} from '../../generated/network/SourceControl.js';
export type RegisterBody = Pick<
  SourceRegister,
  'registration' | 'scope' | 'expected_revision' | 'predecessor_binding'
>;
export interface SourceEntry {
  local: string;
  mapping: Pick<
    import('../host/ledger.js').Binding,
    'bindingId' | 'hostSessionId' | 'cwd' | 'kind'
  >;
  bindingRevision: string;
  registration: Registration;
  stopped: boolean;
  stopReason: string | null;
  operationId: string | null;
  head: string | null;
  facts: Record<string, SourceFact>;
  observation: SourceGetResult | null;
}
export interface RegisterOperation {
  id: string;
  local: string;
  body: RegisterBody;
  bindingId: string;
  bindingRevision: string;
  receipt: SourceRegisterResult | null;
}
export interface Revocation {
  id: string;
  local: string;
  sourceBinding: string;
  receipt: SourceRevokeResult | null;
}
export interface SourceState {
  version: 1;
  entries: Record<string, SourceEntry>;
  operations: Record<string, RegisterOperation>;
  revocations: Record<string, Revocation>;
  witnesses: Record<
    string,
    {
      request:
        | import('../../generated/network/SourceControl.js').SourceRegister
        | import('../../generated/network/SourceControl.js').SourceGet;
    }
  >;
}
