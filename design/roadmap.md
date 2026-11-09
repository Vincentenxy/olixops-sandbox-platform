# 平台迭代计划

## 当前交付：业务管理最小可运行版本

已打通 JWT 身份、项目角色、沙箱生命周期、基本执行与文件能力、受控 HTTP 验证、配额、幂等与审计。底层使用官方 Go SDK，不执行任意 Kubernetes PodSpec。

## 下一阶段：公司统一登录与持久化工作区

PVC 工作区的场景、资源模型、状态、回收策略和开发验收清单见 [持久化工作区方案](persistent-workspace-plan.md)。该文件是设计提案，尚未实现。

用户/对话/工作区的产品规则先按 [设计草案](workspace-product-draft.md) 评审，依据见 [官方产品调研](workspace-market-research.md)；用户已确认，第一阶段实现见 [个人 Namespace 与工作区](workspace-phase1.md)。

1. 确认公司 IdP 的 issuer、client、回调地址与角色来源，接入授权码 + PKCE / BFF 会话。
2. 已实现个人逻辑 Namespace、Workspace/PVC 供应和归属校验；后续补回收站和备份。
3. 存储供应服务使用最小 Kubernetes ServiceAccount 权限；生产用 InClusterConfig，本地显式 kubeconfig/context，不依赖当前默认 context。
4. 已实现销毁实例保留工作区；数据删除尚未开放，后续实现回收站和受控清理。
5. 接入你已验证的 local-path/PVC 场景，说明节点亲和性与备份限制。PVC 持久化不等于高可用。

## 开发体验与发布

- 后台命令取消、cursor 增量日志、SSE 输出、PTY/WebSocket 终端。
- 预览应用采用独立 origin、短时会话和每请求鉴权，避免将用户应用放在管理页面同源运行。
- 源码版本与依赖锁定 → 隔离构建任务 → 私有镜像仓库 → 发布流程。构建凭据不进入用户沙箱。

## 规模化

资源规格模板及 CPU/内存总量配额、用户级配额、创建速率限制、批量队列、状态恢复页、分页、审计归档、多集群调度。

预热池必须独立验证版本支持；当前 release-1.1.1 的 poolRef 路径不允许请求级 volumes，不能把持久化工作区的 PVC 随意加入共享池。
