package auriga.platform;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.iam.EmergencyAccounts;
import auriga.iam.IamStore;
import auriga.iam.ProviderManager;
import auriga.iam.SigningKeys;
import java.nio.file.Files;
import java.nio.file.Path;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.jupiter.api.io.TempDir;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.DefaultApplicationArguments;
import org.springframework.boot.test.system.CapturedOutput;
import org.springframework.boot.test.system.OutputCaptureExtension;
import org.springframework.mock.env.MockEnvironment;

@ExtendWith(OutputCaptureExtension.class)
class DevBootstrapTest {
  @TempDir Path directory;
  final SigningKeys keys = mock(SigningKeys.class);
  final EmergencyAccounts emergency = mock(EmergencyAccounts.class);
  final ProviderManager providers = mock(ProviderManager.class);
  final IamStore store = mock(IamStore.class);

  @SuppressWarnings("unchecked")
  DevBootstrap bootstrap(MockEnvironment env) {
    return new DevBootstrap(
        keys,
        emergency,
        providers,
        store,
        env,
        mock(ObjectProvider.class),
        mock(ObjectProvider.class),
        mock(ObjectProvider.class));
  }

  @ParameterizedTest
  @ValueSource(strings = {"combined", "prod", "default"})
  void accidentalProductionSeedHasNoSideEffectsAndWarns(String profile, CapturedOutput output)
      throws Exception {
    MockEnvironment env =
        new MockEnvironment()
            .withProperty("auriga.platform.combined", "true")
            .withProperty("auriga.platform.seed", "true")
            .withProperty(
                "auriga.platform.enrollment-file", directory.resolve("enrollment.json").toString());
    env.setActiveProfiles(profile);
    bootstrap(env).run(new DefaultApplicationArguments());
    verifyNoInteractions(keys, emergency, providers, store);
    assertFalse(Files.exists(directory.resolve("enrollment.json")));
    assertTrue(output.getAll().contains("Dev seed ignored"));
  }

  @ParameterizedTest
  @ValueSource(strings = {"dev", "e2e"})
  void explicitTestProfileAllowsSeedAndWarns(String profile, CapturedOutput output)
      throws Exception {
    Path enrollment = directory.resolve("enrollment.json");
    Files.writeString(enrollment, "{}");
    MockEnvironment env =
        new MockEnvironment()
            .withProperty("auriga.platform.enrollment-file", enrollment.toString());
    env.setActiveProfiles("combined", profile);
    when(store.isolated(eq(SigningKeys.PLATFORM), any())).thenReturn(true);
    bootstrap(env).run(new DefaultApplicationArguments());
    verify(store).isolated(eq(SigningKeys.PLATFORM), any());
    verify(store).tenant(eq(DevBootstrap.DEFAULT_TENANT), any());
    assertTrue(output.getAll().contains("Dev seed enabled"));
  }
}
