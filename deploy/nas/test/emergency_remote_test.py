"""Run the complete normalized-config checker with emergency-policy mutations."""
import contextlib
import copy
import io
import json
from pathlib import Path
import runpy
import sys
import tempfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
KEY = 'AURIGA_IAM_EMERGENCY_REMOTE_ENABLED'


class EmergencyPolicyTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        materials = self.root / 'config/materials'
        materials.mkdir(parents=True)
        access = json.loads((ROOT / 'access-domain.example.json').read_text())
        (materials / 'access.json').write_text(json.dumps(access))
        (materials / 'edge.config.json').write_text(json.dumps({
            'accessConfigFile': '/etc/auriga/access.json',
            'instanceControlIngress': {'denyLocalIPv4': ['192.0.2.10']},
            'iamHop': {'serverName': 'platform'},
            'iamRpc': {'address': 'platform:9091', 'serviceTokenFile': '/run/auriga-internal/edge/service.jwt'},
        }))
        self.stack = 'AURIGA_EDGE_LAN_IP=192.0.2.10\nAURIGA_INTERNAL_EDGE_IP=192.0.2.138\n'
        common = {'privileged': False, 'cap_drop': ['ALL'], 'security_opt': ['no-new-privileges:true']}
        services = {name: copy.deepcopy(common) for name in
                    ('edge', 'platform', 'agent', 'panel-prepare', 'panel-renew', 'postgres', 'nats', 'registry')}
        for name in ('edge', 'platform', 'agent'):
            services[name]['volumes'] = [{
                'source': str(materials / 'access.json'), 'target': '/etc/auriga/access.json', 'read_only': True,
            }]
        for name in ('platform', 'edge'):
            services[name]['volumes'].append({
                'source': str(self.root / 'internal-tokens'), 'target': '/run/auriga-internal',
                'read_only': name == 'edge',
            })
        for name in ('platform', 'postgres', 'nats', 'registry'):
            services[name]['networks'] = {'internal': {}}
        for name in ('platform', 'registry'):
            services[name]['ports'] = [{'host_ip': '127.0.0.1'}]
        services['edge'].update({
            'container_name': 'auriga-nas-test-edge', 'labels': {'auriga.role': 'edge'},
            'networks': {'internal': {'ipv4_address': '192.0.2.138'}, 'lan': {}},
            'healthcheck': {'test': ['-probe-tcp']}, 'environment': {},
        })
        for name, command in (('panel-prepare', '-best-effort'), ('panel-renew', '-renew')):
            services[name].update({'image': 'fixture-edge', 'network_mode': 'none', 'read_only': True,
                                   'command': [command], 'volumes': []})
        services['panel-renew'].update({'user': '10001:10001', 'restart': 'unless-stopped'})
        services['agent'].update({'network_mode': 'host',
                                  'environment': {'AURIGA_DEPLOYMENT_ACCESS_FILE': '/etc/auriga/access.json'}})
        native = {'ID': 'auriga-dsh', 'TENANT': access['tenantId'], 'SECRETHASH': '', 'REDIRECTURIS': '',
                  'SCOPES': 'auriga.connect', 'GRANTS': 'urn:ietf:params:oauth:grant-type:device_code,refresh_token',
                  'PLATFORM': 'true', 'NATIVECONNECT': 'true'}
        self.environment = {'AURIGA_IAM_CLIENTS_0_' + key: value for key, value in native.items()}
        self.environment.update({
            'AURIGA_LINKS_TRUSTED_PROXIES': '192.0.2.138', 'SPRING_PROFILES_ACTIVE': 'combined',
            'AURIGA_IAM_NETWORK_SOURCE_MAX_SECONDS': '3600', 'AURIGA_IAM_NETWORK_SOURCE_DECISION_SECONDS': '60',
            'SERVER_SSL_ENABLED': 'true', 'AURIGA_IAM_EDGE_TOKEN_EXPORT_ENABLED': 'true', KEY: 'true',
        })
        services['platform'].update({'environment': self.environment, 'user': '10001:10001', 'group_add': ['65532']})
        self.compose = {'services': services, 'networks': {'internal': {'internal': True}}}

    def run_checker(self):
        (self.root / 'stack.env').write_text(self.stack)
        compose_file = self.root / 'compose.json'
        compose_file.write_text(json.dumps(self.compose))
        output = io.StringIO()
        result = 0
        with mock.patch.object(sys, 'argv', ['internal-heartbeat.py', str(self.root), str(compose_file)]), contextlib.redirect_stdout(output):
            try:
                runpy.run_path(str(ROOT / 'test/internal-heartbeat.py'), run_name='__main__')
            except SystemExit as error:
                result = error.code
        return result, output.getvalue()

    def test_only_explicit_boolean_setting_is_allowed(self):
        for value in ('true', 'false'):
            with self.subTest(value=value):
                self.stack += KEY + '=' + value + '\n'
                self.environment[KEY] = value
                self.assertEqual(self.run_checker(), (0, 'PASS internal_heartbeat_normalized_config\n'))

    def test_default_and_empty_setting_require_true(self):
        self.assertEqual(self.run_checker()[0], 0)
        self.stack += KEY + '=\n'
        self.assertEqual(self.run_checker()[0], 0)
        self.environment[KEY] = 'false'
        self.assertEqual(self.run_checker(), (1, 'FAIL internal_heartbeat_no_dev_emergency_override\n'))

    def test_missing_nonboolean_mismatched_and_extra_overrides_are_rejected(self):
        baseline = dict(self.environment)
        mutations = [{}, {KEY: 'TRUE'}, {KEY: True}, {KEY: ''}, {KEY: '1'}, {KEY: 'false'},
                     {KEY: 'true', 'AURIGA_IAM_EMERGENCY_PROXY': 'true'},
                     {KEY: 'true', 'AURIGA_IAM_EMERGENCY_REMOTE_OTHER': 'true'}]
        for mutation in mutations:
            with self.subTest(mutation=mutation):
                self.environment.clear()
                self.environment.update({key: value for key, value in baseline.items() if key != KEY})
                self.environment.update(mutation)
                self.assertEqual(self.run_checker(), (1, 'FAIL internal_heartbeat_no_dev_emergency_override\n'))


if __name__ == '__main__':
    unittest.main()
