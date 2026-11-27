import java.nio.file.Path;
import org.apache.maven.wrapper.PathAssembler;
import org.apache.maven.wrapper.WrapperExecutor;

/** Resolves the pinned wrapper ZIP location without downloading or launching Maven. */
class DistributionCachePath {
    public static void main(String[] args) {
        var properties = Path.of(".mvn/wrapper/maven-wrapper.properties");
        var configuration = WrapperExecutor.forWrapperPropertiesFile(properties).getConfiguration();
        var cache = new PathAssembler(Path.of(args[0])).getDistribution(configuration);
        System.out.println(cache.getZipFile());
    }
}
