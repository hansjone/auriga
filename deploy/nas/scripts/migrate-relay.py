"""Read-only LiteLLM export and retryable Bifrost management import. Secrets stay in memory."""
import base64
import asyncio
import contextlib
from collections import defaultdict
from datetime import datetime, timedelta, timezone
from decimal import Decimal, ROUND_CEILING, ROUND_FLOOR
import hashlib
import json
import os
from pathlib import Path
import re
import sys
from urllib.parse import urlencode, urlsplit
import urllib.request

MAX_INTEGER = 9007199254740991
MODEL = re.compile(r'^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$')


class MigrationError(Exception):
    pass


class SnapshotSource:
    def __init__(self, models, keys):
        self.models, self.keys = models, keys

    def get(self, path):
        if path == '/model/info':
            return {'data': self.models}
        from urllib.parse import parse_qs
        query = parse_qs(urlsplit(path).query)
        page = int(query['page'][0])
        return {'keys': self.keys[(page - 1) * 100:page * 100], 'total_pages': max(1, (len(self.keys) + 99) // 100)}


async def load_litellm():
    # Read the original encrypted DB with the original release's decryptor.
    # The source transaction is explicitly read-only and no snapshot is written.
    os.environ['LITELLM_MASTER_KEY'] = Path('/old-master').read_text().strip()
    os.environ['LITELLM_SALT_KEY'] = Path('/old-salt').read_text().strip()
    os.environ['LITELLM_LOCAL_MODEL_COST_MAP'] = 'True'
    from urllib.parse import quote
    password = quote(Path('/old-pg').read_text().strip(), safe='')
    os.environ['DATABASE_URL'] = 'postgresql://auriga_litellm:' + password + '@postgres:5432/litellm'
    from prisma import Prisma
    from litellm.proxy.common_utils.encrypt_decrypt_utils import decrypt_value_helper
    client = Prisma()
    await client.connect()
    try:
        async with client.tx(timeout=timedelta(seconds=120)) as transaction:
            await transaction.execute_raw('SET TRANSACTION READ ONLY')
            rows = await transaction.litellm_proxymodeltable.find_many(take=1001)
            keys = await transaction.litellm_verificationtoken.find_many(take=10001)
        models = []
        for row in rows:
            params = row.litellm_params
            if isinstance(params, str):
                params = json.loads(params)
            params = {key: decrypt_value_helper(value=value, key=key, return_original_value=key != 'api_key')
                      for key, value in params.items()}
            info = row.model_info
            if isinstance(info, str):
                info = json.loads(info)
            models.append({'model_name': row.model_name, 'litellm_params': params,
                           'model_info': {**(info or {}), 'id': row.model_id}})
        return SnapshotSource(models, [key.model_dump(mode='json') for key in keys])
    finally:
        await client.disconnect()


class API:
    def __init__(self, origin, authorization, tenant=None):
        self.origin = origin
        self.headers = {'Authorization': authorization, 'Content-Type': 'application/json'}
        if tenant:
            self.headers['X-Auriga-Tenant'] = tenant
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def request(self, method, path, body=None, operation=None):
        headers = dict(self.headers)
        if operation:
            headers['Idempotency-Key'] = operation
        if method == 'POST' and path == '/api/pools':
            headers['If-None-Match'] = '*'
        data = None if body is None else json.dumps(body, sort_keys=True, separators=(',', ':')).encode()
        request = urllib.request.Request(self.origin + path, data=data, headers=headers, method=method)
        try:
            with self.opener.open(request, timeout=30) as response:
                raw = response.read(8 * 1024 * 1024 + 1)
            if len(raw) > 8 * 1024 * 1024:
                raise MigrationError('response_limit')
            return json.loads(raw)
        except Exception:
            raise MigrationError('management_request_failed') from None

    def get(self, path):
        return self.request('GET', path)

    def write(self, method, path, body, operation):
        return self.request(method, path, body, operation)


def operation(kind, identifier):
    return 'litellm-v1-' + hashlib.sha256((kind + ':' + identifier).encode()).hexdigest()


def price(value):
    if value is None:
        return 1
    result = int((Decimal(str(value)) * 1000000000000).to_integral_value(rounding=ROUND_CEILING))
    if not 0 <= result <= MAX_INTEGER:
        raise MigrationError('unsupported_price')
    return max(1, result)


def plan(source):
    models = source.get('/model/info')['data']
    providers, pools = [], defaultdict(list)
    for model in models:
        params, info = model['litellm_params'], model.get('model_info', {})
        inert = {'allow_client_keepalive_override', 'merge_reasoning_content_in_choices',
                 'use_in_pass_through', 'use_litellm_proxy', 'use_xai_oauth'}
        extras = set(params) - {'model', 'api_base', 'api_key'}
        if extras - inert or any(params[key] not in (None, False) for key in extras):
            raise MigrationError('unsupported_model_parameters')
        alias, upstream = model['model_name'], params['model']
        kind, separator, remote = upstream.partition('/')
        if not separator:
            kind, remote = 'openai', upstream
        if kind not in ('openai', 'anthropic', 'gemini', 'deepseek', 'minimax') or not MODEL.fullmatch(alias):
            raise MigrationError('unsupported_model')
        defaults = {'openai': 'https://api.openai.com', 'anthropic': 'https://api.anthropic.com',
                    'gemini': 'https://generativelanguage.googleapis.com', 'deepseek': 'https://api.deepseek.com',
                    'minimax': 'https://api.minimax.io'}
        base = params.get('api_base') or defaults[kind]
        parsed = urlsplit(base)
        if parsed.scheme not in ('http', 'https') or not parsed.hostname or parsed.path.rstrip('/') not in ('', '/v1') or parsed.query or parsed.fragment or parsed.username:
            raise MigrationError('unsupported_provider_path')
        base = parsed.scheme + '://' + parsed.netloc
        secret = params.get('api_key')
        if not isinstance(secret, str) or not secret or secret.startswith('os.environ/') or '****' in secret:
            raise MigrationError('provider_secret_unavailable')
        identifier = str(info.get('id') or operation('model', alias + ':' + upstream))
        configuration = {'name': 'litellm-' + hashlib.sha256(identifier.encode()).hexdigest(),
                         'kind': kind, 'baseUrl': base, 'enabled': True}
        providers.append((identifier, configuration, secret))
        pools[alias].append((identifier, remote, info))
        # Validate the complete source before the first destination write.
        price(info.get('input_cost_per_token'))
        price(info.get('output_cost_per_token'))
        context = int(info.get('max_input_tokens') or 65536)
        output = int(info.get('max_output_tokens') or 4096)
        if not remote or not 1 <= output <= context <= 10000000:
            raise MigrationError('unsupported_context_limits')
    keys = []
    for page in range(1, 101):
        result = source.get('/key/list?' + urlencode({'return_full_object': 'true', 'page': page, 'size': 100}))
        for item in result['keys']:
            if isinstance(item, str):
                item = source.get('/key/info?' + urlencode({'key': item}))['info']
            if any(item.get(field) for field in (
                    'tpm_limit', 'tpd_limit', 'max_parallel_requests', 'model_max_budget',
                    'allowed_cache_controls', 'permissions', 'config',
                    'team_id', 'user_id', 'organization_id', 'project_id', 'budget_id',
                    'budget_limits', 'budget_fallbacks', 'aliases', 'policies', 'router_settings',
                    'access_group_ids', 'object_permission_id', 'jwt_key_mappings', 'auto_rotate')):
                raise MigrationError('unsupported_key_policy')
            if item.get('budget_duration') not in (None, '', '1mo'):
                raise MigrationError('unsupported_budget_period')
            routes = set(item.get('allowed_routes') or [])
            if routes and not routes.intersection({'llm_api_routes', 'openai_routes'}) and not {'/v1/chat/completions', '/v1/models'}.issubset(routes):
                raise MigrationError('unsupported_key_routes')
            raw = item.get('token') or item.get('key')
            if isinstance(raw, str) and raw.startswith('sk-'):
                digest = hashlib.sha256(raw.encode()).hexdigest()
            elif isinstance(raw, str) and re.fullmatch('[a-f0-9]{64}', raw):
                digest = raw
            else:
                raise MigrationError('legacy_key_hash_unavailable')
            allowed = item.get('models') or ['*']
            if allowed == ['all-proxy-models']:
                allowed = ['*']
            if allowed != ['*'] and any(name not in pools for name in allowed):
                raise MigrationError('unsupported_key_models')
            budget = item.get('max_budget')
            remaining = MAX_INTEGER if budget is None else max(0, int(((Decimal(str(budget)) - Decimal(str(item.get('spend') or 0))) * 1000000).to_integral_value(rounding=ROUND_FLOOR)))
            rpm = int(item.get('rpm_limit') or 0)
            if remaining > MAX_INTEGER or not 0 <= rpm <= 1000000:
                raise MigrationError('unsupported_key_budget')
            body = {'name': 'litellm:' + digest, 'active': not bool(item.get('blocked')),
                    'monthlyBudgetMicros': remaining, 'requestsPerMinute': rpm, 'models': allowed}
            expires = item.get('expires')
            if expires:
                expires = datetime.fromisoformat(expires.replace('Z', '+00:00'))
                if expires.tzinfo is None:
                    expires = expires.replace(tzinfo=timezone.utc)
                expires = expires.isoformat()
            keys.append((digest, body, expires))
        if page >= int(result.get('total_pages') or 1):
            break
    else:
        raise MigrationError('key_page_limit')
    if len(models) > 1000 or any(len(routes) > 8 for routes in pools.values()):
        raise MigrationError('model_limit')
    return providers, pools, keys


def migrate(source, target):
    providers, pools, keys = plan(source)
    identifiers = {}
    for identifier, configuration, secret in providers:
        provider = target.write('POST', '/api/providers', configuration, operation('provider', identifier))
        identifiers[identifier] = provider['id']
        target.write('POST', '/api/providers/' + provider['id'] + '/keys', {'secret': secret}, operation('credential', identifier))
    for name, routes in pools.items():
        info = routes[0][2]
        body = {'id': name, 'enabled': True, 'routes': [{'providerId': identifiers[key], 'model': model, 'weight': 1} for key, model, _ in routes],
                'contextWindow': int(info.get('max_input_tokens') or 65536), 'maxOutputTokens': int(info.get('max_output_tokens') or 4096),
                'price': {'currency': 'USD', 'version': 'litellm-import-v1', 'effectiveAt': '2000-01-01T00:00:00Z',
                          'inputMicrosPerMillionTokens': price(info.get('input_cost_per_token')),
                          'outputMicrosPerMillionTokens': price(info.get('output_cost_per_token'))}}
        target.write('POST', '/api/pools', body, operation('pool', name))
    for digest, body, expires in keys:
        key = target.write('POST', '/api/virtual-keys', body, operation('key', digest))['key']
        binding = {'keyId': key['id'], 'sha256': digest}
        if expires:
            binding['expiresAt'] = expires
        target.write('PUT', '/api/legacy-key-bindings', binding, operation('binding', digest))
    return {'providers': len(providers), 'models': len(pools), 'keys': len(keys),
            'minimumPriceModels': sum(1 for routes in pools.values() if not routes[0][2].get('input_cost_per_token') or not routes[0][2].get('output_cost_per_token')),
            'unlimitedKeysCapped': sum(1 for _, body, _ in keys if body['monthlyBudgetMicros'] == MAX_INTEGER)}


def main():
    access = json.loads(Path('/access.json').read_text())
    password = Path('/new-admin').read_text().strip()
    authorization = 'Basic ' + base64.b64encode(('auriga:' + password).encode()).decode()
    target = API('http://bifrost:8080', authorization, access['tenantId'])
    # Third-party decryption/client diagnostics never enter logs or evidence output.
    with quiet_helpers():
        source = asyncio.run(load_litellm())
        result = migrate(source, target)
    print(json.dumps({'migration': 'completed', **result}))


@contextlib.contextmanager
def quiet_helpers():
    # Prisma starts a child engine: redirect OS descriptors as well as Python streams.
    sys.stdout.flush()
    sys.stderr.flush()
    saved = (os.dup(1), os.dup(2))
    try:
        with open(os.devnull, 'w') as sink:
            os.dup2(sink.fileno(), 1)
            os.dup2(sink.fileno(), 2)
            with contextlib.redirect_stdout(sink), contextlib.redirect_stderr(sink):
                yield
    finally:
        for original, backup in zip((1, 2), saved):
            os.dup2(backup, original)
            os.close(backup)


if __name__ == '__main__':
    try:
        main()
    except Exception:
        print('relay migration failed; source data retained, commit withheld', file=sys.stderr)
        sys.exit(1)
