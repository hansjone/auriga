package io.github.hansjone.auriga.iam.api;

import io.github.hansjone.auriga.contracts.iam.v1.*;

public interface AccountDirectory {
  GetAccountResponse get(GetAccountRequest request);

  BatchGetAccountsResponse batch(BatchGetAccountsRequest request);
}
