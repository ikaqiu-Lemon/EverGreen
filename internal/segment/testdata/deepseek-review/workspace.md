---
id: 'ns-20260929-deepseek-harness-review'
note: 'n-20260929-deepseek-harness-review'
note_hash: 'sha256:2c01c84ee5b1e402f1c1fe96432fb2a48d3205e424cb7c839217c691b5e12f6d'
title: '对 DeepSeek Harness 的一次 deepseek：审阅式学习版'
created_at: '2026-09-29'
updated_at: '2026-10-05T16:01:32+08:00'
tags:
  - 'agent-harness'
  - 'deepseek'
  - 'self-evolution'
---

## 划分结果

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjMiXSwicmVsIjoic3VwcG9ydCIsInJlYXNvbiI6IuWOn-aWh-esrOS4gOeroOmAkOmhueWumuS5iSBNb2RlbOOAgUhhcm5lc3Mg5Lul5Y-K5LiK5LiL5paH44CB5bel5YW344CB57qm5p2f44CB6aqM6K-B44CB57qg5q2j5LqU57G76IGM6LSjIiwidGFncyI6WyJhZ2VudC1oYXJuZXNzIiwiYXJjaGl0ZWN0dXJlIiwicmVsaWFiaWxpdHkiXSwib3V0cHV0IjoiIn0 -->
### Agent 由 Model 与 Harness 组成，生产级 Harness 承担五类职责 {#cand-harness-production-five-capabilities .eg-candidate .knowledge data-slug=harness-production-five-capabilities}
> **[Knowledge Candidate]**

#### 知识内容

文章采用 `Agent = Model + Harness` 的定义。Harness 的基础职责包括：

1. **上下文管理**：决定每次请求给模型什么以及以什么顺序给出，包括系统提示词、工具定义、RAG、memory、skill 和运行状态。
2. **工具接口**：决定模型能调用什么，包括文件、命令、网络、数据库和 MCP 等外部能力。

生产环境通常还需要：

3. **约束**：通过权限、人工批准、沙箱和资源限制控制允许的动作。
4. **验证**：检查外部结果，而不是用模型自述判断任务是否完成。
5. **纠正**：在网络抖动、限流或文件占用等失败后重试、恢复或补救，并在确认无法恢复前避免暴露中间态。

