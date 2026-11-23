package auriga.workspace.web;

import static auriga.workspace.domain.SharedDshPolicy.*;

import auriga.common.error.*;
import auriga.common.security.*;
import auriga.workspace.api.SharedDshAuthority.Placement;
import auriga.workspace.app.*;
import auriga.workspace.web.model.*;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.validation.Valid;
import java.time.Clock;
import java.util.UUID;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.*;

/** Explicitly enabled only after all trusted adapters have been assembled. */
@RestController
@RequestMapping("/api/workspace/v1")
@ConditionalOnProperty(name = "auriga.workspace.shared-dsh.enabled", havingValue = "true")
public class SharedDshController {
  /** Mandatory live session plus trusted Origin/CSRF verifier, supplied by the IAM assembly. */
  public interface RequestGate {
    void require(HttpServletRequest request, AurigaPrincipal actor, boolean mutation);
  }

  private final SharedDshPlacements placements;
  private final SharedDshTickets tickets;
  private final RequestGate gate;
  private final Clock clock;
  private final SharedDshAdmin admin;
  private final auriga.iam.api.PlacementAuthorization authorization;

  public SharedDshController(
      SharedDshPlacements placements,
      SharedDshTickets tickets,
      RequestGate gate,
      Clock clock,
      SharedDshAdmin admin,
      auriga.iam.api.PlacementAuthorization authorization) {
    this.placements = placements;
    this.tickets = tickets;
    this.gate = gate;
    this.clock = clock;
    this.admin = admin;
    this.authorization = authorization;
  }

  private AurigaPrincipal caller(HttpServletRequest request, boolean mutation) {
    var p = WorkspaceController.actor();
    trusted(() -> gate.require(request, p, mutation));
    return p;
  }

