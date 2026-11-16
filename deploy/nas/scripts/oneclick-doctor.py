#!/usr/bin/env python3
"""Read-only authenticated probes, executed by the fixed-UID operator helper."""
import base64
import datetime
import importlib.util
import json
import os
from pathlib import Path
import stat

from cryptography.hazmat.primitives.serialization import load_der_public_key


def probe():
    spec = importlib.util.spec_from_file_location('bootstrap', '/tools/admin-bootstrap.py')
    bootstrap = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(bootstrap)
    metadata = Path('/enrollment.json').lstat()
    if (not stat.S_ISREG(metadata.st_mode) or stat.S_IMODE(metadata.st_mode) != 0o600
            or metadata.st_uid != os.geteuid() or metadata.st_nlink != 1):
        raise ValueError('enrollment permissions')
    access = json.loads(Path('/access.json').read_text())
    if access['adminOrigin']['scheme'] != 'http' or access['ingress']['kind'] != 'direct':
        raise ValueError('proxy/direct TLS requires browser login verification')
    client = bootstrap.Client(access['adminOrigin'])
    client.port = access['listeners']['admin']['port']
    credentials = json.loads(Path('/enrollment.json').read_text())
    providers = client.request('GET', '/api/iam/v1/login/providers')['items']
    provider = next(item['id'] for item in providers if item['connectorType'] == 'emergency')
    bootstrap.login(client, provider, credentials, lambda _: None)
    print('PASS emergency_login password+TOTP')
    revocations = client.request('GET', '/api/resource/v1/revocations')
    signature = revocations.pop('signature')
    root = json.loads(Path('/roots.json').read_text())['revocation']
    if signature['alg'] != 'Ed25519' or signature['kid'] != root['kid']:
        raise ValueError('revocation signer')
    # Revocation schema admits integer counters, ASCII keys, UUID/digest/time
    # strings and arrays. Sorted compact UTF-8 JSON is its existing JCS form.
    canonical = json.dumps(revocations, sort_keys=True, separators=(',', ':'), ensure_ascii=False).encode()
    public = load_der_public_key(base64.b64decode(root['publicKeySpkiBase64'], validate=True))
    value = signature['value']
    public.verify(base64.urlsafe_b64decode(value + '=' * (-len(value) % 4)), canonical)
    now = datetime.datetime.now(datetime.timezone.utc)
    issued = datetime.datetime.fromisoformat(revocations['issuedAt'].replace('Z', '+00:00'))
    expires = datetime.datetime.fromisoformat(revocations['expiresAt'].replace('Z', '+00:00'))
    if (revocations['tenantId'] != access['tenantId'] or not issued <= now < expires
            or not datetime.timedelta(0) < expires - issued <= datetime.timedelta(hours=24)):
        raise ValueError('revocation validity')
    print('PASS resource_revocations Ed25519+tenant+validity')


if __name__ == '__main__':
    try:
        probe()
    except Exception:
        print('FAIL authenticated_probe — check clock, enrollment, signer and access mode')
        raise SystemExit(1) from None
