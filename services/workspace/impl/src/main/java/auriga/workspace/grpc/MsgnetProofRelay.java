package auriga.workspace.grpc;

import auriga.contracts.msgnet.model.*;
import auriga.iam.api.*;
import auriga.workspace.app.NodeEnrollment;
import com.sun.net.httpserver.*;
import java.io.*;
import java.net.*;
import java.security.cert.X509Certificate;
import java.util.*;
import java.util.concurrent.*;
import javax.net.ssl.*;
import org.springframework.context.SmartLifecycle;

/**
 * Native-node mTLS front end for frozen public C models. Internal JWTs stay behind this listener.
 */
public final class MsgnetProofRelay implements SmartLifecycle, AutoCloseable {
  private static final String BASE = "/api/collab/v2/network";
  private final InetSocketAddress address;
  private final SSLContext tls;
  private final NodeEnrollment enrollment;
  private final NodeControlAuthority pins;
  private final MessageNetworkControlLedger ledger;
  private final MessageNetworkIsolation isolation;
  private final Semaphore slots = new Semaphore(8);
  private HttpsServer server;
  private ExecutorService workers;
  private ScheduledExecutorService deadlines;

  public MsgnetProofRelay(
      InetSocketAddress address,
      SSLContext tls,
      NodeEnrollment enrollment,
      NodeControlAuthority pins,
      MessageNetworkControlLedger ledger,
      MessageNetworkIsolation isolation) {
    this.address = address;
    this.tls = tls;
    this.enrollment = enrollment;
    this.pins = pins;
    this.ledger = ledger;
    this.isolation = isolation;
  }

  @Override
  public synchronized void start() {
    if (server != null) return;
    try {
      server = HttpsServer.create(address, 16);
      server.setHttpsConfigurator(
          new HttpsConfigurator(tls) {
            @Override
            public void configure(HttpsParameters parameters) {
              var ssl = tls.getDefaultSSLParameters();
              ssl.setNeedClientAuth(true);
              parameters.setSSLParameters(ssl);
            }
          });
      workers = Executors.newVirtualThreadPerTaskExecutor();
      server.setExecutor(workers);
      deadlines =
          Executors.newSingleThreadScheduledExecutor(
              Thread.ofPlatform().daemon().name("node-proof-deadlines").factory());
      server.createContext(BASE, this::handle);
      server.start();
    } catch (IOException failure) {
      close();
      throw new IllegalStateException("Cannot start native proof relay", failure);
    }
  }

  private void handle(HttpExchange exchange) throws IOException {
    exchange.getResponseHeaders().set("Cache-Control", "no-store");
    if (!slots.tryAcquire()) {
      exchange.sendResponseHeaders(503, -1);
      exchange.close();
      return;
    }
    var deadline = deadlines.schedule(exchange::close, 25, TimeUnit.SECONDS);
    try {
      if (!(exchange instanceof HttpsExchange https)) throw new SecurityException();
      var leaf = (X509Certificate) https.getSSLSession().getPeerCertificates()[0];
      UUID node = enrollment.identity(leaf), tenant = enrollment.store.route(node, "node");
      var binding = pins.binding(tenant, node);
      if (!binding.tenant().equals(tenant.toString())
          || !binding.pin().getNodeId().equals(node.toString())) throw new SecurityException();
      Object response = route(exchange, tenant, node, binding);
      byte[] raw = ledger.canonicalPublic(response);
      if (raw.length > 32 * 1024 * 1024)
        throw new IllegalStateException("oversize public relay response");
      exchange.getResponseHeaders().set("Content-Type", "application/json");
      exchange.sendResponseHeaders(200, raw.length);
      exchange.getResponseBody().write(raw);
    } catch (SecurityException | javax.net.ssl.SSLPeerUnverifiedException forbidden) {
      exchange.sendResponseHeaders(403, -1);
    } catch (RuntimeException unavailable) {
      exchange.sendResponseHeaders(503, -1);
    } finally {
      deadline.cancel(false);
      slots.release();
      exchange.close();
    }
  }

