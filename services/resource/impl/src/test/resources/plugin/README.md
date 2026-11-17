# npm pack 夹具

`example-dsh-plugin-1.0.0.tgz` 是 npm 11.6.0 对下列本地文件执行 `npm pack --ignore-scripts` 的真实产物，供服务端验证 scoped npm 名、package/ 根布局和 gzip/ustar 兼容性。无外部依赖、无安装脚本。

package.json（单行、无尾换行）：

```json
{"name":"@example/dsh-plugin","version":"1.0.0","description":"pack validation fixture","license":"MIT","main":"index.js"}
```

index.js（LF 尾换行）：

```js
module.exports = {};
```

归档不执行，上传成功后保留原始字节。攻击与超预算归档由测试动态构造。
