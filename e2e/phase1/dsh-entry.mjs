// Follow the host's first-run UI. Never force-click through its modal mask or
// supply a model key; unrelated dialogs must remain blocking.
export async function openAurigaPanel(page) {
  const preview = page.getByRole('dialog', {
    name: /^(预览版说明|Preview Notice)$/,
  });
  const setup = page.getByRole('dialog', {
    name: /^(添加一个 API Key 开始使用|Add an API key to get started)$/,
  });
  const result = { previewAcknowledged: false, modelSetupDeferred: false };
  await page.addLocatorHandler(preview, async (dialog) => {
    await dialog.getByRole('button', { name: /^(继续|Continue)$/ }).click();
    result.previewAcknowledged = true;
  });
  await page.addLocatorHandler(setup, async (dialog) => {
    await dialog
      .getByRole('button', { name: /^(稍后配置|Configure later)$/ })
      .click();
    result.modelSetupDeferred = true;
  });
  try {
    await page.getByRole('button', { name: 'Auriga', exact: true }).click();
    await page.locator('auriga-team-panel').waitFor({ state: 'visible' });
    return result;
  } finally {
    await page.removeLocatorHandler(preview);
    await page.removeLocatorHandler(setup);
  }
}
