"""One-shot service virtual key provisioning; never recreates a revoked key."""
import hashlib
import json
from pathlib import Path
import urllib.error
import urllib.request


def database_connection():
    import psycopg
    return psycopg.connect(host='postgres', dbname='litellm', user='auriga_litellm',
                           password=Path('/run/secrets/litellm_pg_password').read_text().strip(),
                           connect_timeout=10)


def claim_service_key(fingerprint):
    # Serialize the first DDL/claim. Commit the claim BEFORE any key API call:
    # interrupted provisioning requires operator recovery, never an automatic retry.
    with database_connection() as connection:
        with connection.cursor() as cursor:
            cursor.execute('SELECT pg_advisory_xact_lock(7148083201001)')
            cursor.execute('CREATE SCHEMA IF NOT EXISTS auriga_provisioning')
            cursor.execute('''CREATE TABLE IF NOT EXISTS auriga_provisioning.service_key_state (
                service_name text PRIMARY KEY,
                key_hash text NOT NULL CHECK (key_hash ~ '^[0-9a-f]{64}$'),
                state text NOT NULL CHECK (state IN ('claimed', 'ready')),
                created_at timestamptz NOT NULL DEFAULT now())''')
            cursor.execute('''SELECT key_hash,state FROM auriga_provisioning.service_key_state
                              WHERE service_name=%s FOR UPDATE''', ('auriga-team',))
            row = cursor.fetchone()
            if row is not None:
                if row != (fingerprint, 'ready'):
                    raise RuntimeError('incomplete or changed service key; explicit operator recovery required')
                return False
            cursor.execute('''INSERT INTO auriga_provisioning.service_key_state
                              (service_name,key_hash,state) VALUES (%s,%s,'claimed')''',
                           ('auriga-team', fingerprint))
    return True


def complete_service_key(fingerprint):
    with database_connection() as connection:
        with connection.cursor() as cursor:
            cursor.execute('''UPDATE auriga_provisioning.service_key_state SET state='ready'
                              WHERE service_name=%s AND key_hash=%s AND state='claimed' ''',
                           ('auriga-team', fingerprint))
            if cursor.rowcount != 1:
                raise RuntimeError('service key claim changed')


def provision():
    master = Path('/run/secrets/litellm_master').read_text().strip()
    team = Path('/run/secrets/litellm_team').read_text().strip()
    fingerprint = hashlib.sha256(team.encode()).hexdigest()
    if not claim_service_key(fingerprint):
        return
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    # Query only the hash, never put the raw key in a URL or logs.
    req = urllib.request.Request('http://litellm:4000/key/info?key=' + fingerprint,
                                 headers={'Authorization': 'Bearer ' + master})
    try:
        with opener.open(req, timeout=10) as response:
            exists = response.status == 200
    except urllib.error.HTTPError as error:
        status = error.code
        error.close()
        if status not in (400, 404):
            raise RuntimeError('cannot inspect service key') from None
        exists = False
    if not exists:
        body = json.dumps({'key': team, 'key_alias': 'auriga-team',
                           'models': ['all-proxy-models'], 'key_type': 'llm_api',
                           'metadata': {'owner': 'auriga-ai-gateway'}}).encode()
        req = urllib.request.Request('http://litellm:4000/key/generate', data=body,
                                     headers={'Authorization': 'Bearer ' + master,
                                              'Content-Type': 'application/json'})
        with opener.open(req, timeout=30) as response:
            if response.status != 200:
                raise RuntimeError('cannot provision service key')
    complete_service_key(fingerprint)


if __name__ == '__main__':
    try:
        provision()
    except Exception:
        # Upstream error bodies and request objects can contain credentials.
        raise SystemExit('LiteLLM service key provisioning failed; inspect service health') from None
