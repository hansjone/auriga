package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.persistence.UuidV7;
import auriga.contracts.iam.v1.*;
import com.google.protobuf.UnknownFieldSet;
import java.util.List;
import org.junit.jupiter.api.Test;

class NetworkConnectionAnchorBoundaryTest {
  final IamGrpcNetworkAuthorityTest f = new IamGrpcNetworkAuthorityTest();
  static final UnknownFieldSet UNKNOWN =
      UnknownFieldSet.newBuilder()
          .addField(99, UnknownFieldSet.Field.newBuilder().addVarint(1).build())
          .build();

  NetworkConnectionAnchor anchor() {
    return NetworkConnectionAnchor.newBuilder()
        .setAnchorId(UuidV7.next().toString())
        .setConnection(f.connection())
        .build();
  }

  void read(ReadNetworkConnectionAnchorRequest request) {
    f.service.readNetworkConnectionAnchor(request, new IamGrpcDevicesSpringTest.Capture<>());
  }

  @Test
  void rejectsMissingMalformedAndUnknownNestedReferencesBeforePersistence() {
    var a = anchor();
    var ok = ReadNetworkConnectionAnchorRequest.newBuilder().setOriginal(a).setCurrent(a).build();
    f.collab(
        () -> {
          for (var bad :
              List.of(
                  ok.toBuilder().clearOriginal(),
                  ok.toBuilder().clearCurrent(),
                  ok.toBuilder().setUnknownFields(UNKNOWN)))
            IamGrpcNetworkAuthorityTest.error("common.forbidden", () -> read(bad.build()));
          var malformed = new java.util.ArrayList<NetworkConnectionAnchor>();
          malformed.add(a.toBuilder().clearConnection().build());
          malformed.add(a.toBuilder().setUnknownFields(UNKNOWN).build());
          for (String id : List.of("", "bad", java.util.UUID.randomUUID().toString()))
            malformed.add(a.toBuilder().setAnchorId(id).build());
          for (var field : NetworkAuthorityConnection.getDescriptor().getFields())
            malformed.add(
                a.toBuilder().setConnection(f.connection().toBuilder().clearField(field)).build());
          malformed.add(
              a.toBuilder()
                  .setConnection(f.connection().toBuilder().setUnknownFields(UNKNOWN))
                  .build());
          malformed.add(
              a.toBuilder()
                  .setConnection(
                      f.connection().toBuilder()
                          .setTenantId(java.util.UUID.randomUUID().toString()))
                  .build());
          for (var bad : malformed) {
            IamGrpcNetworkAuthorityTest.error(
                "common.forbidden", () -> read(ok.toBuilder().setOriginal(bad).build()));
            IamGrpcNetworkAuthorityTest.error(
                "common.forbidden", () -> read(ok.toBuilder().setCurrent(bad).build()));
          }
        });
    verifyNoInteractions(f.authority);
  }

  @Test
  void responseMustBeCompleteExactAndNeverPermissionBearing() {
    var a = anchor();
    var request =
        ReadNetworkConnectionAnchorRequest.newBuilder().setOriginal(a).setCurrent(a).build();
    var good =
        ReadNetworkConnectionAnchorResponse.newBuilder().setOriginal(a).setCurrent(a).build();
    f.collab(
        () -> {
          for (var bad :
              List.of(
                  good.toBuilder().clearOriginal(),
                  good.toBuilder().clearCurrent(),
                  good.toBuilder().setOriginal(anchor()),
                  good.toBuilder().setCurrent(anchor()),
                  good.toBuilder().setUnknownFields(UNKNOWN))) {
            when(f.authority.readConnectionAnchor(request)).thenReturn(bad.build());
            IamGrpcNetworkAuthorityTest.error("common.unavailable", () -> read(request));
          }
          when(f.authority.readConnectionAnchor(request)).thenReturn(good);
          var capture = new IamGrpcDevicesSpringTest.Capture<ReadNetworkConnectionAnchorResponse>();
          f.service.readNetworkConnectionAnchor(request, capture);
          assertEquals(good, capture.values.getFirst());
          when(f.authority.readConnectionAnchor(request))
              .thenThrow(AurigaException.of(CommonErrors.FORBIDDEN, "owner", "private"));
          IamGrpcNetworkAuthorityTest.error("common.forbidden", () -> read(request));
          doThrow(new IllegalStateException("private"))
              .when(f.authority)
              .readConnectionAnchor(request);
          IamGrpcNetworkAuthorityTest.error("common.unavailable", () -> read(request));
        });
    verifyNoInteractions(f.sources);
  }
}
