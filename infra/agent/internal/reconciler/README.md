# reconciler

每实例串行对账，准备并发受限。Apply 固定完整 spec 深拷贝，并持久 desired 后入队。

真实 Docker 的 ReleaseActivation 从同一个外层同步生命周期统筹 Ensure、执行记录落盘和 Start；
准备与启动分别使用子 timeout，cancelPrep 不会提前取消外层资格。
desired=stopped 首次创建仍授权；已存在实例的停止不受签名过期阻碍。
状态写入失败不会继续创建/启动或发布 Ready，删除失败不会丢掉 desired/执行记录。

重启、重连和 PendingRestart 从独立实际执行记录恢复原 generation/发行，
不会把更新后的 desired 签名套在旧容器上。旧记录、当前 ring/策略/证据/高水位和真实容器均须重验；
无记录不按标签猜测可信身份。恢复失败撤 Ready 和对外端点，保留现存运行进程与重启决策。

控制流中的 restart 与 Apply/Delete 使用同一实例队列。节点不自行增加 generation，
停止后仍通过既有发行验证/启用路径应用平台下发的最新 running 规格。
命令 ID 与完整请求绑定，停止检查点及完成记录保存在本地状态库；连接断开、
节点重启或结果重传不会重复停止已完成的命令。停止/启动失败返回可重试结果，
已停止的命令从启动阶段继续。未知实例、stopped 目标、其他命令类型和非法 grace 拒绝；
grace 不得超过节点的启动超时。结果通过原 NodeControl 流回传，不另开管理入口。
