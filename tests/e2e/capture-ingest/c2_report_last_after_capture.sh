#!/usr/bin/env bash
# C2 · I-…-018：`eg report --last` 的覆盖范围必须与声明面一致 —— 一次成功的 `eg capture`
# 之后 `report --last` 必须能复现该次报告，且 README / `--help` / SKILL 三面口径一致
# （T-evergreen.system_assurance-158614-014）。
#
# 判据来源（都是**冻结**声明面，本 suite 只回读，不新造语义）：
#   · `2026-09-01-eg-cli-contract.md` §1.9：`eg report --last` **只读**，
#     「输出最近一次 `apply` / `capture` 的最终报告（§4.6 S1 必填子集）」，退出码 `0` / `1`；
#   · `README.md` 命令总表：「只读复现最近一次 `apply` / `capture` 的最终报告」；
#   · `eg report --help` 参数行：「输出最近一次 apply / capture 的最终报告（§4.6 的 S1 必填子集）」。
#
# 为什么需要这支 suite：D2 实测，一次**成功**的 `eg capture` 不产生 `.eg/last-report.json`，
# 随后 `eg report --last` 退 `1`，提示语反过来要求用户「先执行一次 `eg apply`」——
#   ① 照 README / `--help` 写的调用直接失败（声明的能力在一半入口上不可用）；
#   ② 提示把用户引向错误动作（他刚刚成功执行的就是 capture，且只做素材入库时没有理由先 apply）；
#   ③ `skill/SKILL.md` 又写成「只读复现最近一次 **apply** 的报告」，三面互相打架 ——
#      同一能力两套口径，回归判据写哪边都能「通过」，漂移可以长期存活。
#
# 本 suite 锁死的判据（真实二进制；事实只回读 stdout / 退出码 / 盘面）：
#   A **capture 落最近一次报告**：一次成功的 `eg capture` 之后 `.eg/last-report.json` 在盘。
#   B **capture 后 report --last 退 0 且报告体可复现**：五键信封 + `data.report` 是 §4.6 的
#     S1 必填 11 键（禁键一个都不许出现）+ `source.id` / `source.path` 与该次 capture 的
#     `data.source_id` / `data.path` 逐字相同 + `git.commit` 与 vault `HEAD` 逐字相同。
#   C **复现文案不得指错命令**：capture 产出的记录，回放时不许自称「最近一次 eg apply」。
#   D **apply 面零回归**：apply 之后 `report --last` 复现的是该次 apply 的报告体（逐字相等）。
#   E **三面一致**：README 命令总表行、`eg report --help` 参数行、`skill/SKILL.md` 的
#     `report --last` 说明**都**必须覆盖 capture（任一面缺 capture 即失败）。
#   F **无历史报告的提示语不预设用户上一步做了什么**：全新 vault 上退 `1`，
#     `data.errors[].code` 落编号域（I-…-015 纪律），且提示不得只把用户引向 `eg apply`。
#   G **只读到底**：`report --last` 跑前后权威面 sha256、commit 数、`git status` 逐字不变。
#
# 刻意**不**断言：报告体里 capture 子集各字段的具体中文文案、`links[]` 的完整集合、
# `warnings[]` 条数（合同只冻结键面与「不得输出假数据」，措辞不是判据）。
#
# 约束：离线、零交互、可重复；一切写只发生在 mktemp -d 沙箱内，真实仓工作区零改动（末尾自查）。
# 用法：cd evergreen && bash tests/e2e/capture-ingest/c2_report_last_after_capture.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"

command -v python3 >/dev/null || { echo "[FAIL] 本脚本用 python3 做键级判定" >&2; exit 1; }

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-c2-reportlast.XXXXXX")"
EG="${WORK}/eg"
VAULT="${WORK}/vault"
FRESH="${WORK}/fresh"

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# run_eg <args...>：跑真实二进制，stdout → out.txt、stderr → err.txt，退出码留在 RC。
# 刻意不写 `|| true`（真仓 e2e 卫生 H4 禁止吞被测命令退出码）。
RC=0
run_eg() {
  RC=0
  "${EG}" "$@" >"${WORK}/out.txt" 2>"${WORK}/err.txt" || RC=$?
}

snapshot() {
  ( cd "$1" && git status --porcelain | sort && echo "--commits $(git rev-list --count HEAD 2>/dev/null || echo 0)"
    for d in domains sources proposals reviews; do
      [ -d "${d}" ] && find "${d}" -type f
    done | sort | xargs -r sha256sum ) || true
}

