"""Fail closed when the durable service-key claim already exists or is unavailable."""
import importlib.util
import json
from pathlib import Path
import secrets
import tempfile
import unittest
from unittest.mock import patch
import urllib.error


spec = importlib.util.spec_from_file_location('service_provision', Path(__file__).with_name('provision.py'))
provision = importlib.util.module_from_spec(spec)
spec.loader.exec_module(provision)


class Response:
    status = 200

    def __enter__(self):
        return self

    def __exit__(self, *args):
        pass


class ProvisionTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        (self.root / 'state').mkdir()
        for name in ['litellm_master', 'litellm_team', 'litellm_pg_password', 'litellm_ui_reader']:
            (self.root / name).write_text('sk-' + secrets.token_hex(32))
        self.generated = 0

        def mapped_path(value):
            return self.root / ('state/team-provisioned' if value.startswith('/state/') else Path(value).name)

        self.addCleanup(patch.stopall)
        patch.object(provision, 'Path', side_effect=mapped_path).start()
        opener = patch.object(provision.urllib.request, 'build_opener').start().return_value

        def open_request(request, **kwargs):
            if '/key/info?' in request.full_url:
                raise urllib.error.HTTPError(request.full_url, 404, 'revoked key', None, None)
            if request.full_url.endswith('/user/new'):
                self.assertEqual(json.loads(request.data)['user_role'], 'proxy_admin_viewer')
                self.assertFalse(json.loads(request.data)['auto_create_key'])
                return Response()
            self.assertEqual(request.full_url, 'http://litellm:4000/key/generate')
            self.assertIn('key', json.loads(request.data))
            self.generated += 1
            return Response()

        opener.open.side_effect = open_request

    def test_persisted_claim_prevents_revoked_key_recreation_without_local_state(self):
        with patch.object(provision, 'claim_service_key', return_value=False, create=True):
            provision.provision()
        self.assertEqual(self.generated, 0)
        self.assertFalse((self.root / 'state/team-provisioned').exists())

    def test_unavailable_state_never_dispatches_key_creation(self):
        with patch.object(provision, 'claim_service_key', side_effect=RuntimeError('state unavailable'), create=True):
            with self.assertRaises(RuntimeError):
                provision.provision()
        self.assertEqual(self.generated, 0)

    def test_fresh_claim_is_completed_after_one_key_creation(self):
        with patch.object(provision, 'claim_service_key', return_value=True, create=True), patch.object(provision, 'complete_service_key', create=True) as complete:
            provision.provision()
        self.assertEqual(self.generated, 2)
        self.assertEqual(complete.call_count, 2)
        self.assertFalse((self.root / 'state/team-provisioned').exists())


if __name__ == '__main__':
    unittest.main()
