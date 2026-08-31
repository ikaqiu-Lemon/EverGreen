---
id: k-20260901-chunk-size
status: active
created_at: 2026-09-01
updated_at: 2026-09-01T10:30:00+08:00
sources:
    - source: s-20260901-rag
      note: n-20260901-rag
      rel: support
      reason: |
        原文给出实测曲线
    - source: s-20260901-alt
      note: n-20260901-alt
      rel: context
      reason: "背景：检索场景差异"
tags: [rag, chunking]
relations:
  - type: limits
    target: k-20260901-embedding
    reason: 受 embedding 窗口限制
unknown_key: 用户手写的未知字段   # 行尾注释也要逐字保留
anchor_demo: &a 复用值
alias_demo: *a
---

前言段落（不属于任何 H2 分区）。

## 知识内容

Chunk 粒度要按检索目标定。

## 解释与依据

- 顶层列表项，带缩进子项
    - 缩进子项一
    - 缩进子项二

      空行之后仍是缩进的延续行
- 第二个顶层列表项

```python
def f():

    return 1
## 围栏内的这一行不是分区标题
```

| 维度 | 取值 |
|-|-|
| 粒度 | 512 |

### H3 小标题

段落跟在 H3 后面。

## 条件与边界

## 用户补充

用户手写内容，逐字保留。
	制表符缩进行

## 理解自检

- [ ] 自检项

## 用户自建的第六个分区

这里的内容原样保留（未知分区不报错、不删除、不重排）。


