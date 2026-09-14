#!/usr/bin/env bash
# 批次C2 · I-…-008（P1/major）判据：**只读命令的退出码闭集恰 {0,1}** ——
# 分页参数非法（负数 / 非整数）必须退 `1` + `status="failed"`，绝不退 `4` + `partial`。
#
# 为什么必须是 1：`4` 的对外语义是「Git 提交失败（磁盘保留现状，不做破坏性还原）」。
# 一次纯参数打错的只读查询若报 `4`，集成方会误触发仓库修复 / 回滚 / 告警等补偿动作；
# `status="partial"`（部分成功）在零命中、零写入、零 commit 的场景下也没有任何「部分」语义。
#
# 判据来源（声明面，逐字）：
#   `2026-09-19-m2-query-contract.md` §1.6：「`1` 参数非法 / 领域未登记 / 未配置
#     `default_domain`。只读命令**不出现** `2` / `3` / `4`」；
#   同文件 §6（只读零副作用判定）：「**退出码只可能是 0 / 1**」；
#   `eg search --help`：「退出码：0（零命中也退 0） | 1 参数非法 / …」；
#   `eg card --help`：「只读：零文件变化、零 commit。退出码：0 | 1」；
#   `eg rel --help`：「退出码：0（含「无实际改动、未提交」）| 1 参数非法 / …」；
#   顶层 `eg --help` 退出码表：「4 Git 提交失败」。
#
# 声明面内部原有冲突（本 issue 一并裁决为**方案 A**）：
#   `2026-12-19-m5-index-architecture-contract.md` §8.2 表 与 §10 A-47 裁决行原写「负数 /
#   非整退 `4`」，与同 milestone 的 `2027-01-17-m5-release-and-version.md`「既有命令的默认
#   输出语义与退出码**未做破坏性变更**」自相矛盾。取 M2 §1.6/§6 + 三条 `--help` 的多数声明面，
#   实现改为 `1` / `failed`，并同步修正 M5 合同 §8.2 与 A-47 裁决行（不是「登记差异」了事）。
#
# 本 suite 锁死的判据（事实只回读 `--json` 信封 / 盘上字节 / git）：
#   A 三条读路径 × 四类非法分页值（`--limit -1` / `--limit abc` / `--offset -5` / `--offset x`）
#     → 退 **1** + `status="failed"` + `ok=false` + `data.errors[]` 至少一条 error 级。
#   B **闭集**：同一批调用（合法 + 非法 + 同族 `--since abc`）的退出码集合恰 `{0,1}`，
#     一格都不许出现 2 / 3 / 4 / 5 / 6。
#   C **同族一致**：`--since abc`（既有退 1）与 `--limit -1` 必须同码 —— 同类「参数非法」
#     不得映射成两个码，否则调用方写不出稳定分支。
#   D **零副作用**：全部非法调用之后，权威字节 sha256 逐字不变、commit 数不变、工作区干净。
#   E **防修复过度**（下列既有行为一格不放宽）：
#     · 写子命令拒收只读 flag（`eg rel add … --limit 1`）仍退 **1**（用法错，不是值非法）；
#     · 未声明分页 flag 的命令（`eg index status --limit 5`）仍退 **1**；
#     · `4` 仍归 Git 提交失败：`ExitCommitFailed` 语义不因本修复被挪用（单测侧
#       `tests/_staged/internal/cli/page_test.go` 同步锁定，本 suite 不重复断言）。
#
# 约束：离线、零交互（stdin 接 /dev/null）、可重复执行；只用 bash / coreutils / git / go /
#   python3，无 jq 依赖；一切写只发生在 mktemp -d 沙箱内；任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/cli-core/c2_readonly_exit_closed_set.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
eg_require_disk 256

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-c2-readonly-exit.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

KDIR_REL='domains/tech/knowledge'
CARD="k-20270413-c2x-alpha"
PEER="k-20270413-c2x-beta"
REL_CARD="${KDIR_REL}/${CARD}.md"
REL_PEER="${KDIR_REL}/${PEER}.md"

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

egv()     { "${EG}" --vault "${VAULT}" "$@" </dev/null; }
eg_code() { local c=0; egv "$@" >"${WORK}/out.txt" 2>"${WORK}/err.txt" || c=$?; echo "${c}"; }
gitv()    { git -C "${VAULT}" "$@"; }
commits() { gitv log --oneline | wc -l | tr -d ' '; }
tree_sha() { (cd "${VAULT}" && find . -path ./.git -prune -o -type f -print | sort |
              xargs sha256sum) | sha256sum | cut -d' ' -f1; }