  private Object route(
      HttpExchange exchange, UUID tenant, UUID node, NodeControlAuthority.Binding binding)
      throws IOException {
    var uri = exchange.getRequestURI();
    if (uri.getRawQuery() != null || !uri.getRawPath().equals(uri.getPath()))
      throw new SecurityException();
    String path = uri.getPath(), method = exchange.getRequestMethod();
    if (method.equals("GET")) {
      if (path.equals(BASE + "/bootstrap/keys")) {
        var publication = ledger.publication();
        scope(
            binding,
            publication.getAuthority().getPayload().getTenantId(),
            publication.getAuthority().getPayload().getRealm());
        return publication;
      }
      if (path.equals(BASE + "/isolation/" + binding.pin().getTargetId())) {
        isolation.beforeControl(tenant, node);
        return ledger.isolation(binding.tenant(), binding.realm(), binding.pin().getTargetId());
      }
      String history = BASE + "/transport/history/", transition = BASE + "/transport/transitions/";
      if (path.startsWith(history)) {
        var value = ledger.history(path.substring(history.length()));
        scope(
            binding,
            value.getBundle().getPayload().getTenantId(),
            value.getBundle().getPayload().getRealm());
        return value;
      }
      if (path.startsWith(transition)) {
        var value = ledger.transition(path.substring(transition.length()));
        scope(
            binding,
            value.getAuthority().getPayload().getTenantId(),
            value.getAuthority().getPayload().getRealm());
        return value;
      }
      throw new SecurityException();
    }
    if (!method.equals("POST")) throw new SecurityException();
    byte[] raw = exchange.getRequestBody().readNBytes(65537);
    if (raw.length > 65536) throw new SecurityException();
    if (path.equals(BASE + "/transport/confirmation")) {
      var query = ledger.confirmationQuery(raw);
      scope(binding, query.getTenantId(), query.getRealm());
      return ledger.confirmation(query);
    }
    if (path.equals(BASE + "/isolation")) {
      var request = ledger.isolationRequest(raw);
      scope(binding, request.getTenantId(), request.getRealm());
      if (!request.getTargetId().equals(binding.pin().getTargetId())) throw new SecurityException();
      if (request.getState() == IsolationRecord.StateEnum.READY)
        return isolation.ready(tenant, node, request);
      if (request.getState() != IsolationRecord.StateEnum.QUARANTINED
          || Boolean.TRUE.equals(request.getNewChainReady())) throw new SecurityException();
      var pending =
          ledger
              .pending(binding.tenant(), binding.realm(), binding.pin().getTargetId())
              .orElse(null);
      isolation.beforeControl(tenant, node);
      var current =
          ledger.isolation(binding.tenant(), binding.realm(), binding.pin().getTargetId());
      boolean exact =
          current != null
              && current.getMinimumControlRevision().equals(request.getMinimumControlRevision())
              && current.getControlDigest().equals(request.getControlDigest())
              && current.getChallenge().equals(request.getChallenge());
      boolean queued =
          pending != null
              && pending
                  .getControl()
                  .getPayload()
                  .getControlRevision()
                  .equals(request.getMinimumControlRevision())
              && pending.getControlDigest().equals(request.getControlDigest())
              && pending.getChallenge().equals(request.getChallenge())
              && current != null
              && new java.math.BigInteger(current.getMinimumControlRevision())
                      .compareTo(new java.math.BigInteger(request.getMinimumControlRevision()))
                  > 0
              && current.getState() != IsolationRecord.StateEnum.READY
              && !Boolean.TRUE.equals(current.getNewChainReady());
      if (!exact && !queued) throw new SecurityException();
      return current;
    }
    throw new SecurityException();
  }

  private static void scope(NodeControlAuthority.Binding binding, String tenant, String realm) {
    if (!binding.tenant().equals(tenant) || !binding.realm().equals(realm))
      throw new SecurityException();
  }

  public synchronized int port() {
    return server == null ? 0 : server.getAddress().getPort();
  }

  @Override
  public synchronized boolean isRunning() {
    return server != null;
  }

  @Override
  public void stop() {
    close();
  }

  @Override
  public synchronized void close() {
    if (server != null) {
      server.stop(0);
      server = null;
    }
    if (workers != null) {
      workers.shutdownNow();
      workers = null;
    }
    if (deadlines != null) {
      deadlines.shutdownNow();
      deadlines = null;
    }
  }
}
