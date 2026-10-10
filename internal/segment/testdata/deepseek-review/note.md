---
id: 'n-20260929-deepseek-harness-review'
source: 's-20260915-deepseek-harness-deepseek'
title: '对 DeepSeek Harness 的一次 deepseek：审阅式学习版'
created_at: '2026-09-29'
updated_at: '2026-09-29T14:34:01+08:00'
tags:
  - 'agent-harness'
  - 'deepseek'
  - 'self-evolution'
---

## 整理正文

<!-- eg:nr:1 eyJrIjoicyIsInMiOiJMMi1MOSJ9 -->
对 DeepSeek Harness 的一次 deepseek：过度工程，还是为自进化而建？

作者：Zhenjia
发布日期：2026-08-20

2026 年 8 月，DeepSeek 发布了自己的 DeepSeek Harness（下面简称 DSH）。相比于我们常见的其他现成 harness（例如 Claude Code、Codex、OpenCode、Pi），它的发布还顺带了一篇看起来玄之又玄的论文和库——《A Programming Paradigm for Spatiotemporal Composability》1（时空可组合性的编程范式），以及基于该论文的 Cordis 插件框架2——并且它的设计看起来具有严重的过度工程。看在论文第一作者也是个东方厨的面子上，我对这个 harness 也进行了一遍 deepseek。

本文写于 2026 年 8 月中，基于 0.1.0 版本的 DSH。

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJndWlkZSJ9 -->
> **[Agent 导读]** 这篇文章适合沿着一条连续问题链学习：生产级 Agent 由什么组成；不同 Harness 把核心边界画在哪里；DSH 为什么把一切做成插件；这种开放度怎样服务自进化；为什么作者最后仍判断它在当下过度工程。先理解事实结构，再判断作者的工程结论。

<!-- eg:nr:1 eyJrIjoicyIsInMiOiJMMTEtTDMxIn0 -->
### 一、Agent 的基本组成

在开始之前先说清楚：一个 agent 到底由什么组成。

2026 年大家对 agent 的定义基本统一为 `Agent = Model + Harness`。更进一步，对于 harness 来讲，大家一般认为它至少包括以下两个部分3：

**上下文管理**：决定每次发给模型的请求里有什么、按什么顺序。例如系统提示词（claude.md/agent.md）、工具的定义、RAG/memory、skill，以及当前的系统状态（例如当前时间、运行系统等等）。

**工具接口**：模型能调用什么。例如文件操作（Read/Write/Edit）、命令执行（Bash）、网络请求（WebFetch/WebSearch）、数据库查询、MCP（Model Context Protocol）协议工具等。

在生产环境中，一般还会有以下三个部分：

**约束**：限定 agent 能做什么、不能做什么。例如工具权限控制（Claude Code 的低/中/高风险评级）、需要人工批准的操作（发通知、删除数据、git push）、沙箱隔离、资源限制（超时、内存、并发数）、故障安全默认值等等。

**验证**：检查 agent 做得对不对。因为现有的 transformer 结构导致 LLM 一定存在幻觉，所以不能通过 llm 的输出来判断它是否真的完成了某件事情，而是要通过检查它执行的结果。例如当 agent 说”退款成功”时，要去查数据库订单状态，发现状态确实是”已退款”才通过。

**纠正**：做错了怎么补救。因为生产环境中失败是常态（网络抖动、API 限流、文件占用等），agent 需要能够优雅地处理故障。核心原则是在确认无法恢复之前不暴露中间态。例如 API 调用超时时，先静默重试 2-3 次，只有确定失败了才告诉用户，而不是每次失败都报错。

