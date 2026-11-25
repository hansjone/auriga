import { fileURLToPath } from 'node:url';

/** Use the async tool dispatch hook before the host synchronously collects env.
 * @param {{auth:any,getServer:()=>any,authorization?:()=>string|null,prepare?:(execution:any)=>Promise<string|undefined>,finishContext?:(id:string)=>void,retain?:(id:string)=>boolean,localUacCredentials?:()=>{empNo:string,token:string}|null}} options */
export function createEnterpriseEnvironment({
  auth,
  getServer,
  authorization,
  prepare,
  finishContext,
  retain,
  localUacCredentials,
}) {
  /** @type {Map<any,any>} */ const issued = new Map();
  const helpers = fileURLToPath(
    new URL('../../skills/uds-skill-auth/scripts/', import.meta.url),
  );
  const empty = {
    DSH_TEAM_TASK_CAPABILITY: '',
    DSH_TEAM_BASE: '',
    DSH_TEAM_HELPERS: helpers,
    DSH_AURIGA_ID: '',
    DSH_AURIGA_TOKEN: '',
  };
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
      DSH_AURIGA_ID: { description: 'Current UAC employee ID for this task.' },
      DSH_AURIGA_TOKEN: {
        description: 'Current UAC token; never print or persist.',
      },
    },
    resolve(/** @type {any} */ execution) {
      if (execution?.signal?.aborted) return { ...empty };
      const localFallback = () => {
        try {
          const cred = localUacCredentials?.();
          if (
            cred &&
            typeof cred.empNo === 'string' &&
            cred.empNo &&
            !cred.empNo.includes('\0') &&
            typeof cred.token === 'string' &&
            cred.token &&
            !cred.token.includes('\0')
          ) {
            return { DSH_AURIGA_ID: cred.empNo, DSH_AURIGA_TOKEN: cred.token };
          }
        } catch {
          /* ignore */
        }
        return {};
      };
      const row = issued.get(execution?.token ?? execution);
      if (!row) return { ...empty, ...localFallback() };
      const env = {
        ...empty,
        DSH_TEAM_TASK_CAPABILITY: row.cap,
        DSH_TEAM_BASE: `http://127.0.0.1:${row.port}/dsh-team`,
      };
      try {
        const fromBroker = /** @type {any} */ (row.current?.());
        if (fromBroker?.DSH_AURIGA_ID && fromBroker?.DSH_AURIGA_TOKEN)
          return { ...env, ...fromBroker };
        return { ...env, ...localFallback() };
      } catch {
        return { ...env, ...localFallback() };
      }
    },
  };
  return {
    contributor,
    release,
    async run(/** @type {any} */ execution, /** @type {()=>any} */ next) {
      const authorizationAtEntry = authorization?.();
      const sessionId = execution?.agent?.session?.header?.id;
      const port = Number(getServer()?.port);
      if (
        (authorization && !authorizationAtEntry) ||
        typeof sessionId !== 'string' ||
        execution?.signal?.aborted ||
        !Number.isInteger(port) ||
        port < 1 ||
        port > 65535
      )
        return next();
      const contextId = await prepare?.(execution);
      if (prepare && !contextId) return next();
      if (authorization && authorization() !== authorizationAtEntry) {
        if (contextId) finishContext?.(contextId);
        return next();
      }
      const cap = auth.mint(sessionId, contextId, execution);
      if (!cap || execution.signal?.aborted) {
        if (cap) auth.revoke(cap);
        else if (contextId) finishContext?.(contextId);
        return next();
      }
      const key = execution.token ?? execution;
      release(execution);
      const row = {
        cap,
        contextId,
        sessionId,
        port,
        current: /** @type {undefined|(()=>object)} */ (undefined),
        signal: execution.signal,
        abort: () => release(execution),
        timer: setTimeout(() => release(execution), 600000),
        off: /** @type {any} */ (null),
      };
      row.timer.unref();
      issued.set(key, row);
      row.signal?.addEventListener('abort', row.abort, { once: true });
      if (contextId) {
        try {
          const current = await auth.launchCredentials(sessionId, cap);
          if (issued.get(key) === row && !execution.signal?.aborted)
            row.current = current;
        } catch {
          // Tools can run without UAC or with raw credential access disabled.
        }
      }
      try {
        return await next();
      } catch (error) {
        if (issued.get(key) === row) release(execution);
        throw error;
      } finally {
        // The environment pair is only needed while the tool starts. The
        // capability may remain live for a background job to refresh it.
        row.current = undefined;
      }
    },
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
