# 验证记录

日期：2026-10-30。目标为本地新平台、独立 PostgreSQL 开发库、既有 OpenSandbox Server。

## 自动化

- PostgreSQL 集成测试 + Go race detector：通过。
- JWT 缺失、缺 exp、缺用户身份、HS256 算法降级：拒绝。
- B 对 A 沙箱的 sync/delete/renew/command/logs/files/read/write/remove/preview：全部拒绝，未调用上游数据面。
- 普通成员无法自行升权，查询列表不包含其他成员的沙箱。
- 同一创建幂等键仅调用一次上游；相同键改变参数返回冲突。
- 20 个并发配额预留、项目上限 3：只成功 3 个。
- 上游 503 导致 Unknown：保留配额、不重放 create；后续按 operationId 找回已创建资源。
- 晚到状态更新不能复活 Deleted 资源。
- 数据面保留 /osb 前缀，固定可信服务地址，不跟随 Endpoint 的其他 host。
- 预览禁止系统端口、外部绝对 URL、点路径穿越及重复编码穿越。
- `go vet ./...`、后端 `go build`：通过。
- `npm run build --prefix web`：类型检查、生产构建通过。
- `npm audit --prefix web`：0 vulnerabilities。
- swaggo 生成和独立 OpenAPI JSON：已产出。

## 浏览器

真实 JWT 登录、新建「平台验收项目」、添加 alice 成员、审计列表、沙箱创建弹窗、取消操作：通过。1440px / 390px 视口无页面级横向溢出。浏览器测试没有通过创建按钮调用集群创建接口；真实创建通过下面的 smoke 脚本完成。

## 真实 OpenSandbox

Server：`http://efficient-lab.qiuer.net/osb`。通过平台 API 创建 10 分钟 TTL 的 `python:3.12` 测试沙箱：

- 平台 ID：`7fe88a4e734679c3b4d8ac378ccaf8ae`。
- 创建并达到 Running：通过。
- 远端执行命令并获取 stdout：通过。
- 上传 /workspace/smoke.txt，下载并验证中文内容：通过。
- 后台启动 Python HTTP 服务，平台代理访问 8000/smoke.txt 并校验正文：通过。
- 删除文件：通过。
- 销毁沙箱，再同步确认 Deleted：通过。

脚本 `python3 scripts/smoke.py --real` 会创建一个真实沙箱，执行后在 finally 清理。正常不需要重复执行它来启动平台。集成测试使用模拟上游，与上述真实联调分别记录。

## 尚未验证

公司 OIDC、生产 JWT 签发契约、TLS 入口、生产 PostgreSQL、Docker 镜像实际构建与 Kubernetes 部署、PVC 持久工作区、完整预览/终端、多副本故障恢复及负载上限，均不在本轮完成范围。

## 本地账号密码登录补充验证（2026-10-30）

- PostgreSQL + race：本地引导用户密码为 bcrypt 哈希；错误账号/密码统一拒绝；JWT 验签、用户身份与 jti 正确。
- 退出后旧 JWT 立即失效；停用用户、会话到期均被拒绝；无会话的旧开发 JWT 被拒绝。
- 重建服务后账号密码不被引导配置覆盖，有效会话继续可用。单 IP 第 11 次登录请求被限流。
- 浏览器：错误密码中文提示、正确登录、密码显示/隐藏、已有项目可见、刷新保留登录、退出返回登录页。
- 390px 与 1440px 登录页无页面横向溢出。前端类型检查和生产构建、go vet、Go 后端构建通过。
- 本轮不创建新的远程沙箱。

## 持久化工作区第一阶段（2026-11-04）

完整记录见 [workspace-phase1.md](workspace-phase1.md)。全量 PostgreSQL race 测试通过；新增针对性测试覆盖逻辑 Namespace、对话绑定与版本、并发配额/独占、权限与数据保留、UID 漂移和 BatchSandbox 消费者。真实集群两个沙箱复用同一 PVC，文件哈希一致，计算实例已清理，工作区和对话保留。前端构建、Go vet/build 通过；浏览器工作区与对话定位页面可见。

浏览器补充通过：定位原 PVC、归档与取消归档、持久挂载创建弹窗（取消创建），1440px/390px 无页面横向溢出。测试没有额外提交浏览器创建实例。
