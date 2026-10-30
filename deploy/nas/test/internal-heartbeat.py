#!/usr/bin/env python3
"""Check the normalized NAS config without starting services or printing secrets."""
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
compose = json.loads(pathlib.Path(sys.argv[2]).read_text())
values = dict(
    line.split('=', 1)
    for line in (root / 'stack.env').read_text().splitlines()
    if line and not line.startswith('#') and '=' in line
)
edge_config = json.loads((root / 'config/materials/edge.config.json').read_text())
services = compose['services']
edge = services['edge']
panel = services['panel-prepare']
renew = services.get('panel-renew', {})
agent = services['agent']
env = agent['environment']
failed = []


def check(ok, name):
    if not ok:
        failed.append(name)
        print('FAIL internal_heartbeat_' + name)


edge_name = 'auriga-nas-test-edge'
check('panel-prepare' not in edge.get('depends_on', {}), 'panel_failure_not_startup_gate')
check(panel.get('network_mode') == 'none' and not panel.get('cap_add') and panel.get('read_only'), 'panel_signer_isolation')
check(panel.get('command') == ['-best-effort'], 'panel_startup_best_effort')
check(renew.get('image') == panel.get('image') and renew.get('command') == ['-renew'], 'panel_hourly_sidecar')
check(renew.get('network_mode') == 'none' and renew.get('read_only') and not renew.get('cap_add'), 'panel_renewal_isolated')
check(renew.get('user') == '10001:10001' and renew.get('restart') == 'unless-stopped', 'panel_renewal_identity')
check(renew.get('volumes') == panel.get('volumes'), 'panel_renewal_exact_mounts')
check('panel-renew' not in edge.get('depends_on', {}), 'panel_renewal_not_startup_gate')
check('-probe-tcp' in edge.get('healthcheck', {}).get('test', []) and '-check' not in edge.get('healthcheck', {}).get('test', []), 'edge_health_independent_of_panel')
access = json.loads((root / 'config/materials/access.json').read_text())
check(edge.get('container_name') == edge_name, 'stable_edge_name')
check(edge.get('labels', {}).get('auriga.role') == 'edge', 'trusted_edge_label')
check(access['instanceControl']['edgeContainer'] == edge_name, 'matching_edge_reference')
check('AURIGA_AGENT_INSTANCE_PLATFORM_URL' not in env and 'AURIGA_AGENT_INSTANCE_PARENT_DOMAIN' not in env and 'AURIGA_AGENT_DOCKER_EDGE_CONTAINER' not in env, 'no_legacy_origin_override')
check(env.get('AURIGA_DEPLOYMENT_ACCESS_FILE') == '/etc/auriga/access.json', 'agent_shared_access')
check(access['instanceControl']['dnsAlias'] != access['portalOrigin']['host'], 'browser_alias_separation')
check(edge_config['accessConfigFile'] == '/etc/auriga/access.json', 'edge_shared_access')
check(edge_config.get('instanceControlIngress') == {'denyLocalIPv4': [values['AURIGA_EDGE_LAN_IP']]}, 'kernel_local_lan_target_excluded')
check(edge_config['iamHop']['serverName'] == 'platform' and edge_config['iamRpc']['address'] == 'platform:9091', 'dedicated_iam_hop_rpc')
check(edge_config['iamRpc']['serviceTokenFile'] == '/run/auriga-internal/edge/service.jwt', 'continued_service_token')
for service in ['edge', 'platform', 'agent']:
    matches = [v for v in services[service]['volumes'] if v['target'] == '/etc/auriga/access.json']
    check(len(matches) == 1 and matches[0]['source'] == str(root / 'config/materials/access.json') and matches[0].get('read_only'), service + '_same_access_readonly')
for service, readonly in [('platform', False), ('edge', True)]:
    matches = [v for v in services[service]['volumes'] if v['target'] == '/run/auriga-internal']
    check(len(matches) == 1 and matches[0]['source'] == str(root / 'internal-tokens') and bool(matches[0].get('read_only')) == readonly, service + '_token_directory_mount')
