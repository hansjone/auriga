package io.github.hansjone.auriga.org;

import com.google.protobuf.ListValue;
import com.google.protobuf.NullValue;
import com.google.protobuf.Struct;
import com.google.protobuf.Timestamp;
import com.google.protobuf.Value;
import io.github.hansjone.auriga.common.context.TenantContext;
import io.github.hansjone.auriga.common.security.AuthenticatedOnly;
import io.github.hansjone.auriga.contracts.org.v1.*;
import io.grpc.stub.StreamObserver;
import java.time.Instant;
import java.util.List;
import java.util.UUID;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** gRPC adapters for internal callers (iam/edge/workspace). */
public final class OrgGrpcServices {
  private OrgGrpcServices() {}

  public static class TenantGrpc extends TenantServiceGrpc.TenantServiceImplBase {
    private final OrgService org;

    public TenantGrpc(OrgService org) {
      this.org = org;
    }

    @Override
    @AuthenticatedOnly
    public void getTenant(GetTenantRequest request, StreamObserver<GetTenantResponse> observer) {
      UUID tid = UUID.fromString(request.getTenantId());
      OrgStore.TenantRow row =
          TenantContext.call(tid, () -> org.store().tx(() -> org.store().tenantById(tid)))
              .orElseThrow(() -> OrgError.error("not_found", 404));
      observer.onNext(GetTenantResponse.newBuilder().setTenant(toProto(row)).build());
      observer.onCompleted();
    }

    @Override
    @AuthenticatedOnly
    public void resolveTenantByHost(
        ResolveTenantByHostRequest request,
        StreamObserver<ResolveTenantByHostResponse> observer) {
      OrgStore.TenantRow row = org.resolveTenantByHost(request.getHost());
      observer.onNext(ResolveTenantByHostResponse.newBuilder().setTenant(toProto(row)).build());
      observer.onCompleted();
    }
  }

  public static class MemberGrpc extends MemberServiceGrpc.MemberServiceImplBase {
    private final OrgService org;

    public MemberGrpc(OrgService org) {
      this.org = org;
    }

    @Override
    @AuthenticatedOnly
    public void getMember(GetMemberRequest request, StreamObserver<GetMemberResponse> observer) {
      UUID tid = UUID.fromString(request.getTenantId());
      UUID aid = UUID.fromString(request.getAccountId());
      Member m =
          TenantContext.call(
              tid,
              () -> {
                ObjectNode json = org.memberJson(aid);
                return toMemberProto(json, tid);
              });
      observer.onNext(GetMemberResponse.newBuilder().setMember(m).build());
      observer.onCompleted();
    }

    @Override
    @AuthenticatedOnly
    public void batchGetMembers(
        BatchGetMembersRequest request, StreamObserver<BatchGetMembersResponse> observer) {
      UUID tid = UUID.fromString(request.getTenantId());
      List<Member> members =
          TenantContext.call(
              tid,
              () ->
                  request.getAccountIdsList().stream()
                      .limit(500)
                      .map(
                          id -> {
                            try {
                              return toMemberProto(org.memberJson(UUID.fromString(id)), tid);
                            } catch (RuntimeException e) {
                              return null;
                            }
                          })
                      .filter(m -> m != null)
                      .toList());
      observer.onNext(BatchGetMembersResponse.newBuilder().addAllMembers(members).build());
      observer.onCompleted();
    }
  }

  public static class AuthorizationGrpc extends AuthorizationServiceGrpc.AuthorizationServiceImplBase {
    private final OrgService org;

    public AuthorizationGrpc(OrgService org) {
      this.org = org;
    }

    @Override
    @AuthenticatedOnly
    public void getEffectivePermissions(
        GetEffectivePermissionsRequest request,
        StreamObserver<GetEffectivePermissionsResponse> observer) {
      UUID tid = UUID.fromString(request.getTenantId());
      UUID aid = UUID.fromString(request.getAccountId());
      var eff = TenantContext.call(tid, () -> org.getEffectivePermissions(aid));
      observer.onNext(
          GetEffectivePermissionsResponse.newBuilder()
              .setMemberType(memberType(eff.memberType()))
              .setStatus(memberStatus(eff.status()))
              .addAllRoleKeys(eff.roleKeys())
              .addAllPermissions(eff.permissions())
              .build());
      observer.onCompleted();
    }

    @Override
    @AuthenticatedOnly
    public void checkGrant(
        CheckGrantRequest request, StreamObserver<CheckGrantResponse> observer) {
      UUID tid = UUID.fromString(request.getTenantId());
      var result =
          TenantContext.call(
              tid,
              () ->
                  org.checkGrant(
                      UUID.fromString(request.getGrantorId()),
                      request.getRoleIdsList().stream().map(UUID::fromString).toList(),
                      request.getGroupId().isBlank()
                          ? null
                          : UUID.fromString(request.getGroupId())));
      observer.onNext(
          CheckGrantResponse.newBuilder()
              .setAllowed(result.allowed())
              .addAllExceedingPermissions(result.exceeding())
              .build());
      observer.onCompleted();
    }

