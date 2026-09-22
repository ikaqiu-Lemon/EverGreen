#!/usr/bin/env bash
# C2 · I-…-017：`--json` 写在**子命令位**时，参数解析失败也必须输出五键信封
# （T-evergreen.system_assurance-158614-014）。
#
# 判据来源（都是**冻结**声明面，本 suite 只回读，不新造语义）：
#   · `2026-09-01-eg-cli-contract.md` §3：`ok` / `data` / `warnings[]` / `exit_code` / `status`
#     五键**必有**（表中「必有」列全为「是」），且 `exit_code` 与进程退出码逐字相同；
#     合同**没有**给出「参数解析失败时不输出信封」的例外。
#   · 同 §3 的 `status` 派生表把退 `1`（用法 / 参数非法，零写入）列为**有信封**的一档
#     （`status: "failed"`）—— 即退 `1` 本身就在信封覆盖范围内。
#   · 全部 `<cmd> --help` 的用法行把 `[--json]` 标在**子命令位**（如 `eg index build [--json]`、
#     `eg proposal show <p-id> [--json]`），构成声明面承诺：这个写法必须有效。
#
# 为什么需要这支 suite：D1/D2 实测，`eg <cmd> … --bogus --json` 在 flag 解析失败时回退成
# **人类可读文本**，信封五键全部消失；把同一个 `--json` 提到全局位（`eg --json <cmd> …`）
# 则信封正常。后果：调用方统一按「解析 stdout JSON → 读 exit_code / errors[].code」对接，
# 参数打错时解析器直接抛异常，无法把「我的调用姿势错了」与「库出问题了」区分开；而所有
# `--help` 示例都把 `--json` 写在子命令位，照抄示例的接入方正好落在失效分支上。
# 既有 d1_cli_surface B 段只覆盖**裸调用**（失败发生在 Validate 层，信封仍在），
# 因此「解析层失败」这一面能长期存活。
#
# 本 suite 锁死的判据（真实二进制；事实只回读 stdout / 退出码 / 盘面）：
#   A **23 条命令表面 × 子命令位 `--json`**（M5 22 + T-…-006 opinion 1）：注入一个未知 flag 后，每条都须
#     退 `1` + stdout 是**合法 JSON** + 恰五键 + `exit_code == 1`（与进程退出码逐字相同）
#     + `status == "failed"` + `ok == false` + `data.errors[]` 至少 1 条 `level=error`
#     且 `code` 非空并落编号域（I-…-015 纪律）。
#   B **反证：不许修成「恒输出 JSON」**：同一批命令去掉 `--json` 时 stdout **不得**是 JSON
#     信封（人类可读面保持原样）。
#   C **两个位置等价**：全局位与子命令位两种写法在 `ok` / `exit_code` / `status` 上逐字一致。
#   D **解析层其余三入口同样有信封**（同一条「五键必有」）：未知命令、缺必需子命令、
#     未知全局 flag —— 只要 argv 里出现 `--json`。
#   E **`--json=false` 仍是人类可读**：显式关掉不得被 argv 扫描误判成开启。
#   F **零副作用**：整段跑完权威面逐字不变、commit 数不变（解析失败恒零写入）。
#
# 刻意**不**断言：具体中文文案、诊断条数、stderr 形态（合同只冻结信封与退出码）。
#
# 约束：离线、零交互、可重复；一切写只发生在 mktemp -d 沙箱内，真实仓工作区零改动（末尾自查）。
# 用法：cd evergreen && bash tests/e2e/cli-core/c2_json_envelope_parse_failure.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"

command -v python3 >/dev/null || { echo "[FAIL] 本脚本用 python3 做信封键级判定" >&2; exit 1; }

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-c2-jsonenv.XXXXXX")"
EG="${WORK}/eg"
VAULT="${WORK}/vault"

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

