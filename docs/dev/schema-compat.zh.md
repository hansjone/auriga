# Schema 兼容与格式版本（统一现场部署 2b）

配合 `docs/dev/site-deploy.zh.md` **D2**。

## 破坏性迁移标记

在 Flyway SQL **文件最前面的注释区**写：

```sql
-- auriga:destructive 删除旧列，N-1 镜像无法再读
```

- 可跟说明文字；大小写不敏感；须出现在文件头连续 `--` 注释/空行区内（遇到第一条非注释语句即停止识别）。
- 本提交若新增/改动了带破坏性标记的迁移，须在同一提交里把 `deploy/site/schema-compat.json` 的 `schemaCompatFloor` 前移为**本提交的父提交**（PR 里就是当前 main 头的 40 位提交号）；否则保持不动（继承父提交的 floor）。
- CI（`.github/workflows/n1-compat.yml` 的 declare，入口 `TestCIN1Gate` / `ValidateFloorAgainstParent`）相对真实父提交（`HEAD^1`）校验：没有破坏性迁移时 floor 必须与父提交声明的完全相同；有时 floor 必须非空、不同于父提交的 floor、是父提交本身或其祖先、并晚于父提交的 floor。允许落后于真实父提交，是因为 PR 合入前 main 可能前进；rollback-smoke 会对 floor 提交的镜像单独再跑一次冒烟。
- 关键字辅助（CI）：`DROP TABLE/COLUMN/TYPE/SCHEMA`、`RENAME`、`ALTER TABLE … ADD … UNIQUE/CHECK`、`ALTER TABLE … ADD COLUMN … NOT NULL` 且无 `DEFAULT`、`FORCE ROW LEVEL SECURITY` / `ALTER POLICY`。命中却未标记则失败。真实门禁仍是回退冒烟测试。
- 本设计合入前的历史迁移不追溯；新增迁移必须按上表标记或改成两阶段兼容迁移。

扫描路径：`services/**/resources/db/**/*.sql`、`services/common/src/main/resources/auriga/db/common/*.sql`。

## 源码声明

| 文件 | 含义 |
|------|------|
| `deploy/site/schema-compat.json` | `schemaVersion`、`schemaCompatFloor`（40 位 hex；空字符串=本设计合入起点）、`formatMax` |
| `deploy/site/format-versions.json` | 本版代码能读的最高格式版本；键至少含 `materials`、`collab-keys`、`materials-upgrades`、`relay` |

`formatMax` 与 `format-versions.json` 必须一致。Go：`LoadSchemaCompat` / `ValidateSchemaCompat` / `ComputeSchemaCompatFloor` / `ValidateMigrationDestructiveness`（见 `deploy/offline/schema_compat.go`）。

## 数据目录当前格式

业务就地改写后应把本流程写出的格式号合并写入 `<数据目录>/format-versions.json`（Go 辅助 `WriteFormatVersion(dataDir, key, version)`，在 `deploy/offline`；NAS bootstrap 是独立模块，接线时需要等价实现）。deploy **不会**把源码 formatMax 写入数据目录。

**现状（2b）：下列流程都还没有写入格式号**，数据目录里没有对应键时回滚检查不比较该键。当前所有格式都是 1，所以不影响正确性；但**提升任一 `formatMax` 之前必须先把对应流程接上写入**，测试 `TestFormatMaxStaysAtOneUntilWritersWired` 会拦住先提升上限的改动。

| 键 | 就地改写流程（接线入口） |
|----|----------------|
| `materials` | `deploy/nas/scripts/lib.sh` 渲染/启动时生成与更新 `$AURIGA_DATA_ROOT/config/materials`（bootstrap `runtime_materials.go`、`shared_materials.go`） |
| `collab-keys` | collab 密钥与轮换快照：`auriga-nas-bootstrap collab-rotation`（`deploy/nas/bootstrap/collab_rotation*_linux.go`，`installer-entry.sh` 的 `collab-rotation` 分支） |
| `materials-upgrades` | `auriga-nas-bootstrap materials-upgrade`（`materials_upgrade_linux.go`，由 `lib.sh`、`installer-entry.sh` 以 `-base $AURIGA_DATA_ROOT/config/materials -out …/materials-upgrades` 调用；已有 `-version 1` 参数） |
| `relay` | `deploy/nas/scripts/migrate-relay.sh`（`upgrade-admin.sh` 调用） |

接线注意：这些流程对数据目录有严格的私有元数据检查（属主、权限、未知叶子），新增 `format-versions.json` 前要确认不会被这些检查拒绝；写入应在流程成功提交之后、原子替换。

## 回滚检查语义

- **floor**：读**当前运行发行**以及历史里在目标之后运行过的每条发行的 `schemaCompatFloor`；目标提交必须不早于它们（提交本身是目标或其祖先的发行不受限；未声明的发行一律拒绝）。
- **FormatMax**：读**回滚目标**的 `formatMax`；当前数据目录格式不得超过。
- 数据与目标运行时记录完全相同（未触碰）仍直接允许（P2）。
