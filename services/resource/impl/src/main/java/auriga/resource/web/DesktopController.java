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
  @PublicEndpoint
  public ResponseEntity<JsonNode> latest(@RequestParam(defaultValue = "stable") String channel) {
    var manifest = releases.latest(channel);
    return ResponseEntity.ok()
        .cacheControl(CacheControl.noStore())
        .header(
            "X-Desktop-Filename", "AurigaDSH-Setup-" + manifest.path("version").asString() + ".exe")
        .body(manifest);
  }

  @GetMapping("/desktop/packages/{sequence}")
  @PublicEndpoint
  public ResponseEntity<InputStreamResource> download(
      @PathVariable long sequence, @RequestHeader(value = "Range", required = false) String range)
      throws java.io.IOException {
    var result = releases.download(sequence);
    long offset = 0;
    if (range != null) {
      try {
        if (!range.matches("bytes=[0-9]+-")) throw new IllegalArgumentException();
        offset = Long.parseLong(range.substring(6, range.length() - 1));
        if (offset >= result.size()) throw new IllegalArgumentException();
      } catch (IllegalArgumentException e) {
        result.stream().close();
        return ResponseEntity.status(HttpStatus.REQUESTED_RANGE_NOT_SATISFIABLE)
            .header("Content-Range", "bytes */" + result.size())
            .build();
      }
      try {
        result.stream().skipNBytes(offset);
      } catch (java.io.IOException e) {
        result.stream().close();
        throw e;
      }
    }
    var response =
        ResponseEntity.status(range == null ? HttpStatus.OK : HttpStatus.PARTIAL_CONTENT)
            .header("Accept-Ranges", "bytes");
    if (range != null)
      response.header(
          "Content-Range", "bytes " + offset + "-" + (result.size() - 1) + "/" + result.size());
    return response
        .cacheControl(CacheControl.noStore())
        .contentType(MediaType.APPLICATION_OCTET_STREAM)
        .contentLength(result.size() - offset)
        .header("Content-Disposition", "attachment; filename=\"" + result.filename() + "\"")
        .body(new InputStreamResource(result.stream()));
  }
}
