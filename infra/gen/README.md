# 生成类型

本模块不定义业务DTO。protobuf 源在 `contracts/proto`，OpenAPI源在 `contracts/openapi`。

- protobuf：从 `infra` 执行 `go run generate.go`，固定现有buf/protoc插件生成并gofmt `gen/auriga`。
- AI/资源HTTP模型：从仓库根执行 `./mvnw -Pgo-openapi-models -pl services/contracts -am generate-sources`，
  再执行 `gofmt -w infra/gen/aiapi infra/gen/resourceapi`。
  Windows用 `mvnw.cmd`。复用固定OpenAPI Generator 7.26.0的Go模型和标准库utils，
  不生成客户端、嵌套go.mod或新增第三方依赖。每次仅清理这两个专属生成目录，勿在其中放手写代码。
- 日常Java构建默认不重写跟踪的Go HTTP文件；Java CI的Linux job显式启用上述profile并检查漂移，
  Windows Java/Go CI仍编译已提交类型。
- `contracttest` 是手写测试，检查四份共享JCS/Ed25519向量和生成模型的presence/null语义，
  不实现生产签名消费。Java签名测试会写本次临时公钥/签名到
  `services/contracts/target/java-signature-vectors.json`；将此绝对路径设为
  `AURIGA_JAVA_SIGNATURE_VECTORS` 再 `go test ./...`，可验证实际Java签名→Go验签。
  未设置此变量时仅跳过可选跨进程传递测试，共享golden向量仍必测。
  Java Linux CI会先检查该签名文件确实存在，再显式设置变量运行contracttest；
  这条Java实际签名→Go验签桥是必跑门禁，不允许仅依赖普通Go job中的可选跳过。

受签名UTC时间必须保留原文本和小数精度；bundle脚本识别源schema中的标准 `$comment`
`auriga-preserve-signature-text`，仅为生成把date-time映射成string。
源JSONSchema继续严格date-time校验；不得把生成类型或字段注解当作完整业务/安全校验器。
例如金额上限、枚举、签名算法、跨字段时间比较、scope、ACL和撤销都要由后续实现显式验证。
