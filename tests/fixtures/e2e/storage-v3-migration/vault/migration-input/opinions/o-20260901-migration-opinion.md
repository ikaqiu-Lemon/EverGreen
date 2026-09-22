---
id: 'o-20260901-migration-opinion'
title: 'A migration should fail closed on drift'
status: 'active'
validation: 'pending'
created_at: '2026-09-02'
updated_at: '2026-09-02T10:00:00Z'
sources:
  - source: 's-20260901-migration'
    note: 'n-20260901-migration'
    rel: 'context'
    reason: 'The source motivates the migration policy.'
tags:
  - 'migration'
  - 'safety'
---

## 观点

A one-time migration should fail closed when an input hash drifts.

## 论据与推理

Silent repair would hide which bytes were actually reviewed.

## 条件与反例

An identical target is a valid idempotent no-op.

## 待验证

Exercise the complete copy before touching the live Vault.

## 用户补充