![Agent Harness 五要素](https://img.zhenjia.dev/agent-harness-components.png)
图片来自李博杰《[AI Agents in Depth](https://bojieli.github.io/ai-agent-book/)》

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJlbXBoYXNpcyJ9 -->
> **[Agent 强调]** 这里的五项不是并列工具清单，而是生产级 Harness 的职责分层：上下文和工具负责让 Agent 能行动，约束、验证与纠正负责让行动可控、结果可信、失败可恢复。

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJkaXN0aW5jdGlvbiJ9 -->
> **[Agent 辨析]** “工具接口”回答模型可以调用什么；“约束”回答哪些调用可以发生；“验证”回答调用结果是否真实达成目标；“纠正”回答失败后怎样恢复。把四者混成一个工具函数，会让权限、正确性和恢复责任难以单独治理。

<!-- eg:nr:1 eyJrIjoicyIsInMiOiJMMzItTDYyIn0 -->
在有了模型、工具、上下文之后，不管是哪个 harness，实际上最核心的部分都是一段 ReAct（Reasoning and Acting，推理与行动）4循环，也叫 agent loop。这个循环让模型交替生成推理轨迹和执行动作：推理帮助模型规划、追踪和更新行动计划，动作则让模型通过工具与外部环境交互获取信息。一个最基本的 ReAct 循环可以这样写：

```typescript
while (true) {
  // 1. 准备上下文：历史消息 + 可用工具 + 上一轮的工具调用结果
  const messages = buildContext(history, tools);

  // 2. 调用模型
  const response = await model.generate(messages);

  // 3. 检查是否结束
  if (response.stopReason === 'end_turn') {
    break;  // 模型判断任务完成
  }

  // 4. 执行工具调用
  if (response.toolCalls) {
    for (const call of response.toolCalls) {
      const result = await executeTool(call);
      history.push({ role: 'tool', content: result });
    }
  }

  // 5. 记录模型响应
  history.push(response);
}
```

这个循环最关键的地方有两个。一个是模型输出了工具调用请求，这时 harness 会去调用工具并把结果放到上下文再发给模型。工具怎么调、参数长什么样，都写在发给模型的 schema 里，所以工具调用能力就是指令遵从：模型能不能根据 schema 选对工具、把参数拼对。这是去年模型发展的主旋律。另一个是模型自己输出了任务已经结束的判断，此时才会真正结束这个循环——这就是所谓的长时间任务能力，像是 kimi k2.7 code、sonnet 5 之类的模型在自己的 context 中明明还有着下一步的信息却会主动停止工作，而 kimi k3、gpt 5.6 以及 opus 4.6 之后的一系列模型则可以在不设置 goal 的情况下继续推进，这里面的差别就是模型本身长时间任务能力的差别。

（注：此处的 agent loop 与 loop engineering 并不相通。agent loop 由模型自己判断循环是否结束；loop engineering 是用外部条件停循环，比如测试是不是全部通过。）

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJzdXBwbGVtZW50In0 -->
> **[Agent 补充]** 可以把这段循环看成状态机：模型响应决定下一状态，工具结果成为下一轮输入，`end_turn` 是模型给出的终止信号。Harness 负责执行和回填，但长期任务是否提前停止仍受模型判断能力影响。

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJ2ZXJpZmljYXRpb24ifQ -->
> **[Agent 待验证]** 原文对 kimi、sonnet、gpt 与 opus 具体版本的长任务能力比较具有明显时间性，也依赖作者当时的使用观察；学习 Agent Loop 机制时可以保留该例子，但不应把型号排序当成长期稳定知识。

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJzdW1tYXJ5In0 -->
> **[Agent 总结]** 本节建立后文的共同坐标：Agent 由 Model 与 Harness 组成；Harness 在生产环境承担五类职责；这些职责最终围绕 ReAct 循环组织模型调用、工具执行和结束判断。

<!-- eg:nr:1 eyJrIjoicyIsInMiOiJMNjQtTDczIn0 -->
### 二、Agent 开发的三种流派

现在 agent 开发基本可以分为下面这三种流派：

![Agent 开发三种流派](https://img.zhenjia.dev/harness-three-paradigms.png)
Agent 开发三种流派

虽然是三种流派，但是其实本质都是在一个框架或者SDK保留的核心之外去添加其他能力。

以开发一个具有退款权限的客服 agent 为例：

<!-- eg:nr:1 eyJrIjoicyIsInMiOiJMNzUtTDEwNiJ9 -->
**[Pydantic AI](https://ai.pydantic.dev/)**：一切都需要自己从零手搓。

```python
from pydantic_ai import Agent

agent = Agent(
    'deepseek:deepseek-v4',
    instructions='你是电商客服，处理订单查询和退款。',
)

@agent.tool_plain
def query_order(order_id: str) -> dict:
    """查询订单的状态和金额。"""
    return db.get_order(order_id)

@agent.tool_plain
def refund(order_id: str, amount: float) -> str:
    """给订单退款。"""
    order = db.get_order(order_id)
    if amount <= 0 or amount > order.paid:    # 需要检查退款金额为正数并且不能大于实际付款金额
        raise ValueError('退款金额非法')
    result = payment.refund(order_id, amount)
    assert db.get_order(order_id).status == 'refunded'   # 检测任务真的已经完成
    return result

result = agent.run_sync('订单 A1024 用户投诉重复扣款，核实后处理')
print(result.output)
result2 = agent.run_sync('把处理结果整理成回复发给用户',
                         message_history=result.new_messages())
```

金额检查和退完去查库确认都写在 `refund` 里，没有单独的 hook。循环和请求拼装在 `run_sync()` 里面。

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJlbXBoYXNpcyJ9 -->
> **[Agent 强调]** 这段代码把业务动作、执行前检查和执行后验证放在同一个 `refund` 函数里。`payment.refund` 是动作；金额范围检查是约束；再次查询订单状态是验证。

<!-- eg:nr:1 eyJrIjoicyIsInMiOiJMMTA4LUwxNDkifQ -->
**成品（[Claude Agent SDK](https://code.claude.com/docs/en/agent-sdk)）**：agent 已经造好了，你的开发从第一行起就是在它预留的扩展点上放东西。金额检查从工具函数里抽出来，挂到 PreToolUse；退完再查一次挂 PostToolUse。自定义工具**只有 MCP（Model Context Protocol）一条路**——包成进程内的 MCP 服务（可以理解为把工具包成一个走标准协议的本地 RPC 服务），没有裸函数直接塞的形态：

```typescript
import { query, tool, createSdkMcpServer } from "@anthropic-ai/claude-agent-sdk";
import { z } from "zod";

const queryOrder = tool("query_order", "查询订单的状态和金额",
  { order_id: z.string() },
  async (a) => ({ content: [{ type: "text", text: await db.getOrder(a.order_id) }] }));
const refund = tool("refund", "给订单退款",
  { order_id: z.string(), amount: z.number() },
  async (a) => ({ content: [{ type: "text", text: await payment.refund(a) }] }));
const server = createSdkMcpServer({ name: "support", version: "1.0.0",
  tools: [queryOrder, refund] });

// refund 执行前先过这段检查
const checkRefund = async (input) => {
  const order = await db.getOrder(input.tool_input.order_id);
  if (input.tool_input.amount <= 0 || input.tool_input.amount > order.paid) {
    return { permissionDecision: "deny", reason: "退款金额非法" };
  }
  return {};
};

for await (const msg of query({
  prompt: "订单 A1024 用户投诉重复扣款，核实后处理",  // 任务输入（用户消息）
  options: {
    systemPrompt: {
      preset: "claude_code",
      append: "你是电商客服，处理订单查询和退款。"
    },  // 另一条路：项目根目录放 CLAUDE.md，会作为项目指令加载
    mcpServers: { support: server },
    allowedTools: ["mcp__support__query_order", "mcp__support__refund"],
    hooks: {
      PreToolUse: [{ matcher: "mcp__support__refund", hooks: [checkRefund] }],
      PostToolUse: [{ matcher: "mcp__support__refund", hooks: [verifyRefundHook] }],
    },
  }
})) { /* ... */ }
```

`query()` 是产品入口，循环和权限系统都在里面。

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJlbXBoYXNpcyJ9 -->
> **[Agent 强调]** 同一个退款流程被拆到不同扩展点：工具通过进程内 MCP 服务注册，`PreToolUse` 在执行前拒绝非法金额，`PostToolUse` 在执行后验证结果，`query()` 拥有循环和权限系统。

<!-- eg:nr:1 eyJrIjoicyIsInMiOiJMMTUxLUwyMDEifQ -->
**平台（[Eve](https://eve.dev/docs)）**：把开发简化到了极致，你写的不是程序，是几个声明文件。没有 PreToolUse 那种执行前的位置。

```plaintext
agent/
├── instructions.md          ← 系统提示词就是这个文件，内容就是"你是电商客服，处理订单查询和退款。"
├── tools/
│   ├── query-order.ts       ← 文件名就是工具名，结构和 refund.ts 一样
│   └── refund.ts
└── hooks/
    └── audit-refund.ts      ← 订阅运行时事件（只读）
```

```typescript
// agent/tools/refund.ts —— 官方文档的示例本来就是退款
import { defineTool } from "eve/tools";
import { z } from "zod";

export default defineTool({
  description: "Refund a charge.",
  inputSchema: z.object({
    orderId: z.string(),
    amount: z.number().positive(),   // 正数检查可以用 schema 声明
  }),
  async execute(input) {
    const order = await db.getOrder(input.orderId);
    if (input.amount > order.paid) throw new Error("退款超过实付金额");
    // 要查数据的规则检查没有执行前的位置，只能写在这里面
    return payment.refund(input);
  },
});
```

```typescript
// agent/hooks/audit-refund.ts
import { defineHook } from "eve/hooks";
import { toolResultFrom } from "eve/tools";
import refund from "../tools/refund";

export default defineHook({
  events: {
    async "action.result"(event) {
      const r = toolResultFrom(event.data.result, refund);
      if (r) auditLog.write(r);
      // 官方文档原话 "Handlers are observe-only. They cannot inject model context"
      // ——查出退款有问题只能对外告警，反馈不回模型
    },
  },
});
```

写完这些文件，剩下的都不归你管：没有 main 函数，没有发消息的调用——部署上去之后，运行、存储、恢复、对话入口全是平台的。正数能写进 schema；要查库的金额上限只能塞进 `execute`。事后 hook 官方写明 observe-only，查出问题喂不回模型。

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJkaXN0aW5jdGlvbiJ9 -->
> **[Agent 辨析]** “平台托管更多”不等于“所有位置都更容易扩展”。Eve 减少运行与持久化负担，但 observe-only Hook 不能把验证失败送回模型；托管范围和可干预范围是两个不同维度。

<!-- eg:nr:1 eyJrIjoicyIsInMiOiJMMjAzLUwyMTAifQ -->
Pydantic AI 的核心只有 `run_sync()` 里那一圈循环和请求拼装，其余全都交给你来改；Claude Agent SDK 的核心是整个产品，循环、内置工具、权限系统、上下文管理都在里面，你的插件放在它预留的清单位置上；Eve 的核心连运行环境都包括，循环、持久化、恢复、对话入口全在平台侧，你交出去的只有 agent 的内容物文件。三种方案差别在核心的边界画在哪里。

![核心的边界画在哪里](https://img.zhenjia.dev/core-boundary.png)
核心的边界画在哪里

开发者要关注的东西越来越少。但是与此同时，**当你采用这些现成的东西、又不去修改它们的源代码的时候，你能改动的范围是受限的——受限于它开放出来的扩展点。**比如说如果你想在消息拼装完成之后，发给 provider 之前用固定规则或者本地小模型对它进行一下压缩（不是在达到上下文上限之后被动压缩，而是采用固定规则或者小模型进行主动压缩）或者做安全审核，三家的做法完全不同。

Pydantic AI 有正式的位置，两个函数的事：

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJzdW1tYXJ5In0 -->
> **[Agent 总结]** Library、Product、Platform 的差别不是功能数量，而是核心边界、扩展点与运行责任如何分配。需求越靠近不可修改的核心，接入通常越间接。

<!-- eg:nr:1 eyJrIjoicyIsInMiOiJMMjEyLUwyMjYifQ -->
```python
from pydantic_ai import Agent, ModelMessage
from pydantic_ai.capabilities import ProcessHistory

def compress(messages: list[ModelMessage]) -> list[ModelMessage]:
    return local_compress(messages)               # 固定规则或本地小模型压缩

def security_review(messages: list[ModelMessage]) -> list[ModelMessage]:
    return [redact_pii(m) for m in messages]  # 审核与脱敏

agent = Agent('deepseek:deepseek-v4',
              capabilities=[ProcessHistory(compress), ProcessHistory(security_review)])
```

`ProcessHistory` 收到的就是即将发给 provider 的完整消息列表，返回什么就发什么，多个处理函数按声明顺序依次过。

<!-- eg:nr:1 eyJrIjoicyIsInMiOiJMMjI4LUwyMzQifQ -->
Claude Agent SDK 没有这个位置：三十多个 hook 事件没有一个站在”请求已拼装、未发出”这里。离得最近的两个，UserPromptSubmit 只能改用户提交的那条消息；PostToolBatch 虽然明写着在下一次模型调用之前触发，但它能做的是往里注入一段 context，同样摸不到拼好的系统提示词、工具 schema 和完整历史。唯一的路是把流量指到你自建的代理上：

```bash
export ANTHROPIC_BASE_URL=http://localhost:8080   # SDK 的请求全部改发到这里
```

代理收下完整请求、拆开 JSON、压缩和审核、转发给 Anthropic、再把流式响应原样传回——需求没变，工程从两个函数变成维护一个中间层服务。

<!-- eg:nr:1 eyJrIjoicyIsInMiOiJMMjM2LUwyNjAifQ -->
而 Eve 相比于 Claude Agent SDK 更加偏向于声明式，他原生也只能通过转接一个 proxy 来完成这个事情。但是因为他底层是 [Vercel AI SDK](https://ai-sdk.dev/)，可以通过 AI SDK 自己的 middleware（中间件）来解决这个问题：

```typescript
// agent/agent.ts
import { defineAgent } from "eve";
import { wrapLanguageModel } from "ai";
import { anthropic } from "@ai-sdk/anthropic";

export default defineAgent({
  model: wrapLanguageModel({
    model: anthropic("claude-opus-4-8"),
    middleware: { transformParams: async ({ params }) => securityReview(compress(params)) },
  }),
});
```

根据上面的例子可以看出来，无论哪种流派，实际上结构都是一样的：都保留一个你碰不到的核心（agent 本体），再把一部分东西开放成可自定义的插件——例如工具、hook、检查。所以实际上这三种开发的流派的区别都只在于可自定义的插件的丰富程度。所谓的Agent开发实际上大多数是在各家框架或者SDK保留的核心之外进行插件的开发。

插件的开发可以分为两部分。一个是**插件本身的形态**：你交给系统的是命令式的代码（会执行的函数，系统在合适的时机调用它，比如 `@agent.tool_plain def refund` 和 Eve 的 `defineTool`），还是声明式的数据（描述性的文件和配置，系统读取它决定行为，比如 Claude Agent SDK 的 CLAUDE.md、hooks 配置、MCP 服务）。另一个是**组装的方式**：你如何把插件接进核心，是命令式的（你写代码去注册、去挂载，比如 main 里的装饰器），还是声明式的（你给一份描述——一个 options 对象、一个目录位置、一份清单——装配由核心完成，比如 Claude Agent SDK 的 options / CLAUDE.md，Eve 把文件放进约定目录）。

于是乎，这三种流派可以根据插件的形态和组装的方式进行一下划分：

插件的形态组装的方式Pydantic AI（库）命令式：带类型标注的函数命令式：main 是你的，装饰器挂上去Claude Agent SDK（成品）声明式为主：文件、配置、经 MCP 协议接入的服务声明式： CLAUDE.md/skill/hooks 配置文件Eve（平台）命令式：`defineTool` / `defineHook` 的代码声明式：文件树约定，放进哪个目录就挂到哪里

那么 DeepSeek Harness（DSH）的做法是什么呢？

<!-- eg:nr:1 eyJrIjoicyIsInMiOiJMMjYyLUwyNzYifQ -->
### 三、DSH：Everything is Plugin

DSH 与 Eve 相似，也是命令式的插件+声明式的组装，但是与 Eve 不同的是他极为开放，声称“一切皆插件”，整个 DeepSeek Harness 没有任何一个地方是核心，所有的地方都是插件。

还是以一个具有退款权限的电商客服 agent 为例，完整的一个项目是一个 npm 包，包含以下部分：

```plaintext
agent/
├── shop-support.cordis.yml   ← 完整组合清单。*.cordis.yml 是官方约定名（底层框架叫 Cordis），路径传给驱动
├── AGENTS.md                 ← 客服工作指令
├── package.json              ← 依赖：官方包 + @shop/dsh-tool-support
└── service.ts                ← TS SDK 驱动
```

这个 yml 文件里的每一部分都是一个插件。

<!-- eg:nr:1 eyJrIjoicyIsInMiOiJMMjc4LUwzMTYifQ -->
```yml
# 对外 JSON-RPC 服务，service.ts 连的就是它
- id: sdk-jsonrpc-server
  name: '@deepseek-ai/dsh-sdk-jsonrpc-server'

- id: llm-deepseek
  name: '@deepseek-ai/dsh-llm-deepseek'
  config:
    thinking: enabled
    reasoningEffort: max

# 官方聚合包：一行装进 agent loop、会话日志、提示词组装、工具执行管线
- id: agent-spine
  name: '@deepseek-ai/dsh-agent-spine-demo'
  config:
    workspaceContext:
      maxBytes: 65536
    skills:
      enabled: false
    toolBash: false
    toolJobs: false

- id: tools-support
  name: '@shop/dsh-tool-support'

- id: sessions
  name: '@deepseek-ai/dsh-session-persistence-jsonl'
  config:
    root: ./sessions

- id: fs-local
  name: '@deepseek-ai/dsh-fs-local'
```

`AGENTS.md` 里是客服的工作指令：

```md
你是电商客服，处理订单查询和退款。
```

<!-- eg:nr:1 eyJrIjoicyIsInMiOiJMMzE4LUwzNjcifQ -->
清单里 `tools-support` 那行指向的插件包要自己写——一个普通的 npm 包，入口是 Cordis 插件的 `apply(ctx)` 函数，有一点像 React 的写法。

```typescript
// @shop/dsh-tool-support 包的入口文件
import type { Context } from '@deepseek-ai/cordis'
import { defineTool, type PreToolDecision, type PostToolDecision } from '@deepseek-ai/dsh-tools'

export function apply(ctx: Context) {
  ctx.tools.register(defineTool({
    name: 'query_order',
    description: '查询订单的状态和金额。',
    parameters: { order_id: { type: 'string', required: true } },
    output: { schema: { type: 'object' }, render: (_a, v) => [{ type: 'text', text: JSON.stringify(v) }] },
    async execute(args) { return db.getOrder(args.order_id) },
  }))

  ctx.tools.register(defineTool({
    name: 'refund',
    description: '给订单退款。',
    parameters: {
      order_id: { type: 'string', required: true },
      amount: { type: 'number', required: true },
    },
    output: { schema: { type: 'string' }, render: (_a, v) => [{ type: 'text', text: v }] },
    async execute(args) { return payment.refund(args.order_id, args.amount) },
  }))

  // 执行前的金额检查
  ctx.on('tools/pre-execute', async (exec, next): Promise<PreToolDecision> => {
    if (exec.name !== 'refund') return next()
    const order = await db.getOrder(exec.arguments.order_id)
    if (exec.arguments.amount <= 0 || exec.arguments.amount > order.paid) {
      return { kind: 'deny', reason: '退款金额非法' }
    }
    return next()
  })

  // 执行后，检测任务真的已经完成
  ctx.on('tools/post-execute', async (exec, result, next): Promise<PostToolDecision> => {
    if (exec.name !== 'refund' || result.isError) return next()
    const order = await db.getOrder(exec.arguments.order_id)
    if (order.status !== 'refunded') {
      return { kind: 'block', feedback: [{ type: 'text', text: '退款接口返回成功但订单状态未变更，请核实' }] }
    }
    return next()
  })
}
```

验证那里的 `block` 会把这次调用的结果直接变成带纠正反馈的失败——和 Eve 那个只能对外告警的 hook 不同，模型自己会看到失败原因。

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJlbXBoYXNpcyJ9 -->
> **[Agent 强调]** 退款后的 `block` 是关键差异：它不是只向外部告警，而是把验证失败变成模型可见的纠正反馈，使 Agent 有机会继续处理。

<!-- eg:nr:1 eyJrIjoicyIsInMiOiJMMzY5LUw0MTUifQ -->
最后是对外跑起来。你的服务进程通过官方 TypeScript SDK 驱动这份组合——写法照官方示例，一个实例跨多次 `run()` 复用：

```typescript
// service.ts
import { DeepSeekHarness } from '@deepseek-ai/dsh-sdk-client'

const harness = new DeepSeekHarness({
  launch: {
    command: 'dsh-jsonrpc-agent',
    args: ['shop-support.cordis.yml'],
    cwd: process.cwd(),
  },
  cwd: process.cwd(),
  provider: 'deepseek-official',
  model: 'deepseek-v4-pro',
})

export async function handleMessage(customerId: string, text: string) {
  const result = await harness.run(text, { sessionId: customerId })
  return result.finalResponse
}
```

上文那种压缩需求，在 DSH 里可以这样写：

```typescript
// @shop/dsh-compressor
import type { Context } from '@deepseek-ai/cordis'
import type { UserMessage } from '@deepseek-ai/dsh-llm'

export function apply(ctx: Context) {
  ctx.on('agent/pre-step', async (_payload, next) => {
    const decision = await next()
    if (decision.kind !== 'enter') return decision
    return { kind: 'enter', messages: await redactAndCompress(decision.messages) }
  })
}

async function redactAndCompress(messages: UserMessage[]): Promise<UserMessage[]> {
  const redacted = messages.map(redactPii)   // 固定规则脱敏
  return localModel.compress(redacted)       // 本地小模型压缩
}
```

挂在插件的地方叫 `agent/pre-step`，在这里你可以对于这一轮即将要发出去的消息进行修改（但是注意为了维持缓存的稳定，这里只允许修改这一轮新增的消息）。

这里其实我们只需要关注`await redactAndCompress(decision.messages)`这里，他之前的内容是为了把它接入dsh发送消息的流水线, 任何一个挂在 `agent/pre-step` 上的插件都是这么写的，这里就不深入了。

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJkaXN0aW5jdGlvbiJ9 -->
> **[Agent 辨析]** `agent/pre-step` 虽位于模型调用前，但原文明确限制它只能修改本轮新增消息，以维持缓存稳定；它不是任意重写完整历史的中间件。

<!-- eg:nr:1 eyJrIjoicyIsInMiOiJMNDE3LUw0MzMifQ -->
装进清单同样是一行：

```yaml
- id: compressor
  name: '@shop/dsh-compressor'
```

甚至于连最核心的 agent loop 也是一个单独的 npm 包。

```yaml
- id: agent-loop
      name: '@deepseek-ai/dsh-agent-loop'
      config:
        agents: []
```

如果你想更改的话，把 `name` 换成自己写的 agent loop 包就可以了。

<!-- eg:nr:1 eyJrIjoicyIsInMiOiJMNDM1LUw0NDYifQ -->
根据上面的例子可以看出来，DSH 和 Eve 的差别不在结构，在声明能覆盖到哪一层：Eve 的文件树里能声明的只有 agent 的内容物——工具、指令、审批，循环、持久化、压缩是平台的，声明不到；DSH 的清单里这些全是插件。**DSH 是 Eve 的形态，库的开放度。**

而他最明显的过度工程也来自于此。为什么要把所有组件都做成插件？就拿 agent loop 来说，到 2026年为止，大家的Agent Loop基本都是ReAct。那它为什么还要构建一套如此开放的插件形态？谜底就在谜面上，随着DSH一起发布的时空可组合性论文，以及它的实现 Cordis，自己给了答案：为了时空可组合性。

在 LLM 来临之前，软件基本是在编译期定死的，如果要增加新的功能，就需要打一个新的包然后重启（虽然有低代码的方式能够通过组合的方式产生新的功能，但是实际上也是遵循了产生新的功能之后重启的方案）。而在LLM出现之后这一情况迅速发生了变化，从23年开始火起来的 text-to-SQL：软件在运行时产生全新的 SQL 代码并且在不需要重启的情况下执行它，到25年年底出现的 Generative UI：软件在运行时根据需要产生定制化的 UI 界面5，这就是论文里所描述的“时间可组合性”，它要的是软件还在跑、请求还在进的时候，能把里面某个组件换掉——对应到上面的清单，就是进程不重启，把 `agent-loop` 那一行换掉。

另外一半则是空间可组合性：当软件中的某一部分在运行时被换掉，软件本身需要知道新的这一部分是什么以及在哪里。就拿前面的例子，如果整个Agent以import的方式直接依赖于系统自带的`'@deepseek-ai/dsh-agent-loop'`，当**Agent**在运行时把它更换成在运行时写出来的更好用的`'@agent/better-loop'`之后，虽然包已经更换完毕，但是Agent还握着启动时的旧地址，请求依然会打到一个已经不存在的旧包上。因此这里需要一层抽象出来的接口（interface），也就是清单上的`id: agent-loop`，`name`则是谁在当下实现这个能力。整份yml是一个随时更新的花名册，写的是这个软件此刻需要哪些能力、各自由哪个包负责；换`name`之后，依赖这条能力的插件按登记卸掉再重新安装，Agent不再写死对官方那个包的依赖。

![Spatiotemporal composability: id is the interface, name is the implementation](https://img.zhenjia.dev/spacetime-composability-en.png)
Spatiotemporal composability: id is the interface, name is the implementation

综上所述，DSH这明显的过度工程都是为了Agent在运行时能够最大限度的修改自身的代码，而这指向了2026年下半年开始最火的一个词：Agent Self-Evolution（自进化；也称 Recursive Self-Improving，递归自我改进 / RSI Agents）

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJzdW1tYXJ5In0 -->
> **[Agent 总结]** DSH 可概括为“声明式清单装配 + 深层命令式插件”：稳定 `id` 与可变 `name` 分离，使工具、持久化、消息处理乃至 Agent Loop 能在运行中替换并重新绑定依赖。

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJ2ZXJpZmljYXRpb24ifQ -->
> **[Agent 待验证]** 文章基于 DSH 0.1.0 和预印本解释设计意图。运行中替换 Agent Loop 时的状态迁移、并发一致性与失败回滚是否达到生产要求，仍需要独立实现证据。

<!-- eg:nr:1 eyJrIjoicyIsInMiOiJMNDQ4LUw0NjEifQ -->
### 四、Agent Self-Evolution（自进化）

到目前为止我认为把这件事阐释得最清楚的是 Lilian Weng 2026 年 7 月发表的《Harness Engineering for Self-Improvement》6。她的文章里定义了很多和 Agent Self-Evolution 相关的概念，例如什么样的 agent 算是能够自进化的 agent。在这里我们只挑两个最重要的东西来讲。

一个是为什么近期Agent的自我改进更有可能发生在Harness层而非Model层，虽然从2022年至今，新模型的发布速度越来越快，只拿 OpenAI 和 Anthropic 两家来看（算上小版本），2023 年平均每月发布 0.5 个新模型，2024 年平均每月约 0.9 个，2025 年平均每月约 2.2 个，到 2026 年 1 至 8 月已经升到平均每月约 2.5 个。但是让模型自行修改自身的权重、并且让这个修改在几十分钟内完成生效，到现在依然是不太可能的：一个难题是训练的稳定性（例如 DeepSeek V4 Pro 0813 半夜上线效果很差、被紧急下线，被调侃为神鬼二象性7），另外一个是评测的片面性（例如虽然 Opus 5 跑分能赢 Fable 5，但是实际使用体感却远远不如）。而修改 Harness 大多只是改几个 md 文件的事，连几十B参数的小模型都可以很快完成。

另外一件事情则是自我改进的对象——按改的东西分五级：改提示词、改上下文结构、改工作流、改 harness 代码、改优化器自己的代码。拿 Claude Code 的配置举例：第一级改提示词。项目根的 `CLAUDE.md`、`~/.claude/CLAUDE.md`，`rule.md`，以及自动记忆目录里的 `MEMORY.md`，写的都是给模型看的一段文本8。例如现在Claude Code会自动记录memory，就是发生在这一层级；第二级改有结构的上下文，对应到Claude Code就是Skill。第三级改执行怎么编排，在Claude Code里则对应的是Sub Agent与Dynamic Workflow。第四级开始动 harness 代码本身。Claude Code 的配置能走到的最深一层是 `.claude/settings.json` 里的 hook，例如之前展示过的`PreToolUse` 以及`PostToolUse`。第五级改的则是去修改优化器的代码，例如假设你有一个优化提示词的程序，在Skill出现之前他只需要考虑提示词修改是写在CLAUDE.md里还是rule.md里，而在Skill出现之后，能够达到这个自改进级别的Agent则需要去修改优化器的代码，让优化器也去考虑这个提示词是否应该修改到skill.md里。

![自我改进的五级](https://img.zhenjia.dev/self-improve-levels.png)
自我改进的五级

于是乎，DSH 选择如此过度工程地把所有的包都做到插件形态的答案便呼之欲出：为了能够让Agent在运行时最大程度的去修改自身的Harness代码，并让这个改动实时生效。

那么现在我们是否有“让Agent修改自身harness代码达到自进化，并且要求这个改动实时生效”的需求了呢？或者说，这个需求的意义在哪里呢？

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJ2ZXJpZmljYXRpb24ifQ -->
> **[Agent 待验证]** 模型发布频率、具体版本表现以及“几十分钟内修改权重仍不现实”都带时间边界；后续引用时必须重新核验。

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJlbXBoYXNpcyJ9 -->
> **[Agent 强调]** 要区分“容易生成修改”和“修改后确实更好”。Harness 文件更容易被改写，只证明修改门槛较低，不自动证明改动正确、安全或值得上线。

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJkaXN0aW5jdGlvbiJ9 -->
> **[Agent 辨析]** 五级描述的是被修改对象逐步深入，不是能力成熟度或风险评分；第五级还会改变“如何寻找改进”的规则本身。

<!-- eg:nr:1 eyJrIjoicyIsInMiOiJMNDYzLUw0NzIifQ -->
### 五、DSH过度工程了吗

其实在运行时修改自身的代码或者配置并不是在Agent出现后才有的需求，在搜索、广告、推荐（搜广推）相关的业务里，早就出现了类似的需求，叫做离线学习（offline learning）和在线学习（online learning）。

**离线学习**是把用户的点击、搜索、曝光等行为先攒成一批日志，再用这批数据训练新的排序模型或策略。训练完成后，经过评测、发布，线上服务才会换成新版本。它和普通的软件发布很像：数据是输入，训练是构建，模型上线是部署。因此离线学习的更新周期通常是几小时、几天甚至更久，但优点是训练过程容易复现、评测和回滚。

**在线学习**则是在服务运行期间，持续把新产生的行为反馈给模型或策略，让它增量更新，而不必等下一轮完整的离线训练。比如用户刚看完一个视频，系统把这个行为写入用户画像或更新模型参数，后续请求就会受到这次行为的影响。这里的“在线”说的是数据和更新链路在线发生，不只是推荐接口响应得快。

不过，“看完视频后立刻收到相关视频”本身还不能证明系统在做在线学习。系统可能只是实时更新了这个用户的兴趣标签或 embedding，也可能只是根据已有模型重新排序；只有当这次行为进入了训练过程，改变了模型或策略本身，才是严格意义上的在线学习。介于两者之间、每隔几分钟或几小时批量更新一次的方案，通常叫 **近线学习（nearline learning）**。

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJkaXN0aW5jdGlvbiJ9 -->
> **[Agent 辨析]** 实时响应、实时更新用户状态和在线学习不是同一件事。严格的在线学习要求新行为进入训练或策略更新并改变后续决策机制。

<!-- eg:nr:1 eyJrIjoicyIsInMiOiJMNDczLUw0NzYifQ -->
那么至今为止，agent已经有实时在线学习和进化的需求了吗？这个问题不能简单回答“有”或“没有”，要看“进化”改到了哪一层。如果说的是运行中吸收任务反馈、更新记忆、用户状态或下一步的工作策略，这类需求已经存在；如果说的是 agent 在运行中自主修改 harness 的深层组件，甚至修改执行内核并立即替换生效，那么这目前仍主要是研究性需求和少数特定场景的需求，还没有成为通用 agent 产品的刚需。

从现成 harness 的实现来看，大多数产品目前只做到自主更新提示词或记忆这一层。例如 Claude Code 和 Codex 会把重要信息写进记忆文档，而更深一层的改动，例如 skill、sub agent、hook，通常还需要人工创建、审核或至少等到下一次运行才生效。

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJlbXBoYXNpcyJ9 -->
> **[Agent 强调]** 是否需要实时自进化必须按修改层级判断：运行中更新记忆与策略已经常见，运行中替换深层 Harness 内核仍是另一类成本和风险。

<!-- eg:nr:1 eyJrIjoicyIsInMiOiJMNDc3LUw0ODEifQ -->
至于 Harness 本身，虽然现在已经有 [Capability Creation](https://pydantic.dev/docs/ai/harness/capability-creation/)、[better-harness](https://github.com/langchain-ai/deepagents/tree/main/examples/better-harness) 这样的工具，但是他们还只停留在进化出对应的能力之后，由人工评测并且部署的阶段。因为相比于让 agent 自己去改写 Harness，如何评价 agent 改完这一段 Harness 之后的效果，才是更重要的事情。以 [Prime Agent](https://github.com/PrimeIntellect-ai/prime-agent) 为例（到今天为止我个人认为这个方向最突出的开源项目；它构建于 Pi 之上——一个和 DSH 相比，开放程度只差是否开放 Agent Loop 的 agent），它把“修改 Harness”放进了一条带评测和记录的 refinement 管线：agent 先提出改动，再运行任务或评测，只有被判定为有效的改动才会被保留，而不是放任 Agent 自己改完之后直接上线。

这也是在现在这个阶段 Agent 自进化很难像搜广推的在线学习那样做成在线自进化的原因。搜广推在线学习的信号客观（是否点击）、便宜（优化机器学习的策略单张显卡都能做到）、高频（对内容的点击频率远高于对 agent 的产物提出评价的频率）。而 agent 改完一段 harness，要知道这次改动是好是坏，信号是「任务有没有做得更好」——要跑一轮评测才有，不仅时间久、价格昂贵（耗费 token），而且很多时候评测集并不能完全反映改动的效果，还需要人来判断。

所以处在当下这个角度，对现在正在设计和开发 Agent 的团队而言，DSH 带有明显的过度工程。他的过度工程是为了运行时进化 harness、并且立刻生效这件事预留的——这件事现在还不是刚需。

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJzdXBwbGVtZW50In0 -->
> **[Agent 补充]** Prime Agent 的 refinement 是受控闭环：生成候选改动，运行任务或评测，记录结果，通过准入才保留。它把“能改”与“允许生效”分成两个阶段。

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJlbXBoYXNpcyJ9 -->
> **[Agent 强调]** 全文结论的关键支点是反馈信号差异：搜广推信号相对客观、便宜、高频；Harness 改动通常需要昂贵任务评测，且自动评测仍可能遗漏真实退化。

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJ2ZXJpZmljYXRpb24ifQ -->
> **[Agent 待验证]** “DSH 对通用团队明显过度工程”是带时间和场景边界的观点。验证它需要真实生产工作负载、状态迁移、回滚成本与不停机替换收益的数据。

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJzdW1tYXJ5In0 -->
> **[Agent 总结]** 作者的完整论证链是：DSH 把核心也开放为插件，是为了运行时深层自进化；但当前通用 Agent 对这种实时替换的需求有限，且证明改动更好的评测成本很高，因此这份开放度在当下显得超前。

<!-- eg:nr:1 eyJrIjoicyIsInMiOiJMNDgzLUw0OTIifQ -->
### Footnotes

- Yifan Shi, Wei Zhang, Tianyi Cui. *A Programming Paradigm for Spatiotemporal Composability*. Preprint, 2026. [https://github.com/cordiverse/paper](https://github.com/cordiverse/paper) ↩
- Cordis 插件框架，[https://github.com/cordiverse/cordis](https://github.com/cordiverse/cordis) ↩
- 李博杰《AI Agents in Depth》第一章，[https://bojieli.github.io/ai-agent-book/](https://bojieli.github.io/ai-agent-book/) ；Lilian Weng, Harness Engineering for Self-Improvement, [https://lilianweng.github.io/posts/2026-07-04-harness/](https://lilianweng.github.io/posts/2026-07-04-harness/) ↩
- Yao et al., ReAct: Synergizing Reasoning and Acting in Language Models, 2022, [https://arxiv.org/abs/2210.03629](https://arxiv.org/abs/2210.03629) ↩
- 这是我自己的猜想：我认为未来的软件会是 Generative Architecture——架构里某一部分组件的代码完全由运行时产生。例如我买鞋子的时候只关注尺码、类型和材质，那么从前端 UI 到后端的查询语句里都应该只有这三个限定条件；再比如我剪视频时只希望手动调整特效和转场，那么整个界面就应该只展示调整特效和转场的窗口。 ↩
- Lilian Weng, Harness Engineering for Self-Improvement, 2026-07-04, [https://lilianweng.github.io/posts/2026-07-04-harness/](https://lilianweng.github.io/posts/2026-07-04-harness/) ↩
- 神鬼二象性：DeepSeek V4 Pro 0813 第一次在半夜上线时效果非常差，被紧急下线；重新上线后效果依然不及预期；最终大家发现它只有在 DeepSeek Harness 的极简模式下效果才会非常好，于是被调侃为”神鬼二象性”。 ↩
- Claude Code Memory，[https://code.claude.com/docs/en/memory](https://code.claude.com/docs/en/memory) ↩

<!-- eg:nr:1 eyJrIjoiYSIsImEiOiJyZWZsZWN0aW9uIn0 -->
> **[Agent 反思]** 文章最有学习价值的地方，是用同一退款案例连续比较不同核心边界，再把 DSH 插件机制连接到自进化目标。其局限也很明确：产品 API、模型表现和行业成熟度都高度时效化；机制性知识可保留，时效判断应拆成待验证 Opinion。

## 存疑与待验证

## 用户补充

