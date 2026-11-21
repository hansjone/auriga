package auriga.workspace.web;

import static org.assertj.core.api.Assertions.assertThat;

import java.net.URI;
import org.junit.jupiter.api.Test;

class SharedDshControllerExchangeUrlTest {
  private static final String POOL = "01a1fc08-dc04-7402-b04e-e64382a4e320";

  @Test
  void pathMountedExchangeIsReturnedRelativeToTheBrowserOrigin() {
    assertThat(
            SharedDshController.browserExchangeUrl(
                URI.create("http://10.0.0.133:8080/i/" + POOL + "/_auriga/placement-ticket")))
        .isEqualTo("/i/" + POOL + "/_auriga/placement-ticket");
  }

  @Test
  void subdomainExchangeKeepsItsAbsoluteOrigin() {
    var url = "https://" + POOL + ".i.example.test/_auriga/placement-ticket";
    assertThat(SharedDshController.browserExchangeUrl(URI.create(url))).isEqualTo(url);
  }
}