# envelope_check <退出码> <标签>：五键 + exit_code 逐字相同 + status/ok + errors[] 编号纪律。
envelope_check() {
  python3 - "${WORK}/out.txt" "$1" "$2" <<'PY' || return 1
import json, re, sys
path, rc, label = sys.argv[1], int(sys.argv[2]), sys.argv[3]
raw = open(path, encoding="utf-8").read()
need = {"ok", "data", "warnings", "exit_code", "status"}
dec, env = json.JSONDecoder(), None
for i, ch in enumerate(raw):
    if ch == "{":
        try:
            env, _ = dec.raw_decode(raw[i:]); break
        except ValueError:
            continue
if env is None:
    head = raw.strip().splitlines()[:2]
    print(f"  [FAIL] {label}：stdout 不是 JSON 信封（合同 §3 五键必有；退 {rc} 属有信封一档）"
          f"，实得：{head}")
    sys.exit(1)
bad = []
missing = sorted(need - set(env))
extra = sorted(set(env) - need)
if missing:
    bad.append(f"缺键 {missing}")
if extra:
    bad.append(f"多键 {extra}（信封恰五键）")
if env.get("exit_code") != rc:
    bad.append(f"exit_code={env.get('exit_code')!r} 与进程退出码 {rc} 不一致（合同 §3 要求逐字相同）")
if env.get("status") != "failed":
    bad.append(f"status={env.get('status')!r}，退 {rc} 应派生为 failed")
if env.get("ok") is not False:
    bad.append(f"ok={env.get('ok')!r}，失败路径应为 false")
data = env.get("data") if isinstance(env.get("data"), dict) else {}
errs = data.get("errors") or []
lvl_err = [d for d in errs if (d or {}).get("level") == "error"]
if not lvl_err:
    bad.append(f"data.errors[] 无 error 级条目（实得 {errs!r}）—— 机读侧拿不到失败归因")
for i, d in enumerate(lvl_err):
    code = (d or {}).get("code") or ""
    if not code:
        bad.append(f"data.errors[{i}] code 为空（I-…-015：error 级 code 必须落编号域）")
    elif not re.fullmatch(r"(E|W|I|Q)\d+", code):
        bad.append(f"data.errors[{i}] code={code!r} 不在编号域 ^(E|W|I|Q)\\d+$")
if not isinstance(env.get("warnings"), list):
    bad.append(f"warnings 不是数组：{env.get('warnings')!r}")
if bad:
    print(f"  [FAIL] {label}：" + "；".join(bad)); sys.exit(1)
print(f'  · {label}：rc={rc} exit_code={env.get("exit_code")} status={env.get("status")} '
      f'errors={[d.get("code") for d in lvl_err]}')
PY
}

# is_envelope：stdout 是否为五键信封（0 = 是），供 B / E 段做反证。
is_envelope() {
  python3 - "${WORK}/out.txt" <<'PY'
import json, sys
raw = open(sys.argv[1], encoding="utf-8").read()
need = {"ok", "data", "warnings", "exit_code", "status"}
dec = json.JSONDecoder()
for i, ch in enumerate(raw):
    if ch == "{":
        try:
            obj, _ = dec.raw_decode(raw[i:])
        except ValueError:
            continue
        sys.exit(0 if isinstance(obj, dict) and need <= set(obj) else 1)
sys.exit(1)
PY
}

envelope_field() {
  python3 - "${WORK}/out.txt" "$1" <<'PY'
import json, sys
raw, key = open(sys.argv[1], encoding="utf-8").read(), sys.argv[2]
dec = json.JSONDecoder()
for i, ch in enumerate(raw):
    if ch == "{":
        try:
            obj, _ = dec.raw_decode(raw[i:]); print(json.dumps(obj.get(key))); sys.exit(0)
        except ValueError:
            continue
print("null")
PY
}

snapshot() {
  ( cd "${VAULT}" && git status --porcelain | sort && echo "--commits $(git rev-list --count HEAD 2>/dev/null || echo 0)"
    for d in domains sources proposals reviews; do
      [ -d "${d}" ] && find "${d}" -type f
    done | sort | xargs -r sha256sum ) || true
}

