export { createAurigaI18n, messages, type Locale } from './i18n';
export { default as Placeholder } from './Placeholder.vue';
export { pickLocalized } from './locale';

export {
  createApiClient,
  type ApiClient,
  type ApiClientOptions,
} from './api/client';
export { readCsrfCookie, isWriteMethod } from './api/csrf';
export {
  ApiError,
  isApiError,
  mapProblemMessage,
  type Problem,
} from './api/problem';
export {
  createIamApi,
  type IamApi,
  type LoginProvider,
  type LoginTransaction,
  type CreateLoginTransaction,
  type Me,
  type QrChallenge,
} from './api/iam';
export {
  createOrgApi,
  type OrgApi,
  type Member,
  type ListMembersQuery,
} from './api/org';

export { default as StepContainer } from './steps/StepContainer.vue';
export { default as FormStepView } from './steps/FormStepView.vue';
export { default as QrStepView } from './steps/QrStepView.vue';
export { default as RedirectStepView } from './steps/RedirectStepView.vue';
export { default as WebAuthnStepView } from './steps/WebAuthnStepView.vue';
export { default as DeviceStepView } from './steps/DeviceStepView.vue';
export { default as FailStepView } from './steps/FailStepView.vue';
export { default as CompleteStepView } from './steps/CompleteStepView.vue';
export { default as ProviderSelect } from './steps/ProviderSelect.vue';

export {
  enableAurigaMock,
  disableAurigaMock,
  isAurigaMockEnabled,
  resetMockState,
  setMockSession,
} from './mock/enable';
export {
  TEAM_PROVIDER_ID,
  OIDC_PROVIDER_ID,
  MOCK_PASSWORD,
  MOCK_TOTP,
  MOCK_INVITE_CODE,
  MOCK_QR_CHALLENGE,
  mockProviders,
  passwordStep,
  totpStep,
  completeStep,
  failCredentialsStep,
} from './mock/data';

export type * from './generated/steps';
export type {
  paths as IAMPaths,
  components as IAMComponents,
} from './generated/iam';
export type {
  paths as OrgPaths,
  components as OrgComponents,
} from './generated/org';
export type {
  paths as WorkspacePaths,
  components as WorkspaceComponents,
} from './generated/workspace';
