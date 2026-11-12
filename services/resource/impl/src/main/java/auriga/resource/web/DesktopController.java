package auriga.resource.web;

import static auriga.resource.web.ResourceBody.*;
import static auriga.resource.web.ResourceWorkflowController.*;

import auriga.common.security.*;
import auriga.resource.app.*;
import jakarta.servlet.http.HttpServletRequest;
import java.util.*;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.core.io.InputStreamResource;
import org.springframework.http.*;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.JsonNode;

@RestController
@RequestMapping("/api/resource/v1")
@ConditionalOnProperty({
  "auriga.resource.desktop-directory",
  "auriga.resource.desktop-public-key-file"
})
public class DesktopController {
  private final DesktopReleases releases;

  public DesktopController(DesktopReleases releases) {
    this.releases = releases;
  }

  @GetMapping("/admin/desktop/releases")
  @RequiresPermission(DesktopReleases.MANAGE)
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> list() {
    return response(releases.list(actor()), 200);
  }

  @PostMapping(value = "/admin/desktop/releases", consumes = "application/octet-stream")
  @RequiresPermission(DesktopReleases.MANAGE)
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> publish(HttpServletRequest request) throws java.io.IOException {
    String encoded = header(request, "X-Desktop-Manifest");
    if (encoded == null || encoded.length() > 43700) throw invalid();
    byte[] bytes;
    try {
      bytes = Base64.getUrlDecoder().decode(encoded);
    } catch (IllegalArgumentException e) {
      throw invalid();
    }
    return response(
        releases.publish(
            actor(), parse(bytes, 32768, null, Set.of("signature")), request.getInputStream()),
        201);
  }

  @DeleteMapping("/admin/desktop/releases/{sequence}")
  @RequiresPermission(DesktopReleases.MANAGE)
  @RequiresRecentStepUp
  public ResponseEntity<Void> withdraw(@PathVariable long sequence) {
    releases.withdraw(actor(), sequence);
    return ResponseEntity.noContent().build();
  }

  @GetMapping("/desktop/latest")
  @RequiresPermission(ResourceAccess.READ)
  public ResponseEntity<JsonNode> latest() {
    return response(releases.latest(actor()), 200);
  }

  @GetMapping("/desktop/packages/{sequence}")
  @RequiresPermission(ResourceAccess.READ)
  public ResponseEntity<InputStreamResource> download(@PathVariable long sequence) {
    var result = releases.download(actor(), sequence);
    return ResponseEntity.ok()
        .cacheControl(CacheControl.noStore())
        .contentType(MediaType.APPLICATION_OCTET_STREAM)
        .contentLength(result.size())
        .header("Content-Disposition", "attachment; filename=\"Auriga-Desktop.zip\"")
        .body(new InputStreamResource(result.stream()));
  }
}
