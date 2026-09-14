#!/usr/bin/env bash
# C2 · I-…-016：诊断按 `level` 严格分桶 —— warning / info 只进信封 `warnings[]`，
# `data.errors[]` 只留 error 级（T-evergreen.system_assurance-158614-014）。
#
# 判据来源（都是**冻结**声明面，本 suite 只回读，不新造语义）：
#   · `2026-09-01-eg-cli-contract.md` §3 信封表：`warnings[]` = 「**warning / info 级**诊断条目
#     （结构见 §5）；无则空数组」；同 §3 末句：「错误（**error 级**）条目放在 `data.errors[]`」。
#   · `skill/SKILL.md` §11.x：「拿不到锁时按真实墙钟退避重试，每次退避留痕 `W28`
#     （`level=warning`，**不改退出码**）」+「看到 `W28` 如实报『正在等待并发写者释放锁』，
#     **不**当成错误上报」。
#
# 为什么需要这支 suite：D2/D3 实测，`run.lock` 被并发写者持有时，退避重试的 N 条
# `W28`（`level: "warning"`）与终态 `E16`（`level: "error"`）**一起**被塞进 `data.errors[]`，
# 而信封 `warnings[]` **恒为空数组**。后果：一次正常的锁竞争在监控里放大成 N+1 条 error、
# `errors[0]` 不再是根因、照 SKILL 实现的 Agent 必然违反「W28 不当错误上报」这一条。
# 既有 `precheck_exit5.sh` 只覆盖 E15 面，`run_lock.sh` 读 txnctl 标记而不读信封分桶，
# 因此这一面能长期存活。
#
# 本 suite 锁死的判据（真实两进程持锁 + 真实二进制；事实只回读 `--json` 信封与盘面）：
#   A **全域构造性判据**：遍历一批覆盖读 / 写 / 只读诊断 / 失败 / 成功的命令，任一退出码下
#     `data.errors[]` 里**不存在** `level != "error"` 的条目（这是「桶按 level 分」的本体，
#     不是逐路径白名单）。
#   B **锁忙路径（E16 面，与 precheck_exit5 的 E15 面并列）**：外部进程真实持有 `run.lock` 时，
#     A 类写命令与 B 类索引维护命令均须：退 `5` + `status=failed` + 信封 `warnings[]`
#     **至少 1 条 `W28`** + `data.errors[]` **恰 1 条且为 `E16`** + `errors[0]` 就是根因
#     + 权威 Markdown 零改动 + 零 commit。
#   C **不搬错方向**：error 级条目一条都不许漏进 `warnings[]`（分桶是双向的）。
#   D **info 级同样属 warnings 桶**：`eg check` 的 `I1` 恒在 `warnings[]`、不进 `data.errors[]`。
#   E **成功路径的 warning 不受影响**：`capture` 省略 `--domain` 的未编号 warning 仍在 `warnings[]`。
#
# 刻意**不**断言：W28 的条数（取决于真实墙钟退避次数）、等待耗时、`E16` 的中文文案。
#
# 约束：离线、零交互、可重复；一切写只发生在 mktemp -d 沙箱内，真实仓工作区零改动（末尾自查）。
# 用法：cd evergreen && bash tests/e2e/cli-core/c2_warning_bucket_split.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-c2-bucket.XXXXXX")"
EG="${WORK}/eg"
TXN="${WORK}/txnctl"
BASEV="${WORK}/base"
CARD_A="domains/tech/knowledge/k-20260913-bucket-a.md"
HOLD_PID=""

