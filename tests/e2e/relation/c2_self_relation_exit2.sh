#!/usr/bin/env bash
# 批次C2 · I-…-010（P1/major）判据：**自环关系必须在 plan 校验阶段拦下 → 退 2 / failed** ——
# 而不是穿透到写入层被折成「部分写入被跳过」的 `3` / `partial`。
#
# 为什么必须是 2：`3` 的声明语义是「部分写入被跳过」、`status="partial"` 是「部分成功」，
# 调用方据此会认为磁盘上已落了一部分改动，从而去做回滚 / 重放 / 对账等**补偿动作**，
# 而真相是零写入零 commit —— 补偿本身成了新的风险源。同一类「关系端点非法」也不能被拆成
# 两个码（自环 3 / 悬空 2），否则关系写入的错误处理无法统一。
#
# 判据来源（声明面，逐字）：
#   `2026-09-01-eg-cli-contract.md` §4 退出码表：`2` = 校验失败（零写入）；`3` = 部分写入被
#     跳过；`4` = Git 提交失败；
#   `2026-09-08-changeplan-contract.md` §4.1：error 恰 E1–E6（M3 后扩到 E10），
#     「触发即退 `2`、零写入、无 commit」；E5 = 「op 字段不成立」族（§2 表第 1 行、§3 的
#     `url`/`title` 两者同缺、§3 的 `question` 为空均判 E5）——自环即 `from` 与 `target`
#     取值组合不成立，故归 **E5**，不自创编号（编号集合封闭）。
#   自环是**纯静态**的 plan 级语义约束（无需读盘即可判定），与 E2/E3/E4 同阶段。
#
# 本 suite 锁死的判据（事实只回读 `--json` 信封 / 盘上字节 / git）：
#   A `eg rel add A supports A` → 退 **2** + `status="failed"` + `ok=false` + 零 commit + 字节不变。
#   B 诊断可定位：`data.errors[]` 至少一条 `code="E5"`（plan 级编号，**不为空**）+ 带 `ops[0]` 路径。
#   C **文案不再自相矛盾**：不得出现「其余 op 照常执行」（M6 原子事务下不成立），
#     也不得出现「原子事务已放弃 / 写入失败」——校验阶段拦下时事务根本没开始。
#   D **不重复登记**：同一条 message 不得同时以 error 和 warning 各出现一次。
#   E **同族同码**：自环与 E2 悬空（target 侧 / from 侧）必须同为 2；type 越界仍 1；
#     合法关系仍 0，重复执行仍 0（幂等），闭集恰 {0,1,2}。
#   F 纵深防御仍在：`eg apply` 里的 `add_relation` 自环同样退 2、零写入零 commit。
#   G 四类关系类型（supports / opposing / derives / limits，冻结合同 F4 的封闭四值）的自环
#     一律 2 —— opposing 会走
#     方向规范化分支，必须证明它也在校验阶段被拦（规范化后两端仍相同）。
#
# 约束：离线、零交互（stdin 接 /dev/null）、可重复执行；只用 bash / coreutils / git / go /
#   python3，无 jq 依赖；一切写只发生在 mktemp -d 沙箱内；任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/relation/c2_self_relation_exit2.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
eg_require_disk 256

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-c2-selfrel.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

KDIR_REL='domains/tech/knowledge'
CARD_A="k-20270413-c2r-alpha"
CARD_B="k-20270413-c2r-beta"
GHOST="k-29991231-nonexistent"
REL_A="${KDIR_REL}/${CARD_A}.md"

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
chash()   { printf 'sha256:%s\n' "$(sha256sum "${VAULT}/$1" | cut -d' ' -f1)"; }
# tree_sha 只覆盖**权威面**：排除 .git、.index/、.eg/ —— .index/ 按 I-…-030 的口径是
# 「派生物 + 运行时证据」混居目录（A 类写路径取 run.lock 会留痕），.eg/last-report.json 是
# 每条命令都会刷新的运行时报告缓存；两者都不是权威 Markdown 字节。
tree_sha() { (cd "${VAULT}" && find . -path ./.git -prune -o -path ./.index -prune -o \
              -path ./.eg -prune -o -type f -print | sort | xargs sha256sum) |
              sha256sum | cut -d' ' -f1; }