command -v python3 >/dev/null || die "本脚本用 python3 做 JSON 信封判定"

# 收集本 suite 里所有实际观测到的退出码（判据 B 的闭集反证素材）。
CODES_SEEN=""
note_code() { CODES_SEEN="${CODES_SEEN} $1"; }

# assert_readonly_param_error <期望码> <场景> -- <命令…>
# 退出码 == 期望码 + JSON 信封 ok=false / status=failed / data.errors 至少一条 error 级。
assert_readonly_param_error() {
  local want="$1" what="$2" code
  shift 3                                  # 丢掉 want / what / 分隔符 --
  code="$(eg_code "$@" --json)"
  note_code "${code}"
  [ "${code}" = "${want}" ] ||
    { cat "${WORK}/out.txt"; die "${what}：退出码应为 ${want}（只读参数非法），实得 ${code}"; }
  python3 - "${WORK}/out.txt" "${what}" <<'PY' || exit 1
import json, sys
path, what = sys.argv[1], sys.argv[2]
raw = open(path, encoding="utf-8").read()
try:
    env = json.loads(raw)
except Exception as exc:                       # noqa: BLE001
    print(f"  [FAIL] {what}：--json 输出不是合法 JSON：{exc}\n{raw}")
    sys.exit(1)
bad = []
if env.get("exit_code") != 1:
    bad.append(f'exit_code={env.get("exit_code")!r}，应为 1')
if env.get("status") != "failed":
    bad.append(f'status={env.get("status")!r}，应为 "failed"（零命中零写入没有「部分」语义）')
if env.get("ok") is not False:
    bad.append(f'ok={env.get("ok")!r}，应为 false')
errs = (env.get("data") or {}).get("errors") or []
if not [e for e in errs if e.get("level") == "error"]:
    bad.append(f"data.errors 应至少一条 error 级，实得 {errs!r}")
if bad:
    print(f"  [FAIL] {what}：" + "；".join(bad))
    sys.exit(1)
PY
}

