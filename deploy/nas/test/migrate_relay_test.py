import copy
import importlib.util
from pathlib import Path
import secrets
import sys
import unittest

sys.dont_write_bytecode = True

spec = importlib.util.spec_from_file_location('migration', Path(__file__).parents[1] / 'scripts/migrate-relay.py')
migration = importlib.util.module_from_spec(spec)
spec.loader.exec_module(migration)


class MigrationTest(unittest.TestCase):
    def test_provider_secret_models_and_legacy_hash_are_migrated_idempotently(self):
        secret = secrets.token_urlsafe(32)
        source = Source(secret)
        target = Target()
        before = copy.deepcopy(source.models)
        first = migration.migrate(source, target)
        again = migration.migrate(source, target)
        self.assertEqual(first, again)
        self.assertEqual(before, source.models)
        self.assertEqual(len(target.providers), 1)
        self.assertEqual(target.credentials, [secret])
        self.assertEqual(target.pools[0]['id'], 'old-model')
        self.assertEqual(target.bindings[0]['sha256'], 'a' * 64)
        self.assertNotIn(secret, str(first))

    def test_unsupported_source_fails_before_any_destination_write(self):
        source = Source(secrets.token_urlsafe(32))
        source.models[0]['litellm_params']['model'] = 'unsupported/model'
        target = Target()
        with self.assertRaises(migration.MigrationError):
            migration.migrate(source, target)
        self.assertEqual(target.providers, [])

    def test_invalid_price_fails_before_any_destination_write(self):
        source = Source(secrets.token_urlsafe(32))
        source.models[0]['model_info']['input_cost_per_token'] = -1
        target = Target()
        with self.assertRaises(migration.MigrationError):
            migration.migrate(source, target)
        self.assertEqual(target.providers, [])

    def test_inherited_or_unrepresentable_policy_fails_before_writes(self):
        for field in ('team_id', 'tpm_limit', 'aliases', 'budget_duration', 'allowed_routes'):
            with self.subTest(field=field):
                source = Source(secrets.token_urlsafe(32))
                original = source.get
                def read(path):
                    result = original(path)
                    if path.startswith('/key/list'):
                        result['keys'][0][field] = 'unsupported'
                    return result
                source.get = read
                target = Target()
                with self.assertRaises(migration.MigrationError):
                    migration.migrate(source, target)
                self.assertEqual(target.providers, [])


class Source:
    def __init__(self, secret):
        self.models = [{'model_name': 'old-model', 'model_info': {'id': 'source-1'},
                        'litellm_params': {'model': 'openai/model', 'api_base': 'https://api.example.com', 'api_key': secret}}]

    def get(self, path):
        if path == '/model/info':
            return {'data': self.models}
        if path.startswith('/key/list'):
            return {'keys': [{'token': 'a' * 64, 'models': ['old-model'], 'max_budget': None,
                              'allowed_routes': ['llm_api_routes']}], 'total_pages': 1}
        raise AssertionError('unexpected source read')


class Target:
    def __init__(self):
        self.replies, self.providers, self.credentials, self.pools, self.bindings = {}, [], [], [], []

    def write(self, method, path, body, operation):
        if operation in self.replies:
            return self.replies[operation]
        if path == '/api/providers':
            self.providers.append(body)
            result = {'id': 'provider-id'}
        elif path.endswith('/keys'):
            self.credentials.append(body['secret'])
            result = {}
        elif path == '/api/pools':
            self.pools.append(body)
            result = body
        elif path == '/api/virtual-keys':
            result = {'key': {'id': 'key-id'}}
        elif path == '/api/legacy-key-bindings':
            self.bindings.append(body)
            result = {}
        else:
            raise AssertionError('unexpected destination write')
        self.replies[operation] = result
        return result


if __name__ == '__main__':
    unittest.main()
