import { AurigaTeamPanel } from './TeamPanel';
export { AurigaTeamPanel };
export function registerPanel() {
  const existing = customElements.get('auriga-team-panel');
  if (existing && existing !== AurigaTeamPanel)
    throw new Error('auriga-team-panel belongs to a different module');
  if (!existing) customElements.define('auriga-team-panel', AurigaTeamPanel);
}
registerPanel();
