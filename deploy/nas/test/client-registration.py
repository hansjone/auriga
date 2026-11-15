"""Check both public clients and the private UAC mount in normalized NAS Compose."""
import json
import sys

platform = json.load(open(sys.argv[1]))['services']['platform']
env = platform['environment']
expected = {}
for index, client_id, native in [(0, 'auriga-dsh', 'false'), (1, 'auriga-dsh-native', 'true')]:
    fields = {
        'ID': client_id, 'TENANT': env['AURIGA_IAM_CLIENTS_0_TENANT'],
        'SECRETHASH': '', 'REDIRECTURIS': '', 'SCOPES': 'auriga.connect',
        'GRANTS': 'urn:ietf:params:oauth:grant-type:device_code,refresh_token',
        'PLATFORM': 'true', 'NATIVECONNECT': native,
    }
    expected.update({f'AURIGA_IAM_CLIENTS_{index}_{key}': value for key, value in fields.items()})
assert {key: str(value) for key, value in env.items() if key.startswith('AURIGA_IAM_CLIENTS_')} == expected
mounts = [mount for mount in platform['volumes'] if mount['target'] == '/etc/auriga/uac']
assert len(mounts) == 1 and mounts[0]['read_only'] and mounts[0]['source'].endswith('/config/materials/uac')
