import assert from 'node:assert/strict';

/** Check actual browser text geometry, including ancestor clipping and page overflow. */
export async function assertTabLayout(page, labels) {
  const measurements = [];
  for (const label of labels) {
    const tab = page
      .locator('.auriga-tabs-scroll')
      .getByText(label, { exact: true });
    await tab.scrollIntoViewIfNeeded();
    const measurement = await tab.evaluate((element) => {
      const range = document.createRange();
      range.selectNodeContents(element);
      const text = range.getBoundingClientRect();
      let clipped = false;
      for (
        let parent = element;
        parent && !parent.classList.contains('auriga-tabs-scroll');
        parent = parent.parentElement
      ) {
        const style = parent.ownerDocument.defaultView.getComputedStyle(parent),
          box = parent.getBoundingClientRect();
        if (
          ['hidden', 'clip'].includes(style.overflowX) &&
          (text.left < box.left - 1 || text.right > box.right + 1)
        )
          clipped = true;
      }
      return {
        label: element.textContent,
        client: element.clientWidth,
        scroll: element.scrollWidth,
        textWidth: text.width,
        lines: new Set([...range.getClientRects()].map((rect) => rect.top))
          .size,
        clipped,
      };
    });
    assert.equal(measurement.label, label);
    assert.equal(
      measurement.lines,
      1,
      'Tab text must remain on one line: ' + label,
    );
    assert.equal(
      measurement.clipped,
      false,
      'Tab text must not be clipped: ' + label,
    );
    assert.ok(
      measurement.scroll <= measurement.client + 1,
      'Tab text must fit its item: ' + label,
    );
    measurements.push(measurement);
  }
  const content = await page.locator('.auriga-app').evaluate((element) => ({
    client: element.clientWidth,
    scroll: element.scrollWidth,
  }));
  assert.ok(
    content.scroll <= content.client + 1,
    'The page must not overflow horizontally',
  );
  return { measurements, content };
}
