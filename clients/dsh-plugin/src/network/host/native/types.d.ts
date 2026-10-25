/** Structural APIs from lockfile-pinned upstream @deepseek-ai/dsh*@0.2.0-rc.2. */
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
  inbox: {
    nextTurn: readonly { source: { kind: string; rpcId?: string } }[];
    nextStep: readonly { source: { kind: string; rpcId?: string } }[];
  };
}
export interface NativeContext {
  sessionController: {
    inspect(id: string): Promise<{ meta: Header; events: readonly Event[] }>;
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
    stateOf(session: NativeSession, key: 'agentPreset'): string | undefined;
  };
  sessionPersistence: {
    stat(id: string): Promise<{ header: Header } | undefined>;
  };
}
export interface RuntimeFingerprint {
  version: string;
  controllerSha256: string;
  persistenceSha256: string;
  sessionSha256: string;
  agentLoopSha256: string;
}
/** Locations supplied by the running host loader. */
export interface RuntimeArtifacts {
  manifestPath: string;
  controllerPath: string;
  persistencePath: string;
  sessionPath: string;
  agentLoopPath: string;
}
/** Functional readiness and durability; does not grant or assert OS isolation. */
export interface HostOperationAuthority {
  assertReady(): Promise<void>;
  assertDurableProvider(
    provider: NativeContext['sessionPersistence'],
    id: string,
  ): Promise<void>;
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
