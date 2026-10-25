package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.api.NetworkSourceAuthority.Dispatch;
import auriga.collab.app.NetworkMailboxService.NodeTarget;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import org.junit.jupiter.api.Test;

class NetworkDispatchStampTest {
  @Test
  void contractMaximum256RecipientsFitsBoundedSnapshotAndReconstructsEveryMember() {
    var locals = new ArrayList<String>();
    var members = new HashMap<String, String>();
    var types = new HashMap<String, String>();
    for (int i = 1; i <= 256; i++) {
      locals.add(Integer.toString(i));
      members.put("20." + i, "membership-" + i);
      types.put("20." + i, "session");
    }
    var dispatch = new Dispatch("{}", "snapshot", members, types, null, null, null);
    var context =
        NetworkSourceStamp.dispatchContext(dispatch, List.of(new NodeTarget("20", locals)));
    var persisted = NetworkJson.parse(NetworkJson.canonical(context));
    assertEquals(256, persisted.path("members").size());
    for (String local : locals) {
      assertEquals(
          "membership-" + local,
          NetworkSourceStamp.memberContext(persisted, "20", local)
              .path("membership_id")
              .asString());
    }
  }
}
