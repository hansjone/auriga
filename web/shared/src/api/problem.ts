import type { components as CommonLike } from '../generated/iam';

/** Problem shape from common.yaml (also referenced by iam/org generated types). */
export type Problem = {
  type?: string;
  title: string;
  status: number;
  detail?: string;
  instance?: string;
  code: string;
  params?: Record<string, unknown>;
  errors?: { field: string; code: string; message?: string }[];
  traceId?: string;
};

export class ApiError extends Error {
  readonly problem: Problem;
  readonly status: number;

  constructor(problem: Problem) {
    super(problem.detail || problem.title || problem.code);
    this.name = 'ApiError';
    this.problem = problem;
    this.status = problem.status;
  }
}

export function isApiError(e: unknown): e is ApiError {
  return e instanceof ApiError;
}

/** Resolve i18n key `errors.<code>`; fall back to detail/title/unknown. */
export function mapProblemMessage(
  problem: Problem,
  t: (key: string) => string,
): string {
  const key = `errors.${problem.code}`;
  const localized = t(key);
  if (localized !== key) return localized;
  if (problem.detail) return problem.detail;
  if (problem.title) return problem.title;
  return t('errors.unknown');
}

/** Narrow helper when OpenAPI components embed Problem. */
export type IamProblem = CommonLike['schemas'] extends { Problem: infer P }
  ? P
  : Problem;