STEP=0
PASS=0
cleanup() { [ -n "${HOLD_PID}" ] && kill -9 "${HOLD_PID}" 2>/dev/null || true; rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# egv <vault> <args...>：跑 eg --json，stdout 落 out.txt，退出码留在 LAST_RC（不吞错、也不当场判）。
# 刻意不写 `|| true`：真仓 e2e 卫生判据 H4 禁止用它吞掉被测命令的退出码。
LAST_RC=0
egv() {
  local v="$1"; shift
  LAST_RC=0
  "${EG}" --vault "${v}" "$@" --json >"${WORK}/out.txt" 2>"${WORK}/err.txt" || LAST_RC=$?
}
# codev <vault> <args...>：跑 eg --json 并回显退出码。
codev() {
  local v="$1" rc=0; shift
  "${EG}" --vault "${v}" "$@" --json >"${WORK}/out.txt" 2>"${WORK}/err.txt" || rc=$?
  printf '%s' "${rc}"
}
gitv() { local v="$1"; shift; git -C "${v}" "$@"; }
commits() { gitv "$1" rev-list --count HEAD 2>/dev/null || echo 0; }
sha() { sha256sum "$1" 2>/dev/null | cut -d' ' -f1; }

# check_buckets <文件> <标签>：A/C/D 的公共判据 —— 两个桶按 level 严格分流。
check_buckets() {
  python3 - "$1" "$2" <<'PY' || exit 1
import json, sys
path, label = sys.argv[1], sys.argv[2]
raw = open(path, encoding="utf-8").read()
dec, env = json.JSONDecoder(), None
for i, ch in enumerate(raw):
    if ch == "{":
        try:
            env, _ = dec.raw_decode(raw[i:]); break
        except ValueError:
            continue
if env is None:
    print(f"  [FAIL] {label}：输出不是 JSON 信封"); sys.exit(1)
data = env.get("data") if isinstance(env.get("data"), dict) else {}
errs = data.get("errors") or []
warns = env.get("warnings")
bad = []
# 信封 warnings[] 必须始终在场且是数组（合同 §3：无则空数组）
if not isinstance(warns, list):
    bad.append(f"信封 warnings 不是数组：{warns!r}")
    warns = []
# A：data.errors[] 只许 error 级
for i, d in enumerate(errs):
    lv = (d or {}).get("level")
    if lv != "error":
        bad.append(f"data.errors[{i}] level={lv!r} 不是 error"
                   f"（code={d.get('code')!r} message={(d.get('message') or '')[:70]!r}）"
                   "—— warning / info 的家是信封 warnings[]（合同 §3）")
# C：反方向也不许串桶
for i, d in enumerate(warns):
    if (d or {}).get("level") == "error":
        bad.append(f"warnings[{i}] 是 error 级（code={d.get('code')!r}）——error 的家是 data.errors[]")
if bad:
    print(f"  [FAIL] {label}：" + "；".join(bad)); sys.exit(1)
print(f'  · {label}：exit_code={env.get("exit_code")} status={env.get("status")} '
      f'errors={len(errs)} 条（全 error 级）warnings={len(warns)} 条 '
      f'levels={sorted({(w or {}).get("level") for w in warns})}')
PY
}

acquire_lock() {
  local v="$1" rdy="${WORK}/lockrdy.$$"
  rm -f "${rdy}"
  "${TXN}" lock-hold --vault "${v}" --hold-ms 60000 --ready "${rdy}" \
    --argv "txnctl lock-hold(C2 I-016 suite)" >"${WORK}/hold.log" 2>&1 &
  HOLD_PID=$!
  local i
  for i in $(seq 1 120); do [ -f "${rdy}" ] && break; sleep 0.05; done
  [ -f "${rdy}" ] || die "外部持锁者未就绪（见 ${WORK}/hold.log）"
}
release_lock() {
  [ -n "${HOLD_PID}" ] && kill -9 "${HOLD_PID}" 2>/dev/null || true
  HOLD_PID=""
  sleep 0.2
}
fresh() { local v="${WORK}/$1"; rm -rf "${v}"; cp -a "${BASEV}" "${v}"; echo "${v}"; }

seed_card() {
  local v="$1" rel="$2" id="$3"
  mkdir -p "$(dirname "${v}/${rel}")"
  cat >"${v}/${rel}" <<CARD_EOF
---
id: ${id}
title: 分桶判据语料 ${id}
status: active
created_at: '2026-09-01'
updated_at: '2026-09-12T10:00:00+08:00'
tags: [fix]
sources: []
---

# 分桶判据语料 ${id}

## 知识内容

诊断分桶判据用的正文一行。

## 解释与依据

依据占位。

## 条件与边界

仅本判据使用。

## 用户补充

用户自己写的一行。

## 理解自检

- [ ] 能说出 warning 的家在哪个桶？
CARD_EOF
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg 与 txnctl（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译 eg 失败"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${TXN}" ./tests/lib/txnctl) || die "编译 txnctl 失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. seed
step "seed：init + config set + 一份原文 + 两张卡 + 派生索引（.index/ 与 run.lock 在场）"
mkdir -p "${BASEV}"
[ "$(codev "${BASEV}" init --domain tech)" = "0" ] || { cat "${WORK}/out.txt"; die "eg init 失败"; }
[ "$(codev "${BASEV}" config set default_domain tech)" = "0" ] || die "config set 失败"
BODY='I-…-016 判据语料：诊断桶按 level 分流是信封合同，不是实现细节。'
printf '%s%s%s%s\n' "${BODY}" "${BODY}" "${BODY}" "${BODY}" >"${WORK}/art.md"
: >"${WORK}/empty.md"
[ "$(codev "${BASEV}" capture --url https://example.com/c2bucket --title 'C2 bucket seed' \
      --reason 分桶判据 --body-file "${WORK}/art.md")" = "0" ] || { cat "${WORK}/out.txt"; die "capture 失败"; }
seed_card "${BASEV}" "${CARD_A}" "k-20260913-bucket-a"
seed_card "${BASEV}" "domains/tech/knowledge/k-20260913-bucket-b.md" "k-20260913-bucket-b"
gitv "${BASEV}" add -A >/dev/null
gitv "${BASEV}" -c user.name=eg -c user.email=eg@example.com commit -q -m "seed: I-…-016 分桶判据语料"
[ "$(codev "${BASEV}" index build)" = "0" ] || { cat "${WORK}/out.txt"; die "index build 失败"; }
[ -f "${BASEV}/.index/run.lock" ] || die "index build 后应存在 .index/run.lock"
ok "库就绪（commit=$(commits "${BASEV}")，run.lock 在场）"

# ---------------------------------------------------------------- 2. A 段：全域分桶
step "A 全域构造性判据：任一命令 / 任一退出码，data.errors[] 里零 non-error 条目"
V="$(fresh a)"
A_CASES=(
  "search 判据"
  "search 判据 --limit -1"
  "search ''"
  "card show k-20260913-bucket-a"
  "card show k-nope"
  "rel k-20260913-bucket-a"
  "unreviewed"
  "unreviewed --since 2026-13-45"
  "index status"
  "index status --strict"
  "check"
  "report --last"
  "config get default_domain"
  "config set nope 1"
  "mark-reviewed --target k-nope --user-request"
  "rel add k-20260913-bucket-a supports k-nope --reason 判据 --user-request"
  "rel add k-20260913-bucket-a supports k-20260913-bucket-a --reason 判据 --user-request"
  "delete --target k-20260913-bucket-a --reason r --confirm"
  "proposal approve p-20260913-001 --user-request"
  "mark-reviewed --target k-20260913-bucket-a --user-request"
  "reconcile --user-request"
)
for c in "${A_CASES[@]}"; do
  # shellcheck disable=SC2086
  eval "egv \"\${V}\" ${c}"
  check_buckets "${WORK}/out.txt" "eg ${c}"
done
ok "A 段 ${#A_CASES[@]} 条路径：两个桶按 level 严格分流（含退 0 / 1 / 2 / 3 路径）"

# ---------------------------------------------------------------- 3. D 段：info 级
step "D info 级（I1）同属 warnings 桶：eg check 的 I1 恒在信封 warnings[]"
egv "${V}" check
python3 - "${WORK}/out.txt" <<'PY' || exit 1
import json, sys
env = json.load(open(sys.argv[1], encoding="utf-8"))
warns = env.get("warnings") or []
infos = [w for w in warns if w.get("level") == "info"]
errs = ((env.get("data") or {}).get("errors")) or []
bad = []
if not infos:
    bad.append(f"eg check 应在信封 warnings[] 给出 info 级条目，实得 levels="
               f"{sorted({w.get('level') for w in warns})}")
if any((d or {}).get("code") == "I1" for d in errs):
    bad.append("I1 落进了 data.errors[]")
if bad:
    print("  [FAIL] " + "；".join(bad)); sys.exit(1)
print(f'  · eg check：warnings[] 含 {len(infos)} 条 info（codes='
      f'{sorted({w.get("code") for w in infos})}），data.errors[] 无 I1')
PY
ok "info 级条目在 warnings 桶（合同 §3 把 warning / info 并列写在同一桶）"

# ---------------------------------------------------------------- 4. E 段：成功路径 warning
step "E 成功路径的未编号 warning 仍在 warnings[]（分桶修复不得顺手改动成功面）"
# 用**独立干净库**取材：A 段已在同一库上跑过写命令（mark-reviewed / reconcile / 失败的 rel add），
# 成功面的取材不该受前序写状态影响 —— 判据本体是「成功路径的 warning 仍在 warnings 桶」。
V="$(fresh e)"
egv "${V}" capture --url https://example.com/c2bucket2 --title 'C2 bucket second' \
  --reason 分桶判据 --body-file "${WORK}/art.md"
python3 - "${WORK}/out.txt" <<'PY' || exit 1
import json, sys
env = json.load(open(sys.argv[1], encoding="utf-8"))
warns = env.get("warnings") or []
if env.get("exit_code") != 0:
    print(f'  [FAIL] capture 成功路径应退 0，实得 {env.get("exit_code")}；'
          f'errors={json.dumps((env.get("data") or {}).get("errors"), ensure_ascii=False)}'); sys.exit(1)
if not any(w.get("level") == "warning" for w in warns):
    print(f"  [FAIL] capture 省略 --domain 应在 warnings[] 留 default_domain 落位提醒，实得 {warns!r}")
    sys.exit(1)
print(f'  · capture：exit_code=0，warnings[] {len(warns)} 条（含未编号 warning，code 位为空是合同 §5 授权）')
PY
ok "成功路径 warning 面未受影响"

# ---------------------------------------------------------------- 5. B 段：锁忙（E16 面）
step "B 锁忙路径（E16 面，与 precheck_exit5 的 E15 面并列）：W28 进 warnings[]、errors[] 恰一条 E16"
LOCK_CASES=(
  "A|mark-reviewed --target k-20260913-bucket-a --user-request"
  "A|rel add k-20260913-bucket-a supports k-20260913-bucket-b --reason 判据 --user-request"
  "A|edit --target k-20260913-bucket-a --section 用户补充 --content 锁忙判据 --user-request"
  "A|deprecate --target k-20260913-bucket-a --reason 锁忙判据 --user-request"
  "B|index sync"
  "B|index rebuild"
)
for entry in "${LOCK_CASES[@]}"; do
  kind="${entry%%|*}"; cmdline="${entry#*|}"
  V="$(fresh "lock_$(printf '%s' "${cmdline}" | tr -c 'a-z0-9' '_' | cut -c1-24)")"
  PRE_A="$(sha "${V}/${CARD_A}")"
  N0="$(commits "${V}")"
  acquire_lock "${V}"
  # shellcheck disable=SC2086
  RC="$(EG_LOCK_TIMEOUT_MS=800 codev "${V}" ${cmdline})"
  release_lock
  cp "${WORK}/out.txt" "${WORK}/lock.json"
  [ "${RC}" = "5" ] || { cat "${WORK}/lock.json"; die "锁忙时 ${kind} 类 eg ${cmdline} 应退 5，实得 ${RC}"; }
  python3 - "${WORK}/lock.json" "${kind}|eg ${cmdline}" <<'PY' || exit 1
import json, sys
env = json.load(open(sys.argv[1], encoding="utf-8"))
label = sys.argv[2]
data = env.get("data") if isinstance(env.get("data"), dict) else {}
errs = data.get("errors") or []
warns = env.get("warnings") or []
bad = []
if env.get("status") != "failed":
    bad.append(f'status 应为 failed，实得 {env.get("status")!r}')
w28 = [w for w in warns if w.get("code") == "W28"]
if not w28:
    bad.append("信封 warnings[] 应至少 1 条 W28（锁等待留痕），实得 codes="
               f'{[w.get("code") for w in warns]}')
if any(w.get("level") != "warning" for w in w28):
    bad.append("W28 的 level 必须是 warning")
if len(errs) != 1:
    bad.append(f'data.errors[] 应恰 1 条（终态 E16），实得 {len(errs)} 条：'
               f'{[(d.get("code"), d.get("level")) for d in errs]}')
elif errs[0].get("code") != "E16" or errs[0].get("level") != "error":
    bad.append(f'errors[0] 应为 E16/error，实得 {(errs[0].get("code"), errs[0].get("level"))}')
if any(d.get("code") == "W28" for d in errs):
    bad.append("W28 仍在 data.errors[] 里（SKILL 逐字要求不得把 W28 当错误上报）")
if bad:
    print(f"  [FAIL] {label}：" + "；".join(bad)); sys.exit(1)
print(f'  · {label}：exit_code=5 status=failed，warnings[] {len(w28)} 条 W28，'
      f'errors[] 恰 1 条 E16（errors[0] 即根因）')
PY
  [ "$(sha "${V}/${CARD_A}")" = "${PRE_A}" ] || die "锁忙路径必须零字节落盘：${cmdline}"
  [ "$(commits "${V}")" = "${N0}" ] || die "锁忙路径必须零 commit：${cmdline}"
  check_buckets "${WORK}/lock.json" "${kind}|eg ${cmdline}（分桶复核）"
done
ok "锁忙 ${#LOCK_CASES[@]} 条路径（4 A 类 + 2 B 类）：W28 在 warnings 桶、errors 恰一条 E16、零写入零 commit"

# ---------------------------------------------------------------- 6. 收口
step "收口：真实仓工作区零改动"
[ "$(git -C "${REPO_ROOT}" status --porcelain | sort)" = "${BEFORE_REPO_STATUS}" ] ||
  die "真实仓工作区被污染"
ok "真实仓状态逐字未变"

printf '\n===== C2 · I-…-016 判据全部通过（%d 项断言）=====\n' "${PASS}"
