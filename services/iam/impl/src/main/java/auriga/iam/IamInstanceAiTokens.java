package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.AuthenticatedOnly;
import auriga.common.security.AuthorizationRules;
import auriga.contracts.iam.v1.*;
import auriga.contracts.org.v1.*;
import auriga.iam.api.AccountDirectory;
import auriga.iam.api.InstanceAiAuthorization;
import com.google.protobuf.Timestamp;
import io.grpc.stub.StreamObserver;
import java.util.Set;
import java.util.UUID;

/** Workspace supplies its authoritative binding over the authenticated internal transport. */
public class IamInstanceAiTokens extends InstanceAiTokenServiceGrpc.InstanceAiTokenServiceImplBase {
  private final TokenService tokens;
  private final AccountDirectory accounts;
  private final InstanceAiAuthorization authorization;

  public IamInstanceAiTokens(
      TokenService tokens, AccountDirectory accounts, InstanceAiAuthorization authorization) {
    this.tokens = tokens;
    this.accounts = accounts;
    this.authorization = authorization;
  }

  @Override
  @AuthenticatedOnly
  public void issueInstanceAiToken(
      IssueInstanceAiTokenRequest request, StreamObserver<IssueInstanceAiTokenResponse> observer) {
    var caller =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    if (!caller.isInternalService()
        || !"svc:workspace".equals(caller.subject())
        || !TenantContext.PLATFORM_TENANT.equals(caller.tenantId()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    UUID tenant = uuid(request.getTenantId());
    UUID owner = uuid(request.getOwnerId());
    UUID instance = uuid(request.getInstanceId());
    IamError.validate(
        !TenantContext.PLATFORM_TENANT.equals(tenant)
            && request.getCredentialVersion() >= 1
            && request.getCredentialVersion() <= 9007199254740991L);
    if (!TenantContext.current().filter(tenant::equals).isPresent())
      throw new AurigaException(CommonErrors.FORBIDDEN);

    GetEffectivePermissionsResponse effective;
    try {
      GetAccountResponse response =
          accounts.get(
              GetAccountRequest.newBuilder()
                  .setTenantId(tenant.toString())
                  .setAccountId(owner.toString())
                  .build());
      if (response == null || !response.hasAccount())
        throw new AurigaException(CommonErrors.NOT_FOUND);
      Account account = response.getAccount();
      if (!tenant.toString().equals(account.getTenantId())
          || !owner.toString().equals(account.getAccountId())
          || account.getStatus() != AccountStatus.ACCOUNT_STATUS_ACTIVE)
        throw new AurigaException(CommonErrors.FORBIDDEN);
      effective =
          authorization.getEffectivePermissions(
              GetEffectivePermissionsRequest.newBuilder()
                  .setTenantId(tenant.toString())
                  .setAccountId(owner.toString())
                  .build());
    } catch (AurigaException e) {
      if (Set.of("common.not_found", "common.forbidden", "common.unavailable")
          .contains(e.errorCode().code())) throw e;
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    } catch (RuntimeException e) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
    if (effective == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    if (effective.getStatus() != MemberStatus.MEMBER_STATUS_ACTIVE
        || (effective.getMemberType() != MemberType.MEMBER_TYPE_MEMBER
            && effective.getMemberType() != MemberType.MEMBER_TYPE_EXTERNAL))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    TokenService.IssuedToken issued;
    try {
      issued =
          tokens.instanceAi(
              tenant,
              owner,
              instance,
              request.getCredentialVersion(),
              Set.copyOf(effective.getPermissionsList()));
    } catch (AurigaException e) {
      if (e.errorCode().code().equals("common.forbidden")) throw e;
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    } catch (RuntimeException e) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
    observer.onNext(
        IssueInstanceAiTokenResponse.newBuilder()
            .setAccessToken(issued.accessToken())
            .setExpiresAt(Timestamp.newBuilder().setSeconds(issued.expiresAt().getEpochSecond()))
            .build());
    observer.onCompleted();
  }

  private static UUID uuid(String value) {
    try {
      UUID id = UUID.fromString(value);
      IamError.validate(id.toString().equals(value));
      return id;
    } catch (IllegalArgumentException e) {
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    }
  }
}
