import { AurigaTeamPanel } from './TeamPanel';
export { AurigaTeamPanel };
export function registerPanel() {
  if (!customElements.get('auriga-team-panel'))
    customElements.define('auriga-team-panel', AurigaTeamPanel);
}
registerPanel();
