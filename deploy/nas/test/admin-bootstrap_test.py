import base64
import importlib.util
import json
from pathlib import Path
import threading
import unittest
import subprocess
import sys
import tempfile
from unittest.mock import patch
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.hazmat.primitives import serialization

spec = importlib.util.spec_from_file_location('bootstrap', Path(__file__).resolve().parents[1] / 'scripts/admin-bootstrap.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class BootstrapTests(unittest.TestCase):
    def test_local_decrypt_is_exclusive_and_rejects_incomplete_handoff(self):
        private = rsa.generate_private_key(public_exponent=65537, key_size=3072)
        payload = {'stage': 'complete', 'admin': {'password': base64.b64encode(__import__('os').urandom(24)).decode()}}
        with tempfile.TemporaryDirectory() as folder:
            folder = Path(folder)
            key = folder / 'recipient.key'
            key.write_bytes(private.private_bytes(serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8, serialization.NoEncryption()))
            encrypted = folder / 'encrypted.log'
            encrypted.write_text('AURIGA_BOOTSTRAP_ENVELOPE ' + json.dumps(module.seal(payload, private.public_key())))
            output = folder / 'accounts.json'
            args = [sys.executable, str(Path(__file__).resolve().parents[1] / 'scripts/admin-bootstrap-decrypt.py'), '--input', str(encrypted), '--private-key', str(key), '--output', str(output)]
            done = subprocess.run(args, capture_output=True, text=True)
            self.assertEqual(done.returncode, 0, done.stderr)
            self.assertNotIn(payload['admin']['password'], done.stdout + done.stderr)
            self.assertEqual(json.loads(output.read_text()), payload)
            original = output.read_bytes()
            self.assertNotEqual(subprocess.run(args, capture_output=True).returncode, 0)
            self.assertEqual(output.read_bytes(), original)
            encrypted.write_text('AURIGA_BOOTSTRAP_ENVELOPE ' + json.dumps(module.seal(dict(payload, stage='totp-enrollment'), private.public_key())))
            partial = folder / 'partial.json'
            args[-1] = str(partial)
            self.assertNotEqual(subprocess.run(args, capture_output=True).returncode, 0)
            self.assertFalse(partial.exists())
            self.assertEqual(subprocess.run(args + ['--allow-partial'], capture_output=True).returncode, 0)
            self.assertEqual(json.loads(partial.read_text())['stage'], 'totp-enrollment')

    def test_complete_handoff_contains_recovery_but_only_after_both_fresh_logins(self):
        private = rsa.generate_private_key(public_exponent=65537, key_size=3072)
        clients, writes, envelopes, logins = [], [], [], []

        class Client:
            def __init__(self, origin, ca):
                self.number, self.cookies, self.csrf = len(clients), {}, None
                clients.append(self)

            def request(self, method, path, *args, **kwargs):
                if path.endswith('/login/providers'):
                    return {'items': [{'id': 'emergency', 'connectorType': 'emergency'}, {'id': 'team', 'connectorType': 'team-account'}]}
                if path.endswith('/admin/roles'):
                    return {'items': [{'id': 'owner', 'key': 'owner', 'builtin': True}, {'id': 'member', 'key': 'member', 'builtin': True}]}
                return {}

            def write(self, path, body):
                writes.append((self.number, path))
                return {'password': base64.b64encode(__import__('os').urandom(24)).decode()} if path.endswith('/rotate') else {'code': 'fixture-invitation'}

        def login(client, provider, credentials, checkpoint, invitation=None):
            logins.append((client.number, provider, bool(invitation)))
            if provider == 'team':
                credentials['totpSecret'] = base64.b32encode(__import__('os').urandom(20)).decode()
                checkpoint('totp-enrollment')
            return credentials.get('username', 'emergency')

        with patch.object(module, 'login', side_effect=login):
            state = module.bootstrap({'mode': 'ip', 'adminOrigin': {}, 'portalOrigin': {}}, {}, private.public_key(), envelopes.append, client_type=Client)
        self.assertEqual(logins, [(0, 'emergency', False), (1, 'team', True), (2, 'team', False), (3, 'team', True), (4, 'team', False)])
        self.assertEqual(writes[-1], (2, '/api/iam/v1/admin/emergency/rotate'))
        self.assertEqual(state['stage'], 'complete')
        self.assertEqual(module.unseal(envelopes[-1], private), state)
        self.assertNotIn(state['emergencyRecovery']['password'], json.dumps(envelopes))

    def test_rotation_requires_independent_normal_admin_authorization(self):
        private = rsa.generate_private_key(public_exponent=65537, key_size=3072)
        created, writes, envelopes = [], [], []

        class Client:
            def __init__(self, origin, ca):
                self.number, self.cookies, self.csrf = len(created), {}, None
                created.append(self)

            def request(self, method, path, *args, **kwargs):
                if path.endswith('/login/providers'):
                    return {'items': [{'id': 'emergency', 'connectorType': 'emergency'}, {'id': 'team', 'connectorType': 'team-account'}]}
                if path.endswith('/admin/roles'):
                    if self.number == 2:
                        raise module.BootstrapError('org.forbidden')
                    return {'items': [{'id': 'owner', 'key': 'owner', 'builtin': True}]}
                return {}

            def write(self, path, body):
                writes.append(path)
                return {'code': 'fixture-invitation'}

        access = {'mode': 'ip', 'adminOrigin': {}, 'portalOrigin': {}}
        with patch.object(module, 'login', return_value='fixture-account'):
            with self.assertRaisesRegex(module.BootstrapError, 'org.forbidden'):
                module.bootstrap(access, {}, private.public_key(), envelopes.append, client_type=Client)
        self.assertNotIn('/api/iam/v1/admin/emergency/rotate', writes)
        recovered = module.unseal(envelopes[-1], private)
        self.assertEqual(recovered['stage'], 'admin-verified')
        self.assertIn('password', recovered['admin'])

    def test_totp_is_checkpointed_before_confirmation_and_uses_declared_field(self):
        events = []
        secret = base64.b32encode(__import__('os').urandom(20)).decode().rstrip('=')

        class Client:
            def request(self, method, path, body=None):
                if method == 'GET':
                    return {'account': {'id': 'fixture-account'}}
                if path.endswith('/transactions'):
                    return {'id': '01900000-0000-7000-8000-000000000001', 'status': 'pending', 'step': {'formId': 'first_totp', 'fields': [
                        {'name': 'enrollment', 'kind': 'info', 'label': {'en': 'Secret: ' + secret}}, {'name': 'otp', 'kind': 'otp'}]}}
                events.append(('advance', body['values']))
                return {'status': 'completed'}

        credentials = {}
        with patch.object(module, 'otp', return_value='fixture-otp'):
            result = module.login(Client(), 'team', credentials, lambda stage: events.append(('checkpoint', stage)))
        self.assertEqual(result, 'fixture-account')
        self.assertEqual(events[0], ('checkpoint', 'totp-enrollment'))
        self.assertEqual(events[1], ('advance', {'otp': 'fixture-otp'}))
        self.assertEqual(credentials['totpSecret'], secret)

    def test_sealed_checkpoint_requires_the_matching_recipient(self):
        private = rsa.generate_private_key(public_exponent=65537, key_size=3072)
        payload = {'stage': 'totp-enrollment', 'admin': {'password': base64.b64encode(__import__('os').urandom(32)).decode()}}
        sealed = module.seal(payload, private.public_key())
        self.assertNotIn(payload['admin']['password'], json.dumps(sealed))
        self.assertEqual(module.unseal(sealed, private), payload)
        wrong = rsa.generate_private_key(public_exponent=65537, key_size=3072)
        with self.assertRaises(Exception):
            module.unseal(sealed, wrong)
        damaged = dict(sealed, ciphertext=sealed['ciphertext'][:-4] + 'AAAA')
        with self.assertRaises(Exception):
            module.unseal(damaged, private)
        with self.assertRaises(ValueError):
            module.seal(payload, rsa.generate_private_key(public_exponent=65537, key_size=2048).public_key())

    def test_transport_uses_real_loopback_without_forwarded_identity_headers(self):
        observed = []

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_GET(self):
                observed.append((self.client_address[0], dict(self.headers)))
                self.send_response(200)
                self.send_header('content-type', 'application/json')
                self.end_headers()
                self.wfile.write(b'{"items":[]}')

        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=server.serve_forever)
        thread.start()
        try:
            origin = {'scheme': 'http', 'host': '192.0.2.10', 'port': server.server_port}
            client = module.Client(origin)
            self.assertEqual(client.request('GET', '/api/iam/v1/login/providers'), {'items': []})
            peer, headers = observed[0]
            self.assertEqual(peer, '127.0.0.1')
            self.assertEqual(headers['Host'], '192.0.2.10:' + str(server.server_port))
            self.assertFalse(any('forwarded' in key.lower() or 'client-ip' in key.lower() for key in headers))
            for host in ['alice@example.com', 'example.com\r\nX-Test: bad', 'example.com/path']:
                with self.assertRaises(ValueError):
                    module.Client(dict(origin, host=host))
        finally:
            server.shutdown()
            server.server_close()
            thread.join()


if __name__ == '__main__':
    unittest.main()
