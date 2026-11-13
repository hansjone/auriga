package auriga.collab.network.keys;

import static org.junit.jupiter.api.Assertions.assertEquals;

import auriga.collab.app.NetworkJson;
import org.junit.jupiter.api.Test;

class MsgnetContractAdapterTest {
  @Test
  void generatedModelRoundTripKeepsSignedCanonicalBytes() throws Exception {
    try (var input =
        getClass()
            .getClassLoader()
            .getResourceAsStream("network-fixtures/auto-renewal-v2-vectors.json")) {
      var original =
          KeyTestMaterial.JSON
              .readTree(input)
              .path("cases")
              .get(0)
              .path("input")
              .path("publication");
      var model =
          KeyTestMaterial.JSON.treeToValue(
              original, auriga.contracts.msgnet.model.Publication.class);
      assertEquals(
          NetworkJson.canonical(original), NetworkJson.canonical(MsgnetNetworkRenewal.tree(model)));
    }
  }
}
