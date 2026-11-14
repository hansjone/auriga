package auriga.collab.api;

import auriga.contracts.msgnet.model.ControlPackage;
import auriga.contracts.msgnet.model.InstallationReceipt;
import auriga.contracts.msgnet.model.IsolationRecord;
import auriga.contracts.msgnet.model.ProjectionMaterial;
import auriga.contracts.msgnet.model.ProjectionReceipt;
import auriga.contracts.msgnet.model.ProjectionRequest;
import auriga.contracts.msgnet.model.ProtocolMaterial;
import auriga.contracts.msgnet.model.Publication;
import auriga.contracts.msgnet.model.RenewalStatus;
import auriga.contracts.msgnet.model.SealedMaterial;

/** collab 拥有的续期端口；调用者身份由安装认证上下文提供，不能从请求字段自认。 */
public interface NetworkRenewal {
  Publication currentPublication();

  RenewalStatus status();

  ControlPackage createControlPackage(ControlPackage request);

  IsolationRecord confirmControl(InstallationReceipt receipt);

  IsolationRecord enforceIsolation(IsolationRecord request);

  ProtocolMaterial committedProtocol(ProjectionRequest request);

  ProjectionMaterial committedTransport(ProjectionRequest request);

  SealedMaterial sealedBootstrap(ProjectionRequest request);

  ProjectionReceipt confirmProjection(ProjectionReceipt receipt);
}
