"""Local first-admin setup through the real edge loopback, never a database edit.

Requires Python 3 and cryptography from the explicitly approved LiteLLM image.
Only recipient-encrypted checkpoints go to stdout; no plaintext credential file
is created on the NAS. The private recipient key stays on the operator computer.
"""
import argparse
import base64
import hashlib
import hmac
import http.client
import json
import os
from pathlib import Path
import re
import secrets
import ssl
import struct
import sys
import time
from http.cookies import SimpleCookie

from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import padding, rsa
from cryptography.hazmat.primitives.ciphers.aead import AESGCM

AAD = b'auriga-nas-admin-bootstrap-v1'


def seal(payload, recipient):
    if not isinstance(recipient, rsa.RSAPublicKey) or recipient.key_size < 3072:
        raise ValueError('recipient must be RSA with at least 3072 bits')
    key, nonce = os.urandom(32), os.urandom(12)
    wrapped = recipient.encrypt(key, padding.OAEP(mgf=padding.MGF1(hashes.SHA256()), algorithm=hashes.SHA256(), label=AAD))
    ciphertext = AESGCM(key).encrypt(nonce, json.dumps(payload).encode(), AAD)
    encode = lambda value: base64.b64encode(value).decode('ascii')
    return {'schemaVersion': 1, 'wrappedKey': encode(wrapped), 'nonce': encode(nonce), 'ciphertext': encode(ciphertext)}


def unseal(envelope, private):
    if envelope.get('schemaVersion') != 1:
        raise ValueError('unsupported envelope')
    decode = lambda value: base64.b64decode(value, validate=True)
    key = private.decrypt(decode(envelope['wrappedKey']), padding.OAEP(mgf=padding.MGF1(hashes.SHA256()), algorithm=hashes.SHA256(), label=AAD))
    return json.loads(AESGCM(key).decrypt(decode(envelope['nonce']), decode(envelope['ciphertext']), AAD))


class BootstrapError(Exception):
    """Messages contain only fixed operation names or safe server error codes."""


class LoopbackTLS(http.client.HTTPSConnection):
    def connect(self):
        self.sock = self._context.wrap_socket(
            __import__('socket').create_connection(('127.0.0.1', self.port), self.timeout),
            server_hostname=self.host,
        )


class Client:
    def __init__(self, origin, ca_file=None):
        self.scheme, self.host, self.port = origin['scheme'], origin['host'], origin['port']
        if (self.scheme not in ('http', 'https') or not re.fullmatch(r'[a-z0-9.-]{1,253}', self.host)
                or not isinstance(self.port, int) or not 0 < self.port < 65536):
            raise ValueError('invalid deployment origin')
        self.authority = self.host + ':' + str(self.port)
        self.origin = self.scheme + '://' + self.authority
        self.context = ssl.create_default_context(cafile=ca_file) if self.scheme == 'https' else None
        self.cookies, self.csrf = {}, None

    def request(self, method, path, body=None, headers=None, expected=(200, 201)):
        if not path.startswith('/api/') or '\r' in path or '\n' in path:
            raise ValueError('only same-deployment API paths are allowed')
        wire = {'Host': self.authority, 'Origin': self.origin, 'Accept': 'application/json'}
        if self.cookies:
            wire['Cookie'] = '; '.join(key + '=' + value for key, value in self.cookies.items())
        if headers:
            wire.update(headers)
        data = None if body is None else json.dumps(body).encode()
        if data is not None:
            wire['Content-Type'] = 'application/json'
        connection = (LoopbackTLS(self.host, self.port, timeout=20, context=self.context) if self.context
                      else http.client.HTTPConnection('127.0.0.1', self.port, timeout=20))
        try:
            connection.request(method, path, data, wire)
            response = connection.getresponse()
            for key, value in response.getheaders():
                if key.lower() == 'set-cookie':
                    cookie = SimpleCookie()
                    cookie.load(value)
                    for name, item in cookie.items():
                        self.cookies[name] = item.value
            raw = response.read(2 * 1024 * 1024 + 1)
            if len(raw) > 2 * 1024 * 1024:
                raise BootstrapError('API response too large')
            value = json.loads(raw) if raw else None
            if response.status not in expected:
                code = value.get('code', '') if isinstance(value, dict) else ''
                safe = code if re.fullmatch(r'[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*', code) else 'unclassified'
                raise BootstrapError('API HTTP ' + str(response.status) + ' ' + safe)
            return value
        finally:
            connection.close()

    def write(self, path, body):
        if not self.csrf:
            self.csrf = secrets.token_urlsafe(32)
            self.request('POST', '/api/iam/v1/admin/session/csrf-tokens', {'token': self.csrf},
                         {'X-Auriga-CSRF-Bootstrap': 'admin-session-v1'})
        return self.request('POST', path, body, {'X-Auriga-CSRF': self.csrf})


