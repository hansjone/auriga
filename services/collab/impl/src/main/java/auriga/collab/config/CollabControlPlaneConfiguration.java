package auriga.collab.config;

import auriga.collab.api.CollabAdmission;
import auriga.collab.app.OnlineCollabAdmission;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.grpc.AurigaClientInterceptor;
import auriga.common.grpc.GrpcChannels;
import auriga.contracts.iam.v1.DeviceAccessServiceGrpc;
import auriga.contracts.iam.v1.VerifyDeviceCredentialRequest;
import auriga.contracts.org.v1.AuthorizationServiceGrpc;
import auriga.contracts.org.v1.GetEffectivePermissionsRequest;
import io.grpc.ManagedChannel;
import java.io.File;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.time.Instant;
import java.util.Set;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.env.Environment;

/** Own svc:collab identity; no gateway credentials or global token-provider override. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.services.collab.enabled:true} && ${auriga.collab.control-plane.enabled:false}")
public class CollabControlPlaneConfiguration {
  private static final String PREFIX = "auriga.collab.control-plane.";

  @Bean(destroyMethod = "close")
  ControlPlaneChannels collabControlPlaneChannels(Environment env) {
    Path tokenFile = Path.of(env.getRequiredProperty(PREFIX + "service-token-file"));
    var interceptor =
        new AurigaClientInterceptor(
            () -> {
              try {
                String token = Files.readString(tokenFile).trim();
                if (token.isEmpty() || token.contains("\n") || token.contains("\r"))
                  throw new AurigaException(CommonErrors.UNAVAILABLE);
                return token;
              } catch (java.io.IOException e) {
                throw new AurigaException(CommonErrors.UNAVAILABLE);
              }
            },
            Duration.ofSeconds(5));
    ManagedChannel iam = open(env, "iam-target", interceptor);
    try {
      return new ControlPlaneChannels(iam, open(env, "org-target", interceptor));
    } catch (RuntimeException e) {
      iam.shutdownNow();
      throw e;
    }
  }

  @Bean
  CollabAdmission onlineCollabAdmission(ControlPlaneChannels channels) {
    var devices = DeviceAccessServiceGrpc.newBlockingStub(channels.iam());
    var permissions = AuthorizationServiceGrpc.newBlockingStub(channels.org());
    return new OnlineCollabAdmission(
        actor -> {
          var result =
              devices.verifyDeviceCredential(
                  VerifyDeviceCredentialRequest.newBuilder()
                      .setTenantId(actor.getTenantId())
                      .setAccountId(actor.getAccountId())
                      .setDeviceId(actor.getCredentialSubject())
                      .build());
          if (!result.getActive()
              || !result.getTenantId().equals(actor.getTenantId())
              || !result.getAccountId().equals(actor.getAccountId())
              || !result.getDeviceId().equals(actor.getCredentialSubject())
              || !result.hasExpiresAt()
              || result.getExpiresAt().getSeconds() <= Instant.now().getEpochSecond())
            throw new AurigaException(CommonErrors.FORBIDDEN);
        },
        actor ->
            permissions.getEffectivePermissions(
                GetEffectivePermissionsRequest.newBuilder()
                    .setTenantId(actor.getTenantId())
                    .setAccountId(actor.getAccountId())
                    .build()));
  }

  private static ManagedChannel open(
      Environment env, String targetKey, AurigaClientInterceptor interceptor) {
    String target = env.getRequiredProperty(PREFIX + targetKey);
    String certificate = env.getProperty(PREFIX + "tls-cert-chain", "");
    if (!certificate.isBlank())
      return GrpcChannels.openTls(
          target,
          new File(certificate),
          new File(env.getRequiredProperty(PREFIX + "tls-private-key")),
          new File(env.getRequiredProperty(PREFIX + "tls-trust-cert-collection")),
          interceptor);
    // Do not resolve hostnames: only numeric literal loopback is allowed without mTLS.
    String host =
        target.startsWith("[")
            ? target.substring(1, target.indexOf(']'))
            : target.substring(0, target.lastIndexOf(':'));
    if (!Set.of("127.0.0.1", "::1", "0:0:0:0:0:0:0:1").contains(host))
      throw new IllegalStateException("Collab non-loopback control plane requires mTLS");
    return GrpcChannels.open(target, interceptor);
  }

  public record ControlPlaneChannels(ManagedChannel iam, ManagedChannel org)
      implements AutoCloseable {
    @Override
    public void close() {
      iam.shutdownNow();
      org.shutdownNow();
    }
  }
}
