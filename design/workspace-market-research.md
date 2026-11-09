# 持久化工作区：成熟产品模式调研

调研日期：2026-11-04。状态：设计参考，不是功能交付或生产认证。仅依据产品官方文档；未部署这些产品做实测，也不把文档能力等同于当前 OpenSandbox 集群能力。

## 1. 结论

本项目可参考的共同模式是：**稳定的用户/工作区身份 + 可停止重建的计算资源 + 明确管理的持久数据 + 单独的删除策略**。

与当前 Kubernetes/OpenSandbox 组合最贴近的是 JupyterHub 的用户 PVC 重用，以及 Coder 的持久资源与计算资源分离；Daytona 提供了独立卷管理和“使用中不可删除”的参考；Codespaces 提供停止、重启、数据范围和保留策略的产品表达。

这不意味着要照搬某一家实现。尤其是以下内容属于本平台的设计选择，而不是本次调研证明的行业统一标准：

- 一个用户可以创建多个独立工作区。
- 对话关联工作区，并在后续对话继续使用。
- 工作区默认不因闲置自动删除。
- 第一版一个工作区同一时刻只允许一个沙箱挂载。
- 删除先进入可恢复回收站。

本次查阅的资料没有为“每个 AI 对话必须拥有一个卷”“对话删除自动删卷”提供依据，不能据此声称这些是成熟产品的通用设计。

## 2. 版本与资料边界

| 产品 | 本次资料范围 | 页面日期说明 |
|---|---|---|
| Coder | 官方 `/docs`，页面导航显示 `v2.39 Mainline` | 已查看页面未标注明确发布日期/最后更新日；不将 Mainline 称为企业已验证的稳定版本 |
| GitHub Codespaces | 官方 GitHub Docs 当前在线文档 | 已查看页面未标注明确发布日期/最后更新日；具体政策可能受组织限制 |
| Daytona | 官方 `www.daytona.io/docs/en/`，页面显示 `v0.227 Latest` | 另列 `v2.0 Alpha`，本调研不引用 Alpha 的行为推断当前正式线 |
| JupyterHub | Zero to JupyterHub 官方 `/en/stable/` 文档 | 已查看页面未标注明确发布日期/最后更新日；stable 别名会更新，开发时需锁定部署 Chart/KubeSpawner 版本 |

版本标记和页面内容来自调研当天的在线资料，不能作为以后无需核查的永久版本结论。

## 3. Coder：稳定工作区、可替换的计算

**官方事实：**