# ---------------------------------------------------------------- 0. 构建 + seed
step "构建 eg（CGO_ENABLED=0，与发布口径一致）并 init 两个 vault"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
mkdir -p "${VAULT}" "${FRESH}"
"${EG}" --vault "${VAULT}" init --domain tech >/dev/null 2>&1 || die "init 失败"
"${EG}" --vault "${VAULT}" config set default_domain tech >/dev/null 2>&1 || die "config set 失败"
"${EG}" --vault "${FRESH}" init --domain tech >/dev/null 2>&1 || die "init（fresh）失败"
"${EG}" --vault "${FRESH}" config set default_domain tech >/dev/null 2>&1 || die "config set（fresh）失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. A 段
step "A 一次成功的 eg capture 之后 .eg/last-report.json 必须在盘"
ART="${WORK}/art.md"
python3 - "${ART}" <<'PY'
import sys
open(sys.argv[1], "w", encoding="utf-8").write("D2 证据语料：退出码是对外合同。" * 8 + "\n")
PY
run_eg --vault "${VAULT}" capture --url https://example.com/ev-018 \
  --title 'D2 证据原文' --reason D2 --body-file "${ART}" --json
[ "${RC}" = "0" ] || { cat "${WORK}/out.txt" "${WORK}/err.txt"; die "eg capture 应退 0，实得 ${RC}"; }
cp "${WORK}/out.txt" "${WORK}/capture.json"
[ -f "${VAULT}/.eg/last-report.json" ] || {
  ls -a "${VAULT}/.eg" 2>/dev/null || true
  die "capture 成功却没有 .eg/last-report.json —— report --last 的真源缺失（合同 §1.9 覆盖 capture）"
}
ok "capture 成功即落最近一次报告记录"

# ---------------------------------------------------------------- 2. B 段
step "B capture 后 eg report --last --json：退 0 + 五键 + data.report 是 §4.6 S1 必填子集"
HEAD_SHA="$(git -C "${VAULT}" rev-parse HEAD)"
run_eg --vault "${VAULT}" report --last --json
[ "${RC}" = "0" ] || { cat "${WORK}/out.txt" "${WORK}/err.txt"; die "capture 后 eg report --last 应退 0，实得 ${RC}"; }
python3 - "${WORK}/out.txt" "${WORK}/capture.json" "${HEAD_SHA}" <<'PY' || die "B 段：报告体不符合 §4.6 S1 必填子集"
import json, sys

def envelope(path):
    raw = open(path, encoding="utf-8").read()
    dec = json.JSONDecoder()
    for i, ch in enumerate(raw):
        if ch == "{":
            try:
                obj, _ = dec.raw_decode(raw[i:])
            except ValueError:
                continue
            if isinstance(obj, dict):
                return obj
    return None

rep_env, cap_env, head = envelope(sys.argv[1]), envelope(sys.argv[2]), sys.argv[3]
bad = []
need = {"ok", "data", "warnings", "exit_code", "status"}
if rep_env is None:
    print("  [FAIL] report --last 的 stdout 不是 JSON 信封"); sys.exit(1)
if set(rep_env) != need:
    bad.append(f"信封键 {sorted(rep_env)} != 恰五键 {sorted(need)}")
if rep_env.get("exit_code") != 0 or rep_env.get("status") != "completed" or rep_env.get("ok") is not True:
    bad.append(f"ok/exit_code/status = {rep_env.get('ok')!r}/{rep_env.get('exit_code')!r}/{rep_env.get('status')!r}")
data = rep_env.get("data") if isinstance(rep_env.get("data"), dict) else {}
rep = data.get("report")
if not isinstance(rep, dict):
    print(f"  [FAIL] data.report 缺失或不是对象（实得 data 键 {sorted(data)}）—— capture 的报告体没被复现")
    sys.exit(1)
required = ["source", "note", "cards", "relations", "open_questions", "links",
            "git", "skipped", "default_domain_fallback", "high_impact", "warnings"]
missing = [k for k in required if k not in rep]
if missing:
    bad.append(f"§4.6 S1 必填缺键 {missing}")
forbidden = [k for k in ("commit", "written", "errors", "timestamps", "reviews") if k in rep]
if forbidden:
    bad.append(f"报告体出现禁键 {forbidden}")
cap = cap_env.get("data") if isinstance(cap_env, dict) else {}
src = rep.get("source") or {}
if src.get("id") != cap.get("source_id"):
    bad.append(f"report.source.id={src.get('id')!r} != capture 的 data.source_id={cap.get('source_id')!r}")
