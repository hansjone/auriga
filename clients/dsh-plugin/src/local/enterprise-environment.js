import { fileURLToPath } from 'node:url';

/** Public shellEnv/tools-result/jobs adapter, matching the original host lifecycle.
 * resolve is called with a trusted tool execution, never a page/model payload.
 * @param {{auth:any,getServer:()=>any,prepare?:(execution:any)=>Promise<string|undefined>,retain?:(id:string)=>boolean}} options */
export function createEnterpriseEnvironment({
  auth,
  getServer,
  prepare,
  retain,
}) {
  /** @type {Map<any,any>} */ const issued = new Map();
  const helpers = fileURLToPath(
    new URL('../../skills/uds-skill-auth/scripts/', import.meta.url),
  );
  function release(/** @type {any} */ execution) {
    const key = execution?.token ?? execution;
    const row = issued.get(key);
    if (!row) return;
    issued.delete(key);
    auth.revoke(row.cap);
    row.signal?.removeEventListener('abort', row.abort);
    clearTimeout(row.timer);
    row.off?.();
  }
  const contributor = {
    name: 'auriga-enterprise',
    variables: {
      DSH_TEAM_TASK_CAPABILITY: {
        description: 'Session task capability; never print or persist.',
      },
      DSH_TEAM_BASE: { description: 'Owned loopback enterprise endpoint.' },
      DSH_TEAM_HELPERS: { description: 'Packaged Python helper directory.' },
      auriga_id: { description: 'Current UAC employee ID for this task.' },
      auriga_token: {
        description: 'Current UAC token; never print or persist.',
      },
    },
    resolve(
      /** @type {any} */ execution,
      /** @type {string|undefined} */ contextId = undefined,
    ) {
      release(execution);
      const empty = {
        DSH_TEAM_TASK_CAPABILITY: '',
        DSH_TEAM_BASE: '',
        DSH_TEAM_HELPERS: helpers,
        auriga_id: '',
        auriga_token: '',
      };
      const sessionId = execution?.agent?.session?.header?.id;
      const port = Number(getServer()?.port);
      if (
        typeof sessionId !== 'string' ||
        execution?.signal?.aborted ||
        !Number.isInteger(port) ||
        port < 1 ||
        port > 65535
      )
        return empty;
      const cap = auth.mint(sessionId, contextId, execution);
      if (!cap) return empty;
      const row = {
        cap,
        contextId,
        sessionId,
        signal: execution.signal,
        abort: () => release(execution),
        timer: setTimeout(() => release(execution), 600000),
        off: /** @type {any} */ (null),
      };
      row.timer.unref();
      issued.set(execution.token ?? execution, row);
      row.signal?.addEventListener('abort', row.abort, { once: true });
      return {
        ...empty,
        DSH_TEAM_TASK_CAPABILITY: cap,
        DSH_TEAM_BASE: `http://127.0.0.1:${port}/dsh-team`,
      };
    },
  };
  if (prepare) {
    const resolve = contributor.resolve;
    contributor.resolve = /** @type {any} */ (
      async (/** @type {any} */ execution) => {
        const context = await prepare(execution);
        const env = resolve(execution, context);
        if (!context || !env.DSH_TEAM_TASK_CAPABILITY) return env;
        try {
          const key = execution.token ?? execution;
          const row = issued.get(key);
          const current = await auth.launchCredentials(row.sessionId, row.cap);
          if (issued.get(key) !== row || execution.signal?.aborted) return env;
          return { ...env, ...current() };
        } catch {
          // Absence of UAC, disallowed raw reads and expired tasks must not
          // prevent ordinary local tools from starting; neither alias is set.
          return env;
        }
      }
    );
  }
  return {
    contributor,
    release,
    finish(
      /** @type {any} */ execution,
      /** @type {any} */ result,
      /** @type {any} */ jobs,
    ) {
      const row = issued.get(execution?.token ?? execution),
        value = result?.value;
      if (
        row &&
        !result?.isError &&
        ['background', 'promoted'].includes(value?.kind) &&
        typeof value.jobId === 'string' &&
        jobs?.events?.subscribe
      ) {
        const job = jobs
          .list(row.sessionId)
          .find((/** @type {any} */ j) => j.id === value.jobId);
        if (job && ['running', 'stopping'].includes(job.status)) {
          // Only this native jobs registry can transfer the tool's authority
          // to a background job. Cancellation alone never extends a context.
          if (retain && (!row.contextId || !retain(row.contextId))) {
            release(execution);
            return;
          }
          row.signal?.removeEventListener('abort', row.abort);
          auth.retain?.(row.cap);
          row.signal = null;
          row.off = jobs.events.subscribe(
            { owner: row.sessionId },
            (/** @type {any} */ event) => {
              if (
                event.job?.id === value.jobId &&
                ['settled', 'stopping', 'removed'].includes(event.type)
              )
                release(execution);
            },
          );
          return;
        }
      }
      release(execution);
    },
    dispose() {
      for (const key of [...issued.keys()]) release(key);
    },
  };
}