1. 用户可以从模板创建自己的工作区，并通过页面或 CLI 管理；工作区面向开发者或 coding agent。[Manage workspaces](https://coder.com/docs/user-guides/workspace-management)
2. 工作区可包含持久资源和临时资源；停止时持久资源保留，临时资源可以销毁重建。删除工作区则销毁两类资源，删除流程不可逆。[Workspace lifecycle](https://coder.com/docs/user-guides/workspace-lifecycle)
3. 存储资源应依赖不可变的 owner/workspace ID，避免用户改名或工作区改名导致 Terraform 重新创建卷。[Resource persistence](https://coder.com/docs/admin/templates/extending-templates/resource-persistence/)

**对本平台的设计启发：**

工作区名是展示字段；PVC 名和资源关联使用不可变 workspaceId。采用“停止运行但保留文件”的按钮语义；创建/启动/停止/删除分别有操作记录。镜像和启动脚本用于重建环境，不把 Pod ID 当长期工作身份。

**不应照搬：**

Coder 持久性由模板控制，不是任何模板都天然安全。其 Terraform 删除路径、权限和自动休眠策略与当前 OpenSandbox 平台不同；本项目不能直接继承这些保证，也不应自动引入 Terraform 作为 PVC 管理前提。

## 4. GitHub Codespaces：停止保留、重建有明确范围

**官方事实：**

1. Codespace 停止会结束运行进程，但已保存的项目变更可供下次启动使用；停止后仍产生存储占用。[Codespace lifecycle](https://docs.github.com/en/codespaces/about-codespaces/understanding-the-codespace-lifecycle)
2. 同一文档说明容器重建保留 `/workspaces`，其外的容器修改会清除；删除 codespace 也会删除尚未另存的工作。
3. 官方提供保留期设置和删除前提示，并存在保留例外；不能把 Codespaces 理解为所有工作区默认永久保存。[Automatic deletion](https://docs.github.com/en/codespaces/setting-your-user-preferences/configuring-automatic-deletion-of-your-codespaces)

**对本平台的设计启发：**

页面明确“停止释放计算，但存储继续占用配额”。固定说明 `/workspace` 是本平台持久目录。删除前展示数据影响；保留策略应可见，不能隐藏在后台默认值里。

**不应照搬：**

用户要求隔几天继续使用原数据，因此本平台建议默认不因闲置自动删工作区。将来如公司要求到期清理，应先有明确政策、到期提示和例外机制，而非直接复制 Codespaces 的默认周期。Git 提交也不能替代未提交文件、数据集和输出文件的工作区保留。

## 5. Daytona：卷作为独立资源管理

**官方事实：**

1. 正式文档线将卷作为独立资源，支持创建、查询、列出、挂载和删除；内容独立于单个沙箱生命周期。[Volumes](https://www.daytona.io/docs/en/volumes/)
2. 卷删除异步执行，活动沙箱占用时返回 409；同一页面说明并发写同路径可能覆盖，卷并非事务系统。[Delete volumes / Share data](https://www.daytona.io/docs/en/volumes/#delete-volumes)
3. 资源以组织分组，有个人组织和协作组织，协作组织提供角色权限。[Organizations](https://www.daytona.io/docs/en/organizations/)

**对本平台的设计启发：**

为工作区提供独立列表和管理入口；异步操作有状态；使用中禁止删数据。组织级资源边界与用户对象权限要分开考虑，不能只凭接入一把共享 API Key 就获得用户隔离。

**不应照搬：**

Daytona 卷文档采用 FUSE/S3，并推荐共享卷加 subpath；本项目拟用 Kubernetes PVC。它们不是相同存储实现，不能推断具有相同一致性、性能、容量或删除语义。第一版选择一个工作区一个 PVC 和独占挂载，是为当前驱动与权限模型降低复杂度，并非宣称 Daytona 也是如此。`v2.0 Alpha` 的新快照、内存等语义不纳入本项目承诺。

## 6. JupyterHub：与用户绑定的 PVC 跨 Pod 重用

**官方事实：**

1. Zero to JupyterHub 使用 Kubernetes PVC/PV 管理用户数据，每个用户有自己的 PVC。
2. 用户关闭服务器时 Pod 删除、卷卸载，PVC/PV 仍保留；下次启动检测并挂载已有 PVC。
3. 已有用户的 PVC 不会因为修改模板就自动迁移存储类型；删除原 PVC 会有数据损失风险。

上述三点均见官方 [Customizing User Storage](https://z2jh.jupyter.org/en/stable/jupyterhub/customizing/user-storage.html)。

**对本平台的设计启发：**

Kubernetes 存储资源和计算 Pod 分开管理，稳定身份指向 PVC，Pod 可随时替换。PVC 不以沙箱为垃圾回收 owner；存储规格调整与数据迁移应是独立流程。

**不应照搬：**

该页面的基本模式是用户主目录，而本平台需要一个用户管理多个工作区、可与不同对话关联，不能直接缩减成每用户一个永久共享目录。也不能把“重用已有 PVC”推导为硬盘故障后数据必然可恢复。

## 7. 对照表

表格是对前述官方事实的压缩归纳；“建议借鉴”是本项目推论。

| 产品 | 身份归属 | 计算与存储关系 | 停止 / 删除 | 保留与归档 | 建议借鉴 |
|---|---|---|---|---|---|
| Coder | 用户有工作区，可按模板管理 | 模板区分持久与临时资源 | 停止保留持久资源；删除销毁资源 | 可配置调度/休眠，不能等同永久保留 | 稳定工作区 ID、不可变卷身份、生命周期拆分 |
| Codespaces | 用户创建 codespace，关联开发项目 | 停止保存工作；重建保留指定目录 | 停止终止进程；删除影响未另存数据 | 有保留期与例外，非默认无限期 | 明确保存路径、计算/存储占用、删除提示 |
| Daytona v0.227 文档线 | 组织作为资源边界 | 独立 Volume 可跨沙箱使用 | 卷单独删除；占用中阻止删除 | 卷生命周期不依赖单个沙箱 | 独立卷管理、异步操作、占用保护 |
| JupyterHub stable | 用户身份关联 PVC | 用户 Pod 与 PVC 分离 | 关闭用户服务器保留 PVC | 原卷后续启动重用；删除需单独管理 | 用户数据跨 Pod 持续、平台处理资源关联 |

## 8. 推荐给 OlixOps 的取舍

以下是基于调研和本项目需求提出的草案，仍需用户确认：

1. Project 管理成员和配额；User 是权限主体；Workspace 是用户拥有的长期数据资源。
2. 一个用户有多个 Workspace；第一版每个 Workspace 一个 PVC，固定挂载 `/workspace`。
3. Sandbox 是可更换的计算实例；停止运行或到期不删除 Workspace/PVC。
4. Conversation 保存外部对话引用，关联 Workspace；它是业务上下文，不是 PVC 的 Kubernetes owner，也不是权限主体。
5. 同一工作区可以由多次对话继续使用，但第一版串行操作运行实例，避免多个 AI 同时修改同一份代码。
6. 对话结束/归档/删除默认不删工作区；工作区删除必须独立确认，经过回收站。
7. 工作区默认不因闲置清理，存储配额持续计入；停止和归档都不能显示为“免费且不占容量”。

## 9. 证据不足与开发前验证

- 本调研没有证明“对话关联”是四个产品的统一标准；需要按 OlixOps 的本地 AI/未来对话系统接口设计。
- 没有证明当前 OpenSandbox Server/Controller、Namespace 配置、存储驱动已经满足设计；应使用目标版本和测试工作区做挂载/销毁/重建验收。
- 持久化、备份、可复现环境、进程恢复和版本管理是不同能力；不能因为工作区存在就承诺其他能力。
- 需要验证 PVC UID、占用观察、删除保护、WFFC Pending、RWO 多 Pod 情况以及 local-path 节点故障行为。
- 文档结论可用于确认产品模型，不能代替真实故障和恢复测试；本轮不进入开发。
