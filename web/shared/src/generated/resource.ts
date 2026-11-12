export interface paths {
  '/api/resource/v1/admin/review': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description Shared plugin version metadata only; private packages excluded. Requires a live admin session, recent verification, explicit permission and package ACL. */
    get: operations['listMarketAdminReview'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/published': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description Shared plugin version metadata only; private packages excluded. Requires a live admin session, recent verification, explicit permission and package ACL. */
    get: operations['listMarketAdminPublished'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/packages': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 只列当前主体可见包；不可见或跨租户对象统一404。 */
    get: operations['listResourcePackages'];
    put?: never;
    /** @description 创建本人草稿；L4强制private且WorkspacePolicy.allowPersonalPlugins为true。 */
    post: operations['createResourcePackage'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/packages/{packageId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 目录可见不自动授予包字节或安装权。 */
    get: operations['getResourcePackage'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    /** @description 仅本人草稿，不能更改主人、租户或等级；L4禁止共享。 */
    patch: operations['patchResourcePackage'];
    trace?: never;
  };
  '/api/resource/v1/admin/packages/{packageId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    /** @description 仅owner；不允许改L0门锁；等级与可见范围分别校验。 */
    patch: operations['patchResourcePackageLevel'];
    trace?: never;
  };
  '/api/resource/v1/packages/{packageId}/versions': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 读取可见包版本；不能列出其他作者的私有草稿。 */
    get: operations['listResourceVersions'];
    put?: never;
    /** @description 限64MiB gzip归档：流式暂存、摘要/归档验证后登记；不执行安装脚本，不代取任意URL。同版本内容不可覆盖。 L4私有分支在对象验证后签名保存，仍标未审核；不得由此进入共享published。 If-Match is mandatory and refers to the package revision. Permissions, ETag, idempotency replay/conflict and version uniqueness are checked before object commit; publication and GC share a digest lock. Upload requires strict SemVer. The archive is at most 64 MiB; edge permits an additional bounded 128 KiB of multipart metadata/framing. */
    post: operations['uploadResourceVersion'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/packages/{packageId}/versions/{versionId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 读取可见版本元数据；不把签名等同于ACL。 */
    get: operations['getResourceVersion'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/packages/{packageId}/versions/{versionId}/submissions': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 仅本人；送审绑定精确digest+revision，修改草稿使旧审核失效。 */
    post: operations['submitResourceVersion'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/reviews/{reviewId}/decisions': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 审核者不能是版本作者；审核结果绑定精确摘要/修订。 */
    post: operations['decideResourceReview'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/packages/{packageId}/versions/{versionId}/publications': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 发布者不能是版本作者；审核后签名并提交不可变发布，审核者可与发布者相同。 */
    post: operations['publishResourceVersion'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/packages/{packageId}/versions/{versionId}/withdrawals': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 普通下架阻新下载/分发；malicious须签名撤销并由workspace/agent禁用，不能宣称立即清除。 Repeating malicious withdrawal with a fresh key is an idempotent no-op for an already revoked version/digest; it creates no additional revocation entry. Pending workspace stop delivery is persisted and retried independently of known list revisions. */
    post: operations['withdrawResourceVersion'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/distributions': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 仅本人实例；逐项核等级/可见/版本/策略，仅记录意图，202不代表已安装。 Requires recent step-up within 600 seconds for both administrator and member callers; MARKET-CLIENT must align with this contract. */
    post: operations['requestResourceDistribution'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/distributions/{distributionId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 仅本人或显式授权管理者；必须generation=observedGeneration且ready才applied。 */
    get: operations['getResourceDistribution'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/packages/{packageId}/versions/{versionId}/content': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 先完成业务鉴权再读对象；不可见404，无无鉴权digest接口。 */
    get: operations['downloadResourceContent'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/internal/resource/v1/downloads/{downloadId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 仅内部节点mTLS。locator不构成授权，每次重试在线核SAN/吊销、授权expiry、租户/实例/版本/digest、当前调度/selectionRevision。首批只整包200，不支持Range。 */
    get: operations['downloadNodeResource'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/reviews': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 当前租户待审队列，不含其他租户或无审查授权的包。 */
    get: operations['listResourceReviews'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/reviews/{reviewId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 读取精确待审版本/摘要/修订，审核者不能是作者。 */
    get: operations['getResourceReview'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/packages/{packageId}/versions/{versionId}/downgrade-authorizations': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 仅owner显式审计授权：核当前实例高水位/归属/精确目标版本、摘要、有效撤销清单，签发一次性限实例授权，最长10分钟；不降低持久高水位或授权其他旧版本。 */
    post: operations['issueResourceDowngrade'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/packages/{packageId}/versions/{versionId}/drafts': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description Live tenant membership and object ACL are checked on every request. Responses are never cached. */
    post: operations['resetMarketAdminDraft'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/packages/{packageId}/versions/{versionId}/personal-signatures': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description Live tenant membership and object ACL are checked on every request. Responses are never cached. */
    post: operations['signMarketAdminPersonal'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/review/packages/{packageId}/versions/{versionId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description Live tenant membership and object ACL are checked on every request. Responses are never cached. */
    get: operations['getMarketAdminReviewDetail'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/published/packages/{packageId}/versions/{versionId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description Live tenant membership and object ACL are checked on every request. Responses are never cached. */
    get: operations['getMarketAdminPublishedDetail'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/distributions': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description Live tenant membership and object ACL are checked on every request. Responses are never cached. */
    get: operations['listMarketAdminDistributions'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/internal/resource/v1/downloads/{downloadId}/renewals': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description Direct node mTLS required. Reauthorizes the current exact workspace selection and renews the same node-bound locator for at most five minutes. Idempotency replays return the original expiry for 24 hours; use a new key for a later renewal. No bearer URL or caller expiry is accepted. */
    post: operations['renewMarketAdminNodeDownload'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/revocations': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description Current tenant signed revocation list, including explicit empty arrays. Membership and resource read permission are checked live; requires configured independent revocation signer. Never cached. */
    get: operations['getResourceRevocations'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/member/download-tickets': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description Authenticated USER portal/admin or DEVICE device context. Live member ACL, policy, published enabled archive and release high water are checked. A 60-second one-use ticket binds member, clientId, packageId, versionId and digest. Device tokens require clientId=did. Ticket appears only in the no-store response/body, never in a URL. Only an idempotency completion marker is persisted. Reusing a key within 24 hours returns 409, including after an uncertain response; use a new key to request a new ticket. No plaintext ticket is stored in operation receipts. Verify the returned signed envelope and revocations with independent trusted roots. */
    post: operations['createMemberResourceDownloadTicket'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/member/downloads': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description Requires the same authenticated member and clientId, rechecks current ACL/publication/revocation. Ticket is atomically consumed before bytes are opened. A repeated or concurrent consume returns 409, including the same request key. This endpoint never replays bytes; disconnects require a fresh ticket and request key. Range returns 400. No credentials in URL. Idempotency-Key is a required syntax-validated request marker on this endpoint; it is not persisted and does not select a replay response. The atomically consumed ticket is the duplicate-consumption guard, independent of that header. */
    post: operations['consumeMemberResourceDownloadTicket'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/member/installations': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description Only the authenticated member/client rows. reportSource=member is a desktop client report, separate from workspace node inventory. Keyset pagination uses nextAfterPackageId. Removed rows retain high water. */
    get: operations['listMemberResourceInstallations'];
    put?: never;
    /** @description Own member/client selection only; device clientId must equal did. If-Match is required: use "0" for the first selection, then the returned installation revision. L0 is deployment-fixed; L1 cannot be disabled/removed; L2 cannot be removed. Install/enable require current ACL, policy, published enabled high-water version and matching digest. Disabled/removed rows retain durable high water. 202 is applying until an exact revision/version/digest report. Uses 24-hour actor-scoped idempotency. */
    post: operations['selectMemberResourceInstallation'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/member/installation-reports': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description Authenticated member/client reports only its exact installationRevision, versionId and digest. Stale reports return 409. Applied enabled versions are reauthorized; failed returns the fixed resource.apply_failed code. Does not accept arbitrary error text. Report and selection revision are separate; reportedRevision identifies the actual client observation. */
    post: operations['reportMemberResourceInstallation'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/desktop/latest': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description Public signed manifest for the latest listed release. It remains visible after notAfter so new users can download; automatic updaters must reject expired manifests until renewed. */
    get: operations['getDesktopLatest'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/desktop/packages/{sequence}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description Public stable Desktop release. Read-only, public signed metadata only. Rate limited at edge. */
    get: operations['downloadDesktopPackage'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/desktop/releases': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['listDesktopReleases'];
    put?: never;
    /** @description Admin session and recent step-up required. Upload ZIP bytes and a base64url UTF-8 signed manifest in X-Desktop-Manifest. Signature and whole package digest verified before publication. Strictly increasing sequence and version. Signed bytes use integer canonical JSON; independent offline Ed25519 key. */
    post: operations['publishDesktopRelease'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/desktop/releases/{sequence}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    post?: never;
    /** @description Recent step-up required. Withdrawal is permanent; sequence high-water retained. */
    delete: operations['withdrawDesktopRelease'];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/catalog/packages': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description P1-K frozen contract; implementation follows in consuming work lines. Catalog covers skill and future tool only; plugin IDs are not exposed here and return 404. Plugin views use unchanged legacy APIs. Enforce live tenant ACL, ETag, idempotency, step-up and author separation. Upload permission alone does not authorize a tool kind/delivery or security reviewer role; server policy must authorize them. */
    get: operations['UnifiedListResourcePackages'];
    put?: never;
    /** @description P1-K frozen contract; implementation follows in consuming work lines. Catalog covers skill and future tool only; plugin IDs are not exposed here and return 404. Plugin views use unchanged legacy APIs. Enforce live tenant ACL, ETag, idempotency, step-up and author separation. Upload permission alone does not authorize a tool kind/delivery or security reviewer role; server policy must authorize them. */
    post: operations['UnifiedCreateResourcePackage'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/catalog/packages/{packageId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description P1-K frozen contract; implementation follows in consuming work lines. Catalog covers skill and future tool only; plugin IDs are not exposed here and return 404. Plugin views use unchanged legacy APIs. Enforce live tenant ACL, ETag, idempotency, step-up and author separation. Upload permission alone does not authorize a tool kind/delivery or security reviewer role; server policy must authorize them. */
    get: operations['UnifiedGetResourcePackage'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/catalog/packages/{packageId}/versions': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description P1-K frozen contract; implementation follows in consuming work lines. Catalog covers skill and future tool only; plugin IDs are not exposed here and return 404. Plugin views use unchanged legacy APIs. Enforce live tenant ACL, ETag, idempotency, step-up and author separation. Upload permission alone does not authorize a tool kind/delivery or security reviewer role; server policy must authorize them. */
    get: operations['UnifiedListResourceVersions'];
    put?: never;
    /** @description P1-K frozen contract; implementation follows in consuming work lines. Catalog covers skill and future tool only; plugin IDs are not exposed here and return 404. Plugin views use unchanged legacy APIs. Enforce live tenant ACL, ETag, idempotency, step-up and author separation. Upload permission alone does not authorize a tool kind/delivery or security reviewer role; server policy must authorize them. */
    post: operations['UnifiedUploadResourceVersion'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/catalog/packages/{packageId}/versions/{versionId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description P1-K frozen contract; implementation follows in consuming work lines. Catalog covers skill and future tool only; plugin IDs are not exposed here and return 404. Plugin views use unchanged legacy APIs. Enforce live tenant ACL, ETag, idempotency, step-up and author separation. Upload permission alone does not authorize a tool kind/delivery or security reviewer role; server policy must authorize them. */
    get: operations['UnifiedGetResourceVersion'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/catalog/packages/{packageId}/versions/{versionId}/submissions': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description P1-K frozen contract; implementation follows in consuming work lines. Catalog covers skill and future tool only; plugin IDs are not exposed here and return 404. Plugin views use unchanged legacy APIs. Enforce live tenant ACL, ETag, idempotency, step-up and author separation. Upload permission alone does not authorize a tool kind/delivery or security reviewer role; server policy must authorize them. */
    post: operations['UnifiedSubmitResourceVersion'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/catalog/review': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description P1-K frozen contract; implementation follows in consuming work lines. Catalog covers skill and future tool only; plugin IDs are not exposed here and return 404. Plugin views use unchanged legacy APIs. Enforce live tenant ACL, ETag, idempotency, step-up and author separation. Upload permission alone does not authorize a tool kind/delivery or security reviewer role; server policy must authorize them. */
    get: operations['UnifiedListMarketAdminReview'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/catalog/published': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description P1-K frozen contract; implementation follows in consuming work lines. Catalog covers skill and future tool only; plugin IDs are not exposed here and return 404. Plugin views use unchanged legacy APIs. Enforce live tenant ACL, ETag, idempotency, step-up and author separation. Upload permission alone does not authorize a tool kind/delivery or security reviewer role; server policy must authorize them. */
    get: operations['UnifiedListMarketAdminPublished'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/catalog/reviews/{reviewId}/decisions': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description P1-K frozen contract; implementation follows in consuming work lines. Catalog covers skill and future tool only; plugin IDs are not exposed here and return 404. Plugin views use unchanged legacy APIs. Enforce live tenant ACL, ETag, idempotency, step-up and author separation. Upload permission alone does not authorize a tool kind/delivery or security reviewer role; server policy must authorize them. */
    post: operations['UnifiedDecideResourceReview'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/catalog/packages/{packageId}/versions/{versionId}/publications': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description P1-K frozen contract; implementation follows in consuming work lines. Catalog covers skill and future tool only; plugin IDs are not exposed here and return 404. Plugin views use unchanged legacy APIs. Enforce live tenant ACL, ETag, idempotency, step-up and author separation. Upload permission alone does not authorize a tool kind/delivery or security reviewer role; server policy must authorize them. */
    post: operations['UnifiedPublishResourceVersion'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/catalog/packages/{packageId}/versions/{versionId}/withdrawals': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description P1-K frozen contract; implementation follows in consuming work lines. Catalog covers skill and future tool only; plugin IDs are not exposed here and return 404. Plugin views use unchanged legacy APIs. Enforce live tenant ACL, ETag, idempotency, step-up and author separation. Upload permission alone does not authorize a tool kind/delivery or security reviewer role; server policy must authorize them. */
    post: operations['UnifiedWithdrawResourceVersion'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/catalog/packages/{packageId}/versions/{versionId}/manifest': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description P1-K frozen contract; implementation follows in consuming work lines. Return the exact stored immutable auriga-resource.json UTF-8 bytes, without JSON parsing, reserialization, newline or BOM changes. Locator does not grant access: check tenant/package/version ACL on every read. Caller hashes bytes and compares version digest and verified envelope before parsing. Plugin IDs return 404. */
    get: operations['UnifiedDownloadResourceManifest'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/catalog/reviews/{reviewId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description P1-K frozen contract; implementation follows in consuming work lines. Catalog covers skill and future tool only; plugin IDs are not exposed here and return 404. Plugin views use unchanged legacy APIs. Enforce live tenant ACL, ETag, idempotency, step-up and author separation. Upload permission alone does not authorize a tool kind/delivery or security reviewer role; server policy must authorize them. */
    get: operations['UnifiedGetResourceReview'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/catalog/member/download-tickets': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description P1-K frozen contract. Implementation follows in consuming work lines. Mutation requires If-Match and Idempotency-Key. Identity, tenant, ownership, kind and exact revision are checked from trusted authentication. Plugin IDs return 404. P1 enables skill only; tool delivery remains denied until dedicated authorization is enabled. */
    post: operations['UnifiedCreateDownloadTicket'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/catalog/member/downloads': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description P1-K frozen contract. Implementation follows in consuming work lines. Mutation requires If-Match and Idempotency-Key. Identity, tenant, ownership, kind and exact revision are checked from trusted authentication. Plugin IDs return 404. P1 enables skill only; tool delivery remains denied until dedicated authorization is enabled. */
    post: operations['UnifiedDownloadArtifact'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/catalog/member/installations': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description P1-K frozen contract. Implementation follows in consuming work lines. Identity, tenant, ownership, kind and exact revision are checked from trusted authentication. Plugin IDs return 404. P1 enables skill only; tool delivery remains denied until dedicated authorization is enabled. */
    get: operations['UnifiedListInstallations'];
    put?: never;
    /** @description P1-K frozen contract. Implementation follows in consuming work lines. Mutation requires If-Match and Idempotency-Key. Identity, tenant, ownership, kind and exact revision are checked from trusted authentication. Plugin IDs return 404. P1 enables skill only; tool delivery remains denied until dedicated authorization is enabled. */
    post: operations['UnifiedSelectInstallation'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/catalog/member/installation-reports': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description P1-K frozen contract. Implementation follows in consuming work lines. Mutation requires If-Match and Idempotency-Key. Identity, tenant, ownership, kind and exact revision are checked from trusted authentication. Plugin IDs return 404. P1 enables skill only; tool delivery remains denied until dedicated authorization is enabled. */
    post: operations['UnifiedReportInstallation'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/catalog/member/renewals': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description P1-K frozen contract. Implementation follows in consuming work lines. Mutation requires If-Match and Idempotency-Key. Identity, tenant, ownership, kind and exact revision are checked from trusted authentication. Plugin IDs return 404. P1 enables skill only; tool delivery remains denied until dedicated authorization is enabled. */
    post: operations['UnifiedRenewInstallation'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/catalog/distributions': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description P1-K frozen contract. Implementation follows in consuming work lines. Identity, tenant, ownership, kind and exact revision are checked from trusted authentication. Plugin IDs return 404. P1 enables skill only; tool delivery remains denied until dedicated authorization is enabled. */
    get: operations['UnifiedListDistributions'];
    put?: never;
    /** @description P1-K frozen contract. Implementation follows in consuming work lines. Mutation requires If-Match and Idempotency-Key. Identity, tenant, ownership, kind and exact revision are checked from trusted authentication. Plugin IDs return 404. P1 enables skill only; tool delivery remains denied until dedicated authorization is enabled. */
    post: operations['UnifiedCreateDistribution'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/catalog/distributions/{distributionId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description P1-K frozen contract. Implementation follows in consuming work lines. Identity, tenant, ownership, kind and exact revision are checked from trusted authentication. Plugin IDs return 404. P1 enables skill only; tool delivery remains denied until dedicated authorization is enabled. */
    get: operations['UnifiedGetDistribution'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/catalog/packages/{packageId}/versions/{versionId}/artifacts/{artifactId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description P1-K frozen contract. Implementation follows in consuming work lines. Identity, tenant, ownership, kind and exact revision are checked from trusted authentication. Plugin IDs return 404. P1 enables skill only; tool delivery remains denied until dedicated authorization is enabled. */
    get: operations['UnifiedDownloadVersionArtifact'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/internal/resource/v1/catalog/downloads/{downloadId}/manifest': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description Exact immutable manifest bytes. Node mTLS SAN and tenant, owner, current instance scheduling, selectionRevision, generation, kind, exact version and digest authorization checked online for every read. Locator grants no access. Lease expiry or revoked version denies download. No redirects or Range. */
    get: operations['UnifiedDownloadNodeManifest'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/internal/resource/v1/catalog/downloads/{downloadId}/artifacts/{artifactId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description Exact immutable artifact bytes. Node mTLS SAN and tenant, owner, current instance scheduling, selectionRevision, generation, kind, exact version and digest authorization checked online for every read. Locator grants no access. Lease expiry or revoked version denies download. No redirects or Range. */
    get: operations['UnifiedDownloadNodeArtifact'];
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
    ResourceEnvelope: {
      /** @constant */
      schemaVersion: 1;
      tenantId: components['schemas']['Uuid'];
      packageId: components['schemas']['Uuid'];
      versionId: components['schemas']['Uuid'];
      /** @description 同一租户/包的单调安全序号，不能用 SemVer 或 build metadata 替代。 */
      releaseSequence: components['schemas']['Counter'];
      /** @description 必须晚于 statement.issuedAt，最长 24 小时；续签不改变身份/字节/安全序号且必须重新授权审计。 */
      notAfter: components['schemas']['Time'];
      statement: components['schemas']['Statement'];
      signature: components['schemas']['Signature'];
    };
    RevocationList: {
      /** @constant */
      schemaVersion: 1;
      tenantId: components['schemas']['Uuid'];
      revision: components['schemas']['Counter'];
      issuedAt: components['schemas']['Time'];
      /** @description issuedAt 之后且最多 24 小时；离线最迟于此失效并停用受管资源。 */
      expiresAt: components['schemas']['Time'];
      revokedKids: string[];
      revokedArtifacts: components['schemas']['RevokedArtifact'][];
      signature: components['schemas']['Signature'];
    };
    DowngradeAuthorization: {
      /** @constant */
      schemaVersion: 1;
      authorizationId: components['schemas']['Uuid'];
      tenantId: components['schemas']['Uuid'];
      instanceId: components['schemas']['Uuid'];
      packageId: components['schemas']['Uuid'];
      versionId: components['schemas']['Uuid'];
      digest: components['schemas']['Digest'];
      fromReleaseSequence: components['schemas']['Counter'];
      toReleaseSequence: components['schemas']['Counter'];
      issuedAt: components['schemas']['Time'];
      expiresAt: components['schemas']['Time'];
      reason: string;
      signature: components['schemas']['Signature'];
    };
    Package: {
      /** Format: uuid */
      id: string;
      /** Format: uuid */
      tenantId: string;
      name: string;
      /** @constant */
      kind: 'plugin';
      /** Format: uuid */
      ownerId: string;
      /** @enum {string} */
      level: 'l0' | 'l1' | 'l2' | 'l3' | 'l4';
      /** @enum {string} */
      visibility: 'tenant' | 'members' | 'groups' | 'private';
      groupIds: string[];
      /** Format: int64 */
      revision: number;
      /** Format: date-time */
      createdAt: string;
      enabled: boolean;
      /** Format: uuid */
      defaultVersionId: string | null;
    };
    /** @description 新建只允许本人L3待审草稿或L4/private个人包；level/owner/tenant由服务端决定，不能直接创建L0-L2。 */
    PackageInput: {
      name: string;
      /** @constant */
      kind: 'plugin';
      /** @enum {string} */
      visibility: 'tenant' | 'members' | 'groups' | 'private';
      groupIds?: string[];
    };
    PackagePatch: {
      name?: string;
      /** @enum {string} */
      visibility?: 'tenant' | 'members' | 'groups' | 'private';
      groupIds?: string[];
    };
    AdminPackagePatch: {
      /** @enum {string} */
      level?: 'l1' | 'l2' | 'l3';
      enabled?: boolean;
      /** Format: uuid */
      defaultVersionId?: string;
      reason: string;
      /** @enum {string} */
      visibility?: 'tenant' | 'members' | 'groups';
      groupIds?: string[];
    };
    PackagePage: {
      items: components['schemas']['Package'][];
      nextCursor?: string;
    };
    Version: {
      /** Format: uuid */
      id: string;
      /** Format: uuid */
      packageId: string;
      version: string;
      digest: components['schemas']['Digest'];
      /** Format: int64 */
      sizeBytes: number;
      /** @constant */
      mediaType: 'application/gzip';
      /** @enum {string} */
      state:
        | 'draft'
        | 'pending_review'
        | 'approved'
        | 'rejected'
        | 'published'
        | 'deprecated'
        | 'withdrawn';
      /** Format: int64 */
      revision: number;
      /** Format: int64 */
      releaseSequence?: number;
      resourceEnvelope?: components['schemas']['ResourceEnvelope'];
      /** Format: date-time */
      createdAt: string;
    };
    UploadMetadata: {
      version: string;
      expectedDigest: components['schemas']['Digest'];
      manifest: {
        name: string;
        dshVersionRange: string;
        /** @enum {string} */
        os: 'linux' | 'windows' | 'any';
        /** @enum {string} */
        arch: 'amd64' | 'arm64' | 'any';
      };
      changeNotes?: string;
    };
    Reason: {
      reason: string;
    };
    Review: {
      /** Format: uuid */
      id: string;
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      versionId: string;
      digest: components['schemas']['Digest'];
      /** Format: int64 */
      versionRevision: number;
      /** @enum {string} */
      state: 'pending' | 'approved' | 'rejected';
      /** Format: int64 */
      revision: number;
    };
    ReviewDecision: {
      /** @enum {string} */
      decision: 'approve' | 'reject';
      reason: string;
      /** Format: int64 */
      versionRevision: number;
      digest: components['schemas']['Digest'];
    };
    Withdrawal: {
      /** @enum {string} */
      mode: 'withdrawn' | 'malicious';
      reason: string;
    };
    DistributionInput: {
      /** Format: uuid */
      instanceId: string;
      selections: {
        /** Format: uuid */
        packageId: string;
        /** Format: uuid */
        versionId: string;
        /** @enum {string} */
        action: 'install' | 'enable' | 'disable' | 'remove';
      }[];
    };
    Distribution: {
      /** Format: uuid */
      id: string;
      /** Format: uuid */
      instanceId: string;
      /** Format: uuid */
      ownerId: string;
      /** Format: int64 */
      selectionRevision: number;
      /**
       * @description Once workspace accepts a newer selection revision, requested/resolved/applying becomes superseded. Already applied or failed rows keep the latest recorded outcome and error; appliedGeneration never replaces a later real failure.
       * @enum {string}
       */
      state:
        | 'requested'
        | 'resolved'
        | 'applying'
        | 'applied'
        | 'failed'
        | 'superseded';
      /** Format: int64 */
      generation?: number;
      /** Format: int64 */
      observedGeneration?: number;
      ready?: boolean;
      errorCode?: string;
      /** Format: int64 */
      revision: number;
      selections?: {
        /** Format: uuid */
        packageId: string;
        /** Format: uuid */
        versionId: string;
        /** @enum {string} */
        action: 'install' | 'enable' | 'disable' | 'remove';
      }[];
      /**
       * Format: int64
       * @description Last generation genuinely observed ready. Historical rows retain this successful observation after a newer selection replaces them.
       */
      appliedGeneration?: number;
    };
    DownloadAuthorization: {
      /** Format: uuid */
      downloadId: string;
      /** Format: uuid */
      tenantId: string;
      /** Format: uuid */
      nodeId: string;
      /** Format: uuid */
      instanceId: string;
      /** Format: uuid */
      ownerId: string;
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      versionId: string;
      digest: components['schemas']['Digest'];
      /** Format: int64 */
      selectionRevision: number;
      /** Format: date-time */
      expiresAt: string;
      /** Format: uri */
      url: string;
    };
    VersionPage: {
      items: components['schemas']['Version'][];
      nextCursor?: string;
    };
    ReviewPage: {
      items: components['schemas']['Review'][];
      nextCursor?: string;
    };
    DowngradeInput: {
      /** Format: uuid */
      instanceId: string;
      /** Format: int64 */
      fromReleaseSequence: number;
      reason: string;
    };
    MarketAdminFile: {
      path: string;
      /** Format: int64 */
      sizeBytes: number;
      digest: string;
    };
    MarketAdminMetadata: {
      version: string;
      expectedDigest: string;
      manifest: {
        name: string;
        dshVersionRange: string;
        /** @enum {string} */
        os: 'linux' | 'windows' | 'any';
        /** @enum {string} */
        arch: 'amd64' | 'arm64' | 'any';
      };
      changeNotes?: string;
      permissions: string[];
    };
    MarketAdminReviewRecord: {
      review: components['schemas']['Review'];
      reason: string;
      /** Format: uuid */
      reviewerId?: string;
    };
    MarketAdminDetail: {
      resourcePackage: components['schemas']['Package'];
      version: components['schemas']['Version'];
      /** Format: uuid */
      authorId: string;
      metadata: components['schemas']['MarketAdminMetadata'] | null;
      files: components['schemas']['MarketAdminFile'][] | null;
      reviews: components['schemas']['MarketAdminReviewRecord'][];
      previousVersion?: components['schemas']['Version'];
      previousFiles?: components['schemas']['MarketAdminFile'][];
      revoked?: boolean;
    };
    MarketAdminDistributionPage: {
      items: components['schemas']['Distribution'][];
      nextCursor?: string;
    };
    MemberDownloadTicketInput: {
      /** Format: uuid */
      clientId: string;
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      versionId: string;
      digest: string;
    };
    MemberDownloadTicket: {
      ticket: string;
      /** Format: uuid */
      clientId: string;
      /** Format: date-time */
      expiresAt: string;
      resourceEnvelope: components['schemas']['ResourceEnvelope'];
      revocations: components['schemas']['RevocationList'];
    };
    MemberDownloadInput: {
      /** Format: uuid */
      clientId: string;
      ticket: string;
    };
    MemberInstallationInput: {
      /** Format: uuid */
      clientId: string;
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      versionId: string;
      digest: string;
      /** @enum {string} */
      action: 'install' | 'enable' | 'disable' | 'remove';
    };
    MemberInstallationReport: {
      /** Format: uuid */
      clientId: string;
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      versionId: string;
      digest: string;
      /** Format: int64 */
      installationRevision: number;
      /** @enum {string} */
      state: 'applied' | 'failed';
    };
    MemberInstallation: {
      /** Format: uuid */
      clientId: string;
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      versionId: string;
      digest: string;
      /** Format: int64 */
      releaseSequence: number;
      /** @enum {string} */
      level: 'l1' | 'l2' | 'l3' | 'l4';
      /** @enum {string} */
      desired: 'enabled' | 'disabled' | 'removed';
      /** @enum {string} */
      state: 'applying' | 'applied' | 'failed';
      /** Format: int64 */
      revision: number;
      /** @constant */
      reportSource: 'member';
      /** Format: int64 */
      reportedRevision?: number;
      /** Format: date-time */
      reportedAt?: string;
      /** @constant */
      errorCode?: 'resource.apply_failed';
    };
    MemberInstallationPage: {
      items: components['schemas']['MemberInstallation'][];
      /** Format: uuid */
      nextAfterPackageId?: string;
    };
    DesktopManifest: {
      /** @constant */
      schema: 1;
      /** @constant */
      product: 'Auriga Desktop';
      version: string;
      packageId: string;
      /** @constant */
      channel: 'stable';
      /** @constant */
      platform: 'win-x64';
      /** Format: int64 */
      sizeBytes: number;
      sha256: string;
      minimumVersion: string;
      /** Format: date-time */
      publishedAt: string;
      /** Format: date-time */
      notAfter: string;
      /** Format: int64 */
      sequence: number;
      notes: string;
      signature: {
        /** @constant */
        alg: 'Ed25519';
        /** @constant */
        kid: 'auriga-desktop-release-1';
        value: string;
      };
      components: {
        dshVersion: string;
        nodeVersion: string;
        pluginVersion: string;
      };
    };
    DesktopReleaseItem: {
      manifest: components['schemas']['DesktopManifest'];
      withdrawn: boolean;
    };
    DesktopReleaseLedger: {
      highestSequence?: number;
      items?: components['schemas']['DesktopReleaseItem'][];
      audit?: {
        [key: string]: unknown;
      }[];
    };
    /** @description Catalog manifest for skill/tool. PluginManifest remains in the package format for internal migration only; catalog APIs do not expose plugin projections. */
    UnifiedManifest:
      | components['schemas']['SkillManifest']
      | components['schemas']['ToolManifest'];
    UnifiedEnvelope: components['schemas']['Envelope'];
    /** @description Shared L3 skill/tool only. Server derives tenant, owner and level. Private skill stays local. Plugin CRUD uses legacy APIs; catalog rejects plugin kind/IDs. */
    UnifiedPackageInput: {
      name: string;
      /** @enum {string} */
      kind: 'skill' | 'tool';
      /** @enum {string} */
      visibility: 'tenant' | 'members' | 'groups';
      groupIds?: string[];
    };
    /** @description Shared L3 skill/tool. Plugin L0-L4/private views continue through legacy APIs, without catalog projection. */
    UnifiedPackage: {
      /** Format: uuid */
      id: string;
      /** Format: uuid */
      tenantId: string;
      name: string;
      /** @enum {string} */
      kind: 'skill' | 'tool';
      /** Format: uuid */
      ownerId: string;
      /** @enum {string} */
      level: 'l3';
      /** @enum {string} */
      visibility: 'tenant' | 'members' | 'groups';
      groupIds: string[];
      /** Format: int64 */
      revision: number;
      /** Format: date-time */
      createdAt: string;
      enabled: boolean;
      /** Format: uuid */
      defaultVersionId: string | null;
    };
    /** @description Draft version. Reviews bind manifest digest and version revision. Published manifests and artifacts are immutable. */
    UnifiedVersion: {
      /** Format: uuid */
      id: string;
      /** Format: uuid */
      packageId: string;
      version: string;
      /** @description SHA-256 of exact immutable auriga-resource.json UTF-8 bytes. Artifact digests are in the manifest. Legacy Version.digest still addresses plugin archive bytes. */
      digest: components['schemas']['Digest'];
      /** @enum {string} */
      state:
        | 'draft'
        | 'pending_review'
        | 'approved'
        | 'rejected'
        | 'published'
        | 'deprecated'
        | 'withdrawn';
      /** Format: int64 */
      revision: number;
      /** Format: int64 */
      releaseSequence?: number;
      resourceEnvelope?: components['schemas']['Envelope'];
      /** Format: date-time */
      createdAt: string;
      /** @description Display only; not signed JSON bytes. Never reserialize this field for verification or installation. Download exact bytes using manifestDownloadPath, hash them against digest and the verified envelope before parsing. */
      manifest: components['schemas']['UnifiedManifest'];
      /** @enum {string} */
      kind: 'skill' | 'tool';
      readonly risk: components['schemas']['UnifiedRiskAssessment'];
      /** @description Same-origin locator for exact immutable raw manifest UTF-8 bytes, not a bearer URL. Every request checks tenant/package/version ACL; caller must verify digest/envelope. Published and draft reads use the current corresponding ACL. */
      manifestDownloadPath: string;
    };
    /** @description Draft multipart upload. expectedDigest is exact manifest bytes SHA-256. Include manifest as a binary part, plus one binary artifacts part per manifest artifact in array order. Online tools use no artifacts. Validate all hashes before committing the immutable version. */
    UnifiedUploadMetadata: {
      version: components['schemas']['$defs-Version'];
      expectedDigest: components['schemas']['Digest'];
      changeNotes?: string;
    };
    UnifiedPackagePage: {
      items: components['schemas']['UnifiedPackage'][];
      nextCursor?: string;
    };
    UnifiedVersionPage: {
      items: components['schemas']['UnifiedVersion'][];
      nextCursor?: string;
    };
    /** @description Read-only server assessment, recalculated on content/permission changes. Publication transaction checks the current assessment and review identities. */
    UnifiedRiskAssessment: {
      /**
       * @description Server-derived floor: command skill or any nonempty tools/outboundHosts/dataAccess is at least r2; tool at least r2, git r3. Server scans text too; declarations cannot downgrade command content.
       * @enum {string}
       */
      minimumLevel: 'r1' | 'r2' | 'r3';
      /**
       * @description Effective server risk. Authorized reviewers may raise it only; changes increment revision and invalidate earlier reviews.
       * @enum {string}
       */
      level: 'r1' | 'r2' | 'r3';
      /** @description Distinct non-author reviewers; one for r1, two for r2/r3. Repeated identities count once. */
      requiredReviewers: number;
      /** @description Zero for r1; at least one qualified security reviewer for r2/r3. Server verifies qualification independently of input role. */
      requiredSecurityReviewers: number;
    } & (unknown & unknown & unknown);
    /** @description Decision binds exact versionRevision and raw manifest digest. Author cannot review/publish. Required role is server-authorized; publication counts distinct qualified non-author identities. */
    UnifiedReviewDecision: {
      /** @enum {string} */
      decision: 'approve' | 'reject';
      reason: string;
      /** Format: int64 */
      versionRevision: number;
      digest: components['schemas']['Digest'];
      /**
       * @description Claimed review duty; server must verify the authenticated reviewer qualification. Identity is never supplied by this body.
       * @enum {string}
       */
      role: 'general' | 'security';
      /**
       * @description Optional raise-only risk proposal. Server rejects lower than current/floor; an accepted change invalidates prior decisions and requires review of the new revision.
       * @enum {string}
       */
      riskLevel?: 'r1' | 'r2' | 'r3';
    };
    UnifiedReviewRecord: {
      /** Format: uuid */
      reviewerId: string;
      /** @enum {string} */
      role: 'general' | 'security';
      /** @enum {string} */
      decision: 'approve' | 'reject';
      reason: string;
      /** Format: int64 */
      versionRevision: number;
      digest: components['schemas']['Digest'];
      /** Format: date-time */
      createdAt: string;
    };
    /** @description Review identity and immutable decision history for the exact version revision. Version contains kind and server risk/quorum. Failed author/qualification/quorum checks cannot approve/publish. */
    UnifiedReview: {
      /** Format: uuid */
      reviewId: string;
      /** Format: uuid */
      authorId: string;
      version: components['schemas']['UnifiedVersion'];
      /** Format: int64 */
      revision: number;
      /** @enum {string} */
      status: 'pending' | 'approved' | 'rejected';
      decisions: components['schemas']['UnifiedReviewRecord'][];
    };
    UnifiedReviewPage: {
      items: components['schemas']['UnifiedReview'][];
      nextCursor?: string;
    };
    UnifiedResourceIdentity: {
      /** Format: uuid */
      tenantId: string;
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      versionId: string;
      /** @enum {string} */
      kind: 'skill' | 'tool';
      version: components['schemas']['$defs-Version'];
      manifestDigest: components['schemas']['Digest'];
    };
    UnifiedLease: {
      envelopeNotAfter: components['schemas']['Time'];
      revocationsExpiresAt: components['schemas']['Time'];
      expiresAt: components['schemas']['Time'];
      /** Format: int64 */
      revocationRevision: number;
    };
    UnifiedArtifactDownload: {
      artifactId: string;
      digest: components['schemas']['Digest'];
      /** Format: int64 */
      sizeBytes: number;
      /** @description Same-origin path only. Reject absolute URLs, authority paths, query, fragments, percent escapes, redirects and dot segments. */
      downloadPath: string;
    };
    UnifiedResourceRef: {
      identity: components['schemas']['UnifiedResourceIdentity'];
      envelope: components['schemas']['Envelope'];
      /** @description Same-origin path only. Reject absolute URLs, authority paths, query, fragments, percent escapes, redirects and dot segments. */
      manifestDownloadPath: string;
      artifacts: components['schemas']['UnifiedArtifactDownload'][];
      lease: components['schemas']['UnifiedLease'];
      /** Format: int64 */
      releaseSequence: number;
      enabled: boolean;
      /**
       * Format: int64
       * @description Resource-assigned persisted per-installation CAS revision. Preserved by signature renewal and echoed by node/Desktop reports. Omission is transport-compatible only: never accept applied without a positive expected revision.
       */
      installationRevision?: number;
      /**
       * @description Preferred desired state when present. If absent on legacy data, derive ENABLED/DISABLED from enabled. REMOVED is an uninstall tombstone carrying the new installationRevision, retained until a matching removed acknowledgement and retired in the next generation. Reject unknown or unspecified values.
       * @enum {string}
       */
      desiredState?: 'ENABLED' | 'DISABLED' | 'REMOVED';
    };
    UnifiedSelection: {
      identity: components['schemas']['UnifiedResourceIdentity'];
      /** @enum {string} */
      action: 'install' | 'enable' | 'disable' | 'remove';
    };
    UnifiedDownloadTicketInput: {
      /** Format: uuid */
      clientId: string;
      identity: components['schemas']['UnifiedResourceIdentity'];
      /** Format: int64 */
      installationRevision: number;
    };
    UnifiedDownloadTicket: {
      ticket: string;
      /** Format: uuid */
      clientId: string;
      expiresAt: components['schemas']['Time'];
      resource: components['schemas']['UnifiedResourceRef'];
      revocations: components['schemas']['RevocationList'];
    };
    UnifiedDownloadInput: {
      /** Format: uuid */
      clientId: string;
      ticket: string;
      artifactId: string;
    };
    UnifiedInstallationInput: {
      /** Format: uuid */
      clientId: string;
      selection: components['schemas']['UnifiedSelection'];
    };
    UnifiedInstallationReport: {
      /** Format: uuid */
      clientId: string;
      identity: components['schemas']['UnifiedResourceIdentity'];
      /** Format: int64 */
      installationRevision: number;
      /** Format: int64 */
      selectionRevision: number;
      /** Format: int64 */
      generation: number;
      /** Format: int64 */
      observedGeneration: number;
      /** @enum {string} */
      state:
        | 'applying'
        | 'applied'
        | 'failed'
        | 'pending_restart'
        | 'superseded'
        | 'overridden'
        | 'expired'
        | 'revoked'
        | 'disabled'
        | 'removed';
      /** @enum {string} */
      reportSource: 'member' | 'node';
      errorCode?: string;
      observedAt: components['schemas']['Time'];
    } & unknown;
    UnifiedInstallation: {
      /** Format: uuid */
      clientId: string;
      resource: components['schemas']['UnifiedResourceRef'];
      /** Format: int64 */
      installationRevision: number;
      /** Format: int64 */
      selectionRevision: number;
      /** Format: int64 */
      generation: number;
      /** @enum {string} */
      state:
        | 'applying'
        | 'applied'
        | 'failed'
        | 'pending_restart'
        | 'superseded'
        | 'overridden'
        | 'expired'
        | 'revoked'
        | 'disabled'
        | 'removed';
      /** @enum {string} */
      reportSource: 'member' | 'node';
      report?: components['schemas']['UnifiedInstallationReport'];
    };
    UnifiedInstallationPage: {
      items: components['schemas']['UnifiedInstallation'][];
      nextCursor?: string;
    };
    UnifiedDistributionInput: {
      /** Format: uuid */
      instanceId: string;
      selections: components['schemas']['UnifiedSelection'][];
    };
    UnifiedDistribution: {
      /** Format: uuid */
      distributionId: string;
      /** Format: uuid */
      instanceId: string;
      /** Format: uuid */
      ownerId: string;
      /** Format: int64 */
      selectionRevision: number;
      /** Format: int64 */
      generation: number;
      selections: components['schemas']['UnifiedSelection'][];
      /** @enum {string} */
      state:
        | 'applying'
        | 'applied'
        | 'failed'
        | 'pending_restart'
        | 'superseded'
        | 'overridden'
        | 'expired'
        | 'revoked'
        | 'disabled'
        | 'removed';
      /** @enum {string} */
      reportSource: 'node';
      reports: components['schemas']['UnifiedInstallationReport'][];
    };
    UnifiedDistributionPage: {
      items: components['schemas']['UnifiedDistribution'][];
      nextCursor?: string;
    };
    UnifiedRenewalInput: {
      /** Format: uuid */
      clientId: string;
      identity: components['schemas']['UnifiedResourceIdentity'];
      /** Format: int64 */
      installationRevision: number;
      /** Format: int64 */
      selectionRevision: number;
      /** Format: int64 */
      releaseSequence: number;
    };
    UnifiedGuardianState: {
      /** Format: uuid */
      tenantId: string;
      /** Format: uuid */
      ownerId: string;
      /** Format: uuid */
      hostId: string;
      /** Format: int64 */
      selectionRevision: number;
      /** Format: int64 */
      generation: number;
      /** @enum {string} */
      state:
        | 'verifying'
        | 'active'
        | 'stopping'
        | 'isolating'
        | 'private_only'
        | 'blocked';
      /** @enum {string} */
      reason:
        | 'startup'
        | 'lease_expired'
        | 'revoked'
        | 'identity_changed'
        | 'clock_rollback'
        | 'verification_failed'
        | 'cleanup_failed';
      lease: components['schemas']['UnifiedLease'];
      processStopped: boolean;
      viewCleared: boolean;
      newSessionRequired: boolean;
    } & unknown;
    UnifiedMigrationProof: {
      /** @constant */
      schemaVersion: 2;
      legacyEnvelope: components['schemas']['ResourceEnvelope'];
      unifiedEnvelope: components['schemas']['Envelope'];
      legacyArtifactId: string;
      /** @description Must equal signed marker.activatedAt; immutable package migration t0. */
      activatedAt: components['schemas']['Time'];
      marker: components['schemas']['UnifiedMigrationMarker'];
    };
    UnifiedHighWatermark: {
      /** Format: uuid */
      tenantId: string;
      /** Format: uuid */
      packageId: string;
      /** Format: int64 */
      releaseSequence: number;
      /** Format: uuid */
      versionId: string;
      verifiedDigestAliases: components['schemas']['Digest'][];
    };
    UnifiedDowngradeAuthorization: {
      /** @constant */
      schemaVersion: 2;
      /** Format: uuid */
      authorizationId: string;
      /** Format: uuid */
      instanceId: string;
      identity: components['schemas']['UnifiedResourceIdentity'];
      /** Format: int64 */
      fromReleaseSequence: number;
      /** Format: int64 */
      toReleaseSequence: number;
      issuedAt: components['schemas']['Time'];
      expiresAt: components['schemas']['Time'];
      reason: string;
      signature: components['schemas']['Signature'];
      /** @constant */
      purpose: 'downgrade';
    };
    UnifiedMemberInstallationReport: {
      /** Format: uuid */
      clientId: string;
      identity: components['schemas']['UnifiedResourceIdentity'];
      /** Format: int64 */
      installationRevision: number;
      /** Format: int64 */
      selectionRevision: number;
      /** Format: int64 */
      generation: number;
      /** Format: int64 */
      observedGeneration: number;
      /** @enum {string} */
      state:
        | 'applying'
        | 'applied'
        | 'failed'
        | 'pending_restart'
        | 'superseded'
        | 'overridden'
        | 'expired'
        | 'revoked'
        | 'disabled'
        | 'removed';
      /** @constant */
      reportSource: 'member';
      errorCode?: string;
      observedAt: components['schemas']['Time'];
    } & unknown;
    UnifiedMigrationMarker: {
      /** @constant */
      schemaVersion: 2;
      /** @constant */
      purpose: 'migration';
      /** Format: uuid */
      tenantId: string;
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      firstVersionId: string;
      /** Format: int64 */
      firstReleaseSequence: number;
      activatedAt: components['schemas']['Time'];
      signature: components['schemas']['Signature'];
    };
    UnifiedLedgerFile: {
      /** @constant */
      schemaVersion: 1;
      /** Format: uuid */
      tenantId: string;
      /** Format: uuid */
      ownerId: string;
      /** Format: uuid */
      hostId: string;
      /** Format: int64 */
      revision: number;
      /** Format: int64 */
      selectionRevision: number;
      /** Format: int64 */
      generation: number;
      installations: components['schemas']['UnifiedInstallation'][];
      processedDisableReceiptIds: string[];
    };
    UnifiedReceiptFile:
      | {
          /** @constant */
          schemaVersion: 1;
          /** Format: uuid */
          tenantId: string;
          /** Format: uuid */
          ownerId: string;
          /** Format: uuid */
          hostId: string;
          /** @constant */
          recordType: 'installation';
          /** @constant */
          writer: 'plugin';
          /** Format: uuid */
          receiptId: string;
          resource: components['schemas']['UnifiedResourceRef'];
          /** Format: int64 */
          installationRevision: number;
          /** Format: int64 */
          selectionRevision: number;
          /** Format: int64 */
          generation: number;
          recordedAt: components['schemas']['Time'];
        }
      | {
          /** @constant */
          schemaVersion: 1;
          /** Format: uuid */
          tenantId: string;
          /** Format: uuid */
          ownerId: string;
          /** Format: uuid */
          hostId: string;
          /** @constant */
          recordType: 'disable';
          /** @constant */
          writer: 'guardian';
          /** Format: uuid */
          receiptId: string;
          /** Format: uuid */
          transactionId: string;
          /** Format: int64 */
          selectionRevision: number;
          /** Format: int64 */
          generation: number;
          quarantinePath: string;
          /** @enum {string} */
          reason:
            | 'lease_expired'
            | 'revoked'
            | 'identity_changed'
            | 'clock_rollback'
            | 'verification_failed';
          processStopped: boolean;
          viewCleared: boolean;
          newSessionRequired: boolean;
          recordedAt: components['schemas']['Time'];
        };
    UnifiedTransactionFile: {
      /** @constant */
      schemaVersion: 1;
      /** Format: uuid */
      tenantId: string;
      /** Format: uuid */
      ownerId: string;
      /** Format: uuid */
      hostId: string;
      /** Format: uuid */
      transactionId: string;
      /** @constant */
      writer: 'plugin';
      /** @enum {string} */
      operation: 'install' | 'activate' | 'remove' | 'cleanup';
      /** @enum {string} */
      phase: 'prepared' | 'switched' | 'committed' | 'rolled_back';
      /** Format: int64 */
      expectedLedgerRevision: number;
      /** Format: int64 */
      selectionRevision: number;
      /** Format: int64 */
      generation: number;
      /** Format: uuid */
      receiptId: string;
      recordedAt: components['schemas']['Time'];
    };
    UnifiedTrustedClock: {
      /** Format: uuid */
      bootId: string;
      maxObservedTime: components['schemas']['Time'];
      lastTrustedTime: components['schemas']['Time'];
      /** @enum {string} */
      lastTrustedTimeSource:
        'authenticated_server' | 'signed_release' | 'signed_revocations';
      /** Format: int64 */
      lastMonotonicMillis: number;
      /** Format: int64 */
      leaseDeadlineMonotonicMillis: number;
    };
    UnifiedGuardianStateFile: {
      /** @constant */
      schemaVersion: 1;
      /** Format: int64 */
      revision: number;
      guardian: components['schemas']['UnifiedGuardianState'];
      clock: components['schemas']['UnifiedTrustedClock'];
      pendingIsolation?: {
        /** Format: uuid */
        transactionId: string;
        quarantinePath: string;
        /** @enum {string} */
        phase: 'stopping' | 'rename_pending' | 'renamed' | 'receipt_written';
      };
      /** Format: uuid */
      lastDisableReceiptId?: string;
    };
    UnifiedHighWatermarksFile: {
      /** @constant */
      schemaVersion: 1;
      /** Format: int64 */
      revision: number;
      entries: components['schemas']['UnifiedHighWatermark'][];
      migrationMarkers: components['schemas']['UnifiedMigrationMarker'][];
      consumedDowngradeAuthorizationIds: string[];
    };
    UnifiedHostLockOwner: {
      /** @constant */
      schemaVersion: 1;
      /** Format: uuid */
      lockId: string;
      /** @enum {string} */
      role: 'C' | 'E';
      pid: number;
      /** Format: date-time */
      processStartedAt: string;
      /** Format: date-time */
      createdAt: string;
      bootId: string;
      /** Format: int64 */
      monotonicMillis: number;
      /** @constant */
      leaseDurationMillis: 30000;
    };
    FieldError: {
      /** @description JSON Pointer，例如 `/email`。 */
      field: string;
      code: string;
      message?: string;
    };
    /** @description RFC 9457 问题详情。 */
    Problem: {
      /**
       * Format: uri-reference
       * @default about:blank
       */
      type: string;
      /** @description 英文短标题。 */
      title: string;
      status: number;
      /** @description 按 Accept-Language 返回的说明。 */
      detail?: string;
      /** Format: uri-reference */
      instance?: string;
      /**
       * @description 机器可读的错误码，形如 `<域>.<snake_case>`。
       * @example iam.login_tx_expired
       */
      code: string;
      /** @description 错误文案里的参数。 */
      params?: Record<string, never>;
      /** @description 字段级错误（validation_failed 时）。 */
      errors?: components['schemas']['FieldError'][];
      traceId?: string;
    };
    Digest: string;
    /** Format: uuid */
    Uuid: string;
    /** Format: int64 */
    Counter: number;
    /** Format: date-time */
    Time: string;
    Statement: {
      /** @constant */
      purpose: 'plugin';
      name: string;
      version: string;
      /** @enum {string} */
      os: 'linux' | 'windows' | 'any';
      /** @enum {string} */
      arch: 'amd64' | 'arm64' | 'any';
      digest: components['schemas']['Digest'];
      issuedAt: components['schemas']['Time'];
    };
    Signature: {
      /** @constant */
      alg: 'Ed25519';
      kid: string;
      /** @description 64 字节签名的 base64url 无填充编码；解码后须重编码比较，拒绝非规范尾位。 */
      value: string;
    };
    RevokedArtifact: {
      packageId: components['schemas']['Uuid'];
      versionId: components['schemas']['Uuid'];
      digest: components['schemas']['Digest'];
    };
    Name: string;
    '$defs-Version': string;
    '$defs-Statement': {
      /** @constant */
      purpose: 'resource';
      /** @enum {string} */
      kind: 'plugin' | 'skill' | 'tool';
      name: components['schemas']['Name'];
      version: components['schemas']['$defs-Version'];
      manifestDigest: components['schemas']['Digest'];
      artifactDigests: components['schemas']['Digest'][];
      issuedAt: components['schemas']['Time'];
    };
    Envelope: {
      /** @constant */
      schemaVersion: 2;
      tenantId: components['schemas']['Uuid'];
      packageId: components['schemas']['Uuid'];
      versionId: components['schemas']['Uuid'];
      releaseSequence: components['schemas']['Counter'];
      notAfter: components['schemas']['Time'];
      statement: components['schemas']['$defs-Statement'];
      signature: components['schemas']['Signature'];
    };
    Permissions: {
      tools: string[];
      outboundHosts: string[];
      dataAccess: (
        'workspace_read' | 'workspace_write' | 'user_selected_files'
      )[];
    };
    Runtime: {
      /** @enum {string} */
      kind: 'node' | 'python' | 'java';
      versionRange: string;
    };
    SkillArtifact: {
      id: components['schemas']['Name'];
      /** @constant */
      role: 'payload';
      digest: components['schemas']['Digest'];
      sizeBytes: number;
      /** @constant */
      mediaType: 'application/gzip';
      /** @constant */
      os: 'any';
      /** @constant */
      arch: 'any';
    };
    Skill: {
      /** @constant */
      entry: 'SKILL.md';
      /** @constant */
      contentProfile: 'text-only';
      dshVersionRange: string;
    };
    SkillManifest: {
      /** @constant */
      schemaVersion: 1;
      name: components['schemas']['Name'];
      version: components['schemas']['$defs-Version'];
      description: string;
      author: {
        displayName: string;
      };
      license: string;
      permissions: components['schemas']['Permissions'];
      runtimes: components['schemas']['Runtime'][];
      artifacts: components['schemas']['SkillArtifact'][];
      signature: {
        /** @constant */
        mode: 'detached';
        /** @constant */
        algorithm: 'Ed25519';
      };
      /** @constant */
      kind: 'skill';
      skill: components['schemas']['Skill'];
    };
    Artifact: {
      id: components['schemas']['Name'];
      /** @enum {string} */
      role: 'payload' | 'source_snapshot' | 'lockfile';
      digest: components['schemas']['Digest'];
      sizeBytes: number;
      /** @enum {string} */
      mediaType:
        | 'application/gzip'
        | 'application/octet-stream'
        | 'application/json'
        | 'text/plain';
      /** @enum {string} */
      os: 'linux' | 'windows' | 'any';
      /** @enum {string} */
      arch: 'amd64' | 'arm64' | 'any';
    };
    OnlineTool: {
      /** @constant */
      delivery: 'online';
      /** @enum {string} */
      protocol: 'web' | 'mcp';
      /** Format: uri */
      url: string;
      /** @enum {string} */
      authentication: 'none' | 'member_oauth' | 'platform_proxy';
    };
    OnlineManifest: {
      /** @constant */
      schemaVersion: 1;
      name: components['schemas']['Name'];
      version: components['schemas']['$defs-Version'];
      description: string;
      author: {
        displayName: string;
      };
      license: string;
      permissions: components['schemas']['Permissions'];
      runtimes: components['schemas']['Runtime'][];
      artifacts: components['schemas']['Artifact'][];
      signature: {
        /** @constant */
        mode: 'detached';
        /** @constant */
        algorithm: 'Ed25519';
      };
      /** @constant */
      kind: 'tool';
      tool: components['schemas']['OnlineTool'];
    };
    RelativePath: string;
    EntryPoint: {
      artifactId: components['schemas']['Name'];
      path: components['schemas']['RelativePath'];
      args: string[];
    };
    OfflineTool: {
      /** @constant */
      delivery: 'offline';
      entrypoints: components['schemas']['EntryPoint'][];
    };
    OfflineManifest: {
      /** @constant */
      schemaVersion: 1;
      name: components['schemas']['Name'];
      version: components['schemas']['$defs-Version'];
      description: string;
      author: {
        displayName: string;
      };
      license: string;
      permissions: components['schemas']['Permissions'];
      runtimes: components['schemas']['Runtime'][];
      artifacts: components['schemas']['Artifact'][];
      signature: {
        /** @constant */
        mode: 'detached';
        /** @constant */
        algorithm: 'Ed25519';
      };
      /** @constant */
      kind: 'tool';
      tool: components['schemas']['OfflineTool'];
    };
    GitTool: {
      /** @constant */
      delivery: 'git';
      /** Format: uri */
      repository: string;
      commit: string;
      sourceArtifactId: components['schemas']['Name'];
      lockArtifactIds: components['schemas']['Name'][];
      /** @enum {string} */
      packageManager: 'npm' | 'pnpm' | 'pip' | 'maven' | 'gradle';
      installScripts: boolean;
      entrypoint: components['schemas']['EntryPoint'];
    };
    GitManifest: {
      /** @constant */
      schemaVersion: 1;
      name: components['schemas']['Name'];
      version: components['schemas']['$defs-Version'];
      description: string;
      author: {
        displayName: string;
      };
      license: string;
      permissions: components['schemas']['Permissions'];
      runtimes: components['schemas']['Runtime'][];
      artifacts: components['schemas']['Artifact'][];
      signature: {
        /** @constant */
        mode: 'detached';
        /** @constant */
        algorithm: 'Ed25519';
      };
      /** @constant */
      kind: 'tool';
      tool: components['schemas']['GitTool'];
    };
    ToolManifest:
      | components['schemas']['OnlineManifest']
      | components['schemas']['OfflineManifest']
      | components['schemas']['GitManifest'];
  };
  responses: never;
  parameters: {
    Limit: number;
    /** @description 上一页响应里的 nextCursor。 */
    Cursor: string;
    IdempotencyKey: string;
    /** @description 资源的 ETag；不匹配返回 412。 */
    IfMatch: string;
  };
  requestBodies: never;
  headers: {
    /** @description 资源版本。 */
    ETag: string;
  };
  pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
  listMarketAdminReview: {
    parameters: {
      query?: {
        limit?: components['parameters']['Limit'];
        /** @description 上一页响应里的 nextCursor。 */
        cursor?: components['parameters']['Cursor'];
      };
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['VersionPage'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  listMarketAdminPublished: {
    parameters: {
      query?: {
        limit?: components['parameters']['Limit'];
        /** @description 上一页响应里的 nextCursor。 */
        cursor?: components['parameters']['Cursor'];
      };
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['VersionPage'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  listResourcePackages: {
    parameters: {
      query?: {
        limit?: components['parameters']['Limit'];
        /** @description 上一页响应里的 nextCursor。 */
        cursor?: components['parameters']['Cursor'];
      };
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['PackagePage'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  createResourcePackage: {
    parameters: {
      query?: never;
      header?: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['PackageInput'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      201: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Package'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  getResourcePackage: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        packageId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Package'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  patchResourcePackage: {
    parameters: {
      query?: never;
      header: {
        /** @description 当前资源ETag；缺失/不匹配返回common.precondition_failed。 */
        'If-Match': string;
      };
      path: {
        packageId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['PackagePatch'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Package'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.level_locked */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.state_conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  patchResourcePackageLevel: {
    parameters: {
      query?: never;
      header: {
        /** @description 当前资源ETag；缺失/不匹配返回common.precondition_failed。 */
        'If-Match': string;
      };
      path: {
        packageId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['AdminPackagePatch'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Package'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.level_locked */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.state_conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  listResourceVersions: {
    parameters: {
      query?: {
        limit?: components['parameters']['Limit'];
        /** @description 上一页响应里的 nextCursor。 */
        cursor?: components['parameters']['Cursor'];
      };
      header?: never;
      path: {
        packageId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['VersionPage'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  uploadResourceVersion: {
    parameters: {
      query?: never;
      header: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
        /** @description Exact current strong ETag; absent or stale values return 412. */
        'If-Match': string;
      };
      path: {
        packageId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'multipart/form-data': {
          metadata: components['schemas']['UploadMetadata'];
          /** Format: binary */
          archive: string;
        };
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      201: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Version'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.level_locked, resource.download_denied */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.version_immutable, resource.review_required, resource.state_conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.digest_mismatch, resource.signature_invalid */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.storage_unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  getResourceVersion: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        packageId: string;
        versionId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Version'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  submitResourceVersion: {
    parameters: {
      query?: never;
      header: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
        /** @description 当前资源ETag；缺失/不匹配返回common.precondition_failed。 */
        'If-Match': string;
      };
      path: {
        packageId: string;
        versionId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['Reason'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      201: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Review'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.state_conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  decideResourceReview: {
    parameters: {
      query?: never;
      header: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
        /** @description 当前资源ETag；缺失/不匹配返回common.precondition_failed。 */
        'If-Match': string;
      };
      path: {
        reviewId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['ReviewDecision'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Review'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.state_conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  publishResourceVersion: {
    parameters: {
      query?: never;
      header: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
        /** @description 当前资源ETag；缺失/不匹配返回common.precondition_failed。 */
        'If-Match': string;
      };
      path: {
        packageId: string;
        versionId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['Reason'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      201: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Version'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.level_locked, resource.download_denied */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.version_immutable, resource.review_required, resource.state_conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.digest_mismatch, resource.signature_invalid */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.storage_unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  withdrawResourceVersion: {
    parameters: {
      query?: never;
      header: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
        /** @description 当前资源ETag；缺失/不匹配返回common.precondition_failed。 */
        'If-Match': string;
      };
      path: {
        packageId: string;
        versionId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['Withdrawal'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Version'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.level_locked, resource.download_denied */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.version_immutable, resource.review_required, resource.state_conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.digest_mismatch, resource.signature_invalid */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.storage_unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  requestResourceDistribution: {
    parameters: {
      query?: never;
      header?: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['DistributionInput'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      202: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Distribution'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.level_locked, resource.download_denied */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.version_immutable, resource.review_required, resource.state_conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.digest_mismatch, resource.signature_invalid */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.storage_unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  getResourceDistribution: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        distributionId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Distribution'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  downloadResourceContent: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        packageId: string;
        versionId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已授权的不可变归档，校验摘要/长度；不接受重定向。 */
      200: {
        headers: {
          /** @description 固定摘要 */
          ETag?: string;
          /** @description 完整对象长度 */
          'Content-Length'?: number;
          [name: string]: unknown;
        };
        content: {
          'application/gzip': string;
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.level_locked, resource.download_denied */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.version_immutable, resource.review_required, resource.state_conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.digest_mismatch, resource.signature_invalid */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.storage_unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  downloadNodeResource: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        downloadId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已授权的不可变归档，校验摘要/长度；不接受重定向。 */
      200: {
        headers: {
          /** @description 固定摘要 */
          ETag?: string;
          /** @description 完整对象长度 */
          'Content-Length'?: number;
          [name: string]: unknown;
        };
        content: {
          'application/gzip': string;
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.level_locked, resource.download_denied */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.version_immutable, resource.review_required, resource.state_conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.digest_mismatch, resource.signature_invalid */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.storage_unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  listResourceReviews: {
    parameters: {
      query?: {
        limit?: components['parameters']['Limit'];
        /** @description 上一页响应里的 nextCursor。 */
        cursor?: components['parameters']['Cursor'];
      };
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ReviewPage'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  getResourceReview: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        reviewId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Review'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  issueResourceDowngrade: {
    parameters: {
      query?: never;
      header?: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
      };
      path: {
        packageId: string;
        versionId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['DowngradeInput'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      201: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['DowngradeAuthorization'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.level_locked */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.state_conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.signature_invalid */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  resetMarketAdminDraft: {
    parameters: {
      query?: never;
      header: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
        /** @description Exact current strong ETag; absent or stale values return 412. */
        'If-Match': string;
      };
      path: {
        packageId: string;
        versionId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['Reason'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Version'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  signMarketAdminPersonal: {
    parameters: {
      query?: never;
      header: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
        /** @description Exact current strong ETag; absent or stale values return 412. */
        'If-Match': string;
      };
      path: {
        packageId: string;
        versionId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['Reason'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      201: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Version'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  getMarketAdminReviewDetail: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        packageId: string;
        versionId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['MarketAdminDetail'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  getMarketAdminPublishedDetail: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        packageId: string;
        versionId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['MarketAdminDetail'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  listMarketAdminDistributions: {
    parameters: {
      query?: {
        limit?: components['parameters']['Limit'];
        /** @description 上一页响应里的 nextCursor。 */
        cursor?: components['parameters']['Cursor'];
      };
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['MarketAdminDistributionPage'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  renewMarketAdminNodeDownload: {
    parameters: {
      query?: never;
      header?: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
      };
      path: {
        downloadId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description Fresh online authorization */
      201: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['DownloadAuthorization'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.level_locked, resource.download_denied */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.version_immutable, resource.review_required, resource.state_conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.digest_mismatch, resource.signature_invalid */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.storage_unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  getResourceRevocations: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description Signed tenant revocations */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['RevocationList'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  createMemberResourceDownloadTicket: {
    parameters: {
      query?: never;
      header?: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['MemberDownloadTicketInput'];
      };
    };
    responses: {
      /** @description Success. Cache-Control: no-store. */
      201: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['MemberDownloadTicket'];
        };
      };
      /** @description Request rejected. */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  consumeMemberResourceDownloadTicket: {
    parameters: {
      query?: never;
      header?: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['MemberDownloadInput'];
      };
    };
    responses: {
      /** @description Original verified archive bytes. Cache-Control: no-store. */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/gzip': string;
        };
      };
      /** @description Request rejected. */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  listMemberResourceInstallations: {
    parameters: {
      query: {
        clientId: string;
        afterPackageId?: string;
        limit?: components['parameters']['Limit'];
      };
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description Success. Cache-Control: no-store. */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['MemberInstallationPage'];
        };
      };
      /** @description Request rejected. */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  selectMemberResourceInstallation: {
    parameters: {
      query?: never;
      header?: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
        /** @description 资源的 ETag；不匹配返回 412。 */
        'If-Match'?: components['parameters']['IfMatch'];
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['MemberInstallationInput'];
      };
    };
    responses: {
      /** @description Success. Cache-Control: no-store. */
      202: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['MemberInstallation'];
        };
      };
      /** @description Request rejected. */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  reportMemberResourceInstallation: {
    parameters: {
      query?: never;
      header?: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['MemberInstallationReport'];
      };
    };
    responses: {
      /** @description Success. Cache-Control: no-store. */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['MemberInstallation'];
        };
      };
      /** @description Request rejected. */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description Request rejected. */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  getDesktopLatest: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description Success */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['DesktopManifest'];
        };
      };
    };
  };
  downloadDesktopPackage: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional single open-ended byte range for interrupted downloads, e.g. bytes=1048576-. */
        Range?: string;
      };
      path: {
        sequence: number;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description Success */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/octet-stream': string;
        };
      };
      /** @description Partial package bytes. Full SHA-256 must still be verified after resuming. */
      206: {
        headers: {
          /** @description Returned byte interval and total size. */
          'Content-Range'?: string;
          /** @description bytes */
          'Accept-Ranges'?: string;
          [name: string]: unknown;
        };
        content: {
          'application/octet-stream': string;
        };
      };
      /** @description Unsupported or unsatisfiable range. */
      416: {
        headers: {
          /** @description bytes *\/<total size> */
          'Content-Range'?: string;
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  listDesktopReleases: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description Success */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['DesktopReleaseLedger'];
        };
      };
    };
  };
  publishDesktopRelease: {
    parameters: {
      query?: never;
      header: {
        'X-Desktop-Manifest': string;
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/octet-stream': string;
      };
    };
    responses: {
      /** @description Success */
      201: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['DesktopManifest'];
        };
      };
    };
  };
  withdrawDesktopRelease: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        sequence: number;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description Success */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  UnifiedListResourcePackages: {
    parameters: {
      query?: {
        limit?: components['parameters']['Limit'];
        /** @description 上一页响应里的 nextCursor。 */
        cursor?: components['parameters']['Cursor'];
        kind?: 'skill' | 'tool';
      };
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UnifiedPackagePage'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedCreateResourcePackage: {
    parameters: {
      query?: never;
      header: {
        /** @description Required idempotency key bound to the complete request fingerprint. */
        'Idempotency-Key': string;
        /** @description Current resource ETag; initial creation uses "0". Stale or absent preconditions reject the mutation. */
        'If-Match': string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['UnifiedPackageInput'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      201: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UnifiedPackage'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedGetResourcePackage: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        packageId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UnifiedPackage'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedListResourceVersions: {
    parameters: {
      query?: {
        limit?: components['parameters']['Limit'];
        /** @description 上一页响应里的 nextCursor。 */
        cursor?: components['parameters']['Cursor'];
      };
      header?: never;
      path: {
        packageId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UnifiedVersionPage'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedUploadResourceVersion: {
    parameters: {
      query?: never;
      header: {
        /** @description Required idempotency key bound to the complete request fingerprint. */
        'Idempotency-Key': string;
        /** @description Current resource ETag; initial creation uses "0". Stale or absent preconditions reject the mutation. */
        'If-Match': string;
      };
      path: {
        packageId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'multipart/form-data': {
          metadata: components['schemas']['UnifiedUploadMetadata'];
          /** Format: binary */
          manifest: string;
          artifacts: string[];
        };
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      201: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UnifiedVersion'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.level_locked, resource.download_denied */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.version_immutable, resource.review_required, resource.state_conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.digest_mismatch, resource.signature_invalid */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.storage_unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedGetResourceVersion: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        packageId: string;
        versionId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UnifiedVersion'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedSubmitResourceVersion: {
    parameters: {
      query?: never;
      header: {
        /** @description Required idempotency key bound to the complete request fingerprint. */
        'Idempotency-Key': string;
        /** @description Current resource ETag; initial creation uses "0". Stale or absent preconditions reject the mutation. */
        'If-Match': string;
      };
      path: {
        packageId: string;
        versionId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['Reason'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      201: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UnifiedReview'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.state_conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedListMarketAdminReview: {
    parameters: {
      query?: {
        limit?: components['parameters']['Limit'];
        /** @description 上一页响应里的 nextCursor。 */
        cursor?: components['parameters']['Cursor'];
        kind?: 'skill' | 'tool';
      };
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UnifiedReviewPage'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedListMarketAdminPublished: {
    parameters: {
      query?: {
        limit?: components['parameters']['Limit'];
        /** @description 上一页响应里的 nextCursor。 */
        cursor?: components['parameters']['Cursor'];
        kind?: 'skill' | 'tool';
      };
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UnifiedVersionPage'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedDecideResourceReview: {
    parameters: {
      query?: never;
      header: {
        /** @description Required idempotency key bound to the complete request fingerprint. */
        'Idempotency-Key': string;
        /** @description Current resource ETag; initial creation uses "0". Stale or absent preconditions reject the mutation. */
        'If-Match': string;
      };
      path: {
        reviewId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['UnifiedReviewDecision'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UnifiedReview'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.state_conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedPublishResourceVersion: {
    parameters: {
      query?: never;
      header: {
        /** @description Required idempotency key bound to the complete request fingerprint. */
        'Idempotency-Key': string;
        /** @description Current resource ETag; initial creation uses "0". Stale or absent preconditions reject the mutation. */
        'If-Match': string;
      };
      path: {
        packageId: string;
        versionId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['Reason'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      201: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UnifiedVersion'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.level_locked, resource.download_denied */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.version_immutable, resource.review_required, resource.state_conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.digest_mismatch, resource.signature_invalid */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.storage_unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedWithdrawResourceVersion: {
    parameters: {
      query?: never;
      header: {
        /** @description Required idempotency key bound to the complete request fingerprint. */
        'Idempotency-Key': string;
        /** @description Current resource ETag; initial creation uses "0". Stale or absent preconditions reject the mutation. */
        'If-Match': string;
      };
      path: {
        packageId: string;
        versionId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['Withdrawal'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UnifiedVersion'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.level_locked, resource.download_denied */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.version_immutable, resource.review_required, resource.state_conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.digest_mismatch, resource.signature_invalid */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.storage_unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedDownloadResourceManifest: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        packageId: string;
        versionId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description Exact manifest bytes; X-Auriga-Manifest-Digest equals the raw SHA-256 version digest, but is not itself a signature. */
      200: {
        headers: {
          'X-Auriga-Manifest-Digest'?: components['schemas']['Digest'];
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/octet-stream': string;
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedGetResourceReview: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        reviewId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UnifiedReview'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedCreateDownloadTicket: {
    parameters: {
      query?: never;
      header: {
        /** @description Required idempotency key bound to the complete request fingerprint. */
        'Idempotency-Key': string;
        /** @description Current resource ETag; initial creation uses "0". Stale or absent preconditions reject the mutation. */
        'If-Match': string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['UnifiedDownloadTicketInput'];
      };
    };
    responses: {
      /** @description Authorized response */
      200: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UnifiedDownloadTicket'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedDownloadArtifact: {
    parameters: {
      query?: never;
      header: {
        /** @description Required idempotency key bound to the complete request fingerprint. */
        'Idempotency-Key': string;
        /** @description Current resource ETag; initial creation uses "0". Stale or absent preconditions reject the mutation. */
        'If-Match': string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['UnifiedDownloadInput'];
      };
    };
    responses: {
      /** @description Authorized response */
      200: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/octet-stream': string;
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedListInstallations: {
    parameters: {
      query: {
        kind?: 'skill' | 'tool';
        limit?: number;
        cursor?: string;
        clientId: string;
      };
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description Authorized response */
      200: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UnifiedInstallationPage'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedSelectInstallation: {
    parameters: {
      query?: never;
      header: {
        /** @description Required idempotency key bound to the complete request fingerprint. */
        'Idempotency-Key': string;
        /** @description Current resource ETag; initial creation uses "0". Stale or absent preconditions reject the mutation. */
        'If-Match': string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['UnifiedInstallationInput'];
      };
    };
    responses: {
      /** @description Exact revision accepted; 202 requires matching authenticated observation before applied. */
      202: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UnifiedInstallation'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedReportInstallation: {
    parameters: {
      query?: never;
      header: {
        /** @description Required idempotency key bound to the complete request fingerprint. */
        'Idempotency-Key': string;
        /** @description Current resource ETag; initial creation uses "0". Stale or absent preconditions reject the mutation. */
        'If-Match': string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['UnifiedMemberInstallationReport'];
      };
    };
    responses: {
      /** @description Authorized response */
      200: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UnifiedInstallation'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedRenewInstallation: {
    parameters: {
      query?: never;
      header: {
        /** @description Required idempotency key bound to the complete request fingerprint. */
        'Idempotency-Key': string;
        /** @description Current resource ETag; initial creation uses "0". Stale or absent preconditions reject the mutation. */
        'If-Match': string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['UnifiedRenewalInput'];
      };
    };
    responses: {
      /** @description Authorized response */
      200: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UnifiedDownloadTicket'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedListDistributions: {
    parameters: {
      query?: {
        kind?: 'skill' | 'tool';
        limit?: number;
        cursor?: string;
        instanceId?: string;
      };
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description Authorized response */
      200: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UnifiedDistributionPage'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden or iam.step_up_required */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedCreateDistribution: {
    parameters: {
      query?: never;
      header: {
        /** @description Required idempotency key bound to the complete request fingerprint. */
        'Idempotency-Key': string;
        /** @description Current resource ETag; initial creation uses "0". Stale or absent preconditions reject the mutation. */
        'If-Match': string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['UnifiedDistributionInput'];
      };
    };
    responses: {
      /** @description Exact revision accepted; 202 requires matching authenticated observation before applied. */
      202: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UnifiedDistribution'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden or iam.step_up_required */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedGetDistribution: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        distributionId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description Authorized response */
      200: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UnifiedDistribution'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden or iam.step_up_required */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedDownloadVersionArtifact: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        packageId: string;
        versionId: string;
        artifactId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description Authorized response */
      200: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          'application/octet-stream': string;
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedDownloadNodeManifest: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        downloadId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已授权的不可变归档，校验摘要/长度；不接受重定向。 */
      200: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          /** @description 完整对象长度 */
          'Content-Length'?: number;
          [name: string]: unknown;
        };
        content: {
          'application/octet-stream': string;
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.level_locked, resource.download_denied */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.version_immutable, resource.review_required, resource.state_conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.digest_mismatch, resource.signature_invalid */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.storage_unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  UnifiedDownloadNodeArtifact: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        downloadId: string;
        artifactId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已授权的不可变归档，校验摘要/长度；不接受重定向。 */
      200: {
        headers: {
          /** @description Current revision ETag for the next conditional request. */
          ETag?: string;
          /** @description 完整对象长度 */
          'Content-Length'?: number;
          [name: string]: unknown;
        };
        content: {
          'application/octet-stream': string;
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.level_locked, resource.download_denied */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.version_immutable, resource.review_required, resource.state_conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.digest_mismatch, resource.signature_invalid */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description resource.storage_unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
}