![Agent Harness 五要素](https://img.zhenjia.dev/agent-harness-components.png)
图片来自李博杰《AI Agents in Depth》

#### 条件与边界

原文把上下文管理和工具接口描述为 Harness 至少具备的两部分，把约束、验证和纠正描述为生产环境通常还需要的三部分。这是职责划分，不要求五类职责分别实现为五个独立组件。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjYiXSwicmVsIjoic3VwcG9ydCIsInJlYXNvbiI6IuWOn-aWh-esrOS4gOeroOe7meWHuiBSZUFjdCDlvqrnjq_kvKrku6PnoIHjgIHlt6XlhbfosIPnlKjlm57loavov4fnqIvkuI7nu5PmnZ_mnaHku7YiLCJ0YWdzIjpbImFnZW50LWhhcm5lc3MiLCJyZWFjdCIsImFnZW50LWxvb3AiXSwib3V0cHV0IjoiIn0 -->
### ReAct Agent Loop 交替执行上下文构造、模型推理与工具行动 {#cand-react-agent-loop .eg-candidate .knowledge data-slug=react-agent-loop}
> **[Knowledge Candidate]**

#### 知识内容

一个基本 ReAct Agent Loop 按以下顺序运行：

1. 用历史消息、可用工具和上一轮工具结果构造上下文。
2. 调用模型生成响应。
3. 模型返回 `end_turn` 时结束循环。
4. 模型返回工具调用时，由 Harness 执行工具并把结果加入历史。
5. 记录模型响应并进入下一轮。

工具调用的名称、参数和结构由发送给模型的 schema 描述；模型据此选择工具并构造参数。循环是否继续则由模型的结束判断控制。

#### 条件与边界

`agent loop` 由模型判断何时结束；`loop engineering` 使用测试是否通过等外部条件控制循环，两者不是同一个概念。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjEwIiwiQjExIiwiQjEzIiwiQjE1IiwiQjE3Il0sInJlbCI6InN1cHBvcnQiLCJyZWFzb24iOiLljp_mlofnrKzkuoznq6DnlKjlkIzkuIDpgIDmrL4gQWdlbnQg5a-55q-UIFB5ZGFudGljIEFJ44CBQ2xhdWRlIEFnZW50IFNESyDkuI4gRXZlIOeahOi0o-S7u-i-ueeVjCIsInRhZ3MiOlsiYWdlbnQtaGFybmVzcyIsInNkayIsInBsYXRmb3JtIl0sIm91dHB1dCI6IiJ9 -->
### Library、Product 与 Platform 的 Agent 开发边界不同 {#cand-agent-development-boundaries .eg-candidate .knowledge data-slug=agent-development-boundaries}
> **[Knowledge Candidate]**

#### 知识内容

文章用三种形态描述 Agent 开发边界：

![Agent 开发三种流派](https://img.zhenjia.dev/harness-three-paradigms.png)
Agent 开发三种流派

- **Library（Pydantic AI）**：核心主要是循环和请求拼装；开发者直接编写工具、校验、调用流程，并承担更多运行职责。
- **Product（Claude Agent SDK）**：产品已提供循环、内置工具、权限和上下文管理；开发者通过配置、Hook 与 MCP 扩展。
- **Platform（Eve）**：平台进一步托管运行、持久化、恢复和对话入口；开发者提交约定目录中的指令、工具与 Hook。

在退款示例中，Pydantic AI 把金额检查和结果验证写进工具函数；Claude Agent SDK 分别使用 `PreToolUse` 与 `PostToolUse`，自定义工具通过 MCP 接入；Eve 用 schema 和工具 `execute` 做执行前约束，事后 Hook 只能观察而不能向模型注入反馈。

![核心的边界画在哪里](https://img.zhenjia.dev/core-boundary.png)
核心的边界画在哪里

#### 条件与边界

该比较以文章写作时 Pydantic AI、Claude Agent SDK 与 Eve 已公开的接口为准；产品更新后需要重新核验具体扩展点。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjIxIl0sInJlbCI6InN1cHBvcnQiLCJyZWFzb24iOiLljp_mlofnrKzkuoznq6DmmI7noa7ljLrliIbmj5Lku7bmnKzouqvnmoTlvaLmgIHlkozmj5Lku7bmjqXlhaXmoLjlv4PnmoTnu4Too4XmlrnlvI8iLCJ0YWdzIjpbImFnZW50LWhhcm5lc3MiLCJwbHVnaW4tYXJjaGl0ZWN0dXJlIiwiY29tcG9zaXRpb24iXSwib3V0cHV0IjoiIn0 -->
### Agent 插件可按自身形态与组装方式两个维度描述 {#cand-agent-plugin-two-axes .eg-candidate .knowledge data-slug=agent-plugin-two-axes}
> **[Knowledge Candidate]**

#### 知识内容

插件设计可以沿两个彼此独立的维度描述：

1. **插件本身的形态**：命令式插件交付会执行的函数或代码；声明式插件交付文件、配置或协议描述，由系统读取后决定行为。
2. **插件的组装方式**：命令式组装由开发者写代码注册和挂载；声明式组装通过 options、约定目录或清单描述组件，装配由核心完成。

按文章示例，Pydantic AI 是命令式插件加命令式组装；Claude Agent SDK 以声明式文件、配置和 MCP 服务为主并声明式组装；Eve 的工具与 Hook 是命令式代码，但通过约定文件树声明式装配。

#### 条件与边界

“插件形态”和“组装方式”不能合并为一个命令式/声明式标签：一个系统可以交付命令式代码，同时通过声明式清单完成装配。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjIyIiwiQjIzIiwiQjI0IiwiQjI2IiwiQjI4Il0sInJlbCI6InN1cHBvcnQiLCJyZWFzb24iOiLljp_mlofnrKzkuInnq6DlsZXnpLogRFNIIOmhueebruW4g-WxgOOAgUNvcmRpcyDmuIXljZXjgIFhcHBseShjdHgpIOaPkuS7tuOAgeeUn-WRveWRqOacnyBIb29rIOS4jiBTREsg6amx5YqoIiwidGFncyI6WyJkZWVwc2Vlay1oYXJuZXNzIiwiY29yZGlzIiwicGx1Z2luLWFyY2hpdGVjdHVyZSJdLCJvdXRwdXQiOiIifQ -->
### DSH 用 Cordis 清单把模型、工具、循环与持久化装配为插件 {#cand-dsh-cordis-plugin-layout .eg-candidate .knowledge data-slug=dsh-cordis-plugin-layout}
> **[Knowledge Candidate]**

#### 知识内容

DeepSeek Harness 使用 `*.cordis.yml` 声明组合，模型、工具、Agent Loop、会话持久化、文件系统和 JSON-RPC 服务都可以作为插件列入清单。每个插件以 `id` 标识能力槽位，以 `name` 指定当前实现包，并可携带配置。

业务插件是普通 npm 包，入口为 `apply(ctx)`。它可以通过 `ctx.tools.register` 注册工具，也可以订阅 `tools/pre-execute`、`tools/post-execute` 等生命周期事件。`post-execute` 可把验证失败转成模型能看到的纠正反馈。发送模型请求前的处理可以挂到 `agent/pre-step`；为保持缓存稳定，该位置只修改本轮新增消息。服务进程由 TypeScript SDK 驱动清单，一个实例可跨多次 `run()` 复用。

#### 条件与边界

文章描述基于 DSH 0.1.0。`agent/pre-step` 并非任意完整历史改写点，它受“只修改本轮新增消息”的缓存稳定约束。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjI5Il0sInJlbCI6InN1cHBvcnQiLCJyZWFzb24iOiLljp_mlofnrKzkuInnq6DliIbliKvop6Pph4rov5DooYzkuK3mm7_mjaLnu4Tku7bnmoTml7bpl7Tnu7TluqbvvIzku6Xlj4rpgJrov4cgaWQvbmFtZSDph43mlrDlrprkvY3lrp7njrDnmoTnqbrpl7Tnu7TluqYiLCJ0YWdzIjpbInJ1bnRpbWUtY29tcG9zaXRpb24iLCJwbHVnaW4tYXJjaGl0ZWN0dXJlIiwiY29yZGlzIl0sIm91dHB1dCI6IiJ9 -->
### 时空可组合性用稳定能力 ID 支持运行时替换实现 {#cand-dsh-spatiotemporal-composability .eg-candidate .knowledge data-slug=dsh-spatiotemporal-composability}
> **[Knowledge Candidate]**

#### 知识内容

**时间可组合性**允许软件在持续运行和接收请求时替换组件，而不必为每次变化重新打包并重启。

**空间可组合性**让依赖方在组件被替换后找到当前实现。DSH/Cordis 清单以稳定的 `id` 表示能力接口，以 `name` 表示当前实现。替换 `name` 后，依赖该 `id` 的插件按登记卸载并重新安装，从而重新绑定到新实现，而不是继续持有启动时的旧实现地址。

![Spatiotemporal composability: id is the interface, name is the implementation](https://img.zhenjia.dev/spacetime-composability-en.png)
Spatiotemporal composability: id is the interface, name is the implementation

#### 条件与边界

时间可组合性关注运行期间替换组件；空间可组合性关注组件替换后的重新定位。文中的 DSH/Cordis 通过稳定 `id`、可变 `name` 和依赖重装配同时实现这两个维度。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjMyIl0sInJlbCI6InN1cHBvcnQiLCJyZWFzb24iOiLljp_mlofnrKzlm5vnq6DmjInkv67mlLnlr7nosaHkvp3mrKHliJflh7rmj5DnpLror43jgIHnu5PmnoTljJbkuIrkuIvmlofjgIHlt6XkvZzmtYHjgIFIYXJuZXNzIOS7o-eggeS4juS8mOWMluWZqOS7o-eggSIsInRhZ3MiOlsiYWdlbnQtc2VsZi1ldm9sdXRpb24iLCJoYXJuZXNzIiwib3B0aW1pemF0aW9uIl0sIm91dHB1dCI6IiJ9 -->
### Agent 自我改进可按修改对象分为五个层级 {#cand-agent-self-improvement-levels .eg-candidate .knowledge data-slug=agent-self-improvement-levels}
> **[Knowledge Candidate]**

#### 知识内容

Agent 自我改进按修改对象可分为五级：

1. 修改提示词，例如 `CLAUDE.md`、规则文件和 `MEMORY.md`。
2. 修改结构化上下文，例如 Skill。
3. 修改工作流，例如 Sub Agent 与 Dynamic Workflow。
4. 修改 Harness 代码，即执行系统本身。
5. 修改优化器代码，即让负责选择和生成改动的优化器也改变自身策略。

第五级不仅改变被优化对象，还改变“如何决定下一次改什么”的机制。

![自我改进的五级](https://img.zhenjia.dev/self-improve-levels.png)
自我改进的五级

#### 条件与边界

这五级按被修改对象区分：前三层分别作用于文本、结构化上下文和编排，第四层开始修改 Harness 代码，第五层进一步修改优化器自身。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjM2Il0sInJlbCI6InN1cHBvcnQiLCJyZWFzb24iOiLljp_mlofnrKzkupTnq6DlrprkuYnkuInnsbvmm7TmlrDmlrnlvI_vvIzlubbor7TmmI7lrp7ml7bmjqjojZDnu5PmnpzmnKzouqvkuI3og73or4HmmI7lj5HnlJ_kuoblnKjnur_lrabkuaAiLCJ0YWdzIjpbIm1hY2hpbmUtbGVhcm5pbmciLCJvbmxpbmUtbGVhcm5pbmciLCJmZWVkYmFjay1sb29wIl0sIm91dHB1dCI6IiJ9 -->
### 离线、近线与在线学习按反馈进入更新链路的时机区分 {#cand-offline-nearline-online-learning .eg-candidate .knowledge data-slug=offline-nearline-online-learning}
> **[Knowledge Candidate]**

#### 知识内容

- **离线学习**：先积累行为日志，再批量训练模型或策略；经过评测和发布后替换线上版本，更新周期通常为小时或天。
- **在线学习**：服务运行期间持续把新行为反馈给模型或策略并增量更新，不等待下一轮完整离线训练。
- **近线学习**：介于两者之间，以几分钟或几小时为周期批量更新。

看到一次行为后立即返回相关内容，不足以证明系统在做在线学习；只有该行为进入训练过程并改变模型或策略本身，才符合严格意义上的在线学习。实时更新用户标签、embedding 或用既有模型重新排序不等同于在线训练。

#### 条件与边界

“在线”描述的是数据与更新链路在线发生，不是服务接口响应速度。三者的边界取决于更新是否改变模型或策略，以及改变发生的批次和时机。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjQwIl0sInJlbCI6InN1cHBvcnQiLCJyZWFzb24iOiLljp_mlofnrKzkupTnq6Dmj4_ov7AgUHJpbWUgQWdlbnQg5bCGIEhhcm5lc3Mg5L-u5pS55pS-5YWl5bim6K-E5rWL5LiO6K6w5b2V55qEIHJlZmluZW1lbnQg566h57q_IiwidGFncyI6WyJhZ2VudC1zZWxmLWV2b2x1dGlvbiIsImV2YWx1YXRpb24iLCJyZWZpbmVtZW50Il0sIm91dHB1dCI6IiJ9 -->
### Prime Agent 用评测与记录约束 Harness refinement {#cand-prime-agent-refinement-workflow .eg-candidate .knowledge data-slug=prime-agent-refinement-workflow}
> **[Knowledge Candidate]**

#### 知识内容

文章描述的 Prime Agent refinement 流程是：Agent 先提出 Harness 改动，再运行任务或评测，只有被判定有效的改动才被保留；改动不会在生成后未经检查直接上线。

#### 条件与边界

这是文章在 2026 年 8 月对 Prime Agent 实现方式的描述，后续版本可能变化。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjE3IiwiQjE5IiwiQjIwIiwiQjIxIl0sInJlbCI6InN1cHBvcnQiLCJyZWFzb24iOiLljp_mlofnlKjkuInnp43moYbmnrblrp7njrDpgIDmrL7kuI7or7fmsYLlj5HpgIHliY3ljovnvKnnmoTlt67lvILmlK_mjIHov5nkuIDliKTmlq0iLCJ0YWdzIjpbImFnZW50LWhhcm5lc3MiLCJleHRlbnNpYmlsaXR5IiwiYXJjaGl0ZWN0dXJlIl0sIm91dHB1dCI6IiJ9 -->
### Harness 核心边界决定扩展成本与可改范围 {#cand-harness-core-boundary .eg-candidate .opinion data-slug=harness-core-boundary}
> **[Opinion Candidate]**

#### 观点

Agent 框架不可直接修改的核心边界，决定了扩展能力可以插入的位置，也显著影响同一需求的工程成本。

#### 论据与推理

退款示例中，Pydantic AI 可在工具函数内完成检查和验证；Claude Agent SDK 可用执行前后 Hook；Eve 的事后 Hook 只能观察。对“完整请求发给 provider 前做压缩和安全审核”这一需求，Pydantic AI 可用两个处理函数，Claude Agent SDK 因缺少对应扩展点需要维护代理服务，Eve 则需借助底层 AI SDK middleware。需求相同而落点不同，说明成本由扩展点是否覆盖目标生命周期位置决定。

![核心的边界画在哪里](https://img.zhenjia.dev/core-boundary.png)
核心的边界画在哪里

#### 条件与反例

开放度越高也会把实现、兼容、运维和治理责任转移给使用者，因此“更开放”不等于总成本一定更低。若产品原生扩展点恰好覆盖需求，较封闭的核心可能反而减少交付和维护成本。

#### 待验证

需要用多个真实需求比较不同框架的实现量、运行负担和维护成本，并区分“首次实现成本”与“长期总拥有成本”。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjMyIiwiQjM4Il0sInJlbCI6InN1cHBvcnQiLCJyZWFzb24iOiLljp_mlofmr5TovoPlnKjnur_kv67mlLnmqKHlnovmnYPph43kuI7kv67mlLnmj5DnpLror43jgIHphY3nva7lkowgSGFybmVzcyDku6PnoIHnmoTnqLPlrprmgKflj4rml7bpl7TmiJDmnKwiLCJ0YWdzIjpbImFnZW50LXNlbGYtZXZvbHV0aW9uIiwiaGFybmVzcyIsIm1vZGVsLXRyYWluaW5nIl0sIm91dHB1dCI6IiJ9 -->
### 近期 Agent 自我改进更可能先发生在 Harness 层 {#cand-harness-layer-self-improvement .eg-candidate .opinion data-slug=harness-layer-self-improvement}
> **[Opinion Candidate]**

#### 观点

在近期工程条件下，Agent 的可用自我改进更可能先发生在 Harness 层，而不是模型权重层。

#### 论据与推理

模型权重的快速自修改仍受训练稳定性、算力成本和评测片面性约束；提示词、Skill、工作流和 Harness 代码则可由较小模型在较短时间内生成和修改。改动对象越靠近文本配置和普通代码，越容易进入现有版本控制、测试与回滚流程。

#### 条件与反例

该判断依赖当前训练成本和在线更新技术。参数高效更新、可靠自动评测或窄领域高频反馈若显著成熟，权重层改进可能更早落地。更新记忆或用户状态也不等于修改 Harness 代码。

#### 待验证

需要比较不同自我改进层级在真实任务中的周期、成本、稳定性和可回滚性，并持续复核在线权重更新技术的进展。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjQwIl0sInJlbCI6InN1cHBvcnQiLCJyZWFzb24iOiLljp_mlofmr5TovoPmkJzlub_mjqjlj43ppojkuI4gSGFybmVzcyDor4TmtYvkv6Hlj7fvvIzlubbmj4_ov7AgUHJpbWUgQWdlbnQg55qEIHJlZmluZW1lbnQg5YeG5YWl5rWB56iLIiwidGFncyI6WyJhZ2VudC1zZWxmLWV2b2x1dGlvbiIsImV2YWx1YXRpb24iLCJnb3Zlcm5hbmNlIl0sIm91dHB1dCI6IiJ9 -->
### Agent 自进化的当前瓶颈是评测与准入而非生成修改 {#cand-agent-self-evolution-evaluation-gate .eg-candidate .opinion data-slug=agent-self-evolution-evaluation-gate}
> **[Opinion Candidate]**

#### 观点

当前 Agent 自进化的主要瓶颈不是能否生成 Harness 修改，而是能否低成本、可靠地评价修改效果并控制准入。

#### 论据与推理

Agent 已能生成提示词、配置和代码改动，但“任务是否变得更好”通常要通过完整任务评测才能观察。与搜广推的点击信号相比，这类信号更昂贵、更低频，也更容易受评测集覆盖不足影响。Prime Agent 因此把修改放入带记录和评测的 refinement 管线，只保留通过判定的改动。

#### 条件与反例

在具有客观、便宜、高频反馈的窄领域，评价与准入成本可能显著降低，此时生成或运行时安全可能重新成为主要瓶颈。评测通过也不能自动排除对基准过拟合或真实场景回归。

#### 待验证

需要建立能同时覆盖任务质量、安全性、成本、延迟和回归风险的评测集，并验证自动指标与人工判断的一致性及抗过拟合能力。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjI5IiwiQjMyIiwiQjM4IiwiQjQwIl0sInJlbCI6InN1cHBvcnQiLCJyZWFzb24iOiLljp_mlofku47lvZPliY3kuqflk4HmlLnov5vmt7HluqbjgIHmt7HlsYLov5DooYzml7bmm7_mjaLpnIDmsYLlkozor4TmtYvnuqbmnZ_mjqjlh7rmnIDnu4jnu5PorroiLCJ0YWdzIjpbImRlZXBzZWVrLWhhcm5lc3MiLCJzZWxmLWV2b2x1dGlvbiIsImFyY2hpdGVjdHVyZS10cmFkZW9mZiJdLCJvdXRwdXQiOiIifQ -->
### DSH 为实时深层自进化预留的开放度对通用团队过度工程 {#cand-dsh-runtime-self-evolution-overengineering .eg-candidate .opinion data-slug=dsh-runtime-self-evolution-overengineering}
> **[Opinion Candidate]**

#### 观点

对 2026 年仍在设计和开发通用 Agent 的多数团队，DSH 为运行时修改深层 Harness 并立即生效所预留的开放度属于明显的过度工程。

#### 论据与推理

DSH 把 Agent Loop 等深层组件也做成可运行时替换的插件，但文章观察到多数现成 Harness 的自主更新仍停留在提示词和记忆层；Skill、Sub Agent、Hook 与执行内核通常需要人工创建、审核或下一次运行才生效。同时，深层改动缺少便宜、可靠、高频的评测信号，生成后立即上线的收益难以覆盖生命周期、状态迁移和准入复杂度。

#### 条件与反例

结论限定于通用 Agent 和文章写作时点。需要不中断服务地替换组件、拥有高质量自动反馈、且能处理状态迁移和回滚的特定场景，可能从 DSH 的开放度中获得足够收益。

#### 待验证

需要收集采用 DSH 深层热替换的生产案例，量化其收益、故障模式、回滚成本及与重启发布方案的差异。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjI5IiwiQjQ1Il0sInJlbCI6InN1cHBvcnQiLCJyZWFzb24iOiLljp_mlofohJrms6jmmI7noa7mioogR2VuZXJhdGl2ZSBBcmNoaXRlY3R1cmUg5qCH5Li65L2c6ICF54yc5oOz77yM5bm257uZ5Ye66LSt54mp5LiO6KeG6aKR57yW6L6R56S65L6LIiwidGFncyI6WyJnZW5lcmF0aXZlLWFyY2hpdGVjdHVyZSIsInJ1bnRpbWUtY29tcG9zaXRpb24iLCJwcmVkaWN0aW9uIl0sIm91dHB1dCI6IiJ9 -->
### 未来软件会按用户当下需求运行时生成部分架构组件 {#cand-generative-architecture-runtime-components .eg-candidate .opinion data-slug=generative-architecture-runtime-components}
> **[Opinion Candidate]**

#### 观点

未来软件可能按用户当下关注的维度，在运行时生成部分前端界面与后端查询组件。

#### 论据与推理

text-to-SQL 已展示运行时生成并执行查询，Generative UI 已展示按任务生成界面。若用户只关心鞋子的尺码、类型和材质，系统可只生成承载这些条件的界面与查询；视频编辑也可只生成当前需要的特效和转场控制面。

#### 条件与反例

动态生成必须满足安全、正确性、性能、可访问性和可追踪性要求。稳定且高频的工作流可能更适合预构建界面；运行时生成全部链路也可能增加延迟和验证成本。

#### 待验证

需要用真实产品实验比较生成式架构与预构建架构在任务完成率、延迟、错误率、维护成本和用户可预测性上的表现。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjExIiwiQjE5Il0sInJlbCI6InN1cHBvcnQiLCJyZWFzb24iOiLljp_mlofnrKzkuoznq6DliIbliKvnu5nlh7rpgIDmrL7lt6XlhbfkuI4gUHJvY2Vzc0hpc3Rvcnkg6K-35rGC6aKE5aSE55CG56S65L6LIiwidGFncyI6WyJweWRhbnRpYy1haSIsImFnZW50LWhhcm5lc3MiLCJleHRlbnNpYmlsaXR5Il0sIm91dHB1dCI6IiJ9 -->
### Pydantic AI 将工具、业务校验和请求预处理直接开放给应用代码 {#cand-pydantic-ai-extension-model .eg-candidate .knowledge data-slug=pydantic-ai-extension-model}
> **[Knowledge Candidate]**

#### 知识内容

在原文的退款示例中，Pydantic AI 通过 `@agent.tool_plain` 直接注册 Python 工具函数。退款金额约束、支付调用和退款后的数据库状态验证都写在应用自己的 `refund` 函数中；Agent Loop 与请求拼装位于 `run_sync()`。

对于“完整请求发给 provider 前做压缩与安全审核”的需求，Pydantic AI 提供 `ProcessHistory`。处理函数接收即将发送的完整消息列表，返回处理后的列表；多个处理函数按声明顺序执行。

#### 条件与边界

该描述基于文章写作时展示的 Pydantic AI API。Library 边界给予应用代码更直接的控制，同时也把业务校验、验证、运行和治理责任交给使用者。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjEzIiwiQjIwIl0sInJlbCI6InN1cHBvcnQiLCJyZWFzb24iOiLljp_mlofnrKzkuoznq6DlsZXnpLogTUNQIOW3peWFt-OAgVByZVRvb2xVc2UvUG9zdFRvb2xVc2UgSG9vayDkuI4gcXVlcnkg5Lqn5ZOB5YWl5Y-jIiwidGFncyI6WyJjbGF1ZGUtYWdlbnQtc2RrIiwibWNwIiwiaG9va3MiXSwib3V0cHV0IjoiIn0 -->
### Claude Agent SDK 通过 MCP、配置和生命周期 Hook 扩展完整 Agent 产品 {#cand-claude-agent-sdk-extension-model .eg-candidate .knowledge data-slug=claude-agent-sdk-extension-model}
> **[Knowledge Candidate]**

#### 知识内容

原文中的 Claude Agent SDK 示例以 `query()` 作为产品入口，循环、权限系统、内置工具与上下文管理由 SDK 提供。自定义工具通过进程内 MCP 服务接入，没有直接注册裸函数的形态。

退款金额检查挂在 `PreToolUse`，退款后状态验证挂在 `PostToolUse`。项目指令可通过 `systemPrompt` 或项目根目录的 `CLAUDE.md` 加载。

对于“请求已经拼装、尚未发给 provider”这一位置，原文指出当时的 Hook 没有提供完整消息、系统提示词和工具 schema 的统一改写入口。要处理完整请求，需要把 SDK 流量指向自建代理服务，由代理修改并转发请求。

#### 条件与边界

这是文章写作时的 SDK 接口事实，具体 Hook 清单和能力需要随版本重新核验。缺少某个内部位置的扩展点，不等于 SDK 无法完成该需求，而是实现可能转移到外部代理层。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjE1IiwiQjIxIl0sInJlbCI6InN1cHBvcnQiLCJyZWFzb24iOiLljp_mlofnrKzkuoznq6DlsZXnpLogRXZlIOeahCBpbnN0cnVjdGlvbnMvdG9vbHMvaG9va3Mg5paH5Lu25qCR5LiOIG9ic2VydmUtb25seSBIb29rIiwidGFncyI6WyJldmUiLCJhZ2VudC1wbGF0Zm9ybSIsImhvb2tzIl0sIm91dHB1dCI6IiJ9 -->
### Eve 通过约定文件树扩展托管式 Agent，事件 Hook 只观察不回注模型 {#cand-eve-platform-extension-model .eg-candidate .knowledge data-slug=eve-platform-extension-model}
> **[Knowledge Candidate]**

#### 知识内容

Eve 通过约定文件树装配 Agent 内容：`instructions.md` 提供指令，`tools/` 放工具，`hooks/` 放事件处理器；平台负责运行、存储、恢复和对话入口。

工具输入中的正数约束可以写入 schema；需要查询订单数据的金额上限检查仍写在工具 `execute` 内。原文引用 Eve 文档说明事件处理器是 observe-only：`action.result` Hook 可以记录或告警，但不能把检查结果重新注入模型上下文。

对于完整模型请求的压缩与审核，原文通过 Eve 底层 Vercel AI SDK 的 language model middleware，在 `transformParams` 中处理模型参数。

#### 条件与边界

平台托管范围与可干预范围是不同维度。Eve 减少应用方运行负担，但应用能否改变某个生命周期位置仍取决于平台和底层 SDK 暴露的扩展点。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjE5IiwiQjIwIiwiQjIxIl0sInJlbCI6InN1cHBvcnQiLCJyZWFzb24iOiLljp_mlofnlKjlkIzkuIDljovnvKnlkozlronlhajlrqHmoLjpnIDmsYLmr5TovoMgUHlkYW50aWMgQUnjgIFDbGF1ZGUgQWdlbnQgU0RLIOS4jiBFdmUiLCJ0YWdzIjpbIm1vZGVsLXJlcXVlc3QiLCJtaWRkbGV3YXJlIiwic2VjdXJpdHktcmV2aWV3Il0sIm91dHB1dCI6IiJ9 -->
### 完整模型请求的发送前预处理在三种 Agent 边界中具有不同接入路径 {#cand-model-request-preprocessing-paths .eg-candidate .knowledge data-slug=model-request-preprocessing-paths}
> **[Knowledge Candidate]**

#### 知识内容

对于“消息拼装完成后、发给 provider 之前，对完整请求做主动压缩和安全审核”这一需求，文章给出三条接入路径：

- Pydantic AI：使用 `ProcessHistory` 直接读取并返回完整消息列表。
- Claude Agent SDK：当时没有能改写完整请求的对应 Hook，需要通过 `ANTHROPIC_BASE_URL` 把流量转到自建代理服务。
- Eve：自身偏声明式，但可借底层 Vercel AI SDK 的 language model middleware 在 `transformParams` 中处理完整参数。

#### 条件与边界

这是一个按生命周期位置比较扩展点的案例。它记录的是文章写作时各产品公开接口的接入路径，不保证后续版本保持不变。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjI0Il0sInJlbCI6InN1cHBvcnQiLCJyZWFzb24iOiLljp_mlofnrKzkuInnq6Dnu5nlh7ogdG9vbHMvcHJlLWV4ZWN1dGUg5LiOIHRvb2xzL3Bvc3QtZXhlY3V0ZSDnmoTlrozmlbTpgIDmrL7lrp7njrAiLCJ0YWdzIjpbImRlZXBzZWVrLWhhcm5lc3MiLCJ0b29sLWxpZmVjeWNsZSIsInZlcmlmaWNhdGlvbiJdLCJvdXRwdXQiOiIifQ -->
### DSH 工具生命周期 Hook 可在执行前拒绝并在执行后向模型反馈验证失败 {#cand-dsh-tool-lifecycle-feedback .eg-candidate .knowledge data-slug=dsh-tool-lifecycle-feedback}
> **[Knowledge Candidate]**

#### 知识内容

DSH 业务插件可以通过 `ctx.tools.register` 注册工具，并订阅工具生命周期事件：

- `tools/pre-execute` 在工具执行前读取订单信息；退款金额非法时返回 `deny`。
- `tools/post-execute` 在工具执行后检查订单状态；接口返回成功但状态未改变时返回 `block` 和反馈文本。

`post-execute` 的 `block` 会把调用结果转成带纠正反馈的失败，模型能够看到失败原因并继续处理。这与只向外部记录或告警、不能回注模型的 observe-only Hook 不同。

#### 条件与边界

生命周期 Hook 的触发位置和可返回决策取决于 DSH/Cordis 当时的事件合同。本文示例说明的是工具执行管线，不代表所有插件事件都能阻断或改写模型上下文。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjI2Il0sInJlbCI6InN1cHBvcnQiLCJyZWFzb24iOiLljp_mlofnrKzkuInnq6DlsZXnpLogRGVlcFNlZWtIYXJuZXNzIFNES-OAgXNlc3Npb25JZCDkuI4gYWdlbnQvcHJlLXN0ZXAg5o-S5Lu2IiwidGFncyI6WyJkZWVwc2Vlay1oYXJuZXNzIiwic2Vzc2lvbiIsInByZS1zdGVwIl0sIm91dHB1dCI6IiJ9 -->
### DSH 由 SDK 驱动组合实例，并以 sessionId 复用会话和 pre-step 处理新增消息 {#cand-dsh-sdk-session-prestep .eg-candidate .knowledge data-slug=dsh-sdk-session-prestep}
> **[Knowledge Candidate]**

#### 知识内容

服务进程使用 `DeepSeekHarness` SDK 启动 `dsh-jsonrpc-agent` 并加载 `*.cordis.yml`。一个 Harness 实例可以跨多次 `run()` 复用；调用方通过 `sessionId` 把客户或会话关联到持久化记录。

发送模型请求前的消息处理可以挂到 `agent/pre-step`。插件调用 `next()` 取得进入决策，再返回处理后的 `messages`。原文示例先按规则脱敏，再用本地模型压缩。

#### 条件与边界

为维持缓存稳定，原文明确 `agent/pre-step` 只允许修改本轮新增消息，不是任意重写完整历史的入口。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjM4IiwiQjQwIl0sInJlbCI6InN1cHBvcnQiLCJyZWFzb24iOiLljp_mlofnrKzkupTnq6Dmj4_ov7DkuKTkuKogSGFybmVzcyDog73lipvnlJ_miJDlt6XlhbflvZPml7bnmoTpg6jnvbLpmLbmrrUiLCJ0YWdzIjpbImFnZW50LXNlbGYtZXZvbHV0aW9uIiwiY2FwYWJpbGl0eS1jcmVhdGlvbiIsImh1bWFuLXJldmlldyJdLCJvdXRwdXQiOiIifQ -->
### Capability Creation 与 better-harness 在文章写作时仍由人工评测和部署 {#cand-human-gated-harness-capability-tools .eg-candidate .knowledge data-slug=human-gated-harness-capability-tools}
> **[Knowledge Candidate]**

#### 知识内容

文章写作时已经出现 Capability Creation 和 better-harness 等生成 Harness 能力的工具。原文描述它们仍处于“生成对应能力后，由人工评测并部署”的阶段，没有让 Agent 生成后直接在线替换。

#### 条件与边界

这是 2026 年 8 月的项目状态快照，具有强时间边界。后续版本是否支持自动评测、自动准入或运行时部署，需要重新核验。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjYiXSwicmVsIjoic3VwcG9ydCIsInJlYXNvbiI6IuWOn-aWh-esrOS4gOeroOS7juW3peWFtyBzY2hlbWHjgIHlt6XlhbfpgInmi6nkuI7lj4LmlbDmnoTpgKDmjqjlh7rov5nkuIDliKTmlq0iLCJ0YWdzIjpbInRvb2wtY2FsbGluZyIsImluc3RydWN0aW9uLWZvbGxvd2luZyIsImFnZW50LWxvb3AiXSwib3V0cHV0IjoiIn0 -->
### 工具调用能力本质上是模型对工具 Schema 的指令遵从能力 {#cand-tool-calling-is-instruction-following .eg-candidate .opinion data-slug=tool-calling-is-instruction-following}
> **[Opinion Candidate]**

#### 观点

工具调用能力可以主要理解为模型对工具 Schema 的指令遵从能力：模型需要选对工具并按 Schema 构造正确参数。

#### 论据与推理

Harness 把工具名称、描述和参数结构放入模型上下文；模型输出结构化调用请求，Harness 再执行并回填结果。因此在接口已正确暴露的前提下，工具是否选对、参数是否拼对取决于模型对这些指令的理解与遵从。

#### 条件与反例

真实工具能力还受工具描述质量、上下文管理、参数校验、权限控制和执行环境影响。模型能产生合法参数，不等于工具执行结果正确，也不等于任务已经完成。

#### 待验证

需要通过跨模型、跨 Schema 复杂度的工具选择与参数构造评测，区分指令遵从、规划能力和执行环境对成功率的贡献。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjEwIiwiQjIxIl0sInJlbCI6InN1cHBvcnQiLCJyZWFzb24iOiLljp_mlofmr5TovoPkuInnp43mtYHmtL7lkI7vvIzlsIblt6XlhbfjgIFIb29r44CB5qOA5p-l562J5omp5bGV5bel5L2c5qaC5ous5Li65o-S5Lu25byA5Y-RIiwidGFncyI6WyJhZ2VudC1kZXZlbG9wbWVudCIsInBsdWdpbiIsImZyYW1ld29yay1ib3VuZGFyeSJdLCJvdXRwdXQiOiIifQ -->
### 多数 Agent 开发工作发生在框架核心之外的插件层 {#cand-agent-development-is-plugin-development .eg-candidate .opinion data-slug=agent-development-is-plugin-development}
> **[Opinion Candidate]**

#### 观点

在不修改框架源代码的前提下，多数 Agent 开发工作发生在框架保留核心之外的插件层。

#### 论据与推理

Pydantic AI、Claude Agent SDK 与 Eve 都保留一部分调用方无法直接修改的核心，再通过工具、Hook、配置、MCP 或约定目录开放扩展。三者差别主要体现在核心边界和扩展点覆盖范围，而非是否存在插件层。

#### 条件与反例

从零实现 Harness、直接维护框架分支或进行模型训练时，主要工作不一定属于插件开发。不同团队对“插件”的定义也可能包含不同的代码、配置和服务边界。

#### 待验证

需要统计真实 Agent 项目的改动分布，确认业务开发、框架改造、运维治理和模型适配分别占据多少工作量。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjIyIiwiQjIzIiwiQjI4IiwiQjI5Il0sInJlbCI6InN1cHBvcnQiLCJyZWFzb24iOiLljp_mlofmr5TovoMgRXZlIOS4jiBEU0gg55qE5aOw5piO6IyD5Zu077yM5bm25qaC5ous5Li64oCcRFNIIOaYryBFdmUg55qE5b2i5oCB77yM5bqT55qE5byA5pS-5bqm4oCdIiwidGFncyI6WyJkZWVwc2Vlay1oYXJuZXNzIiwicGxhdGZvcm0iLCJleHRlbnNpYmlsaXR5Il0sIm91dHB1dCI6IiJ9 -->
### DSH 具有平台式声明装配形态和库级开放度 {#cand-dsh-platform-shape-library-openness .eg-candidate .opinion data-slug=dsh-platform-shape-library-openness}
> **[Opinion Candidate]**

#### 观点

DSH 可以概括为平台式的声明装配形态与库级开放度的组合。

#### 论据与推理

DSH 与 Eve 都通过声明式结构装配命令式插件；但 Eve 的声明范围主要是工具、指令和审批等 Agent 内容物，循环、持久化与压缩由平台控制。DSH 的 Cordis 清单进一步覆盖模型、会话、文件系统、消息处理和 Agent Loop，因此在平台式装配下仍允许替换深层实现。

#### 条件与反例

“平台形态”和“库级开放度”是文章用于比较的分析标签，不是正式产品分类。DSH 是否真正达到库级可控性，还取决于插件 API、状态迁移、兼容和调试能力。

#### 待验证

需要比较 DSH 与库式框架在深层组件替换、故障定位、版本兼容和运行责任上的实际差异。

<!-- eg:cd:2 eyJub3RlX3JlZnMiOlsiQjMyIiwiQjM4IiwiQjQwIl0sInJlbCI6InN1cHBvcnQiLCJyZWFzb24iOiLljp_mlofnrKzkupTnq6DljLrliIbov5DooYzkuK3mm7TmlrDorrDlv4blkoznrZbnlaXvvIzkuI7oh6rkuLvmm7_mjaLmt7HlsYIgSGFybmVzcyDnu4Tku7YiLCJ0YWdzIjpbImFnZW50LXNlbGYtZXZvbHV0aW9uIiwicnVudGltZS11cGRhdGUiLCJzY29wZSJdLCJvdXRwdXQiOiIifQ -->
### Agent 是否需要实时自进化取决于被修改的层级 {#cand-realtime-self-evolution-demand-depends-on-layer .eg-candidate .opinion data-slug=realtime-self-evolution-demand-depends-on-layer}
> **[Opinion Candidate]**

#### 观点

不能笼统回答 Agent 是否需要实时自进化；需求是否成立取决于更新发生在记忆、状态、策略、工作流还是深层 Harness 内核。

#### 论据与推理

运行中吸收任务反馈、更新记忆、用户状态或下一步策略已经存在现实需求。自主修改 Skill、Sub Agent、Hook、Agent Loop 或执行内核并立即替换，则涉及更高的评测、状态迁移、兼容和回滚成本，在文章写作时主要属于研究与少数特定场景。

#### 条件与反例

拥有客观高频反馈、严格隔离、自动回滚和不中断替换需求的窄领域，可能较早需要深层在线自进化。仅更新用户画像或记忆不应被当作深层 Harness 自改的证据。

#### 待验证

需要按改进层级收集生产案例，分别衡量实时生效的收益、失败半径、准入成本和恢复能力。

### 候选覆盖

| 模块 | Note 块 | 语义模块 | 草稿处置 |
| --- | --- | --- | --- |
| `标题、作者与版本边界` | `B1` | 文章标题、作者、发布时间、研究对象与 DSH 0.1.0 时点。 | Note-only：来源元信息与版本边界保留在 Note，不单独物化。 |
| `Agent 与生产级 Harness 五类职责` | `B3` | Agent=Model+Harness；Harness 承担上下文、工具、约束、验证、纠正。 | `cand-harness-production-five-capabilities` |
| `ReAct 循环、工具调用与结束判断` | `B6` | ReAct 状态循环、工具 Schema 指令遵从、模型结束判断及与 loop engineering 的差异。 | `cand-react-agent-loop` `cand-tool-calling-is-instruction-following` |
| `三种 Agent 开发形态的共同命题` | `B10` | Library、Product、Platform 都在不可直接修改的核心之外开放扩展。 | `cand-agent-development-boundaries` `cand-agent-development-is-plugin-development` |
| `Pydantic AI 退款案例` | `B11` | 工具函数直接承载业务动作、约束与结果验证，主循环在 run_sync 内。 | `cand-pydantic-ai-extension-model` `cand-agent-development-boundaries` |
| `Claude Agent SDK 退款案例` | `B13` | MCP 工具、PreToolUse/PostToolUse 与 query 产品入口形成扩展面。 | `cand-claude-agent-sdk-extension-model` `cand-agent-development-boundaries` |
| `Eve 退款案例与平台托管边界` | `B15` | 约定文件树装配 Agent，平台托管运行；事件 Hook observe-only。 | `cand-eve-platform-extension-model` `cand-agent-development-boundaries` |
| `核心边界综合比较` | `B17` | 三类方案的核心边界决定控制权、运行责任与扩展成本。 | `cand-agent-development-boundaries` `cand-harness-core-boundary` |
| `Pydantic AI 请求发送前预处理` | `B19` | ProcessHistory 直接处理即将发送的完整消息列表。 | `cand-pydantic-ai-extension-model` `cand-model-request-preprocessing-paths` |
| `Claude Agent SDK 请求发送前预处理` | `B20` | 缺少完整请求改写 Hook 时，通过自建代理服务处理完整请求。 | `cand-claude-agent-sdk-extension-model` `cand-model-request-preprocessing-paths` `cand-harness-core-boundary` |
| `Eve 中间件与插件两个维度` | `B21` | 借底层模型中间件处理请求；插件形态与组装方式是正交维度。 | `cand-eve-platform-extension-model` `cand-model-request-preprocessing-paths` `cand-agent-plugin-two-axes` `cand-agent-development-is-plugin-development` `cand-harness-core-boundary` |
| `DSH 项目布局` | `B22` | DSH 用 npm 包、Cordis 清单、AGENTS.md 与 SDK 驱动组成项目。 | `cand-dsh-cordis-plugin-layout` `cand-dsh-platform-shape-library-openness` |
| `Cordis 组合清单` | `B23` | 模型、spine、工具、会话、文件系统和 RPC 服务均作为插件声明。 | `cand-dsh-cordis-plugin-layout` `cand-dsh-platform-shape-library-openness` |
| `DSH 工具注册与生命周期反馈` | `B24` | apply(ctx) 注册工具，pre-execute 可拒绝，post-execute 可向模型反馈验证失败。 | `cand-dsh-cordis-plugin-layout` `cand-dsh-tool-lifecycle-feedback` |
| `DSH SDK 会话复用与 pre-step` | `B26` | SDK 驱动组合实例，sessionId 复用会话，pre-step 处理本轮新增消息。 | `cand-dsh-cordis-plugin-layout` `cand-dsh-sdk-session-prestep` |
| `压缩器与 Agent Loop 也可替换` | `B28` | 压缩器与 Agent Loop 均以稳定 id、可变实现包进入清单。 | `cand-dsh-cordis-plugin-layout` `cand-dsh-platform-shape-library-openness` |
| `时空可组合性与生成式架构` | `B29` | 运行中替换组件、通过稳定能力 id 重新绑定实现，并关联生成式架构设想。 | `cand-dsh-spatiotemporal-composability` `cand-generative-architecture-runtime-components` `cand-dsh-platform-shape-library-openness` `cand-dsh-runtime-self-evolution-overengineering` |
| `自我改进层级与 Harness 优先` | `B32` | 近期自改更易发生在 Harness；修改对象从提示词逐步深入到优化器代码。 | `cand-agent-self-improvement-levels` `cand-harness-layer-self-improvement` `cand-realtime-self-evolution-demand-depends-on-layer` `cand-dsh-runtime-self-evolution-overengineering` |
| `离线、近线与在线学习` | `B36` | 按反馈进入更新链路的时机区分三类学习，并排除仅实时响应的误判。 | `cand-offline-nearline-online-learning` |
| `实时自进化需求取决于修改层级` | `B38` | 记忆与策略在线更新已有需求，深层 Harness 即时替换仍非通用刚需。 | `cand-human-gated-harness-capability-tools` `cand-realtime-self-evolution-demand-depends-on-layer` `cand-harness-layer-self-improvement` |
| `评测准入与过度工程结论` | `B40` | 人工评测/refinement、反馈信号成本与 DSH 对通用团队过度工程的结论。 | `cand-prime-agent-refinement-workflow` `cand-human-gated-harness-capability-tools` `cand-agent-self-evolution-evaluation-gate` `cand-dsh-runtime-self-evolution-overengineering` `cand-realtime-self-evolution-demand-depends-on-layer` |
| `参考资料与时效性来源` | `B45` | 论文、框架文档、作者设想和产品版本观察的来源列表。 | `cand-generative-architecture-runtime-components` |
| `legacy-note-only-B2` | `B2` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B4` | `B4` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B5` | `B5` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B7` | `B7` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B8` | `B8` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B9` | `B9` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B12` | `B12` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B14` | `B14` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B16` | `B16` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B18` | `B18` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B25` | `B25` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B27` | `B27` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B30` | `B30` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B31` | `B31` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B33` | `B33` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B34` | `B34` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B35` | `B35` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B37` | `B37` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B39` | `B39` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B41` | `B41` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B42` | `B42` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B43` | `B43` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B44` | `B44` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |
| `legacy-note-only-B46` | `B46` | legacy Note block retained outside candidate coverage | Note-only：legacy candidate protocol only referenced source blocks |

<!-- eg:cc:2 eyJtb2R1bGUiOiLmoIfpopjjgIHkvZzogIXkuI7niYjmnKzovrnnlYwiLCJub3RlX3JlZnMiOlsiQjEiXSwic3VtbWFyeSI6IuaWh-eroOagh-mimOOAgeS9nOiAheOAgeWPkeW4g-aXtumXtOOAgeeglOeptuWvueixoeS4jiBEU0ggMC4xLjAg5pe254K544CCIiwiZGlzcG9zaXRpb24iOiJub3RlX29ubHkiLCJjYW5kaWRhdGVzIjpbXSwicmVhc29uIjoi5p2l5rqQ5YWD5L-h5oGv5LiO54mI5pys6L6555WM5L-d55WZ5ZyoIE5vdGXvvIzkuI3ljZXni6znianljJbjgIIifQ -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJBZ2VudCDkuI7nlJ_kuqfnuqcgSGFybmVzcyDkupTnsbvogYzotKMiLCJub3RlX3JlZnMiOlsiQjMiXSwic3VtbWFyeSI6IkFnZW50PU1vZGVsK0hhcm5lc3PvvJtIYXJuZXNzIOaJv-aLheS4iuS4i-aWh-OAgeW3peWFt-OAgee6puadn-OAgemqjOivgeOAgee6oOato-OAgiIsImRpc3Bvc2l0aW9uIjoiY2FuZGlkYXRlIiwiY2FuZGlkYXRlcyI6WyJjYW5kLWhhcm5lc3MtcHJvZHVjdGlvbi1maXZlLWNhcGFiaWxpdGllcyJdLCJyZWFzb24iOiIifQ -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJSZUFjdCDlvqrnjq_jgIHlt6XlhbfosIPnlKjkuI7nu5PmnZ_liKTmlq0iLCJub3RlX3JlZnMiOlsiQjYiXSwic3VtbWFyeSI6IlJlQWN0IOeKtuaAgeW-queOr-OAgeW3peWFtyBTY2hlbWEg5oyH5Luk6YG15LuO44CB5qih5Z6L57uT5p2f5Yik5pat5Y-K5LiOIGxvb3AgZW5naW5lZXJpbmcg55qE5beu5byC44CCIiwiZGlzcG9zaXRpb24iOiJjYW5kaWRhdGUiLCJjYW5kaWRhdGVzIjpbImNhbmQtcmVhY3QtYWdlbnQtbG9vcCIsImNhbmQtdG9vbC1jYWxsaW5nLWlzLWluc3RydWN0aW9uLWZvbGxvd2luZyJdLCJyZWFzb24iOiIifQ -->
<!-- eg:cc:2 eyJtb2R1bGUiOiLkuInnp40gQWdlbnQg5byA5Y-R5b2i5oCB55qE5YWx5ZCM5ZG96aKYIiwibm90ZV9yZWZzIjpbIkIxMCJdLCJzdW1tYXJ5IjoiTGlicmFyeeOAgVByb2R1Y3TjgIFQbGF0Zm9ybSDpg73lnKjkuI3lj6_nm7TmjqXkv67mlLnnmoTmoLjlv4PkuYvlpJblvIDmlL7mianlsZXjgIIiLCJkaXNwb3NpdGlvbiI6ImNhbmRpZGF0ZSIsImNhbmRpZGF0ZXMiOlsiY2FuZC1hZ2VudC1kZXZlbG9wbWVudC1ib3VuZGFyaWVzIiwiY2FuZC1hZ2VudC1kZXZlbG9wbWVudC1pcy1wbHVnaW4tZGV2ZWxvcG1lbnQiXSwicmVhc29uIjoiIn0 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJQeWRhbnRpYyBBSSDpgIDmrL7moYjkvosiLCJub3RlX3JlZnMiOlsiQjExIl0sInN1bW1hcnkiOiLlt6Xlhbflh73mlbDnm7TmjqXmib_ovb3kuJrliqHliqjkvZzjgIHnuqbmnZ_kuI7nu5Pmnpzpqozor4HvvIzkuLvlvqrnjq_lnKggcnVuX3N5bmMg5YaF44CCIiwiZGlzcG9zaXRpb24iOiJjYW5kaWRhdGUiLCJjYW5kaWRhdGVzIjpbImNhbmQtcHlkYW50aWMtYWktZXh0ZW5zaW9uLW1vZGVsIiwiY2FuZC1hZ2VudC1kZXZlbG9wbWVudC1ib3VuZGFyaWVzIl0sInJlYXNvbiI6IiJ9 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJDbGF1ZGUgQWdlbnQgU0RLIOmAgOasvuahiOS-iyIsIm5vdGVfcmVmcyI6WyJCMTMiXSwic3VtbWFyeSI6Ik1DUCDlt6XlhbfjgIFQcmVUb29sVXNlL1Bvc3RUb29sVXNlIOS4jiBxdWVyeSDkuqflk4HlhaXlj6PlvaLmiJDmianlsZXpnaLjgIIiLCJkaXNwb3NpdGlvbiI6ImNhbmRpZGF0ZSIsImNhbmRpZGF0ZXMiOlsiY2FuZC1jbGF1ZGUtYWdlbnQtc2RrLWV4dGVuc2lvbi1tb2RlbCIsImNhbmQtYWdlbnQtZGV2ZWxvcG1lbnQtYm91bmRhcmllcyJdLCJyZWFzb24iOiIifQ -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJFdmUg6YCA5qy-5qGI5L6L5LiO5bmz5Y-w5omY566h6L6555WMIiwibm90ZV9yZWZzIjpbIkIxNSJdLCJzdW1tYXJ5Ijoi57qm5a6a5paH5Lu25qCR6KOF6YWNIEFnZW5077yM5bmz5Y-w5omY566h6L-Q6KGM77yb5LqL5Lu2IEhvb2sgb2JzZXJ2ZS1vbmx544CCIiwiZGlzcG9zaXRpb24iOiJjYW5kaWRhdGUiLCJjYW5kaWRhdGVzIjpbImNhbmQtZXZlLXBsYXRmb3JtLWV4dGVuc2lvbi1tb2RlbCIsImNhbmQtYWdlbnQtZGV2ZWxvcG1lbnQtYm91bmRhcmllcyJdLCJyZWFzb24iOiIifQ -->
<!-- eg:cc:2 eyJtb2R1bGUiOiLmoLjlv4PovrnnlYznu7zlkIjmr5TovoMiLCJub3RlX3JlZnMiOlsiQjE3Il0sInN1bW1hcnkiOiLkuInnsbvmlrnmoYjnmoTmoLjlv4PovrnnlYzlhrPlrprmjqfliLbmnYPjgIHov5DooYzotKPku7vkuI7mianlsZXmiJDmnKzjgIIiLCJkaXNwb3NpdGlvbiI6ImNhbmRpZGF0ZSIsImNhbmRpZGF0ZXMiOlsiY2FuZC1hZ2VudC1kZXZlbG9wbWVudC1ib3VuZGFyaWVzIiwiY2FuZC1oYXJuZXNzLWNvcmUtYm91bmRhcnkiXSwicmVhc29uIjoiIn0 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJQeWRhbnRpYyBBSSDor7fmsYLlj5HpgIHliY3pooTlpITnkIYiLCJub3RlX3JlZnMiOlsiQjE5Il0sInN1bW1hcnkiOiJQcm9jZXNzSGlzdG9yeSDnm7TmjqXlpITnkIbljbPlsIblj5HpgIHnmoTlrozmlbTmtojmga_liJfooajjgIIiLCJkaXNwb3NpdGlvbiI6ImNhbmRpZGF0ZSIsImNhbmRpZGF0ZXMiOlsiY2FuZC1weWRhbnRpYy1haS1leHRlbnNpb24tbW9kZWwiLCJjYW5kLW1vZGVsLXJlcXVlc3QtcHJlcHJvY2Vzc2luZy1wYXRocyJdLCJyZWFzb24iOiIifQ -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJDbGF1ZGUgQWdlbnQgU0RLIOivt-axguWPkemAgeWJjemihOWkhOeQhiIsIm5vdGVfcmVmcyI6WyJCMjAiXSwic3VtbWFyeSI6Iue8uuWwkeWujOaVtOivt-axguaUueWGmSBIb29rIOaXtu-8jOmAmui_h-iHquW7uuS7o-eQhuacjeWKoeWkhOeQhuWujOaVtOivt-axguOAgiIsImRpc3Bvc2l0aW9uIjoiY2FuZGlkYXRlIiwiY2FuZGlkYXRlcyI6WyJjYW5kLWNsYXVkZS1hZ2VudC1zZGstZXh0ZW5zaW9uLW1vZGVsIiwiY2FuZC1tb2RlbC1yZXF1ZXN0LXByZXByb2Nlc3NpbmctcGF0aHMiLCJjYW5kLWhhcm5lc3MtY29yZS1ib3VuZGFyeSJdLCJyZWFzb24iOiIifQ -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJFdmUg5Lit6Ze05Lu25LiO5o-S5Lu25Lik5Liq57u05bqmIiwibm90ZV9yZWZzIjpbIkIyMSJdLCJzdW1tYXJ5Ijoi5YCf5bqV5bGC5qih5Z6L5Lit6Ze05Lu25aSE55CG6K-35rGC77yb5o-S5Lu25b2i5oCB5LiO57uE6KOF5pa55byP5piv5q2j5Lqk57u05bqm44CCIiwiZGlzcG9zaXRpb24iOiJjYW5kaWRhdGUiLCJjYW5kaWRhdGVzIjpbImNhbmQtZXZlLXBsYXRmb3JtLWV4dGVuc2lvbi1tb2RlbCIsImNhbmQtbW9kZWwtcmVxdWVzdC1wcmVwcm9jZXNzaW5nLXBhdGhzIiwiY2FuZC1hZ2VudC1wbHVnaW4tdHdvLWF4ZXMiLCJjYW5kLWFnZW50LWRldmVsb3BtZW50LWlzLXBsdWdpbi1kZXZlbG9wbWVudCIsImNhbmQtaGFybmVzcy1jb3JlLWJvdW5kYXJ5Il0sInJlYXNvbiI6IiJ9 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJEU0gg6aG555uu5biD5bGAIiwibm90ZV9yZWZzIjpbIkIyMiJdLCJzdW1tYXJ5IjoiRFNIIOeUqCBucG0g5YyF44CBQ29yZGlzIOa4heWNleOAgUFHRU5UUy5tZCDkuI4gU0RLIOmpseWKqOe7hOaIkOmhueebruOAgiIsImRpc3Bvc2l0aW9uIjoiY2FuZGlkYXRlIiwiY2FuZGlkYXRlcyI6WyJjYW5kLWRzaC1jb3JkaXMtcGx1Z2luLWxheW91dCIsImNhbmQtZHNoLXBsYXRmb3JtLXNoYXBlLWxpYnJhcnktb3Blbm5lc3MiXSwicmVhc29uIjoiIn0 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJDb3JkaXMg57uE5ZCI5riF5Y2VIiwibm90ZV9yZWZzIjpbIkIyMyJdLCJzdW1tYXJ5Ijoi5qih5Z6L44CBc3BpbmXjgIHlt6XlhbfjgIHkvJror53jgIHmlofku7bns7vnu5_lkowgUlBDIOacjeWKoeWdh-S9nOS4uuaPkuS7tuWjsOaYjuOAgiIsImRpc3Bvc2l0aW9uIjoiY2FuZGlkYXRlIiwiY2FuZGlkYXRlcyI6WyJjYW5kLWRzaC1jb3JkaXMtcGx1Z2luLWxheW91dCIsImNhbmQtZHNoLXBsYXRmb3JtLXNoYXBlLWxpYnJhcnktb3Blbm5lc3MiXSwicmVhc29uIjoiIn0 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJEU0gg5bel5YW35rOo5YaM5LiO55Sf5ZG95ZGo5pyf5Y-N6aaIIiwibm90ZV9yZWZzIjpbIkIyNCJdLCJzdW1tYXJ5IjoiYXBwbHkoY3R4KSDms6jlhozlt6XlhbfvvIxwcmUtZXhlY3V0ZSDlj6_mi5Lnu53vvIxwb3N0LWV4ZWN1dGUg5Y-v5ZCR5qih5Z6L5Y-N6aaI6aqM6K-B5aSx6LSl44CCIiwiZGlzcG9zaXRpb24iOiJjYW5kaWRhdGUiLCJjYW5kaWRhdGVzIjpbImNhbmQtZHNoLWNvcmRpcy1wbHVnaW4tbGF5b3V0IiwiY2FuZC1kc2gtdG9vbC1saWZlY3ljbGUtZmVlZGJhY2siXSwicmVhc29uIjoiIn0 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJEU0ggU0RLIOS8muivneWkjeeUqOS4jiBwcmUtc3RlcCIsIm5vdGVfcmVmcyI6WyJCMjYiXSwic3VtbWFyeSI6IlNESyDpqbHliqjnu4TlkIjlrp7kvovvvIxzZXNzaW9uSWQg5aSN55So5Lya6K-d77yMcHJlLXN0ZXAg5aSE55CG5pys6L2u5paw5aKe5raI5oGv44CCIiwiZGlzcG9zaXRpb24iOiJjYW5kaWRhdGUiLCJjYW5kaWRhdGVzIjpbImNhbmQtZHNoLWNvcmRpcy1wbHVnaW4tbGF5b3V0IiwiY2FuZC1kc2gtc2RrLXNlc3Npb24tcHJlc3RlcCJdLCJyZWFzb24iOiIifQ -->
<!-- eg:cc:2 eyJtb2R1bGUiOiLljovnvKnlmajkuI4gQWdlbnQgTG9vcCDkuZ_lj6_mm7_mjaIiLCJub3RlX3JlZnMiOlsiQjI4Il0sInN1bW1hcnkiOiLljovnvKnlmajkuI4gQWdlbnQgTG9vcCDlnYfku6XnqLPlrpogaWTjgIHlj6_lj5jlrp7njrDljIXov5vlhaXmuIXljZXjgIIiLCJkaXNwb3NpdGlvbiI6ImNhbmRpZGF0ZSIsImNhbmRpZGF0ZXMiOlsiY2FuZC1kc2gtY29yZGlzLXBsdWdpbi1sYXlvdXQiLCJjYW5kLWRzaC1wbGF0Zm9ybS1zaGFwZS1saWJyYXJ5LW9wZW5uZXNzIl0sInJlYXNvbiI6IiJ9 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiLml7bnqbrlj6_nu4TlkIjmgKfkuI7nlJ_miJDlvI_mnrbmnoQiLCJub3RlX3JlZnMiOlsiQjI5Il0sInN1bW1hcnkiOiLov5DooYzkuK3mm7_mjaLnu4Tku7bjgIHpgJrov4fnqLPlrprog73lipsgaWQg6YeN5paw57uR5a6a5a6e546w77yM5bm25YWz6IGU55Sf5oiQ5byP5p625p6E6K6-5oOz44CCIiwiZGlzcG9zaXRpb24iOiJjYW5kaWRhdGUiLCJjYW5kaWRhdGVzIjpbImNhbmQtZHNoLXNwYXRpb3RlbXBvcmFsLWNvbXBvc2FiaWxpdHkiLCJjYW5kLWdlbmVyYXRpdmUtYXJjaGl0ZWN0dXJlLXJ1bnRpbWUtY29tcG9uZW50cyIsImNhbmQtZHNoLXBsYXRmb3JtLXNoYXBlLWxpYnJhcnktb3Blbm5lc3MiLCJjYW5kLWRzaC1ydW50aW1lLXNlbGYtZXZvbHV0aW9uLW92ZXJlbmdpbmVlcmluZyJdLCJyZWFzb24iOiIifQ -->
<!-- eg:cc:2 eyJtb2R1bGUiOiLoh6rmiJHmlLnov5vlsYLnuqfkuI4gSGFybmVzcyDkvJjlhYgiLCJub3RlX3JlZnMiOlsiQjMyIl0sInN1bW1hcnkiOiLov5HmnJ_oh6rmlLnmm7TmmJPlj5HnlJ_lnKggSGFybmVzc--8m-S_ruaUueWvueixoeS7juaPkOekuuivjemAkOatpea3seWFpeWIsOS8mOWMluWZqOS7o-eggeOAgiIsImRpc3Bvc2l0aW9uIjoiY2FuZGlkYXRlIiwiY2FuZGlkYXRlcyI6WyJjYW5kLWFnZW50LXNlbGYtaW1wcm92ZW1lbnQtbGV2ZWxzIiwiY2FuZC1oYXJuZXNzLWxheWVyLXNlbGYtaW1wcm92ZW1lbnQiLCJjYW5kLXJlYWx0aW1lLXNlbGYtZXZvbHV0aW9uLWRlbWFuZC1kZXBlbmRzLW9uLWxheWVyIiwiY2FuZC1kc2gtcnVudGltZS1zZWxmLWV2b2x1dGlvbi1vdmVyZW5naW5lZXJpbmciXSwicmVhc29uIjoiIn0 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiLnprvnur_jgIHov5Hnur_kuI7lnKjnur_lrabkuaAiLCJub3RlX3JlZnMiOlsiQjM2Il0sInN1bW1hcnkiOiLmjInlj43ppojov5vlhaXmm7TmlrDpk77ot6_nmoTml7bmnLrljLrliIbkuInnsbvlrabkuaDvvIzlubbmjpLpmaTku4Xlrp7ml7blk43lupTnmoTor6_liKTjgIIiLCJkaXNwb3NpdGlvbiI6ImNhbmRpZGF0ZSIsImNhbmRpZGF0ZXMiOlsiY2FuZC1vZmZsaW5lLW5lYXJsaW5lLW9ubGluZS1sZWFybmluZyJdLCJyZWFzb24iOiIifQ -->
<!-- eg:cc:2 eyJtb2R1bGUiOiLlrp7ml7boh6rov5vljJbpnIDmsYLlj5blhrPkuo7kv67mlLnlsYLnuqciLCJub3RlX3JlZnMiOlsiQjM4Il0sInN1bW1hcnkiOiLorrDlv4bkuI7nrZbnlaXlnKjnur_mm7TmlrDlt7LmnInpnIDmsYLvvIzmt7HlsYIgSGFybmVzcyDljbPml7bmm7_mjaLku43pnZ7pgJrnlKjliJrpnIDjgIIiLCJkaXNwb3NpdGlvbiI6ImNhbmRpZGF0ZSIsImNhbmRpZGF0ZXMiOlsiY2FuZC1odW1hbi1nYXRlZC1oYXJuZXNzLWNhcGFiaWxpdHktdG9vbHMiLCJjYW5kLXJlYWx0aW1lLXNlbGYtZXZvbHV0aW9uLWRlbWFuZC1kZXBlbmRzLW9uLWxheWVyIiwiY2FuZC1oYXJuZXNzLWxheWVyLXNlbGYtaW1wcm92ZW1lbnQiXSwicmVhc29uIjoiIn0 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiLor4TmtYvlh4blhaXkuI7ov4fluqblt6XnqIvnu5PorroiLCJub3RlX3JlZnMiOlsiQjQwIl0sInN1bW1hcnkiOiLkurrlt6Xor4TmtYsvcmVmaW5lbWVudOOAgeWPjemmiOS_oeWPt-aIkOacrOS4jiBEU0gg5a-56YCa55So5Zui6Zif6L-H5bqm5bel56iL55qE57uT6K6644CCIiwiZGlzcG9zaXRpb24iOiJjYW5kaWRhdGUiLCJjYW5kaWRhdGVzIjpbImNhbmQtcHJpbWUtYWdlbnQtcmVmaW5lbWVudC13b3JrZmxvdyIsImNhbmQtaHVtYW4tZ2F0ZWQtaGFybmVzcy1jYXBhYmlsaXR5LXRvb2xzIiwiY2FuZC1hZ2VudC1zZWxmLWV2b2x1dGlvbi1ldmFsdWF0aW9uLWdhdGUiLCJjYW5kLWRzaC1ydW50aW1lLXNlbGYtZXZvbHV0aW9uLW92ZXJlbmdpbmVlcmluZyIsImNhbmQtcmVhbHRpbWUtc2VsZi1ldm9sdXRpb24tZGVtYW5kLWRlcGVuZHMtb24tbGF5ZXIiXSwicmVhc29uIjoiIn0 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiLlj4LogIPotYTmlpnkuI7ml7bmlYjmgKfmnaXmupAiLCJub3RlX3JlZnMiOlsiQjQ1Il0sInN1bW1hcnkiOiLorrrmlofjgIHmoYbmnrbmlofmoaPjgIHkvZzogIXorr7mg7Plkozkuqflk4HniYjmnKzop4Llr5_nmoTmnaXmupDliJfooajjgIIiLCJkaXNwb3NpdGlvbiI6ImNhbmRpZGF0ZSIsImNhbmRpZGF0ZXMiOlsiY2FuZC1nZW5lcmF0aXZlLWFyY2hpdGVjdHVyZS1ydW50aW1lLWNvbXBvbmVudHMiXSwicmVhc29uIjoiIn0 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUIyIiwibm90ZV9yZWZzIjpbIkIyIl0sInN1bW1hcnkiOiJsZWdhY3kgTm90ZSBibG9jayByZXRhaW5lZCBvdXRzaWRlIGNhbmRpZGF0ZSBjb3ZlcmFnZSIsImRpc3Bvc2l0aW9uIjoibm90ZV9vbmx5IiwiY2FuZGlkYXRlcyI6W10sInJlYXNvbiI6ImxlZ2FjeSBjYW5kaWRhdGUgcHJvdG9jb2wgb25seSByZWZlcmVuY2VkIHNvdXJjZSBibG9ja3MifQ -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUI0Iiwibm90ZV9yZWZzIjpbIkI0Il0sInN1bW1hcnkiOiJsZWdhY3kgTm90ZSBibG9jayByZXRhaW5lZCBvdXRzaWRlIGNhbmRpZGF0ZSBjb3ZlcmFnZSIsImRpc3Bvc2l0aW9uIjoibm90ZV9vbmx5IiwiY2FuZGlkYXRlcyI6W10sInJlYXNvbiI6ImxlZ2FjeSBjYW5kaWRhdGUgcHJvdG9jb2wgb25seSByZWZlcmVuY2VkIHNvdXJjZSBibG9ja3MifQ -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUI1Iiwibm90ZV9yZWZzIjpbIkI1Il0sInN1bW1hcnkiOiJsZWdhY3kgTm90ZSBibG9jayByZXRhaW5lZCBvdXRzaWRlIGNhbmRpZGF0ZSBjb3ZlcmFnZSIsImRpc3Bvc2l0aW9uIjoibm90ZV9vbmx5IiwiY2FuZGlkYXRlcyI6W10sInJlYXNvbiI6ImxlZ2FjeSBjYW5kaWRhdGUgcHJvdG9jb2wgb25seSByZWZlcmVuY2VkIHNvdXJjZSBibG9ja3MifQ -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUI3Iiwibm90ZV9yZWZzIjpbIkI3Il0sInN1bW1hcnkiOiJsZWdhY3kgTm90ZSBibG9jayByZXRhaW5lZCBvdXRzaWRlIGNhbmRpZGF0ZSBjb3ZlcmFnZSIsImRpc3Bvc2l0aW9uIjoibm90ZV9vbmx5IiwiY2FuZGlkYXRlcyI6W10sInJlYXNvbiI6ImxlZ2FjeSBjYW5kaWRhdGUgcHJvdG9jb2wgb25seSByZWZlcmVuY2VkIHNvdXJjZSBibG9ja3MifQ -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUI4Iiwibm90ZV9yZWZzIjpbIkI4Il0sInN1bW1hcnkiOiJsZWdhY3kgTm90ZSBibG9jayByZXRhaW5lZCBvdXRzaWRlIGNhbmRpZGF0ZSBjb3ZlcmFnZSIsImRpc3Bvc2l0aW9uIjoibm90ZV9vbmx5IiwiY2FuZGlkYXRlcyI6W10sInJlYXNvbiI6ImxlZ2FjeSBjYW5kaWRhdGUgcHJvdG9jb2wgb25seSByZWZlcmVuY2VkIHNvdXJjZSBibG9ja3MifQ -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUI5Iiwibm90ZV9yZWZzIjpbIkI5Il0sInN1bW1hcnkiOiJsZWdhY3kgTm90ZSBibG9jayByZXRhaW5lZCBvdXRzaWRlIGNhbmRpZGF0ZSBjb3ZlcmFnZSIsImRpc3Bvc2l0aW9uIjoibm90ZV9vbmx5IiwiY2FuZGlkYXRlcyI6W10sInJlYXNvbiI6ImxlZ2FjeSBjYW5kaWRhdGUgcHJvdG9jb2wgb25seSByZWZlcmVuY2VkIHNvdXJjZSBibG9ja3MifQ -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUIxMiIsIm5vdGVfcmVmcyI6WyJCMTIiXSwic3VtbWFyeSI6ImxlZ2FjeSBOb3RlIGJsb2NrIHJldGFpbmVkIG91dHNpZGUgY2FuZGlkYXRlIGNvdmVyYWdlIiwiZGlzcG9zaXRpb24iOiJub3RlX29ubHkiLCJjYW5kaWRhdGVzIjpbXSwicmVhc29uIjoibGVnYWN5IGNhbmRpZGF0ZSBwcm90b2NvbCBvbmx5IHJlZmVyZW5jZWQgc291cmNlIGJsb2NrcyJ9 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUIxNCIsIm5vdGVfcmVmcyI6WyJCMTQiXSwic3VtbWFyeSI6ImxlZ2FjeSBOb3RlIGJsb2NrIHJldGFpbmVkIG91dHNpZGUgY2FuZGlkYXRlIGNvdmVyYWdlIiwiZGlzcG9zaXRpb24iOiJub3RlX29ubHkiLCJjYW5kaWRhdGVzIjpbXSwicmVhc29uIjoibGVnYWN5IGNhbmRpZGF0ZSBwcm90b2NvbCBvbmx5IHJlZmVyZW5jZWQgc291cmNlIGJsb2NrcyJ9 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUIxNiIsIm5vdGVfcmVmcyI6WyJCMTYiXSwic3VtbWFyeSI6ImxlZ2FjeSBOb3RlIGJsb2NrIHJldGFpbmVkIG91dHNpZGUgY2FuZGlkYXRlIGNvdmVyYWdlIiwiZGlzcG9zaXRpb24iOiJub3RlX29ubHkiLCJjYW5kaWRhdGVzIjpbXSwicmVhc29uIjoibGVnYWN5IGNhbmRpZGF0ZSBwcm90b2NvbCBvbmx5IHJlZmVyZW5jZWQgc291cmNlIGJsb2NrcyJ9 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUIxOCIsIm5vdGVfcmVmcyI6WyJCMTgiXSwic3VtbWFyeSI6ImxlZ2FjeSBOb3RlIGJsb2NrIHJldGFpbmVkIG91dHNpZGUgY2FuZGlkYXRlIGNvdmVyYWdlIiwiZGlzcG9zaXRpb24iOiJub3RlX29ubHkiLCJjYW5kaWRhdGVzIjpbXSwicmVhc29uIjoibGVnYWN5IGNhbmRpZGF0ZSBwcm90b2NvbCBvbmx5IHJlZmVyZW5jZWQgc291cmNlIGJsb2NrcyJ9 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUIyNSIsIm5vdGVfcmVmcyI6WyJCMjUiXSwic3VtbWFyeSI6ImxlZ2FjeSBOb3RlIGJsb2NrIHJldGFpbmVkIG91dHNpZGUgY2FuZGlkYXRlIGNvdmVyYWdlIiwiZGlzcG9zaXRpb24iOiJub3RlX29ubHkiLCJjYW5kaWRhdGVzIjpbXSwicmVhc29uIjoibGVnYWN5IGNhbmRpZGF0ZSBwcm90b2NvbCBvbmx5IHJlZmVyZW5jZWQgc291cmNlIGJsb2NrcyJ9 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUIyNyIsIm5vdGVfcmVmcyI6WyJCMjciXSwic3VtbWFyeSI6ImxlZ2FjeSBOb3RlIGJsb2NrIHJldGFpbmVkIG91dHNpZGUgY2FuZGlkYXRlIGNvdmVyYWdlIiwiZGlzcG9zaXRpb24iOiJub3RlX29ubHkiLCJjYW5kaWRhdGVzIjpbXSwicmVhc29uIjoibGVnYWN5IGNhbmRpZGF0ZSBwcm90b2NvbCBvbmx5IHJlZmVyZW5jZWQgc291cmNlIGJsb2NrcyJ9 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUIzMCIsIm5vdGVfcmVmcyI6WyJCMzAiXSwic3VtbWFyeSI6ImxlZ2FjeSBOb3RlIGJsb2NrIHJldGFpbmVkIG91dHNpZGUgY2FuZGlkYXRlIGNvdmVyYWdlIiwiZGlzcG9zaXRpb24iOiJub3RlX29ubHkiLCJjYW5kaWRhdGVzIjpbXSwicmVhc29uIjoibGVnYWN5IGNhbmRpZGF0ZSBwcm90b2NvbCBvbmx5IHJlZmVyZW5jZWQgc291cmNlIGJsb2NrcyJ9 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUIzMSIsIm5vdGVfcmVmcyI6WyJCMzEiXSwic3VtbWFyeSI6ImxlZ2FjeSBOb3RlIGJsb2NrIHJldGFpbmVkIG91dHNpZGUgY2FuZGlkYXRlIGNvdmVyYWdlIiwiZGlzcG9zaXRpb24iOiJub3RlX29ubHkiLCJjYW5kaWRhdGVzIjpbXSwicmVhc29uIjoibGVnYWN5IGNhbmRpZGF0ZSBwcm90b2NvbCBvbmx5IHJlZmVyZW5jZWQgc291cmNlIGJsb2NrcyJ9 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUIzMyIsIm5vdGVfcmVmcyI6WyJCMzMiXSwic3VtbWFyeSI6ImxlZ2FjeSBOb3RlIGJsb2NrIHJldGFpbmVkIG91dHNpZGUgY2FuZGlkYXRlIGNvdmVyYWdlIiwiZGlzcG9zaXRpb24iOiJub3RlX29ubHkiLCJjYW5kaWRhdGVzIjpbXSwicmVhc29uIjoibGVnYWN5IGNhbmRpZGF0ZSBwcm90b2NvbCBvbmx5IHJlZmVyZW5jZWQgc291cmNlIGJsb2NrcyJ9 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUIzNCIsIm5vdGVfcmVmcyI6WyJCMzQiXSwic3VtbWFyeSI6ImxlZ2FjeSBOb3RlIGJsb2NrIHJldGFpbmVkIG91dHNpZGUgY2FuZGlkYXRlIGNvdmVyYWdlIiwiZGlzcG9zaXRpb24iOiJub3RlX29ubHkiLCJjYW5kaWRhdGVzIjpbXSwicmVhc29uIjoibGVnYWN5IGNhbmRpZGF0ZSBwcm90b2NvbCBvbmx5IHJlZmVyZW5jZWQgc291cmNlIGJsb2NrcyJ9 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUIzNSIsIm5vdGVfcmVmcyI6WyJCMzUiXSwic3VtbWFyeSI6ImxlZ2FjeSBOb3RlIGJsb2NrIHJldGFpbmVkIG91dHNpZGUgY2FuZGlkYXRlIGNvdmVyYWdlIiwiZGlzcG9zaXRpb24iOiJub3RlX29ubHkiLCJjYW5kaWRhdGVzIjpbXSwicmVhc29uIjoibGVnYWN5IGNhbmRpZGF0ZSBwcm90b2NvbCBvbmx5IHJlZmVyZW5jZWQgc291cmNlIGJsb2NrcyJ9 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUIzNyIsIm5vdGVfcmVmcyI6WyJCMzciXSwic3VtbWFyeSI6ImxlZ2FjeSBOb3RlIGJsb2NrIHJldGFpbmVkIG91dHNpZGUgY2FuZGlkYXRlIGNvdmVyYWdlIiwiZGlzcG9zaXRpb24iOiJub3RlX29ubHkiLCJjYW5kaWRhdGVzIjpbXSwicmVhc29uIjoibGVnYWN5IGNhbmRpZGF0ZSBwcm90b2NvbCBvbmx5IHJlZmVyZW5jZWQgc291cmNlIGJsb2NrcyJ9 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUIzOSIsIm5vdGVfcmVmcyI6WyJCMzkiXSwic3VtbWFyeSI6ImxlZ2FjeSBOb3RlIGJsb2NrIHJldGFpbmVkIG91dHNpZGUgY2FuZGlkYXRlIGNvdmVyYWdlIiwiZGlzcG9zaXRpb24iOiJub3RlX29ubHkiLCJjYW5kaWRhdGVzIjpbXSwicmVhc29uIjoibGVnYWN5IGNhbmRpZGF0ZSBwcm90b2NvbCBvbmx5IHJlZmVyZW5jZWQgc291cmNlIGJsb2NrcyJ9 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUI0MSIsIm5vdGVfcmVmcyI6WyJCNDEiXSwic3VtbWFyeSI6ImxlZ2FjeSBOb3RlIGJsb2NrIHJldGFpbmVkIG91dHNpZGUgY2FuZGlkYXRlIGNvdmVyYWdlIiwiZGlzcG9zaXRpb24iOiJub3RlX29ubHkiLCJjYW5kaWRhdGVzIjpbXSwicmVhc29uIjoibGVnYWN5IGNhbmRpZGF0ZSBwcm90b2NvbCBvbmx5IHJlZmVyZW5jZWQgc291cmNlIGJsb2NrcyJ9 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUI0MiIsIm5vdGVfcmVmcyI6WyJCNDIiXSwic3VtbWFyeSI6ImxlZ2FjeSBOb3RlIGJsb2NrIHJldGFpbmVkIG91dHNpZGUgY2FuZGlkYXRlIGNvdmVyYWdlIiwiZGlzcG9zaXRpb24iOiJub3RlX29ubHkiLCJjYW5kaWRhdGVzIjpbXSwicmVhc29uIjoibGVnYWN5IGNhbmRpZGF0ZSBwcm90b2NvbCBvbmx5IHJlZmVyZW5jZWQgc291cmNlIGJsb2NrcyJ9 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUI0MyIsIm5vdGVfcmVmcyI6WyJCNDMiXSwic3VtbWFyeSI6ImxlZ2FjeSBOb3RlIGJsb2NrIHJldGFpbmVkIG91dHNpZGUgY2FuZGlkYXRlIGNvdmVyYWdlIiwiZGlzcG9zaXRpb24iOiJub3RlX29ubHkiLCJjYW5kaWRhdGVzIjpbXSwicmVhc29uIjoibGVnYWN5IGNhbmRpZGF0ZSBwcm90b2NvbCBvbmx5IHJlZmVyZW5jZWQgc291cmNlIGJsb2NrcyJ9 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUI0NCIsIm5vdGVfcmVmcyI6WyJCNDQiXSwic3VtbWFyeSI6ImxlZ2FjeSBOb3RlIGJsb2NrIHJldGFpbmVkIG91dHNpZGUgY2FuZGlkYXRlIGNvdmVyYWdlIiwiZGlzcG9zaXRpb24iOiJub3RlX29ubHkiLCJjYW5kaWRhdGVzIjpbXSwicmVhc29uIjoibGVnYWN5IGNhbmRpZGF0ZSBwcm90b2NvbCBvbmx5IHJlZmVyZW5jZWQgc291cmNlIGJsb2NrcyJ9 -->
<!-- eg:cc:2 eyJtb2R1bGUiOiJsZWdhY3ktbm90ZS1vbmx5LUI0NiIsIm5vdGVfcmVmcyI6WyJCNDYiXSwic3VtbWFyeSI6ImxlZ2FjeSBOb3RlIGJsb2NrIHJldGFpbmVkIG91dHNpZGUgY2FuZGlkYXRlIGNvdmVyYWdlIiwiZGlzcG9zaXRpb24iOiJub3RlX29ubHkiLCJjYW5kaWRhdGVzIjpbXSwicmVhc29uIjoibGVnYWN5IGNhbmRpZGF0ZSBwcm90b2NvbCBvbmx5IHJlZmVyZW5jZWQgc291cmNlIGJsb2NrcyJ9 -->

## 用户补充