if src.get("path") != cap.get("path"):
    bad.append(f"report.source.path={src.get('path')!r} != capture 的 data.path={cap.get('path')!r}")
git = rep.get("git") or {}
if git.get("commit") != head:
    bad.append(f"report.git.commit={git.get('commit')!r} != vault HEAD {head!r}")
if bad:
    print("  [FAIL] " + "；".join(bad)); sys.exit(1)
print(f"  · data 键 {sorted(data)}；source.id={src.get('id')} git.commit={git.get('commit')[:7]}")
PY
ok "capture 的报告体按 §4.6 S1 必填子集原样复现（source / git.commit 与该次 capture 逐字一致）"

# ---------------------------------------------------------------- 3. C 段
step "C 复现文案不得指错命令：capture 的记录不许自称「最近一次 eg apply」"
run_eg --vault "${VAULT}" report --last
[ "${RC}" = "0" ] || die "capture 后 eg report --last（文本）应退 0，实得 ${RC}"
if grep -q "最近一次 eg apply" "${WORK}/out.txt" || grep -q "最近一次 apply" "${WORK}/out.txt"; then
  cat "${WORK}/out.txt"
  die "本次记录由 capture 产出，回放文案却自称 apply（报告只许陈述既成事实）"
fi
grep -q "capture" "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "回放文案未交代记录来自 capture（用户无从判断复现的是哪条命令）"; }
ok "回放文案如实指向 capture"

# ---------------------------------------------------------------- 4. D 段
step "D apply 面零回归：apply 之后复现的是该次 apply 的报告体（逐字相等）"
PLAN="${WORK}/plan.json"
SRC_ID="$(python3 - "${WORK}/capture.json" <<'PY'
import json, sys
raw = open(sys.argv[1], encoding="utf-8").read()
dec = json.JSONDecoder()
for i, ch in enumerate(raw):
    if ch == "{":
        try:
            obj, _ = dec.raw_decode(raw[i:])
        except ValueError:
            continue
        print((obj.get("data") or {}).get("source_id", "")); break
PY
)"
[ -n "${SRC_ID}" ] || die "取不到 capture 的 source_id"
cat >"${PLAN}" <<PLAN
{ "plan_version": 1, "verb": "process", "domain": "tech", "reason": "I-018 取证",
  "ops": [
    { "op": "write_note", "source": "${SRC_ID}", "note_id": "n-20260913-i018",
      "title": "I-018 材料笔记",
      "sections": { "材料提炼": "- report --last 的覆盖范围必须与声明面一致。\n" } }
  ]
}
PLAN
run_eg --vault "${VAULT}" apply --plan "${PLAN}" --json
[ "${RC}" = "0" ] || { cat "${WORK}/out.txt" "${WORK}/err.txt"; die "eg apply 应退 0，实得 ${RC}"; }
cp "${WORK}/out.txt" "${WORK}/apply.json"
run_eg --vault "${VAULT}" report --last --json
[ "${RC}" = "0" ] || die "apply 后 eg report --last 应退 0，实得 ${RC}"
python3 - "${WORK}/out.txt" "${WORK}/apply.json" <<'PY' || die "D 段：apply 的报告体未逐字复现"
import json, sys

def envelope(path):
    raw = open(path, encoding="utf-8").read()
    dec = json.JSONDecoder()
    for i, ch in enumerate(raw):
        if ch == "{":
            try:
                obj, _ = dec.raw_decode(raw[i:])
            except ValueError:
                continue
            if isinstance(obj, dict):
                return obj
    return None

rep, ap = envelope(sys.argv[1]), envelope(sys.argv[2])
a = json.dumps((ap.get("data") or {}).get("report"), sort_keys=True, ensure_ascii=False)
b = json.dumps((rep.get("data") or {}).get("report"), sort_keys=True, ensure_ascii=False)
if a != b:
    print(f"  [FAIL] apply 的报告体与 report --last 不一致\napply ：{a}\nreport：{b}"); sys.exit(1)
print("  · apply 报告体逐字复现")
PY
ok "apply 路径零回归（记录被最近一次写命令覆盖，复现仍逐字相等）"

