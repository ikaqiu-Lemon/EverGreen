#!/usr/bin/env bash
# 批次C2 · I-…-011（P1/major）判据：**`--since` / `--until` 必须校验日历有效性** ——
# 日历上不存在的日期（`2026-13-45` / `2026-02-30` / `0000-00-00` / `9999-99-99`）必须退 `1`，
# 不得静默当成合法过滤条件参与字符串比较后退 `0`。
#
# 为什么必须报错：脚本 / Agent 自行拼日期时**月份与日越界是最常见的 off-by-one 产物**。
# 原实现只做形态匹配（`\d{4}-\d{2}-\d{2}`），`--since 2026-13-45` 会得到 `exit 0 + hits=0`，
# 调用方据此得出「库里没有符合条件的卡」的**错误结论**，且全程无 error 无 warning；更糟的是
# `--since 2026-02-30`（2 条）与 `--until 2026-02-30`（0 条）方向相反 —— 非法日期确实进了比较
# 逻辑，结果集不可解释。而 `2026-9-1` 却会报错：「校验过了」给了调用方虚假的安全感。
#
# 判据来源（声明面，逐字）：
#   `2026-09-19-m2-query-contract.md` §1.6：「`1` 参数非法 …」——日期参数的「非法」必须包含
#     **日历上不存在的日期**，而不只是形态不匹配；
#   `eg search --help` / `eg unreviewed --help`：`--since <YYYY-MM-DD>` 声明的是**日期**，
#     不是「任意十位数字串」。
# 采用**方案 A**：改用真实日期解析（等价 `time.Parse("2006-01-02")`，Go 标准库默认拒绝
# `2026-02-30` / `2026-13-45`），非法一律退 1、零命中不再静默。
#
# 本 suite 锁死的判据（事实只回读 `--json` 信封 / 盘上字节 / git）：
#   A **日历非法**：`--since` / `--until` 各 5 类非法值（月越界 / 日越界 / 全零 / 全九 /
#     平年 2 月 30 日）→ 退 **1** + `status="failed"` + `ok=false` + error 级诊断，且诊断文案
#     必须点出「日历上不存在」而不是含糊的「形态」。
#   B **形态非法仍 1**（既有行为一格不放宽）：`abc` / `2026-9-1` / `2026-09-1` / 空串外的短串。
#   C **合法边界仍 0 且结果正确**：`2026-09-01`（命中 2）、`2026-09-13`（命中 0）、
#     闰年 `2024-02-29`（合法，退 0）、平年 `2025-02-28`（合法，退 0）。
#   D **闰年判定必须真实**：`2025-02-29`（平年）非法退 1，`2024-02-29`（闰年）合法退 0 ——
#     这一对反证「不是靠 `月<=12 && 日<=31` 粗判蒙过去的」。
#   E **同族覆盖**：`eg unreviewed` 的 `--since` / `--until` 是同一族日期参数，同样必须拒收
#     日历非法值（原实现与 search 共用同一套形态校验，缺陷同型）。
#   F 只读零副作用 + 闭集：全部调用后权威字节逐字不变、零 commit，观测退出码集合恰 {0,1}。
#
# 约束：离线、零交互（stdin 接 /dev/null）、可重复执行；只用 bash / coreutils / git / go /
#   python3，无 jq 依赖；一切写只发生在 mktemp -d 沙箱内；任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/card-query/c2_date_calendar_valid.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
eg_require_disk 256

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-c2-caldate.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

KDIR_REL='domains/tech/knowledge'
CARD_A="k-20270413-c2d-alpha"
CARD_B="k-20270413-c2d-beta"
KEYWORD='日历判据'

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
# 权威面 sha：排除 .git / .index/（runtime-reserved，I-…-030 口径）/ .eg（报告缓存）。
tree_sha() { (cd "${VAULT}" && find . -path ./.git -prune -o -path ./.index -prune -o \
              -path ./.eg -prune -o -type f -print | sort | xargs sha256sum) |
              sha256sum | cut -d' ' -f1; }

command -v python3 >/dev/null || die "本脚本用 python3 做 JSON 信封判定"

CODES_SEEN=""
note_code() { CODES_SEEN="${CODES_SEEN} $1"; }