    @Override
    @AuthenticatedOnly
    public void checkPermission(
        CheckPermissionRequest request, StreamObserver<CheckPermissionResponse> observer) {
      UUID tid = UUID.fromString(request.getTenantId());
      boolean allowed =
          TenantContext.call(
              tid,
              () ->
                  org.checkPermission(
                      UUID.fromString(request.getAccountId()), request.getPermission()));
      CheckPermissionResponse.Builder b = CheckPermissionResponse.newBuilder().setAllowed(allowed);
      if (!allowed) b.setReason("common.forbidden");
      observer.onNext(b.build());
      observer.onCompleted();
    }
  }

  public static class InvitationGrpc extends InvitationServiceGrpc.InvitationServiceImplBase {
    private final OrgService org;

    public InvitationGrpc(OrgService org) {
      this.org = org;
    }

    @Override
    @AuthenticatedOnly
    public void resolveInvitation(
        ResolveInvitationRequest request, StreamObserver<ResolveInvitationResponse> observer) {
      var r = org.resolveInvitation(request.getCode());
      ResolveInvitationResponse.Builder b = ResolveInvitationResponse.newBuilder();
      if (r.invitation() == null) b.setReason(r.reason() == null ? "not_found" : r.reason());
      else b.setInvitation(toInvitationProto(r.invitation()));
      observer.onNext(b.build());
      observer.onCompleted();
    }

    @Override
    @AuthenticatedOnly
    public void redeemInvitation(
        RedeemInvitationRequest request, StreamObserver<RedeemInvitationResponse> observer) {
      ObjectNode member =
          org.redeemInvitation(
              request.getCode(),
              UUID.fromString(request.getAccountId()),
              request.getDisplayName(),
              request.getEmail(),
              request.getProviderId().isBlank()
                  ? null
                  : UUID.fromString(request.getProviderId()));
      UUID tid =
          TenantContext.current()
              .orElseGet(
                  () ->
                      org.store()
                          .defaultTenant()
                          .map(OrgStore.TenantRow::id)
                          .orElse(OrgIds.DEFAULT_TENANT));
      observer.onNext(
          RedeemInvitationResponse.newBuilder().setMember(toMemberProto(member, tid)).build());
      observer.onCompleted();
    }
  }

  public static class QuotaGrpc extends QuotaServiceGrpc.QuotaServiceImplBase {
    private final OrgService org;

    public QuotaGrpc(OrgService org) {
      this.org = org;
    }

    @Override
    @AuthenticatedOnly
    public void getEffectiveQuota(
        GetEffectiveQuotaRequest request, StreamObserver<GetEffectiveQuotaResponse> observer) {
      UUID tid = UUID.fromString(request.getTenantId());
      ObjectNode q =
          TenantContext.call(
              tid, () -> org.getEffectiveQuota(UUID.fromString(request.getAccountId())));
      observer.onNext(
          GetEffectiveQuotaResponse.newBuilder().setQuota(toQuotaProto(q)).build());
      observer.onCompleted();
    }
  }

  public static class PolicyGrpc extends PolicyServiceGrpc.PolicyServiceImplBase {
    private final OrgService org;

    public PolicyGrpc(OrgService org) {
      this.org = org;
    }

    @Override
    @AuthenticatedOnly
    public void getPolicy(GetPolicyRequest request, StreamObserver<GetPolicyResponse> observer) {
      UUID tid = UUID.fromString(request.getTenantId());
      ObjectNode p = TenantContext.call(tid, () -> org.getPolicy(request.getKey()));
      observer.onNext(
          GetPolicyResponse.newBuilder()
              .setKey(p.path("key").asString())
              .setVersion(p.path("version").asLong())
              .setValue(toStruct(p.path("value")))
              .build());
      observer.onCompleted();
    }
  }

  private static Struct toStruct(JsonNode node) {
    Struct.Builder b = Struct.newBuilder();
    if (node != null && node.isObject())
      node.properties().forEach(e -> b.putFields(e.getKey(), toValue(e.getValue())));
    return b.build();
  }

  private static Value toValue(JsonNode n) {
    if (n == null || n.isNull())
      return Value.newBuilder().setNullValue(NullValue.NULL_VALUE).build();
    if (n.isBoolean()) return Value.newBuilder().setBoolValue(n.asBoolean()).build();
    if (n.isNumber()) return Value.newBuilder().setNumberValue(n.asDouble()).build();
    if (n.isTextual()) return Value.newBuilder().setStringValue(n.asString()).build();
    if (n.isArray()) {
      ListValue.Builder list = ListValue.newBuilder();
      n.forEach(c -> list.addValues(toValue(c)));
      return Value.newBuilder().setListValue(list).build();
    }
    if (n.isObject()) return Value.newBuilder().setStructValue(toStruct(n)).build();
    return Value.newBuilder().setNullValue(NullValue.NULL_VALUE).build();
  }

