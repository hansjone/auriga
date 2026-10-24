interface DocumentPage {
  dispose(): void;
}

/** Keep one controller per document activation, including history restore. */
export function mountDocumentPage(
  root: HTMLElement,
  mount: (root: HTMLElement) => DocumentPage,
) {
  const win = root.ownerDocument.defaultView;
  if (!win) throw new Error('A browser document is required');
  let page: DocumentPage | undefined;
  let disposed = false;
  const show = () => {
    // pageshow also follows the initial load; never mount a second controller.
    if (!disposed && !page && root.isConnected) page = mount(root);
  };
  const hide = () => {
    const previous = page;
    page = undefined;
    previous?.dispose();
  };
  const disposeEntry = () => {
    if (disposed) return;
    disposed = true;
    win.removeEventListener('pagehide', hide);
    win.removeEventListener('pageshow', show);
    hide();
  };
  // Register once for this document, not once per restored controller.
  win.addEventListener('pagehide', hide);
  win.addEventListener('pageshow', show);
  show();
  return disposeEntry;
}