  private static void trusted(Runnable check) {
    try {
      check.run();
    } catch (AurigaException denied) {
      throw denied;
    } catch (RuntimeException unavailable) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  private UUID session(AurigaPrincipal p) {
    if (p.sessionId() == null) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    return UUID.fromString(p.sessionId());
  }

  private static DshPlacement view(Placement p) {
    var b = p.binding();
    return new DshPlacement(
        UUID.fromString(b.getMemberId()),
        UUID.fromString(b.getPlacementId()),
        DshInstanceMode.fromValue(mode(b.getMode())),
        b.getEpoch(),
        b.getRevision(),
        DshPlacement.StateEnum.fromValue(p.state()));
  }

  @PostMapping("/me/placement/open")
  @AuthenticatedOnly
  public ResponseEntity<DshPlacementOpen> open(
      @RequestHeader("Idempotency-Key") String key, HttpServletRequest request) {
    var p = caller(request, true);
    if (p.context() != TokenContext.PORTAL) throw new AurigaException(CommonErrors.FORBIDDEN);
    var result = tickets.open(UUID.fromString(p.subject()), session(p), key);
    return ResponseEntity.ok()
        .header("Cache-Control", "no-store")
        .body(
            new DshPlacementOpen(
                view(new Placement(result.binding(), "ready", 0)),
                result.ticket(),
                browserExchangeUrl(result.exchangeUrl()),
                result.expiresAt()));
  }

  /**
   * A path-mounted instance shares the portal origin. Return only its path so the browser resolves
   * it against the origin it actually uses (a proxied domain or the site IP); subdomain instances
   * keep their absolute HTTPS origin.
   */
  static String browserExchangeUrl(java.net.URI url) {
    String path = url.getRawPath();
    return path != null
            && path.matches("/i/[0-9a-f-]{36}/_auriga/placement-ticket")
            && url.getRawQuery() == null
            && url.getRawFragment() == null
        ? path
        : url.toString();
  }

  @GetMapping("/me/placement")
  @AuthenticatedOnly
  public ResponseEntity<DshPlacement> current(HttpServletRequest request) {
    var p = caller(request, false);
    var result = placements.current(UUID.fromString(p.subject()), session(p));
    return ResponseEntity.ok()
        .eTag("\"" + result.binding().getRevision() + "\"")
        .body(view(result));
  }

  @PostMapping("/admin/members/{memberId}/placement-switches")
  @AdminOnly
  @RequiresPermission("workspace.placements.write")
  public ResponseEntity<java.util.Map<String, Object>> switchMode(
      @PathVariable UUID memberId,
      @RequestHeader("Idempotency-Key") String key,
      @RequestHeader(value = "If-Match", required = false) String etag,
      @RequestBody @Valid DshPlacementSwitchRequest body,
      HttpServletRequest request) {
    var p = administrator(request, true);
    valid(
        body.getTargetMode() != null
            && body.getSessionPolicy() != null
            && body.getSessionPolicy().getValue().equals("discard")
            && body.getFilePolicy() != null
            && body.getFilePolicy().getValue().equals("archive-and-reset"));
    return response(
        admin.switchMode(
            UUID.fromString(p.subject()),
            session(p),
            memberId,
            body.getTargetMode().getValue(),
            body.getReason(),
            key,
            etag),
        true);
  }

  @GetMapping("/admin/placement-switches/{operationId}")
  @AdminOnly
  @RequiresPermission("workspace.placements.read")
  public ResponseEntity<java.util.Map<String, Object>> operation(
      @PathVariable UUID operationId, HttpServletRequest request) {
    administrator(request, false);
    return response(admin.operation(operationId), false);
  }

  private AurigaPrincipal administrator(HttpServletRequest request, boolean write) {
    var p = caller(request, write);
    if (!p.isAdminContext()
        || !p.hasPermission("workspace.placements." + (write ? "write" : "read")))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    trusted(
        () -> {
          if (write) authorization.requireWrite();
          else authorization.requireRead();
        });
    return p;
  }

  private static ResponseEntity<java.util.Map<String, Object>> response(
      SharedDshAdmin.Result r, boolean accepted) {
    return ResponseEntity.status(accepted ? 202 : 200)
        .header("Cache-Control", "no-store")
        .eTag(r.etag())
        .body(r.body());
  }

  public record EntitlementRequest(
      @jakarta.validation.constraints.NotNull Boolean granted,
      @jakarta.validation.constraints.NotBlank @jakarta.validation.constraints.Size(max = 512)
          String reason) {}

  public record StartRequest(
      @jakarta.validation.constraints.NotNull @jakarta.validation.constraints.AssertTrue
          Boolean exportConfirmed,
      @jakarta.validation.constraints.NotNull
          @jakarta.validation.constraints.Pattern(regexp = "discard")
          String sessionPolicy,
      @jakarta.validation.constraints.NotNull
          @jakarta.validation.constraints.Pattern(regexp = "archive-and-reset")
          String filePolicy) {}

  @GetMapping("/admin/members/{memberId}/placement")
  @AdminOnly
  @RequiresPermission("workspace.placements.read")
  public ResponseEntity<java.util.Map<String, Object>> member(
      @PathVariable UUID memberId, HttpServletRequest request) {
    administrator(request, false);
    return response(admin.member(memberId), false);
  }

  @PutMapping("/admin/members/{memberId}/dedicated-entitlement")
  @AdminOnly
  @RequiresPermission("workspace.placements.write")
  public ResponseEntity<java.util.Map<String, Object>> grant(
      @PathVariable UUID memberId,
      @RequestHeader("Idempotency-Key") String key,
      @RequestHeader(value = "If-Match", required = false) String etag,
      @RequestBody @Valid EntitlementRequest body,
      HttpServletRequest request) {
    var p = administrator(request, true);
    return response(
        admin.grant(
            UUID.fromString(p.subject()),
            session(p),
            memberId,
            body.granted(),
            body.reason(),
            key,
            etag),
        false);
  }

  @GetMapping("/admin/members/{memberId}/placement-switches/lookup")
  @AdminOnly
  @RequiresPermission("workspace.placements.read")
  public ResponseEntity<java.util.Map<String, Object>> lookup(
      @PathVariable UUID memberId,
      @RequestHeader("Idempotency-Key") String key,
      HttpServletRequest request) {
    var p = administrator(request, false);
    return response(admin.lookup(UUID.fromString(p.subject()), memberId, key), false);
  }

  @PostMapping("/admin/placement-rebuild-plans")
  @AdminOnly
  @RequiresPermission("workspace.placements.write")
  public ResponseEntity<java.util.Map<String, Object>> createPlan(
      @RequestHeader("Idempotency-Key") String key,
      @RequestBody SharedDshAdmin.PlanRequest body,
      HttpServletRequest request) {
    var p = administrator(request, true);
    return response(
        admin.save(UUID.fromString(p.subject()), session(p), null, body, key, null), false);
  }

  @GetMapping("/admin/placement-rebuild-plans/{planId}")
  @AdminOnly
  @RequiresPermission("workspace.placements.read")
  public ResponseEntity<java.util.Map<String, Object>> plan(
      @PathVariable UUID planId, HttpServletRequest request) {
    administrator(request, false);
    return response(admin.plan(planId), false);
  }

  @PutMapping("/admin/placement-rebuild-plans/{planId}")
  @AdminOnly
  @RequiresPermission("workspace.placements.write")
  public ResponseEntity<java.util.Map<String, Object>> updatePlan(
      @PathVariable UUID planId,
      @RequestHeader("Idempotency-Key") String key,
      @RequestHeader(value = "If-Match", required = false) String etag,
      @RequestBody SharedDshAdmin.PlanRequest body,
      HttpServletRequest request) {
    var p = administrator(request, true);
    return response(
        admin.save(UUID.fromString(p.subject()), session(p), planId, body, key, etag), false);
  }

  @PostMapping("/admin/placement-rebuild-plans/{planId}/start")
  @AdminOnly
  @RequiresPermission("workspace.placements.write")
  public ResponseEntity<java.util.Map<String, Object>> startPlan(
      @PathVariable UUID planId,
      @RequestHeader("Idempotency-Key") String key,
      @RequestHeader(value = "If-Match", required = false) String etag,
      @RequestBody @Valid StartRequest body,
      HttpServletRequest request) {
    var p = administrator(request, true);
    return response(admin.start(UUID.fromString(p.subject()), session(p), planId, key, etag), true);
  }

  @GetMapping("/admin/placement-rebuild-plans/{planId}/progress")
  @AdminOnly
  @RequiresPermission("workspace.placements.read")
  public ResponseEntity<java.util.Map<String, Object>> progress(
      @PathVariable UUID planId, HttpServletRequest request) {
    administrator(request, false);
    return response(admin.progress(planId), false);
  }

  @GetMapping("/admin/placement-capacity")
  @AdminOnly
  @RequiresPermission("workspace.placements.read")
  public ResponseEntity<Object> capacity(HttpServletRequest request) {
    administrator(request, false);
    // Percent-only telemetry cannot prove a complete, fresh byte snapshot.
    throw new AurigaException(CommonErrors.UNAVAILABLE);
  }
}