# assert_bad_date <场景> <是否要求点出日历> -- <命令…>
# 退 1 + status=failed + ok=false + error 级诊断；calendar=yes 时还要求文案点出「日历」。
assert_bad_date() {
  local what="$1" want_cal="$2" code
  shift 3
  code="$(eg_code "$@" --json)"
  note_code "${code}"
  [ "${code}" = "1" ] || { cat "${WORK}/out.txt"; die "${what}：应退 1（参数非法），实得 ${code}"; }
  python3 - "${WORK}/out.txt" "${what}" "${want_cal}" <<'PY' || exit 1
import json, sys
path, what, want_cal = sys.argv[1], sys.argv[2], sys.argv[3]
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
    bad.append(f'status={env.get("status")!r}，应为 "failed"')
if env.get("ok") is not False:
    bad.append(f'ok={env.get("ok")!r}，应为 false')
errs = [e for e in ((env.get("data") or {}).get("errors") or []) if e.get("level") == "error"]
if not errs:
    bad.append("data.errors[] 应至少一条 error 级")
elif want_cal == "yes" and not any("日历" in e.get("message", "") for e in errs):
    bad.append("日历非法的日期，诊断必须点出「日历上不存在」而不是含糊的「形态」，实得 "
               + repr([e.get("message") for e in errs]))
# 静默失效的反证：既然退 1，就绝不允许同时给出 hits / total（那正是原缺陷的形态）
data = env.get("data") or {}
if data.get("hits"):
    bad.append(f'退 1 时不得给出 hits[]（原缺陷是静默给出错误结果集），实得 {data.get("hits")!r}')
if bad:
    print(f"  [FAIL] {what}：" + "；".join(bad))
    sys.exit(1)
PY
}

