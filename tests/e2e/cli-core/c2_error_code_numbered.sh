#!/usr/bin/env bash
# 批次C2 · I-…-015（P1/major）判据：**`data.errors[]` 里每条 error 级条目都必须带编号域内的
# `code`** —— 空码 `""` 只对 **warning** 开放，`skipped[].cause` 的枚举值不得占用 `code` 位。
#
# 判据来源（声明面，逐字）：
#   `2026-09-01-eg-cli-contract.md` §5 `code` 行：「分级编号：`E1`–`E6` / `W1`–`W8` / `I1`；
#     **未编号 warning** 填 `""` 并在 `message` 说明」—— 空码**仅**对 warning 开放；
#   同 §5 A-29 脚注：「实现侧的分级编号在其上**只增不改**…**载荷结构本身不变**」
#     —— 编号域封闭，不含 `content_hash_mismatch` 这类自由标识；
#   `skill/SKILL.md:140` / `:143` / `:828`：退 `2` / 退 `5` 的处置动作**以 `code` 为输入**
#     （「读 `data.errors[]` 的 `code`（E1–E6）+ `op_index` + `path`，改 plan 后重投」）。
#
# 为什么必须修：`code` 为空时接入方**无法按码分支**，只能对中文 `message` 做子串匹配 ——
# 而 `message` 从不在任何合同里被冻结，属随时可改的自由文本。`content_hash_mismatch` 与
# `E*`/`W*` 同处一个键，按码前缀路由的调用方（「`E` 开头 → 修 plan 重投」）会命中 default
# 分支或直接崩。空码条目还无法聚合统计：同一告警在监控里退化成一条不可分类的 `""`。
#
# 本 suite 锁死的判据（事实只回读 `--json` 信封 / 盘上字节 / git）：
#   A **全域断言（非白名单）**：对下表**每一条**命令的 error 路径，`data.errors[]` 中
#     `level == "error"` 的条目**逐条**满足 `code` 匹配 `^(E|W|I|Q)[0-9]+$`。表按「命令 ×
#     错误族」铺开而不是逐路径豁免；新增命令若忘了给码，只要出现在错误路径上就会被抓到。
#   B **码域纯净**：任何 error 条目的 `code` **不得**等于 `skipped[]` 的 kind / cause 枚举
#     （`file_changed` / `content_hash_mismatch` / `user_block_unsafe` /
#     `user_block_not_preserved`），也不得是任何非 `^(E|W|I|Q)\d+$` 的自由标识。
#   C **cause 不许被搬走**：`report.skipped[].cause` 必须**仍然**逐字是
#     `content_hash_mismatch`，且该事实必须**同时**在对应 error 条目的 `message` 里可读
#     （kind + cause 都在）—— 修法是「把 cause 从 code 位移出」，不是「把 cause 删掉」。
#   D **不过度纠正**：未编号 **warning** 的空码是合同**授权**的，必须**继续**存在
#     （反证：若实现图省事把所有空码一律填成某个码，本项立刻转红）。
#   E **不重复登记**：同一次失败不得既给编号条目、又给一条空码孪生条目
#     （原实现在 `rel add` 悬空路径上就是 `["E2", ""]` 两条）。
#   F **载荷仍是六字段**：`code`/`level`/`path`/`op_index`/`message`（+ 可选 `target`）
#     —— 补码不得顺手加第七个字段。
#   G 退出码与信封语义不因补码而变（逐条记录修复前后的 exit_code，全表对照）。
#
# 约束：离线、零交互（stdin 接 /dev/null）、可重复；只用 bash / coreutils / git / go /
#   python3，无 jq 依赖；写只发生在 mktemp -d 沙箱内；任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/cli-core/c2_error_code_numbered.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
eg_require_disk 256

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-c2-code.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"
CARD="k-20270413-c2code"
KDIR_REL='domains/tech/knowledge'

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

command -v python3 >/dev/null || die "本脚本用 python3 做 JSON 信封判定"

