// 端点向量沿用有效签名快照，每次只移动一个端点。
export function extendEndpoints({
  cases,
  issuer_cases,
  boundary_cases,
  signed,
  auth,
  root,
  digest,
}) {
  const firstEndpoint = cases.length;
  const clone = structuredClone;
  const res = (i) => {
    const q = i.publication.predecessor;
    q.keyset = signed(q.keyset.payload);
    i.previous_digest = q.keyset_digest = digest(q.keyset);
    i.publication.keyset = signed(i.publication.keyset.payload);
  };
  const add = (source, id, expected, change) => {
    const v = clone(cases.find((x) => x.id === source));
    v.id = id;
    v.expected = expected;
    change(v.input);
    res(v.input);
    cases.push(v);
  };
  for (const [delta, expected] of [
    [0, 'accept'],
    [1, 'invalid_transition'],
  ]) {
    add(
      'mature-next-activation',
      'maturity-24h-plus-' + delta,
      expected,
      (i) => {
        const at = new Date(Date.parse(i.now) - 86400000 + delta * 1000)
          .toISOString()
          .replace('.000', '');
        for (const list of [
          i.publication.predecessor.keyset.payload.first_publications,
          i.publication.keyset.payload.first_publications,
          i.first_publication_ledger,
        ])
          for (const p of list)
            if (p.kid.startsWith('synthetic-next-')) p.committed_at = at;
        const q = i.publication.predecessor.keyset.payload;
        q.issued_at = at;
        q.valid_until = new Date(Date.parse(at) + 86400000)
          .toISOString()
          .replace('.000', '');
      },
    );
    add(
      'mature-next-activation',
      'overlap-360-minus-' + delta,
      expected,
      (i) => {
        for (const list of [
          i.publication.predecessor.keyset.payload.keys,
          i.publication.keyset.payload.keys,
        ])
          for (const k of list)
            if (k.kid.startsWith('synthetic-business-'))
              k.not_after = new Date(Date.parse(i.now) + (360 - delta) * 1000)
                .toISOString()
                .replace('.000', '');
      },
    );
    add(
      'refresh-valid',
      'issued-now-plus-' + (60 + delta),
      expected,
      (i) =>
        (i.now = new Date(Date.parse(i.now) - (60 + delta) * 1000)
          .toISOString()
          .replace('.000', '')),
    );
    add(
      'refresh-valid',
      'authority-not-before-now-plus-' + delta,
      delta ? 'scope_denied' : 'accept',
      (i) => {
        const p = i.publication;
        p.authority.payload.not_before = new Date(
          Date.parse(i.now) + delta * 1000,
        )
          .toISOString()
          .replace('.000', '');
        p.authority = auth(
          p.authority.payload,
          'Auriga.NetworkRuntimeAuthority.v2',
          root,
          'synthetic-root',
          true,
        );
        p.keyset.payload.authority_digest = digest(p.authority);
        i.latest_control.payload.allowed_authorities = [
          {
            authority_id: p.authority.payload.authority_id,
            authority_digest: digest(p.authority),
          },
        ];
        i.latest_control = auth(
          i.latest_control.payload,
          'Auriga.NetworkRootControl.v2',
          root,
          'synthetic-root',
          true,
        );
      },
    );
  }
  add(
    'restart-future-active-after-clock-correction',
    'restart-partial-purpose-preserves-valid-active',
    'accept',
    (i) => {
      const p = i.publication,
        q = p.predecessor.keyset.payload,
        k = p.keyset.payload;
      const old = q.keys[0];
      old.not_before = '2026-11-12T00:00:00Z';
      k.keys = k.keys.filter((x) => x.purpose !== old.purpose);
      k.keys.push(clone(old));
      k.first_publications = k.first_publications.filter(
        (x) => !x.kid.startsWith('synthetic-restart-0'),
      );
    },
  );
  // 业务钥最大寿命与 active 生效/截止端点。
  for (const [id, before, after, expected] of [
    [
      'business-max-8d',
      '2026-11-12T00:00:00Z',
      '2026-11-20T00:00:00Z',
      'accept',
    ],
    [
      'business-max-8d-plus-1',
      '2026-11-12T00:00:00Z',
      '2026-11-20T00:00:01Z',
      'invalid_transition',
    ],
    [
      'active-start-equals-issued',
      '2026-11-14T00:00:00Z',
      '2026-11-19T00:00:00Z',
      'accept',
    ],
    [
      'active-end-equals-issued',
      '2026-11-12T00:00:00Z',
      '2026-11-14T00:00:00Z',
      'invalid_transition',
    ],
    [
      'active-end-issued-plus-1',
      '2026-11-12T00:00:00Z',
      '2026-11-14T00:00:01Z',
      'accept',
    ],
  ])
    add('refresh-valid', id, expected, (i) => {
      i.publication.predecessor.keyset.payload.keys[0].not_before = before;
      i.publication.predecessor.keyset.payload.keys[0].not_after = after;
      i.publication.keyset.payload.keys[0].not_before = before;
      i.publication.keyset.payload.keys[0].not_after = after;
    });
  for (const value of [-60, -61, 300, 301])
    issuer_cases.push({
      id: 'clock-delta-endpoint-' + value,
      expected: value < -60 || value > 300 ? 'clock_error' : 'accept',
      input: {
        clock: { pg_skew: 0, wall_delta: 1000 + value, monotonic_delta: 1000 },
        write_first_publication: false,
      },
    });
  for (const delta of [0, 1]) {
    const v = clone(
      boundary_cases.find((x) => x.id === 'archive-retains-tombstone'),
    );
    v.id = 'archive-not-after-before-plus-' + delta;
    v.expected = delta ? 'invalid_transition' : 'accept';
    v.input.removed[0].not_after = new Date(
      Date.parse(v.input.before) + delta * 1000,
    )
      .toISOString()
      .replace('.000', '');
    boundary_cases.push(v);
  }
  add(
    'restart-partial-purpose-preserves-valid-active',
    'restart-two-valid-one-recovered',
    'accept',
    (i) => {
      const q = i.publication.predecessor.keyset.payload,
        k = i.publication.keyset.payload;
      const old = q.keys[1];
      old.not_before = '2026-11-12T00:00:00Z';
      k.keys = k.keys.filter((x) => x.purpose !== old.purpose);
      k.keys.push(clone(old));
      k.first_publications = k.first_publications.filter(
        (x) => x.kid !== 'synthetic-restart-1',
      );
    },
  );
  for (const [id, date, expected] of [
    ['restart-old-expiry-equals-issued', '2026-11-14T00:00:00Z', 'accept'],
    [
      'restart-old-start-equals-issued',
      '2026-11-20T00:00:00Z',
      'invalid_transition',
    ],
  ])
    add('restart-future-active-after-clock-correction', id, expected, (i) => {
      for (const list of [
        i.publication.predecessor.keyset.payload.keys,
        i.publication.keyset.payload.keys,
      ])
        for (const key of list)
          if (key.kid === 'synthetic-business-0') {
            key.not_before = id.includes('expiry')
              ? '2026-11-12T00:00:00Z'
              : '2026-11-14T00:00:00Z';
            key.not_after = date;
          }
    });
  add(
    'refresh-valid',
    'predecessor-authority-start-equals-issued',
    'accept',
    (i) => {
      const q = i.publication.predecessor;
      q.authority.payload.not_before = q.keyset.payload.issued_at;
      q.authority = auth(
        q.authority.payload,
        'Auriga.NetworkRuntimeAuthority.v2',
        root,
        'synthetic-root',
        true,
      );
      q.keyset.payload.authority_digest = digest(q.authority);
      q.control.payload.allowed_authorities = [
        {
          authority_id: q.authority.payload.authority_id,
          authority_digest: digest(q.authority),
        },
      ];
      q.control = auth(
        q.control.payload,
        'Auriga.NetworkRootControl.v2',
        root,
        'synthetic-root',
        true,
      );
    },
  );
  add('refresh-valid', 'predecessor-version-equals-minimum', 'accept', (i) => {
    const q = i.publication.predecessor;
    q.control.payload.minimum_keyset_version = q.keyset.payload.keyset_version;
    q.control = auth(
      q.control.payload,
      'Auriga.NetworkRootControl.v2',
      root,
      'synthetic-root',
      true,
    );
  });
  add(
    'mature-next-activation',
    'next-first-version-equals-predecessor',
    'accept',
    (i) => {
      for (const list of [
        i.publication.predecessor.keyset.payload.first_publications,
        i.publication.keyset.payload.first_publications,
        i.first_publication_ledger,
      ])
        for (const proof of list)
          if (proof.kid.startsWith('synthetic-next-'))
            proof.committed_version = '9';
    },
  );
  for (const delta of [0, 1])
    add(
      'authorized-history-archive',
      'consumer-archive-grace-endpoint-' + delta,
      delta ? 'invalid_transition' : 'accept',
      (i) => {
        i.publication.keyset.payload.archive.before = new Date(
          Date.parse(i.now) - 360000 + delta * 1000,
        )
          .toISOString()
          .replace('.000', '');
      },
    );
  add(
    'authorized-history-archive',
    'consumer-archive-expiry-equals-before',
    'accept',
    (i) => {
      i.publication.keyset.payload.archive.before =
        i.publication.predecessor.keyset.payload.keys.at(-1).not_after;
    },
  );
  // 零寿命历史钥独立命中所有钥的正寿命约束。
  add(
    'restart-future-active-after-clock-correction',
    'retired-key-zero-lifetime',
    'invalid_transition',
    (i) => {
      for (const list of [
        i.publication.predecessor.keyset.payload.keys,
        i.publication.keyset.payload.keys,
      ])
        list.find((x) => x.kid === 'synthetic-business-0').not_after =
          list.find((x) => x.kid === 'synthetic-business-0').not_before;
    },
  );
  for (const [id, change] of [
    [
      'config-rotation-plus-preannounce-equals-lifetime',
      (c) =>
        (c.business_key_seconds = c.rotation_seconds + c.preannounce_seconds),
    ],
    [
      'config-preannounce-equals-maturity',
      (c) => (c.maturity_seconds = c.preannounce_seconds),
    ],
  ]) {
    const v = clone(boundary_cases.find((x) => x.id === 'config-defaults'));
    v.id = id;
    change(v.input.config);
    boundary_cases.push(v);
  }
  add(
    'restart-two-valid-one-recovered',
    'restart-one-expired-at-issued',
    'accept',
    (i) => {
      for (const list of [
        i.publication.predecessor.keyset.payload.keys,
        i.publication.keyset.payload.keys,
      ]) {
        const old = list.find((x) => x.kid === 'synthetic-business-2');
        old.not_before = '2026-11-12T00:00:00Z';
        old.not_after = i.now;
      }
    },
  );
  add(
    'restart-future-active-after-clock-correction',
    'restart-unnecessary-all-start-at-issued',
    'invalid_transition',
    (i) => {
      const q = i.publication.predecessor.keyset.payload,
        k = i.publication.keyset.payload;
      for (const key of q.keys) key.not_before = i.now;
      k.keys = clone(q.keys);
      k.first_publications = clone(q.first_publications);
    },
  );
  // 新增端点按名称稳定排列，便于跨语言实现逐项定位。
  cases.splice(
    firstEndpoint,
    cases.length - firstEndpoint,
    ...cases
      .slice(firstEndpoint)
      .sort((a, b) => a.id.localeCompare(b.id, 'en')),
  );
}
