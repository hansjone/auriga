/** Minimal public host API contract, checked against upstream rc.2 exports. */
export interface Header {
  id: string;
  cwd?: string;
  origin?: 'subagent';
  parentSession?: string;
  agentPreset?: string;
}
export interface Event {
  type: string;
  data: { source?: { kind: string; rpcId?: string } };
}
export interface NativeSession {
  id: string;
  header: Header;
}
export interface Agent {
  id: string;
  session: NativeSession;
  inbox: {
    nextTurn: readonly Message[];
    nextStep: readonly Message[];
  };
}
export interface Message {
  source?: { kind: string; rpcId?: string };
}
export interface NativeContext {
  sessionController: {
    inspect(id: string): Promise<{ meta: Header; events: readonly Event[] }>;
    projections(
      input: { sessionId: string },
      signal: AbortSignal,
    ): Promise<{
      asOfSeq: number;
      values: {
        inbox?: {
          'next-turn': readonly Message[];
          'next-step': readonly Message[];
        };
      };
    } | null>;
    resolveAgent(id: string): Promise<{ agent?: Agent; error?: unknown }>;
    create(input: {
      sessionId: string;
      cwd: string;
      agentPreset?: string;
    }): Promise<{ sessionId: string; agentPreset?: string }>;
    prompt(
      input: {
        sessionId: string;
        requestId: string;
        mode: 'queue';
        content: { type: 'text'; text: string }[];
      },
      signal: AbortSignal,
    ): Promise<{ accepted: boolean }>;
  };
  sessions: {
    get(id: string): NativeSession | undefined;
    flush(session: NativeSession): Promise<boolean>;
  };
  agents: {
    get(id: string): Agent | undefined;
    isOwnedBy(id: string, owner: Agent): boolean;
  };
  workspaceRegistry: { archivedSessionIds: readonly string[] };
  agentPresets: {
    resolve(id?: string): Promise<{ id: string; broken?: unknown }>;
  };
  sessionProjections: {
    stateOf(
      session: NativeSession,
      key: 'agentPreset',
    ): string | null | undefined;
  };
  sessionPersistence: {
    readonly identity: symbol;
    flush(): Promise<void>;
    open(
      id: string,
      access: 'read',
    ): Promise<{
      header: Header;
      read(): Promise<{ events: readonly Event[] }>;
      close(): Promise<void>;
    }>;
    stat(id: string): Promise<{ header: Header } | undefined>;
  };
}
export type NativeCapability =
  'read' | 'requests' | 'durability' | 'create' | 'prompt' | 'source';
export interface NativeDiagnostics {
  isolation: {
    required: boolean;
    isolated: boolean;
    mode: string;
    message: string;
  };
  capabilities: Record<
    string,
    { available: boolean; missing: string[]; lastFailure: string | null }
  >;
}
/** Functional capability checks; no isolation/source attestation. */
export interface HostOperationAuthority {
  assertReady(capability?: NativeCapability): Promise<void>;
  assertDurableProvider(
    provider: NativeContext['sessionPersistence'],
    id: string,
  ): Promise<void>;
  reportFailure?(capability: NativeCapability, reason: string): void;
  getDiagnostics(): NativeDiagnostics;
}
export interface SourceBinding {
  local: string;
  bindingId: string;
  hostSessionId: string;
  cwd: string;
  state: string;
  lifecycle: string;
  kind: string;
}
