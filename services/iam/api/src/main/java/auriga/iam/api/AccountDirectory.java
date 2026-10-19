package auriga.iam.api;

import auriga.contracts.iam.v1.*;

public interface AccountDirectory {
  GetAccountResponse get(GetAccountRequest request);

  BatchGetAccountsResponse batch(BatchGetAccountsRequest request);
}