seed_card() { # $1=id  $2=额外 frontmatter 行（可空）
  cat >"${VAULT}/${KDIR_REL}/$1.md" <<CARD_EOF
---
id: $1
title: 卡 $1
status: active
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
tags: [fix]
sources: []${2:+
$2}
---

# 卡 $1

## 知识内容

分页判据用的正文一行。

## 解释与依据

依据占位。

## 条件与边界

仅本判据使用。

## 用户补充

用户自己写的一行。

## 理解自检

- [ ] 能说出只读退出码闭集？
CARD_EOF
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译 eg 失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. 建库 + 语料
step "建库 + 两张卡（含一条关系，让三条读路径都有真实可查对象）"
mkdir -p "${VAULT}"
[ "$(eg_code init --domain tech)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain tech)" = "0" ] || die "config set 失败"
mkdir -p "${VAULT}/${KDIR_REL}"
seed_card "${PEER}" ""
seed_card "${CARD}" "$(printf 'relations:\n  - type: supports\n    target: %s\n    reason: 判据语料关系' "${PEER}")"
gitv add -A >/dev/null
gitv -c user.name=eg -c user.email=eg@example.com commit -q -m "seed: I-…-008 判据语料"
[ -z "$(gitv status --porcelain)" ] || die "前置条件：工作区必须干净"
BASE_COMMITS="$(commits)"
BASE_TREE="$(tree_sha)"
ok "库就绪（commit=${BASE_COMMITS}，权威树 sha=${BASE_TREE:0:12}…）"

# ---------------------------------------------------------------- 2. 前置：合法调用退 0
step "前置：三条读路径的合法调用（含 --limit 0 / offset 超界）恒退 0"
for args in \
  "search 分页 --limit 5" \
  "search 分页 --limit 0" \
  "search 分页 --offset 999" \
  "card show ${CARD} --limit 3" \
  "rel ${CARD} --limit 2 --offset 0" \
; do
  # shellcheck disable=SC2086
  C="$(eg_code ${args} --json)"
  note_code "${C}"
  [ "${C}" = "0" ] || { cat "${WORK}/out.txt"; die "合法调用 eg ${args} 应退 0，实得 ${C}"; }
done
ok "5 组合法只读调用全退 0（含 --limit 0 不限量与 offset 超界空结果）"

# ---------------------------------------------------------------- 3. A 三条读路径 × 四类非法分页值
step "A 分页参数非法（负数 / 非整数）→ 退 1 + status=failed + ok=false + error 级诊断"
assert_readonly_param_error 1 "eg search --limit -1"     -- search 分页 --limit -1
assert_readonly_param_error 1 "eg search --limit abc"    -- search 分页 --limit abc
assert_readonly_param_error 1 "eg search --offset -5"    -- search 分页 --offset -5
assert_readonly_param_error 1 "eg search --offset x"     -- search 分页 --offset x
assert_readonly_param_error 1 "eg card show --limit=-1"  -- card show "${CARD}" --limit=-1
assert_readonly_param_error 1 "eg card show --offset=-3" -- card show "${CARD}" --offset=-3
assert_readonly_param_error 1 "eg card show --limit=abc" -- card show "${CARD}" --limit=abc
assert_readonly_param_error 1 "eg rel --offset=-3"       -- rel "${CARD}" --offset=-3
assert_readonly_param_error 1 "eg rel --limit=-1"        -- rel "${CARD}" --limit=-1
assert_readonly_param_error 1 "eg rel --limit=1.5"       -- rel "${CARD}" --limit=1.5
ok "三条读路径 × 四类非法值共 10 组：一律退 1 + failed（不再是 4 + partial）"

# ---------------------------------------------------------------- 4. C 同族一致
step "C 同族一致：--since abc 与 --limit -1 必须同码（同类参数非法不得两个码）"
SINCE_CODE="$(eg_code search 分页 --since abc --json)"
note_code "${SINCE_CODE}"
LIMIT_CODE="$(eg_code search 分页 --limit -1 --json)"
note_code "${LIMIT_CODE}"
[ "${SINCE_CODE}" = "1" ] || die "--since abc 既有行为是退 1，实得 ${SINCE_CODE}"
[ "${LIMIT_CODE}" = "${SINCE_CODE}" ] ||
  die "同类参数非法必须同码：--since abc=${SINCE_CODE} 与 --limit -1=${LIMIT_CODE} 不一致"
ok "参数非法一族统一为 1（--since abc == --limit -1 == 1）"

# ---------------------------------------------------------------- 5. E 防修复过度
step "E 防修复过度：写子命令拒收只读 flag 仍退 1；未声明分页 flag 的命令仍退 1"
C="$(eg_code rel add "${CARD}" supports "${PEER}" --limit 1 --json)"
note_code "${C}"
[ "${C}" = "1" ] || { cat "${WORK}/out.txt"; die "eg rel add … --limit 1 应退 1（用法错），实得 ${C}"; }
C="$(eg_code index status --limit 5 --json)"
note_code "${C}"
[ "${C}" = "1" ] || { cat "${WORK}/out.txt"; die "eg index status --limit 5 应退 1，实得 ${C}"; }
ok "既有的两条「用法错退 1」反证一格未放宽"

# ---------------------------------------------------------------- 6. B 闭集
step "B 闭集：本 suite 观测到的全部退出码集合恰 {0,1}"
UNIQ="$(printf '%s\n' ${CODES_SEEN} | sort -u | tr '\n' ' ' | sed 's/ *$//')"
[ "${UNIQ}" = "0 1" ] ||
  die "只读退出码闭集必须恰 {0,1}（M2 §1.6/§6），实得 {${UNIQ}}"
ok "闭集成立：观测码集合 = {${UNIQ}}，未出现 2 / 3 / 4 / 5 / 6"

# ---------------------------------------------------------------- 7. D 零副作用
step "D 零副作用：权威字节逐字不变 + 零 commit + 工作区干净"
[ "$(tree_sha)" = "${BASE_TREE}" ] || { gitv status --porcelain; die "只读调用不得改动任何字节"; }
[ "$(commits)" = "${BASE_COMMITS}" ] || die "只读调用不得产生 commit"
[ -z "$(gitv status --porcelain)" ] || { gitv status --porcelain; die "收口：工作区必须干净"; }
ok "全部只读调用零字节变化、零 commit、工作区干净"

printf '\n===== C2 · I-…-008 判据全部通过（%d 项断言）=====\n' "${PASS}"