# hits_of <文件>：读 data.hits 长度（search）。
hits_of() { python3 -c "
import json,sys
print(len((json.load(open(sys.argv[1],encoding='utf-8'))['data'].get('hits') or [])))" "$1"; }

seed_card() {
  cat >"${VAULT}/${KDIR_REL}/$1.md" <<CARD_EOF
---
id: $1
title: 卡 $1 ${KEYWORD}
status: active
created_at: '2026-09-01'
updated_at: '2026-09-12T10:00:00+08:00'
tags: [fix]
sources: []
---

# 卡 $1 ${KEYWORD}

## 知识内容

${KEYWORD}用的正文一行。

## 解释与依据

依据占位。

## 条件与边界

仅本判据使用。

## 用户补充

用户自己写的一行。

## 理解自检

- [ ] 能说出日历校验口径？
CARD_EOF
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译 eg 失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. 建库 + 语料
step "建库 + 两张 updated_at=2026-09-12 的卡（供边界过滤反证）"
mkdir -p "${VAULT}"
[ "$(eg_code init --domain tech)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain tech)" = "0" ] || die "config set 失败"
mkdir -p "${VAULT}/${KDIR_REL}"
seed_card "${CARD_A}"
seed_card "${CARD_B}"
gitv add -A >/dev/null
gitv -c user.name=eg -c user.email=eg@example.com commit -q -m "seed: I-…-011 判据语料"
BASE_COMMITS="$(commits)"
BASE_TREE="$(tree_sha)"
ok "库就绪（commit=${BASE_COMMITS}）"

# ---------------------------------------------------------------- 2. C 合法边界基线
step "C 合法边界：--since 2026-09-01 命中 2；--since 2026-09-13 命中 0；均退 0"
C="$(eg_code search "${KEYWORD}" --since 2026-09-01 --json)"
note_code "${C}"
[ "${C}" = "0" ] || { cat "${WORK}/out.txt"; die "合法 --since 应退 0"; }
[ "$(hits_of "${WORK}/out.txt")" = "2" ] || die "--since 2026-09-01 应命中 2 条"
C="$(eg_code search "${KEYWORD}" --since 2026-09-13 --json)"
note_code "${C}"
[ "${C}" = "0" ] || die "合法 --since（无命中）仍应退 0"
[ "$(hits_of "${WORK}/out.txt")" = "0" ] || die "--since 2026-09-13 应命中 0 条"
for D in 2024-02-29 2025-02-28 2026-12-31 2026-01-01; do
  C="$(eg_code search "${KEYWORD}" --since "${D}" --json)"
  note_code "${C}"
  [ "${C}" = "0" ] || { cat "${WORK}/out.txt"; die "合法日期 ${D} 应退 0，实得 ${C}"; }
done
ok "合法边界（含闰年 2024-02-29、平年 2025-02-28、年末年初）一律退 0，结果集正确"

# ---------------------------------------------------------------- 3. A 日历非法
step "A 日历非法：月越界 / 日越界 / 全零 / 全九 / 平年 2 月 30 日 → 退 1 + 点出「日历」"
for D in 2026-13-45 2026-02-30 0000-00-00 9999-99-99 2026-04-31; do
  assert_bad_date "eg search --since ${D}" yes -- search "${KEYWORD}" --since "${D}"
  assert_bad_date "eg search --until ${D}" yes -- search "${KEYWORD}" --until "${D}"
done
ok "5 类日历非法值 × --since/--until 共 10 组：一律退 1 且诊断点出「日历上不存在」"

# ---------------------------------------------------------------- 4. D 闰年真实判定
step "D 闰年判定必须真实：2025-02-29（平年）非法；2024-02-29（闰年）合法"
assert_bad_date "eg search --since 2025-02-29（平年 2 月 29 日）" yes -- search "${KEYWORD}" --since 2025-02-29
assert_bad_date "eg search --until 2100-02-29（百年非闰）" yes -- search "${KEYWORD}" --until 2100-02-29
C="$(eg_code search "${KEYWORD}" --since 2024-02-29 --json)"
note_code "${C}"
[ "${C}" = "0" ] || die "闰年 2024-02-29 必须合法退 0"
C="$(eg_code search "${KEYWORD}" --since 2000-02-29 --json)"
note_code "${C}"
[ "${C}" = "0" ] || die "2000-02-29（400 年闰）必须合法退 0"
ok "闰年规则真实生效：2025/2100-02-29 非法，2024/2000-02-29 合法（非「月≤12 且日≤31」粗判）"

# ---------------------------------------------------------------- 5. B 形态非法（既有行为不放宽）
step "B 形态非法仍退 1：abc / 2026-9-1 / 2026-09-1 / 20260901 / 2026-09-01T00:00:00"
for D in abc 2026-9-1 2026-09-1 20260901 '2026-09-01T00:00:00' '2026/09/01'; do
  assert_bad_date "eg search --since ${D}（形态非法）" no -- search "${KEYWORD}" --since "${D}"
done
ok "6 类形态非法值仍退 1（既有行为一格未放宽）"

# ---------------------------------------------------------------- 6. E 同族命令：eg unreviewed
step "E eg unreviewed 的 --since / --until 同族同判（形态 + 日历都拒收）"
C="$(eg_code unreviewed --since 2026-09-01 --json)"
note_code "${C}"
[ "${C}" = "0" ] || { cat "${WORK}/out.txt"; die "unreviewed 合法 --since 应退 0"; }
for D in 2026-13-45 2026-02-30 2025-02-29; do
  assert_bad_date "eg unreviewed --since ${D}" yes -- unreviewed --since "${D}"
  assert_bad_date "eg unreviewed --until ${D}" yes -- unreviewed --until "${D}"
done
assert_bad_date "eg unreviewed --since 2026-9-1（形态）" no -- unreviewed --since 2026-9-1
ok "eg unreviewed 与 eg search 同族同判（缺陷同型，一并收敛）"

# ---------------------------------------------------------------- 7. F 零副作用 + 闭集
step "F 只读零副作用 + 观测退出码闭集恰 {0,1}"
UNIQ="$(printf '%s\n' ${CODES_SEEN} | sort -u | tr '\n' ' ' | sed 's/ *$//')"
[ "${UNIQ}" = "0 1" ] || die "只读日期过滤路径的退出码闭集应恰 {0,1}，实得 {${UNIQ}}"
[ "$(tree_sha)" = "${BASE_TREE}" ] || die "只读查询不得改动任何权威字节"
[ "$(commits)" = "${BASE_COMMITS}" ] || die "只读查询不得产生 commit"
[ -z "$(gitv status --porcelain)" ] || { gitv status --porcelain; die "收口：工作区必须干净"; }
ok "闭集 {${UNIQ}}；零权威字节变化、零 commit、工作区干净"

printf '\n===== C2 · I-…-011 判据全部通过（%d 项断言）=====\n' "${PASS}"