# ---------------------------------------------------------------- 5. E 段
step "E 三面一致：README / eg report --help / skill/SKILL.md 都必须覆盖 capture"
E_FAIL=0
README_LINE="$(grep -n '`eg report --last`' "${REPO_ROOT}/README.md" | head -1 || true)"
[ -n "${README_LINE}" ] || die "README 命令总表里找不到 eg report --last"
printf '%s\n' "${README_LINE}" | grep -q "capture" || {
  printf '  README：%s\n' "${README_LINE}"; E_FAIL=1; }
run_eg report --help
printf '%s' "$(cat "${WORK}/out.txt")" | grep -q -- "--last" || die "eg report --help 未打印 --last 参数行"
grep -q "capture" "${WORK}/out.txt" || { sed -n '1,20p' "${WORK}/out.txt"; E_FAIL=1; }
SKILL_LINE="$(grep -n '^`eg report --last` 只读复现' "${REPO_ROOT}/skill/SKILL.md" | head -1 || true)"
[ -n "${SKILL_LINE}" ] || die "skill/SKILL.md 里找不到 report --last 的说明行"
printf '%s\n' "${SKILL_LINE}" | grep -q "capture" || {
  printf '  SKILL：%s\n' "${SKILL_LINE}"; E_FAIL=1; }
[ "${E_FAIL}" = "0" ] ||
  die "三面口径不一致：README / --help / SKILL 至少一面没有把 capture 写进 report --last 的覆盖范围"
ok "三面都把 capture 写进覆盖范围"

# ---------------------------------------------------------------- 6. F 段
step "F 无历史报告：退 1 + code 落编号域 + 提示不预设用户上一步做了什么"
[ ! -f "${FRESH}/.eg/last-report.json" ] || die "fresh vault 不该有历史报告"
run_eg --vault "${FRESH}" report --last --json
[ "${RC}" = "1" ] || die "无历史报告应退 1，实得 ${RC}"
python3 - "${WORK}/out.txt" <<'PY' || die "F 段：无历史报告的诊断不合规"
import json, re, sys
raw = open(sys.argv[1], encoding="utf-8").read()
dec = json.JSONDecoder()
env = None
for i, ch in enumerate(raw):
    if ch == "{":
        try:
            env, _ = dec.raw_decode(raw[i:]); break
        except ValueError:
            continue
if env is None:
    print("  [FAIL] stdout 不是 JSON 信封"); sys.exit(1)
bad = []
errs = ((env.get("data") or {}).get("errors")) or []
lvl_err = [d for d in errs if (d or {}).get("level") == "error"]
if not lvl_err:
    bad.append(f"data.errors[] 无 error 级条目（实得 {errs!r}）")
for i, d in enumerate(lvl_err):
    code = d.get("code") or ""
    if not re.fullmatch(r"(E|W|I|Q)\d+", code):
        bad.append(f"data.errors[{i}] code={code!r} 不在编号域 ^(E|W|I|Q)\\d+$（I-…-015）")
msg = " ".join((d.get("message") or "") for d in lvl_err)
if "先执行一次 eg apply" in msg:
    bad.append(f"提示预设用户上一步应当 apply：{msg!r}")
if "capture" not in msg:
    bad.append(f"提示未覆盖 capture 这一半入口：{msg!r}")
if bad:
    print("  [FAIL] " + "；".join(bad)); sys.exit(1)
print(f"  · code={[d.get('code') for d in lvl_err]}；提示同时覆盖两类写命令")
PY
ok "无历史报告的提示不再把用户单向引向 apply"

# ---------------------------------------------------------------- 7. G 段
step "G 只读到底：report --last 前后权威面 / commit 数 / git status 逐字不变"
BEFORE="$(snapshot "${VAULT}")"
run_eg --vault "${VAULT}" report --last --json
[ "${RC}" = "0" ] || die "report --last 应退 0，实得 ${RC}"
run_eg --vault "${VAULT}" report --last
[ "${RC}" = "0" ] || die "report --last（文本）应退 0，实得 ${RC}"
AFTER="$(snapshot "${VAULT}")"
[ "${BEFORE}" = "${AFTER}" ] || {
  diff <(printf '%s\n' "${BEFORE}") <(printf '%s\n' "${AFTER}") || true
  die "report --last 改动了权威面 / commit 数 / 工作区状态"
}
ok "report --last 零文件变化、零 commit"

# ---------------------------------------------------------------- 收口
step "收口：真实仓工作区未被本 suite 污染"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] ||
  die "真实仓工作区被改动（应只在沙箱内写）"
ok "真实仓工作区状态前后一致"

printf '\n===== C2 · I-…-018 判据全部通过（%d 项断言）=====\n' "${PASS}"