  private static Tenant toProto(OrgStore.TenantRow row) {
    return Tenant.newBuilder()
        .setTenantId(row.id().toString())
        .setSlug(row.slug())
        .setDisplayName(row.displayName())
        .setLocale(row.locale())
        .addAllHosts(row.hosts())
        .build();
  }

  private static Member toMemberProto(ObjectNode json, UUID tenantId) {
    Member.Builder b =
        Member.newBuilder()
            .setAccountId(json.path("id").asString())
            .setTenantId(tenantId.toString())
            .setDisplayName(json.path("displayName").asString())
            .setMemberType(memberType(json.path("memberType").asString()))
            .setStatus(memberStatus(json.path("status").asString()))
            .addAllGroupIds(
                OrgJson.MAPPER.convertValue(json.path("groupIds"), List.class))
            .addAllRoleKeys(
                OrgJson.MAPPER.convertValue(json.path("roleKeys"), List.class))
            .setCreatedAt(ts(Instant.parse(json.path("createdAt").asString())));
    if (json.hasNonNull("email")) b.setEmail(json.path("email").asString());
    return b.build();
  }

  private static Invitation toInvitationProto(ObjectNode json) {
    Invitation.Builder b =
        Invitation.newBuilder()
            .setInvitationId(json.path("id").asString())
            .setTenantId(TenantContext.require().toString())
            .setMemberType(memberType(json.path("memberType").asString()))
            .addAllRoleIds(OrgJson.MAPPER.convertValue(json.path("roleIds"), List.class))
            .addAllGroupIds(OrgJson.MAPPER.convertValue(json.path("groupIds"), List.class))
            .addAllProviderIds(OrgJson.MAPPER.convertValue(json.path("providerIds"), List.class))
            .setRemainingUses(
                Math.max(0, json.path("maxUses").asInt() - json.path("uses").asInt()))
            .setExpiresAt(ts(Instant.parse(json.path("expiresAt").asString())));
    if (json.hasNonNull("email")) b.setEmail(json.path("email").asString());
    if (json.hasNonNull("displayName")) b.setDisplayName(json.path("displayName").asString());
    return b.build();
  }

  private static Quota toQuotaProto(ObjectNode q) {
    Quota.Builder b = Quota.newBuilder();
    if (q.has("instance")) {
      ObjectNode i = (ObjectNode) q.path("instance");
      InstanceQuota.Builder ib = InstanceQuota.newBuilder();
      if (i.has("cpuMillis") && !i.path("cpuMillis").isNull())
        ib.setCpuMillis(i.path("cpuMillis").asLong());
      if (i.has("memoryMb") && !i.path("memoryMb").isNull())
        ib.setMemoryMb(i.path("memoryMb").asLong());
      if (i.has("diskGb") && !i.path("diskGb").isNull()) ib.setDiskGb(i.path("diskGb").asLong());
      if (i.has("maxInstances") && !i.path("maxInstances").isNull())
        ib.setMaxInstances(i.path("maxInstances").asLong());
      if (i.has("alwaysOnAllowed")) ib.setAlwaysOnAllowed(i.path("alwaysOnAllowed").asBoolean());
      b.setInstance(ib);
    }
    if (q.has("model")) {
      ObjectNode m = (ObjectNode) q.path("model");
      ModelQuota.Builder mb = ModelQuota.newBuilder();
      if (m.has("monthlyBudgetMicros") && !m.path("monthlyBudgetMicros").isNull())
        mb.setMonthlyBudgetMicros(m.path("monthlyBudgetMicros").asLong());
      if (m.hasNonNull("currency")) mb.setCurrency(m.path("currency").asString());
      if (m.has("dailyTokens") && !m.path("dailyTokens").isNull())
        mb.setDailyTokens(m.path("dailyTokens").asLong());
      b.setModel(mb);
    }
    return b.build();
  }

  private static MemberType memberType(String v) {
    return switch (v) {
      case "external" -> MemberType.MEMBER_TYPE_EXTERNAL;
      default -> MemberType.MEMBER_TYPE_MEMBER;
    };
  }

  private static MemberStatus memberStatus(String v) {
    return switch (v) {
      case "disabled" -> MemberStatus.MEMBER_STATUS_DISABLED;
      case "offboarded" -> MemberStatus.MEMBER_STATUS_OFFBOARDED;
      default -> MemberStatus.MEMBER_STATUS_ACTIVE;
    };
  }

  private static Timestamp ts(Instant i) {
    return Timestamp.newBuilder().setSeconds(i.getEpochSecond()).setNanos(i.getNano()).build();
  }
}