# 判定器：吃一个 --json 输出文件，逐条检查 error 级条目的 code。
# 用法：check_codes <文件> <场景标签>
check_codes() {
  python3 - "$1" "$2" <<'PY' || exit 1
import json, re, sys
path, label = sys.argv[1], sys.argv[2]
raw = open(path, encoding="utf-8").read()
try:
    env = json.loads(raw)
except Exception as exc:                       # noqa: BLE001
    print(f"  [FAIL] {label}：--json 输出不是合法 JSON：{exc}\n{raw[:400]}")
    sys.exit(1)

NUMBERED = re.compile(r"^(E|W|I|Q)\d+$")
# skipped[] 的 kind / cause 封闭枚举：它们的家在 report.skipped[]，不得占用 code 位。
CAUSE_ENUM = {"file_changed", "content_hash_mismatch",
              "user_block_unsafe", "user_block_not_preserved"}
FIELDS = {"code", "level", "path", "op_index", "message", "target"}

errs = (env.get("data") or {}).get("errors") or []
bad = []
for i, d in enumerate(errs):
    if d.get("level") != "error":
        continue
    code = d.get("code")
    if not code:
        bad.append(f'errors[{i}] code 为空（空码只对 warning 开放，合同 §5）'
                   f'：message={d.get("message", "")[:90]!r}')
    elif code in CAUSE_ENUM:
        bad.append(f'errors[{i}] code={code!r} 是 skipped[].kind/cause 枚举值，'
                   f'越出 E*/W*/I*/Q* 编号域（cause 的家是 report.skipped[].cause）')
    elif not NUMBERED.match(code):
        bad.append(f'errors[{i}] code={code!r} 不匹配 ^(E|W|I|Q)\\d+$（编号域封闭）')
    extra = set(d.keys()) - FIELDS
    if extra:
        bad.append(f'errors[{i}] 出现第七个字段 {sorted(extra)}（载荷恰六字段，A-29）')
if bad:
    print(f"  [FAIL] {label}：" + "；".join(bad))
    sys.exit(1)
print(f'  · {label}：exit_code={env.get("exit_code")} status={env.get("status")} '
      f'error 条目 {len([d for d in errs if d.get("level") == "error"])} 条，'
      f'codes={[d.get("code") for d in errs if d.get("level") == "error"]}')
PY
}

# assert_errpath <标签> <期望退出码或 *> -- <命令…>
assert_errpath() {
  local label="$1" want="$2" code
  shift 3
  code="$(eg_code "$@" --json)"
  if [ "${want}" != "*" ] && [ "${code}" != "${want}" ]; then
    cat "${WORK}/out.txt"
    die "${label}：退出码应为 ${want}，实得 ${code}（本 suite 只补 code，不改退出码语义）"
  fi
  check_codes "${WORK}/out.txt" "${label}"
}

# assert_errpath_in <vault> <标签> <期望退出码或 *> -- <命令…>：同上，但指定库。
#
# I-…-018 取材面搬家：`eg capture` 自本轮起**也**落 `.eg/last-report.json`（合同 §1.9 的覆盖面
# 是「最近一次 apply / capture」），因此「无历史报告」这一 error 面必须取一个**没跑过任何
# 写命令**的库 —— 判据本身（退 1 + code 落编号域）逐字不变、条数不减。
assert_errpath_in() {
  local vault="$1" label="$2" want="$3" code=0
  shift 4
  "${EG}" --vault "${vault}" "$@" --json >"${WORK}/out.txt" 2>"${WORK}/err.txt" || code=$?
  if [ "${want}" != "*" ] && [ "${code}" != "${want}" ]; then
    cat "${WORK}/out.txt"
    die "${label}：退出码应为 ${want}，实得 ${code}（本 suite 只补 code，不改退出码语义）"
  fi
  check_codes "${WORK}/out.txt" "${label}"
}

