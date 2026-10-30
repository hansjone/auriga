# 托管模型后续项

- **MANAGED-MODEL-L2**：模型调用沿 `IamManagedDevices.modelAccess` 两次调用 `DeviceAccessAuthority.credential`，后者在持有 session/device 锁的事务里经 `SessionManager` 调 workspace/org。后续把远程授权与本地只读/CAS核验分离，保持吊销及换代的并发正确性；本轮主控明确只记录、不修改这条既有事务路径。依据：MANAGED-MODEL 首轮独审 L2。
