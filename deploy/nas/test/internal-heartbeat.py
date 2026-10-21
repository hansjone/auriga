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
edge_config = json.loads((root / 'config/edge.config.json').read_text())
services = compose['services']
edge = services['edge']
agent = services['agent']
env = agent['environment']
failed = []


def check(ok, name):
    if not ok:
        failed.append(name)
        print('FAIL internal_heartbeat_' + name)


edge_name = 'auriga-nas-test-edge'
portal = values['AURIGA_PORTAL_HOST']
parent = values['AURIGA_INSTANCE_PARENT_DOMAIN']
check(edge.get('container_name') == edge_name, 'stable_edge_name')
check(edge.get('labels', {}).get('auriga.role') == 'edge', 'trusted_edge_label')
check(
    env.get('AURIGA_AGENT_DOCKER_EDGE_CONTAINER') == edge_name,
    'matching_edge_reference',
)
check(
    env.get('AURIGA_AGENT_INSTANCE_PLATFORM_URL') == f'http://{portal}:8080',
    'internal_http_url',
)
check(
    env.get('AURIGA_AGENT_INSTANCE_PARENT_DOMAIN') == parent,
    'matching_instance_parent',
)
check(
    edge_config['portalHost'] == portal
    and edge_config['listen'] == '0.0.0.0:8080',
    'edge_portal_listener',
)
check(edge_config['instanceParentDomain'] == parent, 'edge_parent_domain')
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
        and {'ALL', 'CAP_ALL'}.intersection(services[name]['cap_drop'])
        for name in services),
    'no_privilege_expansion',
)
check(
    all('no-new-privileges:true' in services[name]['security_opt']
        for name in services),
    'no_new_privileges',
)
platform_env = services['platform']['environment']
check(platform_env.get('SPRING_PROFILES_ACTIVE') == 'combined', 'no_dev_profile')
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