def otp(credentials):
    now = time.time()
    counter = int(now // 30)
    previous = credentials.get('lastTotpCounter', -1)
    if counter <= previous or 30 - now % 30 < 5:
        time.sleep(max((max(counter + 1, previous + 1) * 30 + .1) - now, 0))
        counter = int(time.time() // 30)
    secret = credentials['totpSecret']
    key = base64.b32decode(secret + '=' * (-len(secret) % 8), casefold=True)
    digest = hmac.new(key, struct.pack('>Q', counter), hashlib.sha1).digest()
    offset = digest[-1] & 15
    credentials['lastTotpCounter'] = counter
    return str((struct.unpack('>I', digest[offset:offset + 4])[0] & 0x7fffffff) % 1000000).zfill(6)


def login(client, provider, credentials, checkpoint, invitation=None):
    body = {'providerId': provider, 'returnTo': '/'}
    if invitation:
        body['invitationCode'] = invitation
    transaction = client.request('POST', '/api/iam/v1/login/transactions', body)
    for _ in range(6):
        if transaction.get('status') == 'completed':
            return client.request('GET', '/api/iam/v1/me')['account']['id']
        if transaction.get('status') != 'pending':
            code = transaction.get('step', {}).get('code', '')
            raise BootstrapError(code if re.fullmatch(r'iam\.[a-z_]+', code) else 'login failed')
        step = transaction.get('step', {})
        form = step.get('formId')
        if form == 'emergency':
            values = {'username': credentials['username'], 'password': credentials['password'], 'otp': otp(credentials)}
        elif form in ('register', 'password'):
            values = {key: credentials[key] for key in ('username', 'password')}
            if form == 'register':
                values['displayName'] = credentials['username']
        elif form in ('first_totp', 'totp', 'platform_totp'):
            if form == 'first_totp':
                text = next((field.get('label', {}).get('en', '') for field in step.get('fields', []) if field.get('name') == 'enrollment'), '')
                match = re.search(r'([A-Z2-7]{16,})\s*$', text)
                if not match:
                    raise BootstrapError('TOTP enrollment missing')
                credentials['totpSecret'] = match[1]
                checkpoint('totp-enrollment')  # Seal recovery material before confirming it.
            fields = [field['name'] for field in step.get('fields', []) if field.get('kind') == 'otp']
            if len(fields) != 1 or fields[0] not in ('otp', 'code'):
                raise BootstrapError('unsupported OTP form')
            values = {fields[0]: otp(credentials)}
        else:
            raise BootstrapError('unsupported login form')
        transaction_id = transaction.get('id', '')
        if not re.fullmatch(r'[a-f0-9-]{36}', transaction_id):
            raise BootstrapError('invalid transaction id')
        transaction = client.request('POST', '/api/iam/v1/login/transactions/' + transaction_id + '/advance',
                                     {'type': 'form', 'formId': form, 'values': values})
    raise BootstrapError('login step limit exceeded')


def bootstrap(access, enrollment, recipient, emit, client_type=Client, ca_file=None):
    if access.get('mode') != 'ip':
        raise BootstrapError('this bootstrap currently requires the explicit IP deployment mode')
    state = {'schemaVersion': 1, 'stage': 'prepared', 'admin': {'username': 'admin-' + secrets.token_hex(5), 'password': secrets.token_urlsafe(32)},
             'member': {'username': 'member-' + secrets.token_hex(5), 'password': secrets.token_urlsafe(32)}}

    def checkpoint(stage):
        state['stage'] = stage
        emit(seal(state, recipient))

    checkpoint('prepared')
    emergency = client_type(access['adminOrigin'], ca_file)
    sessions = [emergency]
    try:
        providers = emergency.request('GET', '/api/iam/v1/login/providers')['items']
        provider = lambda kind: next(item['id'] for item in providers if item['connectorType'] == kind)
        login(emergency, provider('emergency'), dict(enrollment), checkpoint)
        roles = emergency.request('GET', '/api/org/v1/admin/roles')['items']
        role = lambda key: next(item['id'] for item in roles if item['key'] == key and item['builtin'])

        def create(actor, name, role_key):
            credentials = state[name]
            invitation = actor.write('/api/org/v1/admin/invitations', {'memberType': 'member', 'roleIds': [role(role_key)],
                                      'groupIds': [], 'providerIds': [provider('team-account')], 'maxUses': 1})
            portal = client_type(access['portalOrigin'], ca_file)
            sessions.append(portal)
            account = login(portal, provider('team-account'), credentials, checkpoint, invitation['code'])
            credentials['accountId'] = account
            checkpoint(name + '-registered')
            fresh = client_type(access['adminOrigin'] if name == 'admin' else access['portalOrigin'], ca_file)
            sessions.append(fresh)
            if login(fresh, provider('team-account'), credentials, checkpoint) != account:
                raise BootstrapError('fresh login identity mismatch')
            credentials['providerType'] = 'team-account'
            checkpoint(name + '-verified')
            return fresh

        normal = create(emergency, 'admin', 'owner')
        normal.request('GET', '/api/org/v1/admin/roles')
        normal.request('GET', '/api/workspace/v1/admin/nodes')
        create(normal, 'member', 'member')
        # Only rotate after independent normal-admin login and permission checks.
        state['emergencyRecovery'] = normal.write('/api/iam/v1/admin/emergency/rotate', {})
        checkpoint('complete')
        return state
    finally:
        for client in sessions:
            try:
                token = client.csrf or next((value for key, value in client.cookies.items() if key.endswith('auriga_csrf')), None)
                headers = {'X-Auriga-CSRF': token} if token else None
                client.request('POST', '/api/iam/v1/logout', {}, headers, expected=(200, 204, 401))
            except Exception:
                print('bootstrap session cleanup failed; inspect session state', file=sys.stderr)
            client.cookies.clear()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--access', required=True)
    parser.add_argument('--enrollment', required=True)
    parser.add_argument('--recipient', required=True)
    parser.add_argument('--tls-ca')
    args = parser.parse_args()
    recipient = serialization.load_pem_public_key(Path(args.recipient).read_bytes())
    # Validate the recipient before authentication or any write.
    seal({}, recipient)
    bootstrap(json.loads(Path(args.access).read_text()), json.loads(Path(args.enrollment).read_text()), recipient,
              lambda envelope: print('AURIGA_BOOTSTRAP_ENVELOPE ' + json.dumps(envelope), flush=True), ca_file=args.tls_ca)


if __name__ == '__main__':
    try:
        main()
    except BootstrapError as error:
        print('admin bootstrap failed: ' + str(error), file=sys.stderr)
        sys.exit(1)
    except Exception:
        print('admin bootstrap failed: transport, material or dependency error; recover the last encrypted checkpoint', file=sys.stderr)
        sys.exit(1)
