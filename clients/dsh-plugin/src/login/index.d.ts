import type { ClientStep, StepInput } from '../generated/steps.js';

export interface LoginOptions {
  locale?: string;
  onSubmit(
    input: StepInput,
    context: { signal: AbortSignal },
  ): Promise<ClientStep>;
  onRestart(context: { signal: AbortSignal }): Promise<ClientStep>;
  onComplete(): void;
  onCancel?(): Promise<void>;
}

export interface LoginView {
  setStep(step: ClientStep): void;
  setFailure(reason: import('./errors.js').LoginFailureReason): void;
  load(action: (context: { signal: AbortSignal }) => Promise<ClientStep>): void;
  setLocale(locale: string): void;
  dispose(): void;
}

export function createLoginView(
  root: HTMLElement,
  options: LoginOptions,
): LoginView;
