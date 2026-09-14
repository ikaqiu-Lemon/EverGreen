#!/usr/bin/env bash
# 批次C2 · I-…-009（P1/major）判据：**`updated_at` 必须在实际发生权威写入时刷新** ——
# 否则 `eg unreviewed` 的复核闭环（判据 `updated_at > reviewed_at`）对「过目之后又被改写」
# 这类最需要复查的变更**完全失灵，且无任何 warning**。
#
# 判据来源（声明面，逐字）：
#   `2026-10-10-m3-user-authorization-contract.md` §2.1 写权限矩阵**第 8 行**：
#     「`updated_at` | ✅ 由 CLI 在实际写入时更新 | ✅ 同左 | §5.5 标题『updated_at /
#      reviewed_at 的隔离』；**两条路径无差别**」——即 Agent 自动路径（P-A）与用户显式
#      路径（P-U）都必须刷新，不存在「只有新建时写一次」的口径；
#   顶层 `eg --help`：「列出未过目的产物（`updated_at > reviewed_at`；只读、不改状态）」;
#   同合同 §2.1 第 7 行 + §5.5 EG-CFM-06：`reviewed_at` 只由 `eg mark-reviewed` 写，
#     且「过目」本身**不是**对内容的修改 —— 因此 `mark-reviewed` 是刷新口径的**显式豁免**
#     （若它也刷新，刚标记完的产物会立刻又变未过目，自证已过目失效）。
#
# 为什么需要这支 suite：D1 审计实测（I-…-009）`edit` / `deprecate` / `restore` 写入后
# `updated_at` 停在建卡时刻，`mark-reviewed → edit → unreviewed` 的闭环静默断链。
# 本 suite 用**真实二进制**把「刷新」与「豁免」两侧同时钉死，避免修复过度（把 mark-reviewed
# 也刷新）或修复不足（只修 edit 一条路径）。
#
# 本 suite 锁死的判据（事实只回读盘上字节 / git / `--json` 信封）：
#   A `eg edit`（用户显式改核心内容）→ 退 0 + `updated_at` 严格变新 + 其余键与用户两段逐字不动。
#   B **复核闭环**：`mark-reviewed` → 跨秒 → `edit` → `eg unreviewed` **必须重新列出该卡**。
#   C 豁免：`mark-reviewed` 自己只写 `reviewed_at`，`updated_at` **逐字不变**。
#   D `deprecate` / `restore`（状态维度）→ 每次都严格变新。
#   E `replaced-by`（替代指针）→ 严格变新。
#   F `rel add` / `rel remove`（关系维度，宿主卡）→ 严格变新。
#   G **Agent 自动路径**（`eg apply`，不带 `--user-request` 的 `add_relation`）→ 同样严格变新
#     （矩阵第 8 行「两条路径无差别」）。
#   H 形态与零写入：`updated_at` 恒**恰一行**（整行覆盖，不长出重复键）、值恒为带时区
#     RFC3339、`created_at` / `id` / `title` 不变、frontmatter 不长出任何新键；
#     被拒的写（缺 `--user-request` 的 `edit`，退 2）**不得**刷新时间戳。
#
# 刻意**不**在本 suite 断言：
#   - 对账 R6 的 `stale` 标记是否刷新 —— 它有自己的声明面（M4 对账合同 §9「不自动重算 /
#     不自动清除」+ `SetStale` 的「不碰 updated_at」），由既有单测
#     `tests/_staged/internal/store/state_write_stale_test.go` 正向锁定，本 suite 不重复；
#   - 提案（`proposals/`）—— 提案 frontmatter 根本没有 `updated_at` 键（M-6）；
#   - 时间戳**精度**：声明面口径是秒级 RFC3339，因此凡需要「严格增大」的判据本 suite 一律
#     跨秒（`sleep 1`）后再发命令；同一秒内的连续两次写入不可判，属精度口径，不在本 issue。
#
# 约束：离线、零交互、可重复、无外部依赖（bash / coreutils / git / go / python3）；
# 一切写操作只发生在 mktemp -d 目录内；任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/lifecycle/c2_updated_at_refresh.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
eg_require_disk 256

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-c2-updated-at.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