seed_card() {
  mkdir -p "${VAULT}/${KDIR_REL}"
  cat >"${VAULT}/${KDIR_REL}/${CARD}.md" <<CARD_EOF
---
id: ${CARD}
title: 诊断码判据语料
status: active
created_at: '2026-09-01'
updated_at: '2026-09-12T10:00:00+08:00'
tags: [fix]
sources: []
---

# 诊断码判据语料

## 知识内容

补码判据用的正文一行。

## 解释与依据

依据占位。

## 条件与边界

仅本判据使用。

## 用户补充

用户自己写的一行。

## 理解自检

- [ ] 能说出 code 位的编号域？
CARD_EOF
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译 eg 失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. 未建库路径
step "A① 未建库时的 error 路径也必须带编号（最早的失败面）"
mkdir -p "${VAULT}"
assert_errpath "未建库 eg report --last" 1 -- report --last
ok "未建库路径的 error 条目全部带编号"

# ---------------------------------------------------------------- 2. 建库 + 语料
step "建库 + 一张卡 + 一份原文（供各错误族取材）"
[ "$(eg_code init --domain tech)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain tech)" = "0" ] || die "config set 失败"
printf 'D2 证据语料：退出码与诊断码都是对外合同，缺一不可。%.0s' 1 2 3 4 5 6 7 8 >"${WORK}/art.md"
: >"${WORK}/empty.md"
[ "$(eg_code capture --url https://example.com/c2code --title 'C2 码判据原文' \
      --reason 补码判据 --body-file "${WORK}/art.md")" = "0" ] || die "capture 正常路径失败"
seed_card
gitv add -A >/dev/null
gitv -c user.name=eg -c user.email=eg@example.com commit -q -m "seed: I-…-015 判据语料"
# 一个只 init 过、没跑过任何写命令的库：专供「无历史报告」这一 error 面取材（I-…-018）。
FRESH="${WORK}/fresh"
mkdir -p "${FRESH}"
"${EG}" --vault "${FRESH}" init --domain tech </dev/null >/dev/null 2>&1 || die "init（fresh）失败"
BASE_COMMITS="$(gitv log --oneline | wc -l | tr -d ' ')"
ok "库就绪（commit=${BASE_COMMITS}）"

# ---------------------------------------------------------------- 3. A 全域：参数 / 用法族
step "A② 参数 / 用法族（退 1）：命令 × 错误族全表铺开，逐条断言编号"
assert_errpath_in "${FRESH}" "eg report --last（无历史）"    1 -- report --last
assert_errpath "eg delete 缺 --reason"                     1 -- delete --target "${CARD}" \
  --proposal p-20260912-001 --confirm --user-request
assert_errpath "eg search --limit -1（分页参数非法）"      1 -- search 判据 --limit -1
assert_errpath "eg search --offset -1"                     1 -- search 判据 --offset -1
assert_errpath "eg search（空检索词）"                     1 -- search ''
assert_errpath "eg search --domain 未登记"                 1 -- search 判据 --domain nope
assert_errpath "eg unreviewed --since 非法日期"            1 -- unreviewed --since 2026-13-45
assert_errpath "eg card show（不存在的 ID）"               1 -- card show k-nope
assert_errpath "eg config set（非法键）"                   1 -- config set nope 1
ok "参数 / 用法族 9 条路径：error 条目全部带编号域内的 code"

# ---------------------------------------------------------------- 4. A 校验 / 前置族
step "A③ 校验 / 前置族（退 2）：零写入类失败同样必须带编号"
assert_errpath "eg capture（正文为空）"                    2 -- capture --url https://example.com/x2 \
  --title 空正文 --reason 补码判据 --body-file "${WORK}/empty.md"
# 合同 §1.3 明写：--url 与 --title 同时缺失属**校验失败**（退 2，零写入），不是参数形态错误
# ——本 suite 只管 code 位，退出码口径照抄合同，不在这里改判。
assert_errpath "eg capture 缺 --url / --title"             2 -- capture --reason r \
  --body-file "${WORK}/art.md"
assert_errpath "eg mark-reviewed（目标不存在）"            2 -- mark-reviewed --target k-nope --user-request
assert_errpath "eg undelete（目标不存在）"                 2 -- undelete --target k-nope --reason r --user-request
assert_errpath "eg rel add（悬空对端）"                    2 -- rel add "${CARD}" supports k-nope \
  --reason 判据 --user-request
assert_errpath "eg rel add（自环）"                        2 -- rel add "${CARD}" supports "${CARD}" \
  --reason 判据 --user-request
assert_errpath "eg proposal approve（不存在的提案）"       2 -- proposal approve p-20260912-001 --user-request
ok "校验 / 前置族 7 条路径：error 条目全部带编号"

# ---------------------------------------------------------------- 5. A 授权门禁族
step "A④ 授权门禁族：缺 --user-request / 缺 --confirm 的拒绝条目也必须带编号"
assert_errpath "eg delete 缺 --user-request" '*' -- delete --target "${CARD}" --reason r --confirm
assert_errpath "eg delete 缺 --confirm"      '*' -- delete --target "${CARD}" --reason r --user-request
ok "授权门禁族 2 条路径：拒绝条目带编号（Agent 可按码分支到「补授权佐证」）"

# ---------------------------------------------------------------- 6. B / C：B3 跳过
step "B/C B3 跳过（退 3）：code 移出 cause 枚举，且 cause 仍留在 report.skipped[].cause"
cat >"${WORK}/stale.json" <<PLAN_EOF
{ "plan_version": 1, "verb": "process", "domain": "tech", "reason": "I-…-015 B3 判据",
  "requirement_ids": ["EG-AUD-D2"], "base": {},
  "ops": [ { "op": "deprecate", "initiator": "user", "target": "${CARD}",
             "reason": "base 未覆盖" } ] }
PLAN_EOF
assert_errpath "eg apply（B3 base 未覆盖）" 3 -- apply --plan "${WORK}/stale.json" --user-request
python3 - "${WORK}/out.txt" <<'PY' || exit 1
import json, sys
env = json.load(open(sys.argv[1], encoding="utf-8"))
data = env.get("data") or {}
errs = [d for d in (data.get("errors") or []) if d.get("level") == "error"]
skipped = ((data.get("report") or {}).get("skipped")) or []
bad = []
# C：cause 的家仍在 report.skipped[].cause，一个字都不许少
if len(skipped) != 1:
    bad.append(f"report.skipped[] 应恰 1 条，实得 {len(skipped)}")
else:
    s = skipped[0]
    if s.get("kind") != "file_changed":
        bad.append(f'skipped[0].kind={s.get("kind")!r}，应逐字 file_changed')
    if s.get("cause") != "content_hash_mismatch":
        bad.append(f'skipped[0].cause={s.get("cause")!r}，应逐字 content_hash_mismatch'
                   "（修法是把 cause 从 code 位移出，不是把 cause 删掉）")
# C：kind 与 cause 必须同时在对应 error 条目的 message 里可读（可观测性不许倒退）
skip_errs = [d for d in errs if "file_changed" in (d.get("message") or "")]
if not skip_errs:
    bad.append("B3 跳过的 error 条目 message 里必须可读到 kind=file_changed")
elif not any("content_hash_mismatch" in (d.get("message") or "") for d in skip_errs):
    bad.append("cause 从 code 位移出后必须落到 message 里，否则 data.errors[] 面丢失归因："
               + repr([d.get("message") for d in skip_errs]))
if bad:
    print("  [FAIL] B3 跳过：" + "；".join(bad))
    sys.exit(1)
print("  · B3：skipped[0].kind=file_changed / cause=content_hash_mismatch 原样保留，"
      "且 kind+cause 在 error message 内可读")
PY
ok "B3 跳过：code 已在编号域内，cause 仍逐字留在 report.skipped[].cause 且 message 可读"

# ---------------------------------------------------------------- 7. E 不重复登记
step "E 同一次失败不得既给编号条目、又给空码孪生条目"
_=$(eg_code rel add "${CARD}" supports k-nope2 --reason 判据 --user-request --json)
python3 - "${WORK}/out.txt" <<'PY' || exit 1
import json, sys
env = json.load(open(sys.argv[1], encoding="utf-8"))
errs = [d for d in ((env.get("data") or {}).get("errors") or []) if d.get("level") == "error"]
empty = [d for d in errs if not d.get("code")]
if empty:
    print("  [FAIL] 悬空对端路径仍留有空码孪生条目（原实现是 ['E2','']）："
          + repr([(d.get("code"), (d.get("message") or "")[:60]) for d in errs]))
    sys.exit(1)
print(f'  · 悬空对端：error codes={[d.get("code") for d in errs]}（无空码孪生）')
PY
ok "无空码孪生条目：同一次失败的登记面唯一"

# ---------------------------------------------------------------- 8. D 不过度纠正
step "D 未编号 warning 的空码是合同授权的，必须继续存在（反证过度纠正）"
# 取材要点：探针必须选**真的会发未编号 warning** 的成功路径 —— `capture` 省略 --domain 会走
# default_domain 落位提醒，`reconcile` 干净盘面会发「零改动即零提交」，两者的 code 位按合同
# §5 就该是空的。（`report --last` / `check` 只发编号诊断，做不了这条反证的取材面。）
FOUND_EMPTY_WARN=0
probe_empty_warn() {
  python3 -c "
import json,sys
env=json.load(open('${WORK}/out.txt',encoding='utf-8'))
ws=env.get('warnings') or []
sys.exit(0 if any(not w.get('code') and w.get('level')=='warning' for w in ws) else 1)
" 2>/dev/null
}
_=$(eg_code capture --url https://example.com/c2warn --title 'C2 空码 warning 取材' \
      --reason 反证过度纠正 --body-file "${WORK}/art.md" --json)
probe_empty_warn && FOUND_EMPTY_WARN=1
if [ "${FOUND_EMPTY_WARN}" = "0" ]; then
  _=$(eg_code reconcile --user-request --json)
  probe_empty_warn && FOUND_EMPTY_WARN=1
fi
[ "${FOUND_EMPTY_WARN}" = "1" ] || die "未编号 warning（空码）已被一并填码 —— 过度纠正：合同 §5 明确对 warning 开放空码"
ok "未编号 warning 的空码仍在（补码只作用于 error 级，未越界扩张到 warning）"

# ---------------------------------------------------------------- 9. G 收口
step "G 收口：本判据只补 code，不新增退出码、不改权威字节结论"
UNIQ="$(gitv log --oneline | wc -l | tr -d ' ')"
[ -n "${UNIQ}" ] || die "git log 读取失败"
ok "git 历史可读（commit=${UNIQ}）；本 suite 的断言全部只回读信封与盘面事实"

printf '\n===== C2 · I-…-015 判据全部通过（%d 项断言）=====\n' "${PASS}"
