"""Real LiteLLM checks, run only in the isolated test-compose project."""
import hashlib
import json
from pathlib import Path
import secrets
import time
import urllib.error
import urllib.request

opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
master = Path('/run/secrets/litellm_master').read_text().strip()
team = Path('/run/secrets/litellm_team').read_text().strip()


def call(path, key=None, body=None):
    headers = {'Content-Type': 'application/json'}
    if key:
        headers['Authorization'] = 'Bearer ' + key
    request = urllib.request.Request('http://litellm:4000' + path,
                                     data=None if body is None else json.dumps(body).encode(),
                                     headers=headers)
    try:
        with opener.open(request, timeout=15) as response:
            raw = response.read()
            return response.status, json.loads(raw) if raw else {}
    except urllib.error.HTTPError as error:
        # Do not print upstream exceptions or response bodies containing keys.
        try:
            return error.code, json.loads(error.read())
        except (ValueError, OSError):
            return error.code, {}


def expect(condition, name):
    if not condition:
        raise RuntimeError(name)
    print('PASS ' + name, flush=True)


if __name__ == '__main__':
    try:
        expect(call('/health/liveliness')[0] == 200, 'liveliness')
        expect(call('/v1/models')[0] in (401, 403), 'missing key rejected')
        expect(call('/v1/models', 'sk-' + secrets.token_hex(32))[0] in (401, 403), 'wrong key rejected')
        status, result = call('/key/generate', master, {'models': ['test-pool'], 'key_alias': 'external-fixture'})
        expect(status == 200 and 'key' in result, 'external virtual key created')
        key = result['key']
        reader = Path('/run/secrets/litellm_ui_reader').read_text().strip()
        expect(reader != master and Path('/run/secrets/litellm_ui_password').read_text().strip() != master, 'independent UI credentials')
        expect(call('/key/info?key=' + hashlib.sha256(key.encode()).hexdigest(), reader)[0] == 200, 'reader inspects arbitrary key hash')
        expect(call('/key/generate', reader, {'models': ['test-pool']})[0] in (401,403), 'reader cannot create keys')
        expect(call('/v1/models', key)[0] == 200, 'virtual key models')
        request = {'model': 'test-pool', 'messages': [{'role': 'user', 'content': 'local only'}],
                   'user': 'tenant-fixture:member-fixture',
                   'metadata': {'tenant_id': 'tenant-fixture', 'account_id': 'member-fixture'}}
        expect(call('/v1/chat/completions', reader, request)[0] in (401,403), 'reader cannot run inference')
        expect(call('/v1/chat/completions', key, request)[0] == 200, 'virtual key chat through local upstream')
        stream_request = urllib.request.Request('http://litellm:4000/v1/chat/completions',
                                                data=json.dumps({**request, 'stream': True}).encode(),
                                                headers={'Authorization': 'Bearer ' + key, 'Content-Type': 'application/json'})
        with opener.open(stream_request, timeout=15) as response:
            stream = response.read().decode()
            expect(response.status == 200 and 'text/event-stream' in response.headers.get('Content-Type', '')
                   and 'local fixture' in stream and 'data: [DONE]' in stream, 'virtual key streaming chat')
        expect(call('/v1/chat/completions', team, request)[0] == 200, 'provisioned service virtual key')
        expect(call('/key/delete', master, {'keys': [key]})[0] == 200, 'key revoked')
        expect(call('/v1/models', key)[0] in (401, 403), 'revoked key rejected')
        status, result = call('/key/generate', master, {'models': ['test-pool'], 'max_budget': 0.000001})
        expect(status == 200 and 'key' in result, 'budget key created')
        budget = result['key']
        expect(call('/v1/chat/completions', budget, request)[0] == 200, 'initial budget call')
        deadline = time.monotonic() + 30
        rejected = False
        while time.monotonic() < deadline:
            status, error = call('/v1/chat/completions', budget, request)
            detail = error.get('error', {})
            if status in (400, 401, 402, 403, 422, 429) and detail.get('type') == 'budget_exceeded':
                rejected = True
                break
            time.sleep(0.25)
        expect(rejected, 'budget exceeded rejected')
        deadline = time.monotonic() + 30
        found = False
        while time.monotonic() < deadline:
            status, rows = call('/spend/logs?user=tenant-fixture%3Amember-fixture', master)
            records = rows if isinstance(rows, list) else rows.get('data', [])
            if status == 200 and any(row.get('end_user') == request['user'] for row in records):
                found = True
                break
            time.sleep(0.25)
        expect(found, 'member usage persisted')
        customer = 'tenant-fixture:budget-member-' + secrets.token_hex(8)
        expect(call('/customer/new', master, {'user_id': customer, 'max_budget': 0.000001,
                                              'rpm_limit': 60, 'tpm_limit': 100000})[0] == 200,
               'open-source member budget configured')
        customer_request = {**request, 'user': customer}
        expect(call('/v1/chat/completions', team, customer_request)[0] == 200, 'member budget initial call')
        deadline = time.monotonic() + 30
        rejected = False
        while time.monotonic() < deadline:
            status, error = call('/v1/chat/completions', team, customer_request)
            if status in (400, 401, 402, 403, 422, 429) and error.get('error', {}).get('type') == 'budget_exceeded':
                rejected = True
                break
            time.sleep(0.25)
        expect(rejected, 'member budget exceeded rejected')
        import importlib.util
        spec = importlib.util.spec_from_file_location('service_provision', '/test/provision.py')
        provision = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(provision)
        with provision.database_connection() as connection:
            with connection.cursor() as cursor:
                cursor.execute("SELECT state FROM auriga_provisioning.service_key_state WHERE service_name='auriga-team'")
                expect(cursor.fetchone() == ('ready',), 'service key claim persisted in PostgreSQL')
        expect(call('/key/delete', master, {'keys': [team]})[0] == 200, 'service key revoked')
        provision.provision()
        expect(call('/v1/models', team)[0] in (401, 403), 'init without local state cannot resurrect revoked service key')
    except Exception as error:
        # Only fixed check names are safe to show; never echo HTTP/DB errors.
        raise SystemExit('FAIL LiteLLM acceptance; inspect preceding PASS checks') from None
