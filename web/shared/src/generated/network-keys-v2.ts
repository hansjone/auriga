export interface paths {
  '/api/collab/v2/network/keys': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /**
     * getNetworkKeysV2
     * @description 固定安装端点；绑定安装身份、tenant/realm及组件。公有引导不依赖消息连接。明文材料只允许内部mTLS；密封引导验证独立安装签名及nonce。禁止重定向及消息内URL。
     */
    get: operations['getNetworkKeysV2'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/collab/v2/network/keys/announcement': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /**
     * getNetworkKeyAnnouncementV2
     * @description 固定安装端点；绑定安装身份、tenant/realm及组件。公有引导不依赖消息连接。明文材料只允许内部mTLS；密封引导验证独立安装签名及nonce。禁止重定向及消息内URL。
     */
    get: operations['getNetworkKeyAnnouncementV2'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/collab/v2/network/keys/status': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /**
     * getNetworkRenewalStatus
     * @description 固定安装端点；绑定安装身份、tenant/realm及组件。公有引导不依赖消息连接。明文材料只允许内部mTLS；密封引导验证独立安装签名及nonce。禁止重定向及消息内URL。
     */
    get: operations['getNetworkRenewalStatus'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/collab/v2/network/controls/packages': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /**
     * createNetworkControlPackage
     * @description 固定安装端点；绑定安装身份、tenant/realm及组件。公有引导不依赖消息连接。明文材料只允许内部mTLS；密封引导验证独立安装签名及nonce。禁止重定向及消息内URL。
     */
    post: operations['createNetworkControlPackage'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/collab/v2/network/controls/receipts': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /**
     * confirmNetworkControl
     * @description 固定安装端点；绑定安装身份、tenant/realm及组件。公有引导不依赖消息连接。明文材料只允许内部mTLS；密封引导验证独立安装签名及nonce。禁止重定向及消息内URL。
     */
    post: operations['confirmNetworkControl'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/collab/v2/network/isolation': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /**
     * enforceNetworkIsolation
     * @description 固定安装端点；绑定安装身份、tenant/realm及组件。公有引导不依赖消息连接。明文材料只允许内部mTLS；密封引导验证独立安装签名及nonce。禁止重定向及消息内URL。
     */
    post: operations['enforceNetworkIsolation'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/collab/v2/network/projections/protocol': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /**
     * getProtocolProjection
     * @description 固定安装端点；绑定安装身份、tenant/realm及组件。公有引导不依赖消息连接。明文材料只允许内部mTLS；密封引导验证独立安装签名及nonce。禁止重定向及消息内URL。
     */
    post: operations['getProtocolProjection'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/collab/v2/network/projections/transport': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /**
     * getTransportProjection
     * @description 固定安装端点；绑定安装身份、tenant/realm及组件。公有引导不依赖消息连接。明文材料只允许内部mTLS；密封引导验证独立安装签名及nonce。禁止重定向及消息内URL。
     */
    post: operations['getTransportProjection'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/collab/v2/network/projections/receipts': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /**
     * confirmNetworkProjection
     * @description 固定安装端点；绑定安装身份、tenant/realm及组件。公有引导不依赖消息连接。明文材料只允许内部mTLS；密封引导验证独立安装签名及nonce。禁止重定向及消息内URL。
     */
    post: operations['confirmNetworkProjection'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/collab/v2/network/bootstrap/materials': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /**
     * getSealedNetworkMaterial
     * @description 固定安装端点；绑定安装身份、tenant/realm及组件。公有引导不依赖消息连接。明文材料只允许内部mTLS；密封引导验证独立安装签名及nonce。禁止重定向及消息内URL。
     */
    post: operations['getSealedNetworkMaterial'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/collab/v2/network/bootstrap/controls': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /**
     * getPublicNetworkControl
     * @description 固定安装端点；绑定安装身份、tenant/realm及组件。公有引导不依赖消息连接。明文材料只允许内部mTLS；密封引导验证独立安装签名及nonce。禁止重定向及消息内URL。
     */
    get: operations['getPublicNetworkControl'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/collab/v2/network/bootstrap/transport': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /**
     * getPublicTransportBundle
     * @description 固定安装端点；绑定安装身份、tenant/realm及组件。公有引导不依赖消息连接。明文材料只允许内部mTLS；密封引导验证独立安装签名及nonce。禁止重定向及消息内URL。
     */
    get: operations['getPublicTransportBundle'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/collab/v2/network/bootstrap/keys': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /**
     * getNetworkKeysV2
     * @description 固定安装端点；绑定安装身份、tenant/realm及组件。公有引导不依赖消息连接。明文材料只允许内部mTLS；密封引导验证独立安装签名及nonce。禁止重定向及消息内URL。
     */
    get: operations['getPublicNetworkPublication'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
}
export type webhooks = Record<string, never>;
export interface components {
  schemas: {
    GenerationTransition: {
      predecessor_digest: string;
      from_generation: string;
      to_generation: string;
      authority_id: string;
      authority_digest: string;
      first_version: string;
    };
    Signature: {
      /** @constant */
      alg: 'Ed25519';
      kid: string;
      signature: string;
    };
    Limits: {
      keyset_seconds: number;
      business_key_seconds: number;
      restart_key_seconds: number;
      next_maturity_seconds: number;
      overlap_seconds: number;
      archive_grace_seconds: number;
    };
    TransportScope: {
      /** @enum {string} */
      component: 'network' | 'edge' | 'broker';
      identity_id: string;
      sans: string[];
      ekus: ('serverAuth' | 'clientAuth')[];
    };
    RuntimeAuthority: {
      /** @constant */
      schema_version: 2;
      tenant_id: string;
      realm: string;
      authority_id: string;
      authority_revision: string;
      runtime_kid: string;
      public_key_spki: string;
      /** Format: date-time */
      not_before: string;
      /** @constant */
      expiry_policy: 'no_hard_expiry';
      /** @constant */
      review_period_days: 365;
      capabilities: (
        | 'keyset'
        | 'announcement'
        | 'business_rotation'
        | 'restart_recovery'
        | 'history_archive'
        | 'network_transport'
      )[];
      limits: components['schemas']['Limits'];
      transport_scope: components['schemas']['TransportScope'][];
    };
    AuthorityReference: {
      authority_id: string;
      authority_digest: string;
    };
    RootControl: {
      /** @constant */
      schema_version: 2;
      tenant_id: string;
      realm: string;
      control_revision: string;
      /** Format: date-time */
      issued_at: string;
      allowed_authorities: components['schemas']['AuthorityReference'][];
      revoked_authority_ids: string[];
      revoked_installation_ids: string[];
      minimum_generation: string;
      minimum_keyset_version: string;
      generation_transition?: components['schemas']['GenerationTransition'];
    };
    SignedRuntimeAuthority: {
      payload: components['schemas']['RuntimeAuthority'];
      root_auth: components['schemas']['Signature'];
    };
    SignedRootControl: {
      payload: components['schemas']['RootControl'];
      root_auth: components['schemas']['Signature'];
    };
    PublicKey: {
      kid: string;
      /** @constant */
      alg: 'Ed25519';
      /** @enum {string} */
      purpose: 'business_delivery' | 'protocol_response' | 'management_state';
      /** @enum {string} */
      state: 'active' | 'next' | 'retired' | 'revoked';
      /** Format: date-time */
      not_before: string;
      /** Format: date-time */
      not_after: string;
      public_key_spki: string;
    };
    FirstPublication: {
      kid: string;
      committed_version: string;
      /** Format: date-time */
      committed_at: string;
    };
    Archive: {
      /** Format: date-time */
      before: string;
      history_digest: string;
    };
    KeysetPayload: {
      /** @constant */
      schema_version: 2;
      tenant_id: string;
      realm: string;
      authority_id: string;
      authority_digest: string;
      control_revision: string;
      generation: string;
      keyset_version: string;
      /** Format: date-time */
      issued_at: string;
      /** Format: date-time */
      valid_until: string;
      /** @enum {string} */
      mode:
        | 'bootstrap'
        | 'refresh'
        | 'activate'
        | 'restart_recovery'
        | 'migration'
        | 'authority_transition';
      keys: components['schemas']['PublicKey'][];
      first_publications: components['schemas']['FirstPublication'][];
      archive: components['schemas']['Archive'];
    };
    SignedKeysetV2: {
      payload: components['schemas']['KeysetPayload'];
      runtime_auth: components['schemas']['Signature'];
    };
    PredecessorProof: {
      keyset: components['schemas']['SignedKeysetV2'];
      keyset_digest: string;
      authority: components['schemas']['SignedRuntimeAuthority'];
      control: components['schemas']['SignedRootControl'];
    };
    Publication: {
      keyset: components['schemas']['SignedKeysetV2'];
      authority: components['schemas']['SignedRuntimeAuthority'];
      control: components['schemas']['SignedRootControl'];
      predecessor?: components['schemas']['PredecessorProof'];
    };
    KeysetAnnouncementV2: {
      /** @constant */
      schema_version: 2;
      tenant_id: string;
      realm: string;
      authority_id: string;
      authority_digest: string;
      control_revision: string;
      generation: string;
      keyset_version: string;
      keyset_digest: string;
      /** Format: date-time */
      valid_until: string;
    };
    SignedKeysetAnnouncementV2: {
      payload: components['schemas']['KeysetAnnouncementV2'];
      runtime_auth: components['schemas']['Signature'];
    };
    TransportCa: {
      ca_id: string;
      /** @enum {string} */
      role: 'server' | 'client';
      certificate_der: string;
      /** Format: date-time */
      not_before: string;
      /** Format: date-time */
      not_after: string;
    };
    TransportTrustBundle: {
      /** @constant */
      schema_version: 2;
      tenant_id: string;
      realm: string;
      authority_id: string;
      authority_digest: string;
      control_revision: string;
      generation: string;
      bundle_version: string;
      /** Format: date-time */
      issued_at: string;
      /** @enum {string} */
      phase: 'dual_trust' | 'new_leaves' | 'retire_old';
      cas: components['schemas']['TransportCa'][];
      scope: components['schemas']['TransportScope'][];
      required_reader_ids: string[];
      previous_bundle_digest: string;
    };
    SignedTransportTrustBundle: {
      payload: components['schemas']['TransportTrustBundle'];
      runtime_auth: components['schemas']['Signature'];
    };
    ControlPackage: {
      package_id: string;
      tenant_id: string;
      realm: string;
      control: components['schemas']['SignedRootControl'];
      control_digest: string;
      target_id: string;
      challenge: string;
    };
    InstallationReceipt: {
      package_id: string;
      tenant_id: string;
      realm: string;
      target_id: string;
      identity_id: string;
      challenge: string;
      control_revision: string;
      control_digest: string;
      /** @enum {string} */
      result: 'installed' | 'rejected';
      /** Format: date-time */
      persisted_at: string;
      identity_auth: components['schemas']['Signature'];
    };
    IsolationRecord: {
      tenant_id: string;
      realm: string;
      target_id: string;
      minimum_control_revision: string;
      control_digest: string;
      /** @enum {string} */
      state: 'quarantined' | 'confirmed' | 'ready';
      identity_compromised: boolean;
      challenge: string;
      new_chain_ready: boolean;
    };
    ProjectionRequest: {
      tenant_id: string;
      realm: string;
      installation_id: string;
      /** @enum {string} */
      component: 'network' | 'edge' | 'broker';
      gateway_id: string;
      nonce: string;
      expected_generation: string;
      expected_keyset_version: string;
      identity_auth: components['schemas']['Signature'];
    };
    ProtocolMaterial: {
      publication: components['schemas']['Publication'];
      active_kid: string;
      active_pkcs8: string;
      next_kid?: string;
      next_pkcs8?: string;
    };
    TlsLeaf: {
      /** @enum {string} */
      component: 'network' | 'edge' | 'broker';
      /** @enum {string} */
      role: 'submit' | 'edge' | 'broker' | 'broker-client' | 'edge-presence';
      certificate_der: string;
      private_pkcs8: string;
      ca_id: string;
      /** Format: date-time */
      not_before: string;
      /** Format: date-time */
      not_after: string;
    };
    ProjectionMaterial: {
      tenant_id: string;
      realm: string;
      installation_id: string;
      /** @enum {string} */
      component: 'network' | 'edge' | 'broker';
      nonce: string;
      generation: string;
      keyset_version: string;
      publication_digest: string;
      bundle: components['schemas']['SignedTransportTrustBundle'];
      leaves: components['schemas']['TlsLeaf'][];
      protocol?: components['schemas']['ProtocolMaterial'];
    };
    SealedMaterial: {
      tenant_id: string;
      realm: string;
      installation_id: string;
      /** @enum {string} */
      component: 'network' | 'edge' | 'broker';
      nonce: string;
      generation: string;
      keyset_version: string;
      bundle_digest: string;
      /** @constant */
      kem: 'DHKEM(X25519,HKDF-SHA256)';
      /** @constant */
      kdf: 'HKDF-SHA256';
      /** @constant */
      aead: 'AES-256-GCM';
      enc: string;
      ciphertext: string;
    };
    ProjectionReceipt: {
      tenant_id: string;
      realm: string;
      installation_id: string;
      /** @enum {string} */
      component: 'network' | 'edge' | 'broker';
      nonce: string;
      generation: string;
      keyset_version: string;
      publication_digest: string;
      bundle_version: string;
      bundle_digest: string;
      /** @enum {string} */
      phase: 'persisted' | 'loaded' | 'handshake_verified' | 'failed';
      /** Format: date-time */
      observed_at: string;
      identity_auth: components['schemas']['Signature'];
      local_selector?: components['schemas']['LocalProjectionSelector'];
    };
    MigrationPermit: {
      /** @constant */
      schema_version: 2;
      tenant_id: string;
      realm: string;
      permit_id: string;
      ancestor_digest: string;
      queue_commitment_digest: string;
      maximum_committed_v1_version: string;
      minimum_v2_version: string;
      minimum_generation: string;
    };
    SignedMigrationPermit: {
      payload: components['schemas']['MigrationPermit'];
      root_auth: components['schemas']['Signature'];
    };
    MigrationReceipt: {
      permit_id: string;
      observed_v1_version: string;
      observed_v1_digest: string;
      v2_generation: string;
      v2_keyset_version: string;
      publication_digest: string;
      barrier_committed: boolean;
      legacy_disabled: boolean;
      frozen_elapsed_seconds: number;
    };
    Tombstone: {
      kid: string;
      identity_digest: string;
      /** Format: date-time */
      not_before: string;
      /** Format: date-time */
      not_after: string;
      /** @enum {string} */
      terminal_state: 'retired' | 'revoked';
    };
    Redelivery: {
      tenant_id: string;
      realm: string;
      message_id: string;
      delivery_id: string;
      body_digest: string;
      sequence: string;
      /** Format: date-time */
      original_expires_at: string;
      previous_envelope_digest: string;
      new_envelope_digest: string;
      /** @enum {string} */
      state: 'pending' | 'delivered' | 'acknowledged' | 'expired' | 'rejected';
    };
    RenewalStatus: {
      tenant_id: string;
      realm: string;
      /** @enum {string} */
      state:
        | 'ready'
        | 'renewing'
        | 'paused'
        | 'clock_error'
        | 'authority_revoked'
        | 'projection_pending'
        | 'degraded';
      authority_id: string;
      /** @constant */
      expiry_policy: 'no_hard_expiry';
      generation: string;
      keyset_version: string;
      control_revision: string;
      /** Format: date-time */
      review_due_at: string;
      review_required: boolean;
      /** Format: date-time */
      next_renewal_at: string;
      /** Format: date-time */
      last_success_at: string;
      /** Format: date-time */
      business_valid_until: string;
      announcement_confirmed: boolean;
      /** @enum {string} */
      failure_code:
        | 'none'
        | 'collab.msgnet_authority_revoked'
        | 'collab.msgnet_authority_unknown'
        | 'collab.msgnet_control_rollback'
        | 'collab.msgnet_control_required'
        | 'collab.msgnet_version_rollback'
        | 'collab.msgnet_same_version_conflict'
        | 'collab.msgnet_scope_denied'
        | 'collab.msgnet_invalid_chain'
        | 'collab.msgnet_invalid_transition'
        | 'collab.msgnet_predecessor_required'
        | 'collab.msgnet_clock_error'
        | 'collab.msgnet_projection_pending'
        | 'collab.msgnet_identity_quarantined'
        | 'collab.msgnet_migration_expired'
        | 'collab.msgnet_migration_outside_commitment';
    };
    RenewalConfig: {
      enabled: boolean;
      kek_secret_file: string;
      kek_id: string;
      authority_source: string;
      control_source: string;
      keyset_seconds: number;
      refresh_seconds: number;
      check_seconds: number;
      business_key_seconds: number;
      rotation_seconds: number;
      preannounce_seconds: number;
      maturity_seconds: number;
      overlap_seconds: number;
      allowed_skew_seconds: number;
      /** @constant */
      review_period_days: 365;
      review_reminder_days: number[];
      projector_identity_id: string;
      projection_directory: string;
      reload_seconds: number;
      leaf_days: number;
      leaf_renew_before_days: number;
      ca_years: number;
      ca_renew_before_years: number;
      forward_jump_seconds: number;
      backward_jump_seconds: number;
      clock_retry_seconds: number;
      migration_margin_seconds: number;
      freeze_seconds: number;
    };
    PrivateKeyAad: {
      tenant_id: string;
      realm: string;
      kid: string;
      /** @enum {unknown} */
      alg: 'Ed25519' | 'X25519';
      /** @enum {unknown} */
      purpose:
        | 'runtime_authority'
        | 'business_delivery'
        | 'protocol_response'
        | 'management_state'
        | 'network_transport_ca'
        | 'network_transport_leaf';
      /** @enum {unknown} */
      component: 'collab' | 'network' | 'edge' | 'broker';
    };
    EncryptedPrivateKey: {
      kek_id: string;
      /** @constant */
      alg: 'AES-256-GCM';
      nonce: string;
      aad: components['schemas']['PrivateKeyAad'];
      ciphertext: string;
    };
    LocalProjectionFile: {
      name: string;
      sha256: string;
      size: number;
    };
    LocalProjectionManifest: {
      tenant_id: string;
      realm: string;
      installation_id: string;
      /** @enum {string} */
      component: 'network' | 'edge' | 'broker';
      nonce: string;
      generation: string;
      keyset_version: string;
      publication_digest: string;
      bundle_version: string;
      bundle_digest: string;
      material_revision: string;
      files: components['schemas']['LocalProjectionFile'][];
    };
    LocalProjectionSelector: {
      tenant_id: string;
      realm: string;
      installation_id: string;
      /** @enum {string} */
      component: 'network' | 'edge' | 'broker';
      nonce: string;
      generation: string;
      keyset_version: string;
      publication_digest: string;
      bundle_version: string;
      bundle_digest: string;
      material_revision: string;
      manifest_digest: string;
    };
    LocalProjectionRequest: {
      /** @enum {string} */
      operation: 'reload' | 'confirm';
      selector: components['schemas']['LocalProjectionSelector'];
    };
    LocalProjectionResponse: {
      selector: components['schemas']['LocalProjectionSelector'];
      /** @enum {string} */
      phase: 'loaded' | 'handshake_verified' | 'failed';
      /** @enum {string} */
      failure_code:
        | 'none'
        | 'version_rollback'
        | 'same_version_conflict'
        | 'invalid_chain'
        | 'projection_pending';
    } & (
      | {
          /** @enum {unknown} */
          phase: 'loaded' | 'handshake_verified';
          /** @constant */
          failure_code: 'none';
        }
      | {
          /** @constant */
          phase: 'failed';
          /** @enum {unknown} */
          failure_code:
            | 'version_rollback'
            | 'same_version_conflict'
            | 'invalid_chain'
            | 'projection_pending';
        }
    );
    InstallationPin: {
      target_id: string;
      identity_id: string;
      signing_spki: string;
      /** @enum {string} */
      component: 'network' | 'edge' | 'broker';
      gateway_id: string;
      sealing_spki: string;
      subject: string;
    };
    InstallationPins: {
      /** @constant */
      schema_version: 2;
      tenant_id: string;
      realm: string;
      pins: components['schemas']['InstallationPin'][];
    };
    UnpublishedRenewalStatus: {
      tenant_id: string;
      realm: string;
      /** @enum {string} */
      state:
        | 'renewing'
        | 'paused'
        | 'clock_error'
        | 'authority_revoked'
        | 'degraded';
      authority_id: string;
      /** @constant */
      expiry_policy: 'no_hard_expiry';
      control_revision: string;
      /** Format: date-time */
      review_due_at: string;
      review_required: boolean;
      /** @constant */
      announcement_confirmed: false;
      /** @enum {string} */
      failure_code:
        | 'none'
        | 'collab.msgnet_authority_revoked'
        | 'collab.msgnet_authority_unknown'
        | 'collab.msgnet_control_rollback'
        | 'collab.msgnet_control_required'
        | 'collab.msgnet_version_rollback'
        | 'collab.msgnet_same_version_conflict'
        | 'collab.msgnet_scope_denied'
        | 'collab.msgnet_invalid_chain'
        | 'collab.msgnet_invalid_transition'
        | 'collab.msgnet_predecessor_required'
        | 'collab.msgnet_clock_error'
        | 'collab.msgnet_projection_pending'
        | 'collab.msgnet_identity_quarantined'
        | 'collab.msgnet_migration_expired'
        | 'collab.msgnet_migration_outside_commitment';
      /** @constant */
      has_publication: false;
    };
    RenewalStatusOutput:
      | components['schemas']['RenewalStatus']
      | components['schemas']['UnpublishedRenewalStatus'];
    MigrationMaterialKey: {
      kid: string;
      /** @enum {string} */
      purpose: 'business_delivery' | 'protocol_response' | 'management_state';
      public_key_spki: string;
      /** Format: date-time */
      not_before: string;
      /** Format: date-time */
      not_after: string;
    };
    MigrationMaterialCommitment: {
      /** @constant */
      schema_version: 2;
      keys: components['schemas']['MigrationMaterialKey'][];
    };
    MigrationCommitmentMember: {
      version: string;
      pair_digest: string;
      material_digest: string;
    };
  };
  responses: never;
  parameters: never;
  requestBodies: never;
  headers: never;
  pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
  getNetworkKeysV2: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 精确已提交对象；Cache-Control no-store。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Publication'];
        };
      };
      /** @description 无效契约或转换 */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 身份、用途、撤销或隔离拒绝 */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 版本、摘要或代际冲突 */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 材料不可用或clock_error */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  getNetworkKeyAnnouncementV2: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 精确已提交对象；Cache-Control no-store。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['SignedKeysetAnnouncementV2'];
        };
      };
      /** @description 无效契约或转换 */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 身份、用途、撤销或隔离拒绝 */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 版本、摘要或代际冲突 */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 材料不可用或clock_error */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  getNetworkRenewalStatus: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 精确已提交对象；Cache-Control no-store。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['RenewalStatusOutput'];
        };
      };
      /** @description 无效契约或转换 */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 身份、用途、撤销或隔离拒绝 */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 版本、摘要或代际冲突 */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 材料不可用或clock_error */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  createNetworkControlPackage: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['ControlPackage'];
      };
    };
    responses: {
      /** @description 精确已提交对象；Cache-Control no-store。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ControlPackage'];
        };
      };
      /** @description 无效契约或转换 */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 身份、用途、撤销或隔离拒绝 */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 版本、摘要或代际冲突 */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 材料不可用或clock_error */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  confirmNetworkControl: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['InstallationReceipt'];
      };
    };
    responses: {
      /** @description 精确已提交对象；Cache-Control no-store。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['IsolationRecord'];
        };
      };
      /** @description 无效契约或转换 */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 身份、用途、撤销或隔离拒绝 */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 版本、摘要或代际冲突 */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 材料不可用或clock_error */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  enforceNetworkIsolation: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['IsolationRecord'];
      };
    };
    responses: {
      /** @description 精确已提交对象；Cache-Control no-store。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['IsolationRecord'];
        };
      };
      /** @description 无效契约或转换 */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 身份、用途、撤销或隔离拒绝 */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 版本、摘要或代际冲突 */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 材料不可用或clock_error */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  getProtocolProjection: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['ProjectionRequest'];
      };
    };
    responses: {
      /** @description 精确已提交对象；Cache-Control no-store。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ProtocolMaterial'];
        };
      };
      /** @description 无效契约或转换 */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 身份、用途、撤销或隔离拒绝 */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 版本、摘要或代际冲突 */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 材料不可用或clock_error */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  getTransportProjection: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['ProjectionRequest'];
      };
    };
    responses: {
      /** @description 精确已提交对象；Cache-Control no-store。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ProjectionMaterial'];
        };
      };
      /** @description 无效契约或转换 */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 身份、用途、撤销或隔离拒绝 */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 版本、摘要或代际冲突 */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 材料不可用或clock_error */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  confirmNetworkProjection: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['ProjectionReceipt'];
      };
    };
    responses: {
      /** @description 精确已提交对象；Cache-Control no-store。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ProjectionReceipt'];
        };
      };
      /** @description 无效契约或转换 */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 身份、用途、撤销或隔离拒绝 */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 版本、摘要或代际冲突 */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 材料不可用或clock_error */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  getSealedNetworkMaterial: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['ProjectionRequest'];
      };
    };
    responses: {
      /** @description 精确已提交对象；Cache-Control no-store。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['SealedMaterial'];
        };
      };
      /** @description 无效契约或转换 */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 身份、用途、撤销或隔离拒绝 */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 版本、摘要或代际冲突 */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 材料不可用或clock_error */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  getPublicNetworkControl: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 精确已提交对象；Cache-Control no-store。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['SignedRootControl'];
        };
      };
      /** @description 无效契约或转换 */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 身份、用途、撤销或隔离拒绝 */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 版本、摘要或代际冲突 */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 材料不可用或clock_error */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  getPublicTransportBundle: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 精确已提交对象；Cache-Control no-store。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['SignedTransportTrustBundle'];
        };
      };
      /** @description 无效契约或转换 */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 身份、用途、撤销或隔离拒绝 */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 版本、摘要或代际冲突 */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 材料不可用或clock_error */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  getPublicNetworkPublication: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 精确已提交对象；Cache-Control no-store。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Publication'];
        };
      };
      /** @description 无效契约或转换 */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 身份、用途、撤销或隔离拒绝 */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 版本、摘要或代际冲突 */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 材料不可用或clock_error */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
}
