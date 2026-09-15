<div align="center">

# Evergreen

**面向人与 coding agent 的确定性本地知识库 —— 单二进制交付。**

笔记以纯 Markdown 存放在你自己的 Git 仓库里，每一次改动都是一个可评审的 commit。
不调用模型、不发网络请求、无遥测。

[![Release](https://img.shields.io/github/v/release/ikaqiu-Lemon/EverGreen?include_prereleases&sort=semver)](https://github.com/ikaqiu-Lemon/EverGreen/releases)
[![CI](https://github.com/ikaqiu-Lemon/EverGreen/actions/workflows/ci.yml/badge.svg)](https://github.com/ikaqiu-Lemon/EverGreen/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.25%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/dl/)
[![Offline](https://img.shields.io/badge/网络请求-零-success)](PRIVACY.md)

[English](README.md) · **简体中文**

</div>

---

## Evergreen 是什么？

Evergreen（`eg`）是一个命令行知识库，只围绕一件事设计：**你拥有的笔记，必须活得比写它的工具更久。**

多数 AI 笔记工具把知识关进私有数据库，再放任模型往里自由写入。Evergreen 把这两件事都反了过来：

- **Markdown 是唯一权威来源。** 人类可读的文件 + YAML frontmatter，放在你自己掌控的 Git 仓库里。
  SQLite 索引只是随时可删、可完整重建的加速层。
- **模型永远不能直接写。** 所有程序化写入都必须走一份经校验的 *ChangePlan*：eg 校验它、只改被授权的
  字节区间、每次操作恰好记一个 Git commit。

结果是：agent 可以放心大规模操作它，而人在五年之后依然能读、能 diff、能评审、能 `git blame`。

```console
$ eg search 注意力
状态：completed（exit_code=0，ok=true）
search：query="注意力"，命中 1 张卡（扫描 2 个 .md，跳过 0 个；只读，零写入零 commit）
分页：--limit=50 --offset=0，本页 1 条 / 共 1 条（截断=false）
k-20260915-attention-parallelism  注意力用并行访问替代了循环结构  [ai-infra] ai,method  2026-09-15T14:12:01+08:00
```

> [!NOTE]
> 本文所有终端片段都是 `eg` 的**真实输出**（人类可读渲染面目前只有中文）。
> 每条命令都另有 `--json`，返回结构稳定、与语言无关的信封 —— agent 与脚本应当只用 `--json`。

### 为什么不直接用一个 Markdown 文件夹？

纯文件夹无法表达「这个论断*为什么*成立」「它和谁冲突」「它是否还有效」。Evergreen 在 Markdown 之上加了
一层很小的封闭结构 —— 原文、材料依据、论证关系、生命周期状态 —— 并且**强制执行**它，让 agent 没法悄悄
产出一个没有来源的论断或一条悬空引用。

### 为什么不用通用的 agent memory / RAG 存储？

那些方案优化的是**检索**，Evergreen 优化的是**评审**。它的保证全都落在「写入了什么」上：保字节的编辑、
多文件原子事务、破坏性操作必须用户显式授权、稳定的退出码，以及一条完整的 Git 审计链。

---

## 目录

- [核心特性](#核心特性)
- [工作原理](#工作原理)
- [安装](#安装)
- [快速开始](#快速开始)
- [核心概念](#核心概念)
  - [vault（知识库）](#vault知识库)
  - [四种实体](#四种实体)
  - [知识卡的五个分区](#知识卡的五个分区)
  - [关系](#关系)
  - [ChangePlan](#changeplan)
- [与 coding agent 配合使用](#与-coding-agent-配合使用)
- [命令参考](#命令参考)
- [读路径：排序、分页与降级](#读路径排序分页与降级)
- [安全模型](#安全模型)
- [退出码与诊断](#退出码与诊断)
- [派生索引](#派生索引)
- [性能](#性能)
- [开发](#开发)
- [平台支持](#平台支持)
- [常见问题](#常见问题)
- [项目信息](#项目信息)

---

## 核心特性

| | |
| --- | --- |
| 🗂 **本地优先** | vault 是你自己选的目录，数据不出本机。 |
| 📝 **Markdown 权威** | 每条事实都落在可读的 `.md` + YAML frontmatter 里；索引是派生物，可随时丢弃。 |
| 🔍 **为评审而设计** | 一次操作 = 一个 Git commit，带类型化 verb（`capture`、`process`、`relate`、`delete` …）。 |
| ✂️ **保字节写入** | 只重写被授权的区间，绝不把 YAML 反序列化后整篇回写，未知键、注释与格式全部原样保留。 |
| 🤖 **对 agent 安全** | 唯一一条经校验的写入通道；破坏性操作需要「已批准的提案」**且**用户显式确认。 |
| 🔒 **崩溃安全** | vault 内 `flock` 文件锁、带前像的事务日志、以及对被中断写入的自动恢复。 |
| 🎯 **确定性** | 所有列表全序、JSON 信封稳定、退出码有冻结合同 —— 可 diff、可脚本化。 |
| 🚫 **零网络** | 不调模型、不抓 URL、无遥测、不自动更新。`CGO_ENABLED=0`，无 C 运行时依赖。 |

---

## 工作原理

Evergreen 有意**不**抓取、也不理解内容。语义由你的 agent 负责，磁盘由 Evergreen 负责。

```text
   ┌──────────────────── 你的 agent（或你本人）────────────────────┐
   │   抓取网页 · 清洗正文 · 判断含义 · 判断关系                    │
   └──────────────┬───────────────────────────────┬───────────────┘
                  │ ① 正文字节                     │ ③ ChangePlan（JSON）
                  ▼                               ▼
        ┌──────────────────┐   ② context   ┌────────────────────────┐
        │   eg capture     │ ────────────▶ │   eg apply --plan      │
        │   收录原文        │  候选卡 +     │  校验 → 授权 → 写区间   │
        └────────┬─────────┘  content_hash │  → 恰一次 commit       │
                 │                         └───────────┬────────────┘
                 ▼                                     ▼
        ┌──────────────────────────────────────────────────────────┐
        │   你的 vault = 一个 Markdown 的 Git 仓库（唯一权威来源）    │
        │   sources/ · domains/<d>/notes/ · domains/<d>/knowledge/  │
        └──────────────┬───────────────────────────────┬───────────┘
                       │ 可重建                         │ 只读
                       ▼                               ▼
             ┌──────────────────┐        ┌──────────────────────────┐
             │ .index/ SQLite   │        │ eg search / card show /  │
             │ FTS5 + 事务日志   │ ─────▶ │ rel（索引不健康时自动     │
             └──────────────────┘        │ 降级为扫 Markdown）       │
                                         └──────────────────────────┘
```

这个形状直接带来两个性质：

1. **索引永远可以扔掉。** 缺失、陈旧或损坏时，读命令会透明降级为扫描 Markdown 并在 `warnings[]` 里
   如实说明。「索引没建」永远不是读失败的理由。
2. **失败的写入不留半成品。** 多文件写入先进日志暂存再原子提交；下一次写入会先检测并恢复被中断的
   事务，然后才继续。

---

## 安装

### 方式一：预编译二进制（推荐）

每个 [release](https://github.com/ikaqiu-Lemon/EverGreen/releases) 都附带 Linux 与 macOS 二进制。

```console
$ VER=v0.6.0-m6
$ OS=$(uname -s | tr '[:upper:]' '[:lower:]')          # linux | darwin
$ ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')

$ curl -fsSLO "https://github.com/ikaqiu-Lemon/EverGreen/releases/download/$VER/eg_${OS}_${ARCH}"
$ curl -fsSLO "https://github.com/ikaqiu-Lemon/EverGreen/releases/download/$VER/SHA256SUMS"

# 先校验再信任
$ grep "eg_${OS}_${ARCH}" SHA256SUMS | sha256sum -c -    # macOS 用 shasum -a 256 -c -

$ chmod +x "eg_${OS}_${ARCH}"
$ sudo mv "eg_${OS}_${ARCH}" /usr/local/bin/eg
$ eg --version
```

每个 release 还带 `PROVENANCE.txt`，把版本号与源码 commit 绑定到产物校验和上。

### 方式二：`go install`

```console
$ go install github.com/ikaqiu-Lemon/EverGreen/cmd/eg@latest
```

### 方式三：从源码构建

```console
$ git clone https://github.com/ikaqiu-Lemon/EverGreen.git
$ cd EverGreen
$ make build          # 本机 bin/eg + 交叉编译 dist/
$ ./bin/eg --version
```

**依赖：** Go 1.25+、Git、GNU Make。只有跑清单驱动的测试套件才需要 Python 3 与 PyYAML。
所有构建均为 `CGO_ENABLED=0`。

离线构建（`GOPROXY=off`）、发布打包与校验和验证的细节见 [INSTALL.md](INSTALL.md)。

---

## 快速开始

vault 就是一个存放你知识的 Git 仓库，由 Evergreen 帮你创建和维护。

### 1. 创建 vault

```console
$ export VAULT="$HOME/notes/evergreen"

$ eg --vault "$VAULT" init --domain ai-infra
$ eg --vault "$VAULT" config set default_domain ai-infra
```

`eg init` 建出骨架并产生第一个 commit：

```console
状态：completed（exit_code=0，ok=true）
vault：/home/you/notes/evergreen
新建 7 项，已存在 0 项
commit：init(ai-infra): 初始化 vault 骨架
```

它同时把 `SKILL.md`（给 agent 的操作规程）写进 vault，与二进制内嵌副本**字节相同**，从而保证
「工具与规程同版本」。

> **提示：** vault 建好后就可以省掉 `--vault`；Evergreen 会从当前目录向上查找含 `evergreen.yml`
> 的目录。

### 2. 收录原文

Evergreen 从不抓取 URL，正文由你（或你的 agent）清洗好后传入。

```console
$ cat > /tmp/body.txt <<'EOF'
Rich Sutton 认为，能利用算力的通用方法，长期表现优于把人类领域知识直接写进系统的方法。
国际象棋、围棋、语音识别与计算机视觉都出现过同一个模式：人工注入知识的方案短期领先，
随后被算力驱动的通用方法反超。
EOF

$ eg capture \
    --url https://example.com/bitter-lesson \
    --title "The Bitter Lesson" \
    --body-file /tmp/body.txt \
    --reason "沉淀算力与通用方法的长期结论"
```

```console
状态：completed（exit_code=0，ok=true）
原文：s-20260915-the-bitter-lesson（sources/s-20260915-the-bitter-lesson.md）
commit：capture(ai-infra): 收录 s-20260915-the-bitter-lesson
数据：
  deduped: false
  has_note: false
  path: sources/s-20260915-the-bitter-lesson.md
  txn_id: t0000000000000001
```

`deduped: true` 表示 URL 或标题已存在 —— Evergreen 会**复用**那条原文并把你的 `--reason` 追加进理由
列表，而不是造一个重复 ID。输入文件请放在 vault **之外**：`eg capture` 会把内容录进 `sources/`。

### 3. 取加工上下文

```console
$ eg context --source s-20260915-the-bitter-lesson --json
```

这是只读调用，返回原文正文、同领域相似的候选卡（`candidates`），以及最关键的 `base`
（文件 → `content_hash` 映射）：

```json
{
  "ok": true,
  "data": {
    "base": {
      "unprocessed.md": "sha256:ed58e18b227f787c87257d9d6356997aae9eb9bb238858ba158d7da6bcdb9584"
    },
    "candidates": [],
    "cards": [],
    "notes": [],
    "proposals": [],
    "domain": "ai-infra",
    "default_domain": "ai-infra",
    "default_domain_fallback": true,
    "source": {
      "id": "s-20260915-the-bitter-lesson",
      "path": "sources/s-20260915-the-bitter-lesson.md",
      "title": "The Bitter Lesson",
      "url": "https://example.com/bitter-lesson",
      "saved_at": "2026-09-15T14:11:23+08:00",
      "body": "…"
    }
  },
  "warnings": [ … ],
  "exit_code": 0,
  "status": "completed"
}
```

这些 hash 是**乐观并发令牌**：必须逐字原样填进 ChangePlan 的 `base`，不得省略、不得自己算。
若文件在你身后被改动，Evergreen 会跳过该文件而不是覆盖它。

### 4. 通过 ChangePlan 写入

存成 `plan.json`，其中 `base` 换成上一步拿到的真实 hash：

```json
{
  "plan_version": 1,
  "verb": "process",
  "domain": "ai-infra",
  "reason": "把 The Bitter Lesson 沉淀成一张可独立复用的知识卡",
  "requirement_ids": ["EG-KNW-04"],
  "convergence": [],
  "base": { "unprocessed.md": "sha256:ed58e18b…" },
  "ops": [
    {
      "op": "write_note",
      "source": "s-20260915-the-bitter-lesson",
      "note_id": "n-20260915-the-bitter-lesson",
      "title": "The Bitter Lesson（Rich Sutton, 2019）",
      "sections": {
        "材料提炼": "- 原文主张：能利用算力的通用方法长期看最有效。\n- 原文举例：国际象棋、围棋、语音识别、计算机视觉。\n",
        "Agent 分析": "- 适用面以「有明确评估信号、可大规模搜索或学习」的任务为主。\n"
      },
      "output_cards": [{ "card": "k-20260915-bitter-lesson", "mode": "新建" }],
      "coverage_gaps": ["counterexample"]
    },
    {
      "op": "create_card",
      "card_id": "k-20260915-bitter-lesson",
      "title": "能利用算力的通用方法长期胜过人工注入知识",
      "tags": ["ai", "method"],
      "sources": [
        {
          "source": "s-20260915-the-bitter-lesson",
          "note": "n-20260915-the-bitter-lesson",
          "rel": "support",
          "reason": "原文用四个领域的历史给出该结论的直接依据"
        }
      ],
      "sections": {
        "知识内容": "在算力成本持续指数下降的前提下，依赖搜索与学习、能随算力扩展的通用方法，长期表现优于把人类领域知识直接写进系统的方法。\n",
        "解释与依据": "- 依据原文：算力可用量随时间指数增长，方法的可扩展性决定长期上限。\n",
        "条件与边界": "- 前提是算力可持续增长、任务具备可大规模搜索或学习的结构。\n",
        "理解自检": "- 如果算力成本停止下降，这个结论还成立吗？\n"
      }
    },
    {
      "op": "add_open_question",
      "note": "n-20260915-the-bitter-lesson",
      "question": "在数据或评估信号受限的任务上，这个结论是否仍然成立？"
    }
  ]
}
```

务必先 dry-run —— 它完整跑校验，**零写入**：

```console
$ eg apply --plan plan.json --dry-run
```

```console
状态：completed（exit_code=0，ok=true）
--dry-run：零写入、零 commit，以下是将写入的清单
知识卡：新建 1 张（k-20260915-bitter-lesson），复用 0 张，补充 0 张
关系：材料 0 条，论证 0 条
写入文件 2 个：domains/ai-infra/notes/n-….md、domains/ai-infra/knowledge/k-….md
commit：无（未产生 commit 或提交失败；磁盘保留当前状态，未做任何还原）
· [I1] info ops[0] ops[0].coverage_gaps：提炼覆盖项缺失：counterexample（反例），已原样透传进报告
```

确认无误后正式写入：

```console
$ eg apply --plan plan.json
```

```console
状态：completed（exit_code=0，ok=true）
材料笔记：n-20260915-the-bitter-lesson（领域 ai-infra，domains/ai-infra/notes/n-….md，重新加工 false）
知识卡：新建 1 张（k-20260915-bitter-lesson），复用 0 张，补充 0 张
关系：材料 1 条，论证 0 条
未决问题：n-20260915-the-bitter-lesson ← 在数据或评估信号受限的任务上，这个结论是否仍然成立？
写入文件 3 个：domains/ai-infra/notes/n-….md、unprocessed.md、domains/ai-infra/knowledge/k-….md
commit：b4a69a2aa11ee969f3bf22e7696670a97654ebb1
显著变更：new_core_card（k-20260915-bitter-lesson）新建知识卡：承载本次加工的新核心含义
```

### 5. 读回来

```console
$ eg search 算力                             # 带排序与分页的知识卡检索
$ eg card show k-20260915-bitter-lesson     # 五分区 + sources + 正反向关系
$ eg rel k-20260915-bitter-lesson           # 论证关系，正向与反向
$ eg report --last                          # 只读复现最近一次写入报告
```

`eg rel` 永远把正向与反向分开报，并交代每条边落在哪个文件里：

```console
$ eg rel k-20260915-bitter-lesson
状态：completed（exit_code=0，ok=true）
rel：k-20260915-bitter-lesson 的正向 0 条 / 反向 1 条（扫描 2 个 .md，跳过 0 个；只读，零写入零 commit）
正向关系 relations_out[]：无
反向关系：k-20260915-attention-parallelism --supports--> k-20260915-bitter-lesson  理由：…（来源卡：domains/ai-infra/knowledge/k-20260915-attention-parallelism.md）
分页：--limit=50 --offset=0，本页 关系条目 1 条 / 共 1 条（截断=false；limit 是本次返回条数的全局上限）
```

落盘的卡片，和你手写出来的完全一样：

```markdown
---
id: 'k-20260915-bitter-lesson'
title: '能利用算力的通用方法长期胜过人工注入知识'
status: 'active'
created_at: '2026-09-15'
updated_at: '2026-09-15T14:11:40+08:00'
sources:
  - source: 's-20260915-the-bitter-lesson'
    note: 'n-20260915-the-bitter-lesson'
    rel: 'support'
    reason: '原文用四个领域的历史给出该结论的直接依据'
tags:
  - 'ai'
  - 'method'
---

## 知识内容

在算力成本持续指数下降的前提下，依赖搜索与学习、能随算力扩展的通用方法，长期表现优于把人类领域知识直接写进系统的方法。

## 解释与依据

- 依据原文：算力可用量随时间指数增长，方法的可扩展性决定长期上限。

## 条件与边界

- 前提是算力可持续增长、任务具备可大规模搜索或学习的结构。

## 用户补充

## 理解自检

- 如果算力成本停止下降，这个结论还成立吗？
```

### 6. 可选：建索引与体检

```console
$ eg index build     # 构建 SQLite/FTS5 加速层
$ eg check           # 只读结构体检（恰 7 个 check，零写入）
$ eg reconcile       # 全库对账（R1–R7，可能写入）
```

---

## 核心概念

### vault（知识库）

```text
evergreen.yml                     # 唯一配置：domains + default_domain
SKILL.md                          # agent 操作规程，由 eg init 写入
unprocessed.md                    # 收件区：已收录但未加工的原文
sources/                          # s-*  收录的原始材料
domains/<domain>/notes/           # n-*  材料笔记（忠于原文）
domains/<domain>/knowledge/       # k-*  知识卡（可复用的论断）
proposals/                        # p-*  高风险操作提案（按需创建）
.index/                           # 派生 SQLite + run.lock + 事务日志（.gitignore）
.eg/                              # 最近一次报告状态（.git/info/exclude）
```

除最后两行外，其余全部纳入 Git。`.index/` 与 `.eg/` 是运行时状态，被排除在仓库之外。

**领域（domain）** 是顶层分区（`ai-infra`、`product` …）。Evergreen **绝不**替你选领域：未配置
`default_domain` 时，除 `init` / `config` 外一律提示配置并退 `1`。

### 四种实体

| 前缀 | 实体 | 作用 |
| --- | --- | --- |
| `s-` | **原文** | 收录的原始材料，作为不可变证据。 |
| `n-` | **材料笔记** | 对单篇原文的忠实提炼；agent 自己的判断另放「Agent 分析」，与原文主张分离。 |
| `k-` | **知识卡** | 一个可独立理解 / 引用 / 复用的知识单元，也是被检索和被连接的对象。 |
| `p-` | **提案** | 高风险操作的申请，等待人工批准。 |

ID 稳定、可读、带日期前缀（`k-20260915-bitter-lesson`）。两条代码级硬规则：`s-` 绝不可写进
`relations`，`k-` 绝不可写进 `sources`。

### 知识卡的五个分区

每张卡恰有这五个分区，顺序固定：

| 分区 | 内容 | 谁可以写 |
| --- | --- | --- |
| 知识内容 | 论断本身，一卡一个知识点 | 仅建卡时由 agent 写；之后修改需用户显式授权 |
| 解释与依据 | 为什么成立，须有材料依据 | agent 可追加 |
| 条件与边界 | 何时成立、何时不成立 | agent 可追加 |
| 用户补充 | 你自己的补充 | **只有你。** agent 永久禁写 |
| 理解自检 | 开放式问题，不预设成立方 | agent 可追加 |

材料笔记也有自己的五个分区：材料提炼、Agent 分析、用户补充、存疑与待验证、产出知识卡。

这套切分正是让 agent 保持诚实的关键：它可以丰富论断周围的**推理**，但无法悄悄重定义论断本身，
也永远碰不到你写的字。

### 关系

分两类：

**材料关系** 把卡连到它的依据 —— 四要素必须齐全：`source` + `note` + `rel`
（`support` / `against` / `context`）+ `reason`。

**论证关系** 把卡与卡相连 —— `from` + `type` + `target` + `reason`：

| 类型 | 含义 |
| --- | --- |
| `derives` | 目标由起点推导而来 |
| `supports` | 起点强化目标 |
| `limits` | 起点限制目标的适用面 |
| `opposing` | 两者相互冲突 |

`opposing` 按两端 ID 字典序归一后**只存一条**，读路径不补对称条目 —— 没有第二条记录需要同步。
`reason` 写成关系名本身会得到 `W2` warning：连接必须携带真实理由。自环被直接拒绝
（`E5`、退 `2`、零写入）。

不改卡正文即可增删关系：

```console
$ eg rel add k-a limits k-b --reason "A 仅在 B 假设的 batch size 下成立"
$ eg rel remove k-a limits k-b --reason "已被一次直接测量取代"
```

`rel remove` 会**物理移除**匹配记录，不留墓碑。删一条本来不存在的关系是幂等 no-op
（`W10`、退 `0`），因此 plan 可以放心重跑。

### ChangePlan

ChangePlan 是 Evergreen 对外的契约：**唯一的程序化写入通道。** 顶层恰 8 个键：

| 键 | 用途 |
| --- | --- |
| `plan_version` | 恒为 `1` |
| `verb` | commit verb —— 主链路用 `process`，重新加工用 `reprocess` |
| `domain` | 一份 plan 只写一个领域 |
| `reason` | 本次写入的理由 |
| `requirement_ids` | 可追溯标签，会体现在 commit 正文 |
| `convergence[]` | 对每张被比较的候选卡逐条记录收敛判定（见下） |
| `base` | `eg context` 给出的 文件 → `content_hash`，用于乐观并发 |
| `ops[]` | 要执行的操作 |

主链路可用的七个 op：

`add_source` · `write_note` · `create_card` · `append_card` · `add_material_rel` ·
`add_relation` · `add_open_question`

未知 op 是硬错误（`E5`、退 `2`、零写入）—— Evergreen 选择失败关闭，而不是猜。

---

## 与 coding agent 配合使用

Evergreen 就是为 agent 驱动而设计的。`eg init` 会把 `SKILL.md` 作为权威操作规程装进 vault，
让你的 agent 直接读它。

### 调用链路

```text
第 0 步  agent 自行抓取并清洗正文        （CLI 不做任何网络 I/O）
   ①     eg capture          收录原文，登记收件区
   ②     eg context          取候选卡与 base content hash
   ③     语义处理             唯一允许调用模型的环节
   ④     eg apply --plan     唯一写入通道
   ⑤     渲染报告             如实转述写了什么、跳过了什么、为什么
```

若抓取失败或正文为空，正确做法是**停下**：在报告里写明原因，不落任何文件。绝不能用占位文本、
摘要或搜索结果冒充正文。

### 判断卡是否已存在

写入前，agent 要把新材料与每张候选卡在三个维度上逐一比较：

- **核心知识**相同还是不同？
- **成立条件**相同还是不同？
- **独立复用用途**相同还是不同？

规则是**宁拆勿并**：任一维度不同 —— 或者判不出来 —— 就新建一张卡，并写明为什么。只有三维度全部
相同才允许复用已有卡。每次比较都记进 `convergence[]`，Evergreen 原样回带、不重新判断。

这个判定恰好映射到七种结果：

| `relation` | 情形 | 应产出的 op |
| --- | --- | --- |
| `independent_new` | 确实是新知识 | `create_card` + `add_material_rel` |
| `same_semantics` | 已被覆盖 | 不新建卡，只 `add_material_rel` |
| `non_core_supplement` | 补充依据或边界 | `append_card` 到「解释与依据」/「条件与边界」（**不动「知识内容」**）+ `add_material_rel` |
| `core_change` | 论断本身变了 | 新建 `create_card` + `add_relation` 指向原卡；**原卡不改写、不失效** |
| `conflict_coexist` | 与已有卡冲突 | 两卡同时 `active` + **恰一条** `opposing` |
| `uncertain` | 本材料无法定论 | `add_open_question`，不建卡 |
| `deprecated` | 让卡失效 | **agent 不可用** —— 只能由用户发起 |

### 如实登记覆盖缺失

写笔记前，agent 要对七类要点自评 —— `core_claim`、`key_evidence`、`counterexample`、`boundary`、
`method`、`conclusion`、`limitation` —— 把**原文未表达**的写进 `coverage_gaps`，它们会以 `I1` info
出现在最终报告里。这个字段记录的是**原文**缺什么，不是用来掩盖 agent 自己漏写的要点，也不得为了
凑满七项而编造原文没有的内容。

### agent 绝不可做的事

- 用编辑器 / shell / 脚本改 vault 内文件，或自行 `git commit` / `reset` / `checkout`。
  只能走 `eg apply`。
- 写已有卡的「知识内容」，或写任何产物的「用户补充」。
- 生成状态类 op（`deprecate`、`restore`、`delete`、`undelete`、`set_replaced_by`）。
  缺 `initiator: user` 时这些是 **error** 而非 warning —— 退 `2`、零写入。
- 一份 plan 写两个领域。
- 把多跳推导结论沉淀成新卡：它没有自己的材料依据，落盘会绕过依据强制。这类结论应写进笔记的
  「Agent 分析」或存疑分区。
- 编造原文、笔记、理由或 ID。

---

## 命令参考

顶层命令共 22 条。权威参数表请跑 `eg <command> --help` —— 每页 help 都自带退出码说明。

**初始化**

| 命令 | 用途 |
| --- | --- |
| `eg init [--domain <d>]...` | 初始化一个 Git 支撑的 vault |
| `eg config get` / `eg config set <k> <v>` | 读写 `default_domain` / `domains` |

**加工主链路**

| 命令 | 用途 |
| --- | --- |
| `eg capture` | 收录原文并登记收件区 |
| `eg context` | 只读：候选卡 + `base` content hash |
| `eg apply --plan <file\|->` | 校验并应用 ChangePlan —— 唯一写入通道 |
| `eg report --last` | 只读复现最近一次产出报告体的写命令（`eg apply` 或 `eg capture`） |

**读路径**

| 命令 | 用途 |
| --- | --- |
| `eg search <query>` | 带排序与分页的知识卡检索 |
| `eg card show <k-id>` | 单卡：五分区 + sources + 正反向关系 |
| `eg rel <k-id>` | 论证关系；`--replaced-by` 切到替代指针视图 |

**编辑与生命周期**（均由用户发起）

| 命令 | 用途 |
| --- | --- |
| `eg rel add` / `eg rel remove` | 增删一条论证关系 |
| `eg edit` | 整段替换被显式授权的卡分区 |
| `eg deprecate` / `eg restore` | 让卡失效 / 恢复 |
| `eg replaced-by` | 在失效卡上写替代指针 |
| `eg mark-reviewed` / `eg unreviewed` | 记录 / 列出过目状态 |

**高风险操作**

| 命令 | 用途 |
| --- | --- |
| `eg proposal new\|list\|show\|approve\|reject` | 提案审批状态机 |
| `eg delete` / `eg undelete` | 逻辑删除 / 撤销逻辑删除 |

**运维对账**

| 命令 | 用途 |
| --- | --- |
| `eg check [--strict]` | 只读结构体检 —— 恰 7 个 check，零写入 |
| `eg reconcile [--dry-run]` | 全库对账 R1–R7，含补写 |
| `eg index build\|rebuild\|status\|sync` | 管理派生索引 |
| `eg bench` | 只读采样五个性能指标 |

### 全局 flag

| Flag | 含义 |
| --- | --- |
| `--json` | 结构化信封：`ok` / `data` / `warnings` / `exit_code` / `status`。写在全局位与子命令位等价，参数解析失败时同样输出信封。 |
| `--vault <path>` | vault 根。默认从 cwd 向上查找 `evergreen.yml`。 |
| `--user-request` | 声明本次调用由用户显式发起。 |
| `--help` / `--version` | 打印后退 `0`。 |

---

## 读路径：排序、分页与降级

**匹配分。** `eg search` 只检索知识卡，材料笔记与原文不进结果。命中 title +3、tags +2、正文 +1，
每词每字段至多计一次。

**全序。** 检索结果按 匹配分降序 → `updated_at` 倒序 → `created_at` 倒序 → `id` 升序 排列。
关系列表按 type 固定次序（`opposing` → `limits` → `supports` → `derives`）→ 对端 ID → path →
条目全等标识 排列。排序与存储后端无关，任何结果都可手工复算。

**分页。** `--limit` 默认 `50`，`--limit 0` 表示不限量。分页在排序**之后**施加，翻页无重无漏，
`total` 恒为分页前总数；截断产生恰一条 `W25`。对 `eg card show` 与 `eg rel`，`--limit` 是横跨正反
两个方向的**单一全局上限**，不会因为有两个列表就返回 `2 × limit` 条。

**可见性。** 三个正交维度，互不推断：

| 维度 | 默认 | 显式打开 |
| --- | --- | --- |
| 失效卡 | 可见，标 `[失效]` | 恒可见，无隐藏开关 |
| 失效的关系**对端** | 隐藏 | `--include-deprecated`（`card show`、`rel`） |
| 逻辑删除 | 检索默认隐藏 | `--include-deleted`，标 `[已删除]` |

**优雅降级。** 索引陈旧（`W22`）、缺失（`W23`）或损坏（`W24`）时，读命令降级为扫描 Markdown 并记
`Q5`。因为 Markdown 是权威，答案完全一致，只是更慢。不可解析的文件一律记 `Q1` 并计入
`skipped_files`，绝不静默跳过。悬空引用照实展示并标注「目标不存在」，同时记一条 `Q2`，
**退出码仍是 `0`** —— 断链是你库里的一个事实，不是这次读取的失败。

---

## 安全模型

### 两条授权路径

Evergreen 区分**是谁发起的**。agent 发起与用户发起是两条不同代码路径，且 plan 不能自证授权：
必须命令行带 `--user-request` **且** plan 内 `initiator: user`，才进入用户显式路径。生命周期与
破坏性操作在 agent 路径上不可用 —— 尝试即退 `2`、零写入。

### 可提不可执

agent 可以**申请**。`eg proposal new --type logical_delete --target <id>` 允许 agent 调用（不要求
`--user-request`），它直接扫描 Markdown 算出影响面并记录。只有人能批准：

```console
$ eg proposal list --status pending
$ eg proposal show p-20260915-001
$ eg proposal approve p-20260915-001 --confirm --user-request
```

批准时会**重算影响面**并与提案记录比对：前提已变则改判 `superseded` 且**不执行**。缺 `--confirm`
退 `6`，权威 Markdown 完全不变；缺 `--user-request` 退 `2`。

### 删除是逻辑删除

```console
$ eg delete --target <id> --reason "重复材料" --proposal p-… --confirm --user-request
$ eg undelete --target <id> --reason "仍然需要"
```

`delete` 只写 `deleted_at` / `deleted_reason`。文件、内容与所有关系记录全部保留 —— 删除是可见性
变化，不是销毁。删一张卡**不会**级联删除指向它的关系。`undelete` 恰好删掉那两个键，`status`
原封不动。

设计上没有物理删除 —— 那是 Git 历史和文件系统该干的事。

### 崩溃安全的写入

M6 已启用 `run.lock`、事务日志、崩溃恢复、块级安全合并和退出码 `5`，可靠性保证如下：

- **`run.lock`** 在单机内串行化写者。争用记 `W28`；锁不可用退 `5` + `E16`。
- **`.index/txn/`** 是记录前像的事务日志，被接受的多文件写入集原子提交。
- **崩溃恢复**自动进行：下一次写入检测到被中断的事务，先恢复并留痕 `W26`。
- **块级安全合并**会拒绝可能破坏用户块的合并（`W27`）；无法逐字保留的用户块被跳过而不是弄坏。
- **`--strict`** 把写前 warning（`W1`/`W2`/`W3`/`W4`/`W6`）升级为 error，命中即在锁内零写入中止，
  退 `5` + `E15`。

> ⚠️ **存在未闭合事务时不可删 `.index/`。** 索引本身永远可重建，但 `.index/txn/` 保存着无法从
> Markdown 重建的前像。先跑 `eg check --json` 列出每个受影响的 `txn_id`，备份
> `.index/txn/<txn_id>/` 后再清理 —— 之后任一写命令会自动恢复并留痕 `W26`。

### 保字节写入

Evergreen 绝不把你的 YAML 过一遍序列化器。它解析原始字节、定位被授权的区间、只重写那段。未知键、
注释风格、引号与键序全部保留 —— 包括当前版本还不认识的内容。「写路径禁止 YAML 序列化」由 CI 的
依赖方向门禁强制，不只是口头约定。

---

## 退出码与诊断

每个退出码都是契约，脚本可以据此分支。

| 码 | 含义 | 写入了吗 |
| --- | --- | --- |
| `0` | 成功（含零命中、含幂等 no-op） | 可能 |
| `1` | 用法 / 参数非法 | **零写入** |
| `2` | 校验失败 | **零写入** |
| `3` | 部分写入被跳过；已完成的写入保留并提交 | 部分 |
| `4` | Git 提交或索引写入失败；磁盘保留现状，不做破坏性还原 | 可能 |
| `5` | 写前强校验失败（`E15`）或锁不可用（`E16`） | **零写入** |
| `6` | 校验已过，仅缺 `--confirm` —— 权威 Markdown 完全不变 | **零写入** |

确认按固定顺序判定：参数错 `1` → 校验失败 `2` → 仅缺确认 `6`。

诊断都带 `code`、`level`、`path`、`op_index`、`message`、`target`，能直接定位到该改哪个 op。
`code` 与 `exit_code` 是两件事 —— 一次运行可以带着 warning 退 `0`。warning 与 error 按 `level`
分流到 `warnings[]` 与 `data.errors[]`。

常见码：`E15` 写前强校验 · `E16` 锁 · `W22`/`W23`/`W24` 索引陈旧/缺失/损坏 · `W25` 分页截断 ·
`W26` 崩溃恢复 · `W27` 不安全块合并 · `W28` 锁争用 · `Q1` 不可解析文件 · `Q2` 悬空引用 ·
`Q5` 降级扫 Markdown。（`W21` 有意不分配。）

---

## 派生索引

索引是 `.index/` 下的 SQLite/FTS5 数据库（基于 `modernc.org/sqlite`，纯 Go，无需 CGO）。它是
**纯加速层**：不被任何命令依赖、不是任何命令的前置、在 `.gitignore` 内、不随仓库分发。

```console
$ eg index build              # 构建
$ eg index status --strict    # 只读体检
$ eg index sync               # 改动后增量收敛
$ eg index rebuild            # 丢弃并从 Markdown 重建
```

因为 Markdown 是权威，你随时可以 `rm -rf .index/` 再重建 —— 前提是没有未闭合事务（见上方警告）。

---

## 性能

```console
$ eg bench
状态：completed（exit_code=0，ok=true）
采样完成：2 张在册卡、2 个 id 采样点、20 个关键词；口径见下（命令面无参数可调）
  search_p95_ms          实测     22 ms   →  建议门槛     40 ms（= ceil(实测 × 1.5 / 10) × 10）
  card_show_p95_ms       实测     23 ms   →  建议门槛     40 ms
  rel_p95_ms             实测     23 ms   →  建议门槛     40 ms
  index_build_ms         实测     91 ms   →  建议门槛    140 ms
  index_incremental_ms   实测     71 ms   →  建议门槛    110 ms
```

只读采样五个指标：`search_p95_ms`、`card_show_p95_ms`、`rel_p95_ms`、`index_build_ms`、
`index_incremental_ms`。读指标预热 3 轮丢弃、计入 50 轮，P95 取升序第 48 小值且不插值；两个构建
指标不预热、各测 3 次取中位数，且在**临时副本**上测量、用完即删。你的 vault 与索引零写入零
commit。关键词与 id 采样集写死在代码里、不随机，因此同一台机器上两次采样可比。

`eg bench` 有意**只报实测值、不判合格**。门槛由仓内性能门禁自己推导并持有，保证数值与判据永不
脱钩。索引不是 healthy+fresh 时它退 `1` 并给出 `E17`，而不是在降级路径上采样、再去比一套为另一
条路径定的门槛。

---

## 开发

```bash
make build        # 本机二进制 + 交叉编译 dist/
make test         # core profile（验收入口）
make test-full    # 全量套件遍历一次
make test-race    # -race 子集
make lint         # gofmt + vet + 写路径守卫 + 依赖方向 + 公开仓卫生
```

`make release` 产出干净的四平台构建，附带 `SHA256SUMS` 与 `PROVENANCE.txt`；
`make print-version` 打印版本号的唯一权威出处。

包边界与设计取舍见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)，贡献要求见
[CONTRIBUTING.md](CONTRIBUTING.md)。

提 patch 前值得先知道两条约定：

1. **验收入口是 `make test`，不是 `go test ./...`。** 每个 profile 都从
   `tests/manifest/suites.yaml` 派生，覆盖 shell、contract、fuzz、mutation 与性能套件，并能判出
   「零执行 / 隐式 skip」。`make test-go` 只是排查 runner 自身问题时的逃生口。
2. **产品包内不放 `_test.go`。** 权威 Go 测试位于 `tests/_staged/`，由清单驱动的 runner 在运行时
   materialize 到产品代码旁。

---

## 平台支持

| 目标 | 状态 |
| --- | --- |
| `linux/amd64` | 验收套件完整跑通 |
| `linux/arm64` | 仅交叉编译，未做原生运行验证 |
| `darwin/amd64` | 仅交叉编译，**未经真机运行验证** |
| `darwin/arm64` | 仅交叉编译，**未经真机运行验证** |

我们**不**声称 darwin 产物通过了 macOS 真机验证，它们仅作为交叉编译产物提供。

`run.lock` 只协调**同一台机器**上的进程。在网络盘或同步盘（Dropbox、iCloud Drive 之类）上不保证
原子性 —— 请把 vault 放本地磁盘，用 Git 同步。

---

## 常见问题

**Evergreen 会调用大模型吗？**
不会。它不调模型、不发网络请求、无遥测，也从不抓取 URL —— 包括你传给 `eg capture` 的那个 URL，
它只被当作元数据记录。所有语义都发生在你的 agent 里，在 `eg` 被调用之前。

**我能手工改笔记吗？**
能，它们是你的文件。「用户补充」分区专属于你，任何 agent 都不得写入。这条约束是给 agent 的：
自动写入必须走 `eg apply`，才能被校验并原子提交。手工改完建议跑一次 `eg check` 确认结构仍然成立。

**删了 `.index/` 会怎样？**
没关系，只要没有未闭合事务 —— 读命令会降级扫 Markdown 并记 `W23`，`eg index build` 可重建。
如果**有**未闭合事务，请先备份 `.index/txn/`。

**我的知识会被 Evergreen 锁死吗？**
不会。那就是一个普通 Git 仓库里的 Markdown + YAML frontmatter。把二进制删掉，每条笔记依然可读、
可 grep、可 diff。

**为什么分区名是中文？**
分区词表是落盘合同的冻结部分，按字节逐字比较，本地化它会破坏已有 vault。CLI 的 help 与人类可读
报告面目前也只有中文（英文本地化尚未实现）。面向机器的那一层是与语言无关的：`--json`、诊断码
（`E15`、`W26`、`Q5` …）、退出码、ID、frontmatter 键全部是稳定的英文 / ASCII，脚本与 agent 不受
影响。

**一个论断后来被证伪了怎么办？**
先 `eg deprecate`，再用 `eg replaced-by` 指向它的替代者。这张卡仍然可见、可检索，只是带上
`[失效]` 标记 —— Evergreen 选择让被取代的知识保持可读而不是抹掉它，从而保留推理链路。

**多台机器怎么办？**
像普通仓库一样用 Git 同步 vault。不要把它放在网络盘 / 同步盘上并从两台机器同时写 —— 锁只在本机
有效。

---

## 项目信息

- **版本：** `0.6.0-m6` · **模块：** `github.com/ikaqiu-Lemon/EverGreen`
- **安全：** 漏洞私下报告流程见 [SECURITY.md](SECURITY.md)
- **隐私：** 纯本地数据处理模型见 [PRIVACY.md](PRIVACY.md)
- **行为准则：** [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- **变更日志：** [CHANGELOG.md](CHANGELOG.md)

## 许可

基于 [Apache License 2.0](LICENSE) 授权。