check(edge['networks']['internal']['ipv4_address'] == values['AURIGA_INTERNAL_EDGE_IP'], 'exact_iam_peer_source')
check(
    set(edge['networks']) == {'internal', 'lan'} and not edge.get('ports'),
    'edge_network_boundary',
)
check(compose['networks']['internal']['internal'] is True, 'internal_network')
check(agent['network_mode'] == 'host', 'existing_agent_host_mode')
check(
    all('lan' not in services[name]['networks']
        for name in ['platform', 'postgres', 'nats', 'registry']),
    'backends_off_lan',
)
check(
    all(port.get('host_ip') == '127.0.0.1'
        for name in ['platform', 'registry']
        for port in services[name]['ports']),
    'backend_loopback_publishes',
)
check(
    all(not services[name].get('ports') for name in ['postgres', 'nats']),
    'database_no_host_ports',
)
check(
    all(services[name].get('privileged', False) is False
        and {'ALL', 'CAP_ALL'}.intersection(services[name].get('cap_drop', []))
        for name in services),
    'no_privilege_expansion',
)
check(
    all('no-new-privileges:true' in services[name]['security_opt']
        for name in services),
    'no_new_privileges',
)
platform_env = services['platform']['environment']
native = {
    'ID': 'auriga-dsh',
    'TENANT': values.get('AURIGA_NAS_NATIVE_CLIENT_TENANT_ID') or '00000000-0000-0000-0000-000000000001',
    'SECRETHASH': '',
    'REDIRECTURIS': '',
    'SCOPES': 'auriga.connect',
    'GRANTS': 'urn:ietf:params:oauth:grant-type:device_code,refresh_token',
    'PLATFORM': 'true',
    'NATIVECONNECT': 'true',
}
check(
    {key: str(value) for key, value in platform_env.items()
     if key.startswith('AURIGA_IAM_CLIENTS_')}
    == {'AURIGA_IAM_CLIENTS_0_' + key: value for key, value in native.items()},
    'native_public_client_exact_registration',
)
check(native['TENANT'] == access['tenantId'], 'native_client_matches_installation_tenant')
edge_env = edge['environment']
check(platform_env.get('AURIGA_LINKS_ALLOWED_ORIGINS', '') == values.get('AURIGA_LINKS_ALLOWED_ORIGINS', ''), 'links_platform_allowlist')
check(platform_env.get('AURIGA_LINKS_TRUSTED_PROXIES') == values['AURIGA_INTERNAL_EDGE_IP'], 'links_backend_only_edge_peer')
check(edge_env.get('AURIGA_EDGE_LINKS_ALLOWED_ORIGINS', '') == values.get('AURIGA_LINKS_ALLOWED_ORIGINS', ''), 'links_edge_allowlist')
check(edge_env.get('AURIGA_EDGE_LINKS_TRUSTED_PROXIES', '') == values.get('AURIGA_LINKS_TRUSTED_PROXIES', ''), 'links_edge_proxy_trust')
for key, default in [('AURIGA_IAM_NETWORK_SOURCE_MAX_SECONDS', '3600'),
                     ('AURIGA_IAM_NETWORK_SOURCE_DECISION_SECONDS', '60')]:
    expected = values.get(key) or default
    check(str(platform_env.get(key)) == expected, key.lower())
check(platform_env.get('SPRING_PROFILES_ACTIVE') == 'combined', 'no_dev_profile')
check(platform_env.get('SERVER_SSL_ENABLED') == 'true' and platform_env.get('AURIGA_IAM_EDGE_TOKEN_EXPORT_ENABLED') == 'true', 'mtls_and_publisher_enabled')
check(services['platform']['user'] == '10001:10001' and '65532' in services['platform']['group_add'], 'publisher_fixed_uid_and_reader_group')
check(
    not any('EMERGENCY' in key and ('REMOTE' in key or 'PROXY' in key)
            for key in platform_env),
    'no_dev_emergency_override',
)
check(
    not any('OBSERVER' in key or 'HEARTBEAT_PROXY' in key for key in env),
    'no_observation_proxy',
)
if failed:
    sys.exit(1)
print('PASS internal_heartbeat_normalized_config')
