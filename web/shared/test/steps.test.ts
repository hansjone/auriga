import { mount, flushPromises } from '@vue/test-utils';
import { describe, expect, it, vi } from 'vitest';
import { createAurigaI18n } from '../src/i18n';
import StepContainer from '../src/steps/StepContainer.vue';
import ProviderSelect from '../src/steps/ProviderSelect.vue';
import {
  completeStep,
  failCredentialsStep,
  mockProviders,
  passwordStep,
  totpStep,
} from '../src/mock/data';
import type { ClientStep } from '../src/generated/steps';

function mountStep(step: ClientStep) {
  return mount(StepContainer, {
    props: { step },
    global: { plugins: [createAurigaI18n('zh-CN')] },
  });
}

describe('StepContainer', () => {
  it('renders form step and emits form submit', async () => {
    const wrapper = mountStep(passwordStep());
    expect(wrapper.find('[data-testid="form-step"]').exists()).toBe(true);
    await wrapper.find('input[name="username"]').setValue('alice');
    await wrapper.find('input[name="password"]').setValue('secret');
    await wrapper.find('form').trigger('submit');
    const events = wrapper.emitted('submit');
    expect(events?.[0]?.[0]).toEqual({
      type: 'form',
      formId: 'password',
      values: { username: 'alice', password: 'secret' },
    });
  });

  it('renders totp otp field', () => {
    const wrapper = mountStep(totpStep());
    expect(wrapper.find('input[name="code"]').exists()).toBe(true);
  });

  it('renders fail step and retries', async () => {
    const wrapper = mountStep(failCredentialsStep());
    expect(wrapper.find('[data-testid="fail-step"]').exists()).toBe(true);
    await wrapper.find('button').trigger('click');
    expect(wrapper.emitted('retry')).toHaveLength(1);
  });

  it('renders complete and emits redirect', async () => {
    const wrapper = mountStep(completeStep('/home'));
    await flushPromises();
    expect(wrapper.emitted('complete')?.[0]?.[0]).toBe('/home');
  });

  it('renders qr / redirect / device / webauthn types', async () => {
    expect(
      mountStep({
        type: 'qr',
        content: 'https://example.com/qr',
        matchCode: '42',
        expiresAt: new Date(Date.now() + 60_000).toISOString(),
      })
        .find('[data-testid="qr-step"]')
        .exists(),
    ).toBe(true);

    const assign = vi.fn();
    const loc = { ...globalThis.location, assign };
    vi.stubGlobal('location', loc);
    mountStep({
      type: 'redirect',
      url: 'https://idp.example.com/start',
    });
    await flushPromises();
    expect(assign).toHaveBeenCalled();

    expect(
      mountStep({
        type: 'device',
        userCode: 'ABCD-EFGH',
        verificationUri: 'https://example.com/device',
        expiresAt: new Date(Date.now() + 60_000).toISOString(),
      })
        .find('[data-testid="device-step"]')
        .exists(),
    ).toBe(true);

    const wrapper = mountStep({
      type: 'webauthn',
      ceremony: 'get',
      publicKey: {},
    });
    expect(wrapper.find('[data-testid="webauthn-step"]').exists()).toBe(true);
  });
});

describe('ProviderSelect', () => {
  it('emits selected provider', async () => {
    const wrapper = mount(ProviderSelect, {
      props: { providers: mockProviders },
      global: { plugins: [createAurigaI18n()] },
    });
    await wrapper.find('[data-provider-id]').trigger('click');
    expect(wrapper.emitted('select')?.[0]?.[0]).toMatchObject({
      id: mockProviders[0]!.id,
    });
  });
});
