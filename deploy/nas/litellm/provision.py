"""One-shot service virtual key provisioning; never recreates a revoked key."""
import hashlib
import json
import os
from pathlib import Path
import urllib.error
import urllib.request


def provision():
    marker = Path('/state/team-provisioned')
    master = Path('/run/secrets/litellm_master').read_text().strip()
    team = Path('/run/secrets/litellm_team').read_text().strip()
    fingerprint = hashlib.sha256(team.encode()).hexdigest()
    if marker.exists():
        if marker.read_text() != fingerprint:
            raise RuntimeError('service key changed; explicit operator migration required')
        return
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    # Query only the hash, never put the raw key in a URL or logs. A crash after
    # key creation but before writing the marker can safely finish on restart.
    req = urllib.request.Request('http://litellm:4000/key/info?key=' + fingerprint,
                                 headers={'Authorization': 'Bearer ' + master})
    try:
        with opener.open(req, timeout=10) as response:
            exists = response.status == 200
    except urllib.error.HTTPError as error:
        if error.code not in (400, 404):
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
    with marker.open('x') as output:
        os.chmod(marker, 0o600)
        output.write(fingerprint)


if __name__ == '__main__':
    try:
        provision()
    except Exception:
        # Upstream error bodies and request objects can contain credentials.
        raise SystemExit('LiteLLM service key provisioning failed; inspect service health') from None
