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


def claim_service_key(fingerprint, service_name="auriga-team"):
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
                              WHERE service_name=%s FOR UPDATE''', (service_name,))
            row = cursor.fetchone()
            if row is not None:
                if row != (fingerprint, 'ready'):
                    raise RuntimeError('incomplete or changed service key; explicit operator recovery required')
                return False
            cursor.execute('''INSERT INTO auriga_provisioning.service_key_state
                              (service_name,key_hash,state) VALUES (%s,%s,'claimed')''',
                           (service_name, fingerprint))
    return True


def complete_service_key(fingerprint, service_name="auriga-team"):
    with database_connection() as connection:
        with connection.cursor() as cursor:
            cursor.execute('''UPDATE auriga_provisioning.service_key_state SET state='ready'
                              WHERE service_name=%s AND key_hash=%s AND state='claimed' ''',
                           (service_name, fingerprint))
            if cursor.rowcount != 1:
                raise RuntimeError('service key claim changed')


def provision():
    master = Path('/run/secrets/litellm_master').read_text().strip()
    team = Path('/run/secrets/litellm_team').read_text().strip()
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def request(path, body=None):
        req = urllib.request.Request('http://litellm:4000' + path,
            data=None if body is None else json.dumps(body).encode(),
            headers={'Authorization': 'Bearer ' + master, 'Content-Type': 'application/json'})
        with opener.open(req, timeout=30) as response:
            if response.status != 200:
                raise RuntimeError('cannot provision service key')

    def ensure(key, service_name, body, user=None):
        fingerprint = hashlib.sha256(key.encode()).hexdigest()
        if not claim_service_key(fingerprint, service_name):
            return
        try:
            # Query only the hash, never put raw keys in URLs or logs.
            request('/key/info?key=' + fingerprint)
            exists = True
        except urllib.error.HTTPError as error:
            status = error.code
            error.close()
            if status not in (400, 404):
                raise RuntimeError('cannot inspect service key') from None
            exists = False
        if not exists:
            if user:
                request('/user/new', user)
            request('/key/generate', {'key': key, 'key_alias': service_name, **body})
        complete_service_key(fingerprint, service_name)

    ensure(team, 'auriga-team', {'models': ['all-proxy-models'], 'key_type': 'llm_api',
                               'metadata': {'owner': 'auriga-ai-gateway'}})
    reader = Path('/run/secrets/litellm_ui_reader').read_text().strip()
    ensure(reader, 'auriga-edge-reader',
           {'user_id': 'auriga-edge-reader', 'key_type': 'read_only', 'allowed_routes': ['/key/info']},
           {'user_id': 'auriga-edge-reader', 'user_role': 'proxy_admin_viewer', 'auto_create_key': False})


if __name__ == '__main__':
    try:
        provision()
    except Exception:
        # Upstream error bodies and request objects can contain credentials.
        raise SystemExit('LiteLLM service key provisioning failed; inspect service health') from None
