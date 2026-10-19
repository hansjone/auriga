package auriga.org;

import java.util.UUID;

final class OrgIds {
  static final UUID DEFAULT_TENANT = UUID.fromString("00000000-0000-0000-0000-000000000001");
  static final UUID ROLE_OWNER = UUID.fromString("00000000-0000-7000-8000-000000000001");
  static final UUID ROLE_ADMIN = UUID.fromString("00000000-0000-7000-8000-000000000002");
  static final UUID ROLE_MEMBER = UUID.fromString("00000000-0000-7000-8000-000000000003");
  static final UUID ROLE_EXTERNAL = UUID.fromString("00000000-0000-7000-8000-000000000004");

  private OrgIds() {}
}
