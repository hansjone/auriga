package auriga.collab.network.keys;

import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.scheduling.annotation.Scheduled;

/** Polls a trusted public release mount and retries complete revocation fanout after commit. */
public final class NetworkKeyMaintenance {
  private static final Logger LOG = LoggerFactory.getLogger(NetworkKeyMaintenance.class);
  private final NetworkKeySettings settings;
  private final NetworkKeyOperations operations;
  private final ObjectProvider<NetworkKeyPush> push;

  public NetworkKeyMaintenance(
      NetworkKeySettings settings,
      NetworkKeyOperations operations,
      ObjectProvider<NetworkKeyPush> push) {
    this.settings = settings;
    this.operations = operations;
    this.push = push;
  }

  @Scheduled(fixedDelay = 30000, initialDelay = 30000)
  public void refresh() {
    try {
      operations.publish(settings.mountedRelease());
    } catch (RuntimeException e) {
      // Retain the last valid release until its original hard deadline; never renew it.
      LOG.warn("Network root publication reload failed; existing hard deadline retained");
    }
    var transport = push.getIfAvailable();
    if (transport == null) {
      LOG.warn("Network key update transport is not configured; durable push remains pending");
      return;
    }
    try {
      operations.pushPending(transport);
    } catch (RuntimeException e) {
      LOG.warn("Network root publication push failed; durable update remains pending");
    }
  }
}
