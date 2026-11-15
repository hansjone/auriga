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

  public SharedDshController(
      SharedDshPlacements placements, SharedDshTickets tickets, RequestGate gate, Clock clock) {
    this.placements = placements;
    this.tickets = tickets;
    this.gate = gate;
    this.clock = clock;
  }

  private AurigaPrincipal caller(HttpServletRequest request, boolean mutation) {
    var p = WorkspaceController.actor();
    gate.require(request, p, mutation);
    return p;
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
                result.exchangeUrl().toString(),
                result.expiresAt()));
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
  public ResponseEntity<DshPlacementSwitch> switchMode(
      @PathVariable UUID memberId,
      @RequestHeader("Idempotency-Key") String key,
      @RequestHeader("If-Match") String etag,
      @RequestBody @Valid DshPlacementSwitchRequest body,
      HttpServletRequest request) {
    var p = caller(request, true);
    if (!p.isAdminContext() || !p.hasPermission("workspace.placements.write"))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    if (!stepUp(p.stepUpTime(), clock.instant()))
      throw new AurigaException(CommonErrors.STEP_UP_REQUIRED);
    valid(
        body.getTargetMode() != null
            && body.getSessionPolicy() != null
            && body.getSessionPolicy().getValue().equals("discard")
            && body.getFilePolicy() != null
            && body.getFilePolicy().getValue().equals("archive-and-reset"));
    valid(etag.matches("\"[1-9][0-9]{0,15}\""));
    long revision = Long.parseLong(etag.substring(1, etag.length() - 1));
    valid(revision <= 9007199254740991L);
    UUID id =
        placements.switchMode(
            UUID.fromString(p.subject()),
            session(p),
            memberId,
            body.getTargetMode().getValue(),
            revision,
            key,
            body.getReason());
    return ResponseEntity.accepted().body(placements.switchView(id));
  }

  @GetMapping("/admin/placement-switches/{operationId}")
  @AdminOnly
  @RequiresPermission("workspace.placements.read")
  public DshPlacementSwitch operation(@PathVariable UUID operationId, HttpServletRequest request) {
    var p = caller(request, false);
    if (!p.isAdminContext() || !p.hasPermission("workspace.placements.read"))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    return placements.switchView(operationId);
  }
}