KDIR_REL='domains/tech/knowledge'
CARD_A="k-20270413-c2u-alpha"
CARD_B="k-20270413-c2u-beta"
REL_A="${KDIR_REL}/${CARD_A}.md"
REL_B="${KDIR_REL}/${CARD_B}.md"
SEED_AT="2026-09-01T10:00:00+08:00"     # 语料里的旧 updated_at（远早于本次运行）
USER_LINE='用户自己写的一行，任何命令都不许动它。'

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
# chash 算 plan.base 用的 content_hash（与 store.ContentHash 同口径：sha256: + 十六进制）。
chash()   { printf 'sha256:%s\n' "$(sha256sum "${VAULT}/$1" | cut -d' ' -f1)"; }

command -v python3 >/dev/null || die "本脚本用 python3 做时刻比较与 JSON 键级判定"

# fmvalue <rel> <key>：取 frontmatter 顶层标量键的值（去掉包裹引号）。
fmvalue() {
  python3 - "${VAULT}/$1" "$2" <<'PY'
import sys
path, key = sys.argv[1], sys.argv[2]
out = ""
with open(path, encoding="utf-8") as fh:
    lines = fh.read().split("\n")
if lines and lines[0] == "---":
    for line in lines[1:]:
        if line == "---":
            break
        if line.startswith(key + ":"):
            out = line[len(key) + 1:].strip().strip("'\"")
            break
print(out)
PY
}

# fmkeys <rel>：frontmatter 顶层键集合（排序后一行），用于反证没长出新键。
fmkeys() {
  awk 'NR==1&&/^---$/{next} /^---$/{exit} /^[A-Za-z_]+:/{sub(/:.*/,"");print}' \
    "${VAULT}/$1" | sort | tr '\n' ' '
}

# stamp_gt <新> <旧>：两个带时区 RFC3339 时刻，严格晚于则退 0。
stamp_gt() {
  python3 - "$1" "$2" <<'PY'
import sys
from datetime import datetime
def p(v):
    return datetime.fromisoformat(v.replace("Z", "+00:00"))
sys.exit(0 if p(sys.argv[1]) > p(sys.argv[2]) else 1)
PY
}

# assert_refreshed <rel> <上一个时刻> <场景>：updated_at 严格变新 + 恰一行 + 合法带时区 RFC3339。
# 回显新的时刻，供调用方作为下一步的基线。
assert_refreshed() {
  local rel="$1" prev="$2" what="$3" now
  [ "$(grep -c '^updated_at:' "${VAULT}/${rel}")" = "1" ] ||
    die "${what}：updated_at 必须恰一行（整行覆盖，不许长出重复键）"
  now="$(fmvalue "${rel}" updated_at)"
  [ -n "${now}" ] || die "${what}：updated_at 不得为空"
  stamp_gt "${now}" "${prev}" ||
    { grep -n '^updated_at:' "${VAULT}/${rel}"; die "${what}：updated_at 必须严格变新（旧 ${prev} → 实得 ${now}）"; }
  echo "${now}"
}

# assert_untouched_fields <rel> <场景> <命令前的键集合>：created_at 与用户段逐字不动、
# frontmatter 顶层键集合与**该命令执行前**逐字一致（刷新是整行覆盖，绝不长出新键）。
assert_untouched_fields() {
  local rel="$1" what="$2" keys_before="$3"
  [ "$(fmvalue "${rel}" created_at)" = "2026-09-01" ] || die "${what}：created_at 不得被改动"
  grep -Fq -- "${USER_LINE}" "${VAULT}/${rel}" || die "${what}：「用户补充」必须逐字保留（B2）"
  [ "$(fmkeys "${rel}")" = "${keys_before}" ] ||
    die "${what}：frontmatter 顶层键集合不得变化（期望 ${keys_before}，实得 $(fmkeys "${rel}")）"
}

