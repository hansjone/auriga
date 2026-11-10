import type { VueWrapper } from '@vue/test-utils';

/** Open the auto pool editor the way an administrator does: edit its row, or add it when absent. */
export async function openAuto(w: VueWrapper) {
  const row = w.find('tr[data-pool="auto"] [data-testid="edit-pool"]');
  await (row.exists() ? row : w.get('[data-testid="add-pool"]')).trigger(
    'click',
  );
}
