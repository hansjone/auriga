package auriga.workspace.app;

import auriga.common.context.TenantContext;
import auriga.common.grpc.GrpcErrors;
import auriga.contracts.org.v1.*;
import auriga.org.web.model.WorkspacePolicy;
import auriga.workspace.api.WorkspaceInputs;
import io.grpc.Channel;
import java.util.UUID;

/**
 * Split deployment adapter, using generated org messages and the common service-token interceptor.
 */
public final class RemoteWorkspaceInputs implements WorkspaceInputs {
  private final Channel channel;

  public RemoteWorkspaceInputs(Channel channel) {
    this.channel = channel;
  }

  @Override
  public Member member(UUID tenant, UUID owner) {
    return TenantContext.call(
        tenant,
        () ->
            GrpcErrors.call(
                    () ->
                        MemberServiceGrpc.newBlockingStub(channel)
                            .getMember(
                                GetMemberRequest.newBuilder()
                                    .setTenantId(tenant.toString())
                                    .setAccountId(owner.toString())
                                    .build()))
                .getMember());
  }

  @Override
  public InstanceQuota quota(UUID tenant, UUID owner) {
    return TenantContext.call(
        tenant,
        () ->
            GrpcErrors.call(
                    () ->
                        QuotaServiceGrpc.newBlockingStub(channel)
                            .getEffectiveQuota(
                                GetEffectiveQuotaRequest.newBuilder()
                                    .setTenantId(tenant.toString())
                                    .setAccountId(owner.toString())
                                    .build()))
                .getQuota()
                .getInstance());
  }

  @Override
  public WorkspacePolicy policy(UUID tenant) {
    return TenantContext.call(
        tenant,
        () -> {
          var value =
              GrpcErrors.call(
                      () ->
                          PolicyServiceGrpc.newBlockingStub(channel)
                              .getPolicy(
                                  GetPolicyRequest.newBuilder()
                                      .setTenantId(tenant.toString())
                                      .setKey("workspace")
                                      .build()))
                  .getValue()
                  .getFieldsMap();
          var policy = new WorkspacePolicy();
          if (value.containsKey("idleMinutes"))
            policy.setIdleMinutes((int) value.get("idleMinutes").getNumberValue());
          if (value.containsKey("maxConcurrentStarts"))
            policy.setMaxConcurrentStarts((int) value.get("maxConcurrentStarts").getNumberValue());
          if (value.containsKey("wakeOnSchedule"))
            policy.setWakeOnSchedule(value.get("wakeOnSchedule").getBoolValue());
          if (value.containsKey("alwaysOnRequiresApproval"))
            policy.setAlwaysOnRequiresApproval(
                value.get("alwaysOnRequiresApproval").getBoolValue());
          if (value.containsKey("defaultDshVersion"))
            policy.setDefaultDshVersion(value.get("defaultDshVersion").getStringValue());
          if (value.containsKey("allowPersonalPlugins"))
            policy.setAllowPersonalPlugins(value.get("allowPersonalPlugins").getBoolValue());
          return policy;
        });
  }
}