seed_card() { # $1=id  $2=额外 frontmatter 行（可空）
  cat >"${VAULT}/${KDIR_REL}/$1.md" <<CARD_EOF
---
id: $1
title: 卡 $1
status: active
created_at: '2026-09-01'
updated_at: '${SEED_AT}'
tags: [fix]
sources: []${2:+
$2}
---

# 卡 $1

## 知识内容

正文占位一行。

## 解释与依据

依据占位。

## 条件与边界

仅本判据使用。

## 用户补充

${USER_LINE}

## 理解自检

- [ ] 能说出刷新口径？
CARD_EOF
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译 eg 失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. 建库 + 语料
step "建库 + 两张卡（updated_at 一律为远早于本次运行的 ${SEED_AT}）"
mkdir -p "${VAULT}"
[ "$(eg_code init --domain tech)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain tech)" = "0" ] || die "config set 失败"
mkdir -p "${VAULT}/${KDIR_REL}"
seed_card "${CARD_A}" ""
seed_card "${CARD_B}" ""
gitv add -A >/dev/null
gitv -c user.name=eg -c user.email=eg@example.com commit -q -m "seed: I-…-009 判据语料"
[ -z "$(gitv status --porcelain)" ] || die "前置条件：工作区必须干净"
KEYS_SEED="$(fmkeys "${REL_A}")"
[ "$(fmvalue "${REL_A}" updated_at)" = "${SEED_AT}" ] || die "前置条件：语料 updated_at 应为 ${SEED_AT}"
ok "库就绪，卡 A frontmatter 顶层键：${KEYS_SEED}"

# ---------------------------------------------------------------- 2. A 用户显式改核心内容
step "A eg edit --user-request（用户显式路径 P-U）→ 退 0 + updated_at 严格变新"
BASE="$(commits)"
KEYS_A="$(fmkeys "${REL_A}")"
printf '%s\n' '用户显式改写的新结论。' >"${WORK}/new-body.md"
[ "$(eg_code edit --target "${CARD_A}" --section '知识内容' \
      --content "${WORK}/new-body.md" --user-request --json)" = "0" ] ||
  { cat "${WORK}/out.txt" "${WORK}/err.txt"; die "用户显式编辑应退 0"; }
grep -Fq '用户显式改写的新结论。' "${VAULT}/${REL_A}" || die "新正文必须逐字生效"
AT_A="$(assert_refreshed "${REL_A}" "${SEED_AT}" "eg edit")"
assert_untouched_fields "${REL_A}" "eg edit" "${KEYS_A}"
[ "$(fmvalue "${REL_A}" status)" = "active" ] || die "eg edit 不得动 status 维度"
if grep -q '^reviewed_at:' "${VAULT}/${REL_A}"; then die "eg edit 不得写 reviewed_at"; fi
[ "$(commits)" = "$((BASE + 1))" ] || die "一次成功编辑应恰 +1 commit"
[ -z "$(gitv status --porcelain)" ] || die "写入必须一并提交（工作区干净）"
ok "eg edit：内容生效 + updated_at ${SEED_AT} → ${AT_A} + 其余键与用户段逐字不动"

# ---------------------------------------------------------------- 3. B 复核闭环（本 issue 的核心影响）
step "B 复核闭环：mark-reviewed → 跨秒 edit → eg unreviewed 必须重新列出该卡"
[ "$(eg_code mark-reviewed --target "${CARD_A}" --json)" = "0" ] ||
  { cat "${WORK}/out.txt"; die "mark-reviewed 应退 0"; }
REVIEWED="$(fmvalue "${REL_A}" reviewed_at)"
[ -n "${REVIEWED}" ] || die "mark-reviewed 应写入 reviewed_at"
egv unreviewed --json >"${WORK}/unrev0.json"
python3 - "${WORK}/unrev0.json" "${CARD_A}" <<'PY' || die "刚过目的卡不应在未过目列表里（前置条件）"
import json, sys
d = json.load(open(sys.argv[1], encoding="utf-8"))["data"]
sys.exit(0 if sys.argv[2] not in [r["id"] for r in d["unreviewed"]] else 1)
PY
sleep 1                                     # 秒级 RFC3339：严格大于必须跨秒
printf '%s\n' '过目之后又被用户改写的内容 —— 必须重新进入未过目列表。' >"${WORK}/body2.md"
[ "$(eg_code edit --target "${CARD_A}" --section '知识内容' \
      --content "${WORK}/body2.md" --user-request --json)" = "0" ] ||
  { cat "${WORK}/out.txt"; die "过目后再编辑应退 0"; }
AT_A="$(assert_refreshed "${REL_A}" "${AT_A}" "过目后再编辑")"
stamp_gt "${AT_A}" "${REVIEWED}" ||
  die "过目后编辑：updated_at（${AT_A}）必须严格晚于 reviewed_at（${REVIEWED}）"
egv unreviewed --json >"${WORK}/unrev1.json"
python3 - "${WORK}/unrev1.json" "${CARD_A}" <<'PY' || { cat "${WORK}/unrev1.json"; exit 1; }
import json, sys
d = json.load(open(sys.argv[1], encoding="utf-8"))["data"]
ids = [r["id"] for r in d["unreviewed"]]
if sys.argv[2] not in ids:
    print(f"  [FAIL] 过目后被改写的卡必须重新出现在 eg unreviewed（实得 {ids}）")
    sys.exit(1)
PY
[ "$(fmvalue "${REL_A}" reviewed_at)" = "${REVIEWED}" ] ||
  die "eg edit 不得改写 reviewed_at（那是 mark-reviewed 的唯一写口）"
ok "复核闭环成立：过目 → 改写 → 该卡重新进入 eg unreviewed"

# ---------------------------------------------------------------- 4. C 豁免：mark-reviewed 不刷新
step "C 豁免：mark-reviewed 只写 reviewed_at，updated_at 逐字不变（防修复过度）"
sleep 1
BEFORE_AT="$(fmvalue "${REL_A}" updated_at)"
[ "$(eg_code mark-reviewed --target "${CARD_A}" --json)" = "0" ] ||
  { cat "${WORK}/out.txt"; die "再次 mark-reviewed 应退 0"; }
[ "$(fmvalue "${REL_A}" updated_at)" = "${BEFORE_AT}" ] ||
  die "mark-reviewed 绝不刷新 updated_at（§5.5 EG-CFM-06；否则自证已过目失效）"
NEW_REVIEWED="$(fmvalue "${REL_A}" reviewed_at)"
stamp_gt "${NEW_REVIEWED}" "${REVIEWED}" || die "再次过目应推进 reviewed_at"
egv unreviewed --json >"${WORK}/unrev2.json"
python3 - "${WORK}/unrev2.json" "${CARD_A}" <<'PY' || die "重新过目后该卡应离开未过目列表"
import json, sys
d = json.load(open(sys.argv[1], encoding="utf-8"))["data"]
sys.exit(0 if sys.argv[2] not in [r["id"] for r in d["unreviewed"]] else 1)
PY
ok "mark-reviewed：updated_at 逐字不变（${BEFORE_AT}），reviewed_at 推进且闭环收敛"

# ---------------------------------------------------------------- 5. D 状态维度
step "D deprecate / restore（状态维度）→ 每次都严格变新"
sleep 1
KEYS_A="$(fmkeys "${REL_A}")"
[ "$(eg_code deprecate --target "${CARD_A}" --reason '判据：结论已过期' --json)" = "0" ] ||
  { cat "${WORK}/out.txt"; die "deprecate 应退 0"; }
[ "$(fmvalue "${REL_A}" status)" = "deprecated" ] || die "deprecate 后 status 应为 deprecated"
AT_A="$(assert_refreshed "${REL_A}" "${AT_A}" "eg deprecate")"
assert_untouched_fields "${REL_A}" "eg deprecate" "${KEYS_A}"
sleep 1
[ "$(eg_code restore --target "${CARD_A}" --reason '判据：结论仍成立' --json)" = "0" ] ||
  { cat "${WORK}/out.txt"; die "restore 应退 0"; }
[ "$(fmvalue "${REL_A}" status)" = "active" ] || die "restore 后 status 应为 active"
AT_A="$(assert_refreshed "${REL_A}" "${AT_A}" "eg restore")"
ok "deprecate / restore：状态每变一次，updated_at 就严格变新（当前 ${AT_A}）"

# ---------------------------------------------------------------- 6. E 替代指针
step "E replaced-by（替代指针）→ 严格变新"
sleep 1
[ "$(eg_code deprecate --target "${CARD_A}" --reason '判据：为替代做准备' --json)" = "0" ] ||
  die "deprecate 应退 0"
AT_A="$(assert_refreshed "${REL_A}" "${AT_A}" "eg deprecate（第二次）")"
sleep 1
[ "$(eg_code replaced-by --target "${CARD_A}" --to "${CARD_B}" --reason '判据：由 B 取代' --json)" = "0" ] ||
  { cat "${WORK}/out.txt"; die "replaced-by 应退 0"; }
grep -q '^replaced_by:' "${VAULT}/${REL_A}" || die "replaced-by 应写入替代指针"
AT_A="$(assert_refreshed "${REL_A}" "${AT_A}" "eg replaced-by")"
ok "replaced-by：替代指针落盘同时刷新 updated_at（当前 ${AT_A}）"

# ---------------------------------------------------------------- 7. F 关系维度（宿主卡）
step "F rel add / rel remove（关系维度）→ 宿主卡严格变新"
AT_B="$(fmvalue "${REL_B}" updated_at)"
[ "${AT_B}" = "${SEED_AT}" ] || die "前置条件：卡 B 的 updated_at 应仍是语料值"
sleep 1
KEYS_B="$(fmkeys "${REL_B}")"
[ "$(eg_code rel add "${CARD_B}" supports "${CARD_A}" --reason '判据：B 支持 A' --json)" = "0" ] ||
  { cat "${WORK}/out.txt"; die "rel add 应退 0"; }
AT_B="$(assert_refreshed "${REL_B}" "${AT_B}" "eg rel add")"
# 键集合此处**允许**多出 relations（语料没有这个序列键，本命令新建它是既有正确行为）；
# 除它之外一个键都不许多，且 created_at / 用户段逐字不动。
assert_untouched_fields "${REL_B}" "eg rel add" \
  "$(printf '%s\n' ${KEYS_B} relations | sort | tr '\n' ' ')"
sleep 1
[ "$(eg_code rel remove "${CARD_B}" supports "${CARD_A}" --reason '判据：移除关系' --json)" = "0" ] ||
  { cat "${WORK}/out.txt"; die "rel remove 应退 0"; }
AT_B="$(assert_refreshed "${REL_B}" "${AT_B}" "eg rel remove")"
ok "rel add / rel remove：宿主卡 updated_at 严格变新（当前 ${AT_B}）"

# ---------------------------------------------------------------- 8. G Agent 自动路径
step "G Agent 自动路径（eg apply 追加正文，不带 --user-request）→ 同样严格变新"
sleep 1
cat >"${WORK}/auto.json" <<PLAN
{ "plan_version": 1, "verb": "process", "domain": "tech",
  "reason": "判据：Agent 自动路径的实际写入同样刷新 updated_at（矩阵第 8 行『两条路径无差别』）",
  "requirement_ids": ["EG-CFM-06"],
  "base": { "${REL_B}": "$(chash "${REL_B}")" },
  "ops": [ { "op": "append_card", "card": "${CARD_B}",
             "sections": { "解释与依据": "- Agent 自动追加的一条依据。\n" } } ] }
PLAN
[ "$(eg_code apply --plan "${WORK}/auto.json" --json)" = "0" ] ||
  { cat "${WORK}/out.txt"; die "Agent 自动路径 apply 应退 0"; }
AT_B="$(assert_refreshed "${REL_B}" "${AT_B}" "eg apply（Agent 自动路径）")"
if grep -q '^reviewed_at:' "${VAULT}/${REL_B}"; then die "Agent 写入一律不得写 reviewed_at"; fi
ok "Agent 自动路径与用户显式路径无差别：updated_at 同样刷新（当前 ${AT_B}）"

# ---------------------------------------------------------------- 9. H 形态 + 零写入不刷新
step "H 形态：恰一行 / 合法带时区 RFC3339 / 键集合不变；被拒的写不刷新时间戳"
python3 - "${VAULT}/${REL_A}" "${VAULT}/${REL_B}" <<'PY' || die "updated_at 必须是带时区 RFC3339"
import re, sys
from datetime import datetime
pat = re.compile(r"^updated_at:\s*'?([^'\n]+)'?$", re.M)
for path in sys.argv[1:]:
    raw = open(path, encoding="utf-8").read()
    hits = pat.findall(raw)
    if len(hits) != 1:
        print(f"  [FAIL] {path}: updated_at 行数 = {len(hits)}")
        sys.exit(1)
    v = hits[0].strip()
    dt = datetime.fromisoformat(v.replace("Z", "+00:00"))
    if dt.tzinfo is None:
        print(f"  [FAIL] {path}: updated_at 缺时区：{v}")
        sys.exit(1)
PY
BEFORE_AT="$(fmvalue "${REL_A}" updated_at)"
SUM_BEFORE="$(sha256sum "${VAULT}/${REL_A}" | cut -d' ' -f1)"
LOG_BEFORE="$(commits)"
sleep 1
[ "$(eg_code edit --target "${CARD_A}" --section '知识内容' --content '偷偷改核心内容。' --json)" = "2" ] ||
  { cat "${WORK}/out.txt"; die "缺 --user-request 的 edit 必须退 2"; }
[ "$(fmvalue "${REL_A}" updated_at)" = "${BEFORE_AT}" ] ||
  die "零写入的失败路径绝不刷新 updated_at（刷新只跟随**实际写入**）"
[ "$(sha256sum "${VAULT}/${REL_A}" | cut -d' ' -f1)" = "${SUM_BEFORE}" ] || die "被拒必须字节不变"
[ "$(commits)" = "${LOG_BEFORE}" ] || die "被拒必须零 commit"
[ -z "$(gitv status --porcelain)" ] || die "收口：工作区必须干净"
ok "形态成立：恰一行 + 带时区 RFC3339 + 键集合不变；被拒路径零写入零刷新"

printf '\n===== C2 · I-…-009 判据全部通过（%d 项断言）=====\n' "${PASS}"