# ---------------------------------------------------------------- 0. 构建 + seed
step "构建 eg（CGO_ENABLED=0，与发布口径一致）并 init 一个 vault"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
mkdir -p "${VAULT}"
"${EG}" --vault "${VAULT}" init >/dev/null 2>&1 || die "init 失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# 命令表面（M5 基线 22 条 + opinion + Storage v3 两条 = 恰 25 条）；
# SubRequired 的几条（config / card / rel / proposal / index / opinion）连带一个合法子动词一起打。
CMDS=(init config capture context apply search card rel report deprecate restore
      replaced-by proposal delete undelete mark-reviewed unreviewed edit reconcile
      check index bench opinion materialize export)
# SUBS：命令 → 需要一起给出的子动词（其余为空）
sub_of() {
  case "$1" in
    config)   echo "get" ;;
    card)     echo "show" ;;
    rel)      echo "add" ;;
    proposal) echo "list" ;;
    index)    echo "status" ;;
    # opinion 用**只读的 show**：解析失败发生在 flag 解析层（早于 Validate），
    # 绝不触碰 validate/reject 写路径骨架，本 suite 只验解析层信封不缺键。
    opinion)  echo "show" ;;
    *)        echo "" ;;
  esac
}
[ "${#CMDS[@]}" = "25" ] || die "命令表面应恰 25 条，实得 ${#CMDS[@]}"

BEFORE_VAULT="$(snapshot)"

# ---------------------------------------------------------------- 1. A 段
step "A 25 条命令 × 子命令位 --json + 未知 flag：退 1 且五键信封在场"
A_FAIL=0
for c in "${CMDS[@]}"; do
  s="$(sub_of "${c}")"
  if [ -n "${s}" ]; then
    run_eg --vault "${VAULT}" "${c}" "${s}" --bogus-flag-xyz --json
  else
    run_eg --vault "${VAULT}" "${c}" --bogus-flag-xyz --json
  fi
  [ "${RC}" = "1" ] || { printf '%s\n' "$(cat "${WORK}/out.txt")" ; die "eg ${c} ${s} --bogus-flag-xyz --json 应退 1（用法/参数非法），实得 ${RC}"; }
  envelope_check "${RC}" "eg ${c} ${s} --bogus-flag-xyz --json" || A_FAIL=1
done
[ "${A_FAIL}" = "0" ] || die "A 段：子命令位 --json 在参数解析失败时丢了信封（合同 §3 五键必有）"
ok "25 条命令表面在解析失败时全部输出五键信封（退 1、status=failed、errors[] 带编号）"

# ---------------------------------------------------------------- 2. B 段
step "B 反证：去掉 --json 时不得输出 JSON 信封（不许修成恒 JSON）"
for c in "${CMDS[@]}"; do
  s="$(sub_of "${c}")"
  if [ -n "${s}" ]; then
    run_eg --vault "${VAULT}" "${c}" "${s}" --bogus-flag-xyz
  else
    run_eg --vault "${VAULT}" "${c}" --bogus-flag-xyz
  fi
  [ "${RC}" = "1" ] || die "eg ${c} ${s} --bogus-flag-xyz 应退 1，实得 ${RC}"
  if is_envelope; then
    cat "${WORK}/out.txt"
    die "eg ${c} ${s} 未带 --json 却输出了 JSON 信封 —— 修法错了（应按输出格式意图分流，不是恒 JSON）"
  fi
done
ok "25 条命令在不带 --json 时保持人类可读面（stdout 无信封）"