# assert_no_derived_index：.index/ 若存在，只许 run.lock / txn 两类 runtime-reserved 条目，
# 且派生索引 DB 恒不存在（与 I-…-030 重钉的判据同源）。
assert_no_derived_index() {
  local f
  for f in eg.db eg.db-wal eg.db-shm; do
    [ ! -e "${VAULT}/.index/${f}" ] || die "只读 / 校验失败路径不得建出派生索引 ${f}"
  done
  if [ -d "${VAULT}/.index" ]; then
    for f in $(ls -A "${VAULT}/.index"); do
      case "${f}" in
        run.lock|txn) ;;
        *) die ".index/ 出现非 runtime-reserved 条目：${f}" ;;
      esac
    done
  fi
}

command -v python3 >/dev/null || die "本脚本用 python3 做 JSON 信封判定"

CODES_SEEN=""
note_code() { CODES_SEEN="${CODES_SEEN} $1"; }

# assert_validation_envelope <场景>：校验 ${WORK}/out.txt 的信封 —— 2 / failed / ok=false
# + 至少一条 code=E5 的 error + 无矛盾措辞 + error/warning 不重复登记同一条 message。
assert_validation_envelope() {
  python3 - "${WORK}/out.txt" "$1" <<'PY' || exit 1
import json, sys
path, what = sys.argv[1], sys.argv[2]
raw = open(path, encoding="utf-8").read()
try:
    env = json.loads(raw)
except Exception as exc:                       # noqa: BLE001
    print(f"  [FAIL] {what}：--json 输出不是合法 JSON：{exc}\n{raw}")
    sys.exit(1)
bad = []
if env.get("exit_code") != 2:
    bad.append(f'exit_code={env.get("exit_code")!r}，应为 2（校验失败、零写入）')
if env.get("status") != "failed":
    bad.append(f'status={env.get("status")!r}，应为 "failed"（零写入零 commit 没有「部分」语义）')
if env.get("ok") is not False:
    bad.append(f'ok={env.get("ok")!r}，应为 false')
errs = (env.get("data") or {}).get("errors") or []
e5 = [e for e in errs if e.get("code") == "E5" and e.get("level") == "error"]
if not e5:
    bad.append(f'data.errors[] 应含 code="E5" 的 error 级条目（plan 级编号不得为空），实得 '
               f'{[(e.get("code"), e.get("level")) for e in errs]!r}')
elif not any(e.get("path", "").startswith("ops[") for e in e5):
    bad.append(f'E5 条目应带 ops[i] 字段路径，实得 {[e.get("path") for e in e5]!r}')
# C 矛盾措辞：校验阶段拦下 → 事务没开始，不许出现写入失败 / 事务放弃 / 其余 op 照常执行
forbidden = ["其余 op 照常执行", "原子事务已放弃", "写入 domains/"]
for frag in forbidden:
    hits = [d.get("message", "") for d in errs if frag in d.get("message", "")]
    hits += [d.get("message", "") for d in (env.get("warnings") or []) if frag in d.get("message", "")]
    if hits:
        bad.append(f'诊断仍含与 M6 原子事务冲突的历史措辞 {frag!r}：{hits!r}')
# D 同一条 message 不得同时以 error 与 warning 各登记一次
emsg = {d.get("message") for d in errs if d.get("level") == "error"}
wmsg = {d.get("message") for d in (env.get("warnings") or []) if d.get("level") == "warning"}
dup = emsg & wmsg
if dup:
    bad.append(f"同一条诊断被 error / warning 重复登记：{sorted(dup)!r}")
if bad:
    print(f"  [FAIL] {what}：" + "；".join(bad))
    sys.exit(1)
PY
}

