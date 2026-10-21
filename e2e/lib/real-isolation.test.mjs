import test from 'node:test';
import assert from 'node:assert/strict';
import { assertRuntimeIsolation } from './real-dsh.mjs';

test('real isolation rejects a privileged, cross-network or writable-metadata runtime', () => {
  const name = 'auriga-net-inst-test';
  const info = {
    Id: 'instance-id',
    Config: { User: '200000:200000' },
    HostConfig: {
      Privileged: false,
      ReadonlyRootfs: true,
      CapDrop: ['ALL'],
      SecurityOpt: ['no-new-privileges:true'],
      Memory: 1024,
      NanoCpus: 1000,
      PidsLimit: 10,
    },
    Mounts: [
      { Destination: '/run/auriga', RW: false },
      { Destination: '/home/dsh', RW: true },
    ],
    NetworkSettings: { Networks: { [name]: {} } },
  };
  const network = {
    Id: 'network-id',
    Name: name,
    Internal: true,
    Driver: 'bridge',
    Labels: { 'auriga.instance': 'inst-test', 'auriga.managed': 'true' },
    Containers: { 'instance-id': {}, 'edge-id': {} },
  };
  const edge = {
    Id: 'edge-id',
    Config: { Labels: { 'auriga.role': 'edge' } },
    NetworkSettings: {
      Networks: {
        [name]: { NetworkID: 'network-id', Aliases: ['portal.example.com'] },
      },
    },
  };
  assertRuntimeIsolation(info, network, edge, 'inst-test');
  for (const mutate of [
    (i) => {
      i.Config.User = '0:0';
    },
    (i) => {
      i.HostConfig.Privileged = true;
    },
    (i) => {
      i.HostConfig.ReadonlyRootfs = false;
    },
    (i) => {
      i.HostConfig.CapDrop = [];
    },
    (i) => {
      i.HostConfig.SecurityOpt = [];
    },
    (i) => {
      i.HostConfig.Memory = 0;
    },
    (i) => {
      i.Mounts[0].RW = true;
    },
    (i) => {
      i.NetworkSettings.Networks.control = {};
    },
  ]) {
    const copy = structuredClone(info);
    mutate(copy);
    assert.throws(() =>
      assertRuntimeIsolation(copy, network, edge, 'inst-test'),
    );
  }
  for (const mutate of [
    (n) => {
      n.Internal = false;
    },
    (n) => {
      n.Labels['auriga.instance'] = 'other';
    },
    (n) => {
      n.Containers.foreign = {};
    },
  ]) {
    const copy = structuredClone(network);
    mutate(copy);
    assert.throws(() => assertRuntimeIsolation(info, copy, edge, 'inst-test'));
  }
  const wrongAlias = structuredClone(edge);
  wrongAlias.NetworkSettings.Networks[name].Aliases = [];
  assert.throws(() =>
    assertRuntimeIsolation(info, network, wrongAlias, 'inst-test'),
  );
});
