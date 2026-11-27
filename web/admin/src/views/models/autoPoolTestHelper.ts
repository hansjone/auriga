import type { VueWrapper } from '@vue/test-utils';

/** Open the auriga pool editor the way an administrator does: edit its row, or add when absent. */
export async function openAuto(w: VueWrapper) {
  const row = w.find('tr[data-pool="auriga"] [data-testid="edit-pool"]');
  await (row.exists() ? row : w.get('[data-testid="add-pool"]')).trigger(
    'click',
  );
}
