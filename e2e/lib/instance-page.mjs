import { setTimeout as sleep } from 'node:timers/promises';

/** Wait within the caller's budget for the route projection, then require HTML. */
export async function waitInstancePage(
  request,
  isPage,
  { deadline, now = Date.now, pause = sleep },
) {
  let last;
  while (now() < deadline) {
    last = await request(deadline - now());
    if (now() >= deadline) break;
    if (isPage(last)) return last;
    // This is the explicit route-state response after authentication and
    // ownership passed. Generic 503, missing routes and auth failures stay fatal.
    if (
      last.status !== 503 ||
      last.body?.code !== 'edge.instance_not_running'
    ) {
      throw new Error(
        `instance page rejected: status=${last.status} code=${last.body?.code ?? 'none'}`,
      );
    }
    await pause(Math.min(500, Math.max(0, deadline - now())));
  }
  throw new Error(
    `instance page deadline exceeded: status=${last?.status ?? 'none'} code=${last?.body?.code ?? 'none'}`,
  );
}
