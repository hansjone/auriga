package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import auriga.iam.api.AccountDirectory;
import io.grpc.stub.StreamObserver;
import tools.jackson.databind.node.ObjectNode;

public class IamAccounts extends AccountServiceGrpc.AccountServiceImplBase
    implements AccountDirectory {
  private final IamStore store;

  public IamAccounts(IamStore store) {
    this.store = store;
  }

  private void tenant(String tenant) {
    IamError.require(TenantContext.require().toString().equals(tenant), "tenant_mismatch", 403);
  }

  private Account convert(ObjectNode row) {
    Account.Builder a =
        Account.newBuilder()
            .setAccountId(row.path("accountId").asString())
            .setTenantId(TenantContext.require().toString())
            .setDisplayName(row.path("displayName").asString(""))
            .setEmail(row.path("email").asString(""))
            .setStatus(
                row.path("status").asString().equals("active")
                    ? AccountStatus.ACCOUNT_STATUS_ACTIVE
                    : AccountStatus.ACCOUNT_STATUS_DISABLED)
            .setCreatedAt(SessionManager.timestamp(row.path("createdAt").asString()));
    if (row.has("lastLoginAt"))
      a.setLastLoginAt(SessionManager.timestamp(row.path("lastLoginAt").asString()));
    return a.build();
  }

  @Override
  public GetAccountResponse get(GetAccountRequest request) {
    tenant(request.getTenantId());
    return store.tx(
        () -> {
          ObjectNode row = store.get("account", request.getAccountId(), false);
          IamError.require(row != null, "not_found", 404);
          return GetAccountResponse.newBuilder().setAccount(convert(row)).build();
        });
  }

  @Override
  public BatchGetAccountsResponse batch(BatchGetAccountsRequest request) {
    tenant(request.getTenantId());
    IamError.require(request.getAccountIdsCount() <= 500, "invalid_batch_size", 400);
    return store.tx(
        () -> {
          var response = BatchGetAccountsResponse.newBuilder();
          for (String id : request.getAccountIdsList()) {
            ObjectNode row = store.get("account", id, false);
            if (row != null) response.addAccounts(convert(row));
          }
          return response.build();
        });
  }

  private void caller() {
    IamError.require(
        AuthorizationRules.currentPrincipal().filter(p -> p.isInternalService()).isPresent(),
        "forbidden",
        403);
  }

  @Override
  @AuthenticatedOnly
  public void getAccount(GetAccountRequest request, StreamObserver<GetAccountResponse> observer) {
    caller();
    observer.onNext(get(request));
    observer.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void batchGetAccounts(
      BatchGetAccountsRequest request, StreamObserver<BatchGetAccountsResponse> observer) {
    caller();
    observer.onNext(batch(request));
    observer.onCompleted();
  }
}