seed_card() {
  cat >"${VAULT}/${KDIR_REL}/$1.md" <<CARD_EOF
---
id: $1
title: 卡 $1
status: active
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
tags: [fix]
sources: []
---

# 卡 $1

## 知识内容

自环判据用的正文一行。

## 解释与依据

依据占位。

## 条件与边界

仅本判据使用。

## 用户补充

用户自己写的一行。

## 理解自检

- [ ] 能说出自环归哪个码？
CARD_EOF
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译 eg 失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. 建库 + 语料
step "建库 + 两张 active 卡"
mkdir -p "${VAULT}"
[ "$(eg_code init --domain tech)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain tech)" = "0" ] || die "config set 失败"
mkdir -p "${VAULT}/${KDIR_REL}"
seed_card "${CARD_A}"
seed_card "${CARD_B}"
gitv add -A >/dev/null
gitv -c user.name=eg -c user.email=eg@example.com commit -q -m "seed: I-…-010 判据语料"
[ -z "$(gitv status --porcelain)" ] || die "前置条件：工作区必须干净"
BASE_COMMITS="$(commits)"
BASE_TREE="$(tree_sha)"
ok "库就绪（commit=${BASE_COMMITS}）"

# ---------------------------------------------------------------- 2. A/B/C/D 自环主判据
step "A~D eg rel add A supports A → 退 2 + failed + E5 + 无矛盾措辞 + 不重复登记"
C="$(eg_code rel add "${CARD_A}" supports "${CARD_A}" --reason '判据：自环' --json)"
note_code "${C}"
[ "${C}" = "2" ] || { cat "${WORK}/out.txt"; die "自环必须退 2（校验失败、零写入），实得 ${C}"; }
assert_validation_envelope "eg rel add 自环"
[ "$(tree_sha)" = "${BASE_TREE}" ] || die "自环被拦下时权威字节一个都不许动"
assert_no_derived_index
[ "$(commits)" = "${BASE_COMMITS}" ] || die "自环必须零 commit"
grep -q '^relations:' "${VAULT}/${REL_A}" && die "自环不得写出 relations 键"
ok "自环：2 / failed / E5(ops[0]) / 零写入零 commit / 无「其余 op 照常执行」等矛盾措辞"

# ---------------------------------------------------------------- 3. G 四类关系类型全覆盖
step "G 封闭四值关系类型的自环一律 2（含走方向规范化分支的 opposing）"
for T in supports opposing derives limits; do
  C="$(eg_code rel add "${CARD_A}" "${T}" "${CARD_A}" --reason "判据：${T} 自环" --json)"
  note_code "${C}"
  [ "${C}" = "2" ] || { cat "${WORK}/out.txt"; die "${T} 自环必须退 2，实得 ${C}"; }
  assert_validation_envelope "eg rel add ${T} 自环"
done
[ "$(tree_sha)" = "${BASE_TREE}" ] || die "四类自环全程零权威字节变化"
assert_no_derived_index
ok "supports / opposing / derives / limits 自环全部 2（opposing 规范化后两端仍相同，同样在校验阶段拦下）"

# ---------------------------------------------------------------- 4. E 对照组与闭集
step "E 对照组：E2 悬空两侧 = 2；type 越界 = 1；合法 = 0；重复 = 0（幂等）"
C="$(eg_code rel add "${CARD_A}" supports "${GHOST}" --reason '判据：悬空 target' --json)"
note_code "${C}"
[ "${C}" = "2" ] || { cat "${WORK}/out.txt"; die "悬空 target 应退 2（E2），实得 ${C}"; }
C="$(eg_code rel add "${GHOST}" supports "${CARD_A}" --reason '判据：悬空 from' --json)"
note_code "${C}"
[ "${C}" = "2" ] || die "悬空 from 应退 2（E2），实得 ${C}"
C="$(eg_code rel add "${CARD_A}" badtype "${CARD_B}" --reason '判据：type 越界' --json)"
note_code "${C}"
[ "${C}" = "1" ] || die "type 越界应退 1（参数非法），实得 ${C}"
[ "$(tree_sha)" = "${BASE_TREE}" ] || die "上述失败路径全程零字节变化"
C="$(eg_code rel add "${CARD_A}" supports "${CARD_B}" --reason '判据：合法关系' --json)"
note_code "${C}"
[ "${C}" = "0" ] || { cat "${WORK}/out.txt"; die "合法关系应退 0，实得 ${C}"; }
grep -q '^relations:' "${VAULT}/${REL_A}" || die "合法关系必须真写入 relations[]"
AFTER_LEGAL_TREE="$(tree_sha)"
AFTER_LEGAL_COMMITS="$(commits)"
# 幂等判据必须**逐字同 reason**：条目全等标识是 from|type|target|reason（M5 §7.4 第 ④ 级
# 排序键），换 reason 就是另一条合法关系，不属重复。
C="$(eg_code rel add "${CARD_A}" supports "${CARD_B}" --reason '判据：合法关系' --json)"
note_code "${C}"
[ "${C}" = "0" ] || die "重复添加同一关系应幂等退 0，实得 ${C}"
[ "$(tree_sha)" = "${AFTER_LEGAL_TREE}" ] || die "幂等重复不得改权威字节"
[ "$(commits)" = "${AFTER_LEGAL_COMMITS}" ] || die "幂等重复不得产生新 commit"
ok "对照组一致：悬空 2 / 越界 1 / 合法 0 / 重复 0（同族「端点非法」统一为 2）"

# ---------------------------------------------------------------- 5. F Agent 路径纵深
step "F eg apply 里的 add_relation 自环同样退 2、零写入零 commit"
BEFORE_TREE="$(tree_sha)"
BEFORE_COMMITS="$(commits)"
cat >"${WORK}/loop.json" <<PLAN
{ "plan_version": 1, "verb": "relate", "domain": "tech",
  "reason": "判据：Agent 路径的自环同样在校验阶段拦下（零写入零 commit）",
  "requirement_ids": ["EG-REL-01"],
  "base": { "${REL_A}": "$(chash "${REL_A}")" },
  "ops": [ { "op": "add_relation", "from": "${CARD_A}", "type": "derives",
             "target": "${CARD_A}", "reason": "判据：自环" } ] }
PLAN
C="$(eg_code apply --plan "${WORK}/loop.json" --json)"
note_code "${C}"
[ "${C}" = "2" ] || { cat "${WORK}/out.txt"; die "apply 自环应退 2，实得 ${C}"; }
assert_validation_envelope "eg apply add_relation 自环"
[ "$(tree_sha)" = "${BEFORE_TREE}" ] || die "apply 自环必须零字节变化"
[ "$(commits)" = "${BEFORE_COMMITS}" ] || die "apply 自环必须零 commit"
ok "Agent 路径与用户显式路径同判：2 / failed / 零写入零 commit"

# ---------------------------------------------------------------- 6. 闭集与收口
step "闭集：本 suite 观测到的 rel add / apply 退出码集合恰 {0,1,2}"
UNIQ="$(printf '%s\n' ${CODES_SEEN} | sort -u | tr '\n' ' ' | sed 's/ *$//')"
[ "${UNIQ}" = "0 1 2" ] || die "关系写入路径的观测码应恰 {0,1,2}（不得出现 3/partial），实得 {${UNIQ}}"
[ -z "$(gitv status --porcelain)" ] || { gitv status --porcelain; die "收口：工作区必须干净"; }
ok "闭集成立：{${UNIQ}}，退出码 3 不再被语义校验失败挪用"

printf '\n===== C2 · I-…-010 判据全部通过（%d 项断言）=====\n' "${PASS}"
