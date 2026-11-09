---
title: 用户、对话与持久工作区
subtitle: OlixOps · 2026-11-04 · 设计已确认，阶段一已交付，后续能力仍待开发
template: sheet
theme: blueprint
lang: zh
cols: 2
---

用户拥有个人逻辑 Namespace，工作区归属该空间。
对话引用工作区，沙箱销毁不删除代码。

## A 第一版怎么用
```flow LR
创建工作区 -> 启动沙箱A: 挂载 PVC
启动沙箱A -> 写入代码: /workspace
写入代码 -> 销毁沙箱A: 保留 PVC
销毁沙箱A -> 启动沙箱B: 等待旧消费者退出
启动沙箱B -> 继续开发: 读取原文件
```

一个工作区对应一个 PVC，同时只允许一个沙箱占用。
源码、数据、输出可保留。进程和系统安装不会自动保留。

## B 谁负责什么
| 对象 | 责任 |
|---|---|
| User / Project | 身份、授权、配额 |
| Workspace | 长期文件，一个用户可有多个 |
| Conversation | 引用工作区，不拥有卷 |
| Sandbox | 临时运行，可重建 |
| PVC / PV | 工作区实际存储 |

一个对话绑定一个当前工作区，多对话可引用同区。
共用工作区就共用当前文件，不会自动生成开发分支。

## C 状态怎么展示
| 维度 | 状态 |
|---|---|
| 工作区 | Creating → Available → Trashed → Deleting → Deleted |
| 存储 | Pending / Bound / Lost / Unknown |
| 挂载 | Reserved → Attaching → Attached → Detaching → Released |

异常用 Error 或 Unknown 表达，并显示核查原因。
首次等待消费者时，Pending 也可允许启动沙箱。
创建结果不明时，保留独占并核查原操作。
删除接口成功后，还要确认旧工作负载和 Pod 退出。

## D 权限和数据保护
| 规则 | 第一版方案 |
|---|---|
| 普通成员 | 仅管理自己的工作区 |
| 项目管理者 | 管理同项目工作区，保留原 owner |
| 停止、闲置、归档 | 保留数据，不自动删除 |
| 删除或归档对话 | 仅改变引用，不删除工作区 |
| 回收站 | 建议保留 7 天，可恢复 |
| 永久清除 | 仅处理主动删除且保留到期的数据 |

默认值是方案建议，需要在开发配置中落定。
归档和回收站都占存储配额。7 天不是闲置清理期限。

## E 为什么这样设计
| 参考 | 借鉴的设计 |
|---|---|
| JupyterHub | 用户 PVC 跨 Pod 保留和复用 |
| Coder | 持久数据与临时计算分开 |
| Codespaces | 停止、重建和删除语义分开 |
| Daytona | 独立卷管理与使用中删除保护 |

[JupyterHub](https://z2jh.jupyter.org/en/stable/jupyterhub/customizing/user-storage.html) · [Coder](https://coder.com/docs/admin/templates/extending-templates/resource-persistence/) · [Codespaces](https://docs.github.com/en/codespaces/about-codespaces/understanding-the-codespace-lifecycle) · [Daytona](https://www.daytona.io/docs/en/volumes/)

对话绑定是本平台的业务设计，需单独验证。
不能把产品文档当作当前集群已经具备的保证。

## F 开发顺序和验收
| 阶段 | 产出 |
|---|---|
| 设计确认 | 先评审产品规则，再开发 |
| A | PVC、权限、独占、启停、故障核对 |
| B | 自助工作区、归档、回收站 |
| C | 对话引用、绑定历史、执行上下文 |
| D | 本地 AI 接入与真实集群验收 |

核心验收：A 写文件 → 销毁 A → B 读同一文件，哈希一致。
补测对话删除保留、越权、并发与切换目标不串目录。
产品细节见同目录 workspace-product-draft.md。

## G 需要确认的推荐值
| 决策 | 推荐 |
|---|---|
| 工作区数量 | 一用户可创建多个，项目配额约束 |
| 默认模板 | 每区申请 10Gi，每项目 5 区 / 50Gi |
| 并发执行 | 同区一个运行实例、一个主动执行上下文 |
| 数据保留 | 默认长期保留，删除后进 7 天回收站 |
| 对话接入 | 先保存外部引用，不重建聊天系统 |

容量建议已用于开发环境。回收站与执行上下文尚未实现。
第一阶段实际交付以 workspace-phase1.md 为准。
