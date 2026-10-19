# Java 开发

## 环境与构建

需要 JDK 25；根父 POM 的 groupId 是 `io.github.hansjone.auriga`，版本为
`0.1.0-SNAPSHOT`。导入 Spring Boot **4.1.1** BOM（建立骨架时 Maven Central
实际发布的最新 4.1.x 稳定版），Wrapper 固定 Maven **3.9.16**。

Linux/macOS 首次检出后 `chmod +x mvnw`，在仓库根目录执行：

```sh
./mvnw -B verify
./mvnw spotless:check
./mvnw spotless:apply
```

Windows 使用 `.\mvnw.cmd -B verify`。PowerShell 中带点号的属性参数整体加引号，
例如 `'-DskipTests=true'`。首次构建下载工具与依赖，需要联网；无需 Docker、全局
Maven、Node 或 npm。独立 Node 22.19.0 和 npm 缓存均放在 contracts 模块的 `target`。
Windows Wrapper 用 JDK 源文件启动下载器，让引导下载与 Maven 使用同一 Java 信任库，
避免 PowerShell TLS 下载差异；wrapper jar 不提交。不要在仓库写入本机缓存路径。

`verify` 包含契约生成与编译、JUnit、ArchUnit 和 Spotless。选择
**google-java-format 1.33.0**（Spotless 3.10.4），只检查各模块的 `src/main/java`
和 `src/test/java`，生成代码不参与格式检查。

## 模块与装配

根 POM 直接聚合全部 Java 模块：

- `services/contracts`：从根目录只读契约生成的 Java/gRPC 和 HTTP 类型。
- `services/common`：租户、outbox 与事件、审计、错误、安全、gRPC、可观测等公共基础设施（见 `common.md`），不能依赖服务。
- `services/{iam,org,workspace,resource,knowledge,collab,audit,ops}/{api,impl}`：
  每个服务的公开接口和实现，artifactId 如 `iam-api`、`iam-impl`。
- `services/test-support`：嵌入式 PostgreSQL 17 的 JUnit 5/Spring 基座。
- `services/platform-app`：依赖所有 impl 的合并部署入口。
- `services/architecture-tests`：导入全部生产模块字节码检查边界。

每个服务有配置入口，默认启用。用 `auriga.services.<服务>.enabled=false` 关闭。
新增 Bean 应在对应入口配置中导入或声明，避免通过全局组件扫描绕过开关。
业务 Bean、迁移与远程适配器由 K2 添加。

可执行包为 `services/platform-app/target/platform-app-0.1.0-SNAPSHOT-exec.jar`。
普通 jar 保留供架构测试导入，避免 Boot 重打包隐藏类。实际运行需配置
`SPRING_DATASOURCE_URL`、`SPRING_DATASOURCE_USERNAME`、`SPRING_DATASOURCE_PASSWORD`。
生产进程不自动启动测试数据库。`/actuator/health` 可用，默认只公开该端点。
冒烟测试使用随机 HTTP 端口，结束后关闭应用、连接池和 PostgreSQL 进程。

## 契约生成

protobuf-maven-plugin 下载 protoc，输入根 `contracts/proto`，生成消息和 gRPC
存根。包名遵循 proto 的 `java_package`：
`io.github.hansjone.auriga.contracts.{iam,org,node}.v1`。

OpenAPI 生成先由 frontend-maven-plugin 安装独立 Node，按
`services/contracts/package-lock.json` 运行 `npm ci`，再执行
`services/contracts/scripts/bundle.mjs`。脚本用固定版 Redocly 解析跨文件的
OpenAPI / `identity/*.schema.json` 引用，输出
`services/contracts/target/bundled/{iam,org,workspace}.json`。仅在生成中间文件中
移除 `/login/callback/*` 的 GET/POST 两个操作，源契约不变，回调留待手写。

openapi-generator Spring generator 以 `interfaceOnly`、Spring Boot 4、Jakarta、
Jackson 3 模式生成接口与模型，包名为
`io.github.hansjone.auriga.<服务>.web.api` / `.web.model`。
关闭不需要的 supporting files，避免三个文档生成同名全局配置类。
所有输出位于 `target/generated-sources`，无需提交。

选择 Redocly 而非另加 Java parser，是为了生成与 CI 共用引用解析行为，并在
Windows/Linux 上一致运行。`web.api`、`web.model` 是契约生成的保留包，不放手写业务实现。

## PostgreSQL 测试基座

使用 Zonky embedded-postgres **2.2.2**，binaries BOM 固定 **17.11.0**，包含
Windows/Linux amd64 原生二进制，只监听 loopback 随机端口。无需 Docker。
每个 JUnit JVM 一个数据库进程，具体测试类各有独立数据库；类内方法自行清理数据。

测试模块把 `test-support` 加为 test scope 依赖。普通 JUnit 测试注册扩展后，可用
`DataSource` 或 `PostgresExtension.Database` 参数注入；Spring 测试例子：

```java
@SpringBootTest(webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT)
@ExtendWith(PostgresExtension.class)
@DirtiesContext
class ExampleTest {
  @DynamicPropertySource
  static void postgres(DynamicPropertyRegistry registry) {
    PostgresExtension.properties(ExampleTest.class, registry);
  }
}
```

必须同时注册扩展；扩展在 JUnit 根 Store 关闭时关闭 PostgreSQL，Spring 用
`@DirtiesContext` 先关闭连接池。传入具体测试类，避免子类间误用同一个数据库。
`PostgresExtension.database(ExampleTest.class)` 可取得 DataSource/JDBC URL。
Linux initdb 不允许 root，应使用普通用户（GitHub runner 已满足）。其他 CPU 架构
需按 Zonky 文档增加对应 binaries 依赖。

## 架构规则与新增服务

ArchUnit 导入全部模块的生产字节码，排除测试夹具。实现只能依赖别的服务的 api；
api 不能依赖自己或其他服务的实现；common 和生成契约不能依赖服务。
`contracts..` 和 `<服务>.web.{api,model}` 保留包视为生成契约。
platform-app 可装配全部实现。反例使用独立 `fixtures.forbidden` 命名空间，逐类断言
每种违规被拒绝；另有合法夹具，检查允许自身实现与跨服务 api，防止规则误伤。
扫描测试断言全部服务与生成代码真实存在，防止空扫描假通过。

各 impl 的 Maven Enforcer 在 validate 阶段禁止直接或传递依赖任何 `*-impl`，
因此即使未写引用，给 `org-impl` 声明 `iam-impl` 也失败。

新增服务时：在根 POM 注册 api/impl，沿用包名和 artifactId；impl 依赖自身 api、common
和 contracts，复制 service-boundary Enforcer；创建带开关的配置入口；在 platform-app
和 architecture-tests 添加依赖，更新架构规则服务名集合和扫描断言。公开 Java 接口
放 api，实现留在服务包；HTTP/gRPC 类型从契约生成。

参考：[Spring Boot](https://docs.spring.io/spring-boot/)、
[Zonky](https://github.com/zonkyio/embedded-postgres)、
[Spring generator](https://openapi-generator.tech/docs/generators/spring/)。
