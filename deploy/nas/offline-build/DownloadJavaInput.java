import java.io.IOException;
import java.net.URI;
import java.nio.file.Files;
import java.nio.file.Path;
import javax.net.ssl.HttpsURLConnection;

/** Downloads an input from a site mirror. The caller verifies the pinned digest before use. */
class DownloadJavaInput {
    public static void main(String[] args) throws IOException {
        var destination = Path.of(args[2]);
        if (!download(args[0], destination) && !download(args[1], destination)) {
            throw new IOException("站点镜像缺少构建输入：" + destination.getFileName());
        }
    }

    private static boolean download(String source, Path destination) throws IOException {
        var uri = URI.create(source);
        if (!"https".equals(uri.getScheme()) || uri.getHost() == null || uri.getUserInfo() != null) {
            throw new IOException("构建输入镜像须使用 HTTPS 且地址中不能包含凭据");
        }
        var connection = (HttpsURLConnection) uri.toURL().openConnection();
        connection.setInstanceFollowRedirects(false);
        connection.setConnectTimeout(12_000);
        connection.setReadTimeout(30_000);
        try {
            int status = connection.getResponseCode();
            if (status == 404) {
                return false;
            }
            if (status != 200) {
                throw new IOException("构建输入镜像返回 HTTP " + status + "：" + uri);
            }
            try (var input = connection.getInputStream()) {
                Files.copy(input, destination);
            }
            return true;
        } finally {
            connection.disconnect();
        }
    }
}