# ---------------------------------------------------------------- 3. C 段
step "C 两个位置等价：全局位与子命令位的信封在 ok/exit_code/status 上逐字一致"
for c in card search check; do
  s="$(sub_of "${c}")"
  run_eg --vault "${VAULT}" ${s:+} "${c}" ${s} --bogus-flag-xyz --json
  sub_rc="${RC}"; sub_ok="$(envelope_field ok)"; sub_ec="$(envelope_field exit_code)"; sub_st="$(envelope_field status)"
  run_eg --json --vault "${VAULT}" "${c}" ${s} --bogus-flag-xyz
  glb_rc="${RC}"; glb_ok="$(envelope_field ok)"; glb_ec="$(envelope_field exit_code)"; glb_st="$(envelope_field status)"
  [ "${sub_rc}" = "${glb_rc}" ] || die "eg ${c}：两种写法退出码不同（子命令位 ${sub_rc} vs 全局位 ${glb_rc}）"
  [ "${sub_ok}${sub_ec}${sub_st}" = "${glb_ok}${glb_ec}${glb_st}" ] ||
    die "eg ${c}：两种写法信封不同（子命令位 ok=${sub_ok} exit_code=${sub_ec} status=${sub_st} vs 全局位 ok=${glb_ok} exit_code=${glb_ec} status=${glb_st}）"
done
ok "--json 在全局位与子命令位对参数解析失败给出逐字一致的信封"

# ---------------------------------------------------------------- 4. D 段
step "D 解析层其余入口同样五键在场：未知命令 / 缺必需子命令 / 未知全局 flag"
run_eg --vault "${VAULT}" no-such-command --json
[ "${RC}" = "1" ] || die "未知命令应退 1，实得 ${RC}"
envelope_check "${RC}" "eg no-such-command --json（未知命令）" || die "D 段：未知命令丢了信封"
run_eg --vault "${VAULT}" rel --json
[ "${RC}" = "1" ] || die "缺必需子命令应退 1，实得 ${RC}"
envelope_check "${RC}" "eg rel --json（缺必需子命令）" || die "D 段：缺子命令丢了信封"
run_eg --vault "${VAULT}" --bogus-global-xyz --json
[ "${RC}" = "1" ] || die "未知全局 flag 应退 1，实得 ${RC}"
envelope_check "${RC}" "eg --bogus-global-xyz --json（未知全局 flag）" || die "D 段：未知全局 flag 丢了信封"
ok "未知命令 / 缺必需子命令 / 未知全局 flag 三个解析入口都输出五键信封"

# ---------------------------------------------------------------- 5. E 段
step "E --json=false 仍走人类可读（argv 扫描不得把显式关闭误判成开启）"
run_eg --vault "${VAULT}" search x --bogus-flag-xyz --json=false
[ "${RC}" = "1" ] || die "eg search --bogus --json=false 应退 1，实得 ${RC}"
if is_envelope; then
  cat "${WORK}/out.txt"
  die "--json=false 显式关闭，却仍输出了 JSON 信封"
fi
run_eg --vault "${VAULT}" search x --bogus-flag-xyz --json=true
[ "${RC}" = "1" ] || die "eg search --bogus --json=true 应退 1，实得 ${RC}"
envelope_check "${RC}" "eg search x --bogus --json=true" || die "E 段：--json=true 未出信封"
ok "--json=false / --json=true 两种显式取值都被如实尊重"

# ---------------------------------------------------------------- 6. F 段
step "F 零副作用：解析失败恒零写入（权威面与 commit 数逐字不变）"
AFTER_VAULT="$(snapshot)"
[ "${BEFORE_VAULT}" = "${AFTER_VAULT}" ] || {
  diff <(printf '%s\n' "${BEFORE_VAULT}") <(printf '%s\n' "${AFTER_VAULT}") || true
  die "解析失败路径改动了权威面 / commit 数"
}
ok "vault 权威面与 commit 数逐字不变（解析失败零写入）"

# ---------------------------------------------------------------- 收口
step "收口：真实仓工作区未被本 suite 污染"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] ||
  die "真实仓工作区被改动（应只在沙箱内写）"
ok "真实仓工作区状态前后一致"

printf '\n===== C2 · I-…-017 判据全部通过（%d 项断言）=====\n' "${PASS}"
