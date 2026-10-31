import { expect, it } from 'vitest';
import { hasPermission } from '../src/admin/permissions';

it.each([
  [['*'], 'org.roles.grant', true],
  [['org.*'], 'org.roles.grant', true],
  [['org.roles.*'], 'org.roles.grant', true],
  [['org.roles.grant'], 'org.roles.grant', true],
  [['org.role.*'], 'org.roles.grant', false],
  [['org.roles.read'], 'org.roles.*', false],
  [['org.roles.*'], 'org.*', false],
  [['org.roles.*'], '*', false],
  [['org.roles.*'], 'org.roles', false],
  [['org.*.read'], 'org.roles.read', false],
  [[], 'org.roles.read', false],
] as const)('matches %j against %s: %s', (permissions, required, expected) => {
  expect(hasPermission(permissions, required)).toBe(expected);
});
