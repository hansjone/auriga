/** Structural native APIs, not Auriga wire contracts. rc.3 only. */
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
  snapshotEvents(): readonly Event[];
}
export interface Agent {
  id: string;
  session: NativeSession;
}
export interface NativeContext {
  sessionController: {
    inspect(id: string): Promise<{ meta: Header; events: readonly Event[] }>;
    resolveAgent(id: string): Promise<{ agent?: Agent; error?: unknown }>;
    create(input: {
      sessionId: string;
      cwd: string;
      agentPreset?: string;
    }): Promise<{ sessionId: string }>;
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
  agents: { get(id: string): Agent | undefined };
  workspaceRegistry: { archivedSessionIds: readonly string[] };
  agentPresets: { resolve(id: string): Promise<{ broken?: unknown }> };
  sessionPersistence: {
    stat(id: string): Promise<{ header: Header } | undefined>;
  };
}
export interface RuntimeFingerprint {
  version: string;
  build: string;
  dirty: boolean;
  controllerSha256: string;
  persistenceSha256: string;
  sessionSha256: string;
}
/** Trusted host/broker code ONLY; never settings, tool parameters or browser IPC. */
export interface NativeSecurity {
  inspectRuntime(): Promise<RuntimeFingerprint>;
  /** Must throw unless the protocol §7 OS boundary is currently enforced. */
  assertIsolated(): Promise<void>;
  /** Must prove the active provider instance and this session's durable writer. */
  assertDurableProvider(
    provider: NativeContext['sessionPersistence'],
    id: string,
  ): Promise<void>;
  hasPromptRequest?(
    id: string,
    requestId: string,
  ): Promise<boolean | 'unverified'>;
}
/** Internal operation authority for port composition, not host provenance. */
export interface HostOperationAuthority extends NativeSecurity {
  assertReady(): Promise<void>;
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
