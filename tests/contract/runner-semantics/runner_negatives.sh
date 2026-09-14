#!/usr/bin/env bash
# 统一 runner 的**反证**门禁（system_assurance · T-…-006，合同 D4 / D5 / D6）。
#
# 被测对象是 tests/runner/run.py 本体。一个"测试的测试"只跑真绿毫无价值：必须证明
# runner 在下列每种情形下**真的会转红**，否则"全绿"只说明 runner 不会报错。
#
# 判据（每条都是一个反例，缺一不可）：
#   N1 失败传播三连：go 用例失败 / shell 非零退出 / 用例失败与真绿混跑
#      —— 单支 suite 的红必须传播到 summary.json（failed≥1、gate_a.pass=false）与进程退出码；
#   N2 零执行：go 包无测试文件、shell 退 0 但证据标记不足 min_cases —— 判红，不判绿；
#   N3 隐式 skip：go `t.Skip` 与 shell `[SKIP]` —— 判红（D6.2）；
#   N4 NOT_RUN 合同：required:true 带 not_run_when、以及 not_run_when 未绑 issue
#      —— 清单自检阶段就非零，不允许进入执行；
#   N5 清单完整性：重复 id、幽灵 target（登记不在盘）、go 包登记了却一个用例都没有 —— 非零；
#   N6 阶段聚合禁令：把 m*_acceptance.sh 登记成 suite —— 非零（D5 防阶段递归重跑）；
#   N7 可选 NOT_RUN 语义：无 sibling teamwork 时 contract-live-drift 单列 NOT_RUN、
#      不计 PASS 分子、退出 0；`--strict` 下升级为退出码 2；
#   N8 单仓 full 语义：full 计划里"允许 NOT_RUN"的 suite 恰好只有 contract-live-drift 一条；
#   N9 收尾标记合同（done_re）：历史脚本的收尾文案不在默认族里时必须能**按 suite 声明**，
#      且声明本身不是旁路 —— 声明与实际输出不符仍判红、非法正则在清单自检阶段就非零。
#
# 全部反例都在**工作树的只读快照副本**里注入（探针包、坏清单都只落副本），真实仓库
# 一个字节都不碰（末尾自查）。离线、零交互、可重复。
#
# 用法：cd evergreen && bash tests/contract/runner-semantics/runner_negatives.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
eg_require_disk 1024

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-runner-neg.XXXXXX")"
SB="${WORK}/sb"
STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# run_in_sb <清单相对路径> <profile> [额外参数...] → 回显退出码，输出落 out.txt
run_in_sb() {
  local suites="$1" profile="$2"; shift 2
  local c=0
  ( cd "${SB}" && bash tests/run.sh --profile "${profile}" --suites-file "${suites}" "$@" ) \
    >"${WORK}/out.txt" 2>&1 || c=$?
  echo "${c}"
}
# 断言 out.txt 命中某个诊断关键字
want_out() { grep -Fq "$1" "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "输出应含「$1」"; }; }

# ---------------------------------------------------------------- 1. 沙箱与探针
step "构建工作树只读副本，并注入 7 支探针 suite（3 go + 4 shell）"
eg_snapshot_worktree "${SB}"
[ -f "${SB}/tests/runner/run.py" ] || die "副本缺 tests/runner/run.py"
# 副本里建一个独立 git 索引：清单工具与 materializer 的"在盘"口径是工作树
# （`git ls-files -c -o --exclude-standard`，D12），没有 .git 就没法在副本内重算派生表。
# 只 init + add，不提交，不碰真实仓库的 .git。
git init -q "${SB}"
git -C "${SB}" add -A

# --- shell 探针：真绿 / 非零 / 零执行 / 隐式 skip
mkdir -p "${SB}/tests/_neg"
cat >"${SB}/tests/_neg/probe_pass.sh" <<'SH'
#!/usr/bin/env bash
set -Eeuo pipefail
echo "  [ok] 探针断言 1"
echo "  [ok] 探针断言 2"
echo "[PASS] 探针全部 2 组断言通过"
SH
cat >"${SB}/tests/_neg/probe_fail.sh" <<'SH'
#!/usr/bin/env bash
set -Eeuo pipefail
echo "  [ok] 前半段真的跑了"
echo "  [FAIL] 探针故意失败" >&2
exit 1
SH
cat >"${SB}/tests/_neg/probe_zero.sh" <<'SH'
#!/usr/bin/env bash
# 零执行探针：退出码 0，但一条断言都没跑（没有任何 [ok]），也没有收尾标记。
set -Eeuo pipefail
echo "什么都没做就返回了"
SH
cat >"${SB}/tests/_neg/probe_skip.sh" <<'SH'
#!/usr/bin/env bash
set -Eeuo pipefail
echo "  [ok] 断言 1"
echo "  [SKIP] 环境不满足，悄悄跳过剩下的断言"
echo "[PASS] 探针通过"
SH

# --- go 探针：用例失败 / t.Skip / 无测试文件
#     runner 约定：产品包在 <pkg>/，测试文件权威位在 tests/_staged/<pkg>/。
for p in negfail negskip negempty; do
  mkdir -p "${SB}/internal/${p}" "${SB}/tests/_staged/internal/${p}"
  printf 'package %s\n\n// 反例探针包（只存在于 runner 反证沙箱副本里）。\nfunc Probe() int { return 1 }\n' \
    "${p}" >"${SB}/internal/${p}/probe.go"
done
cat >"${SB}/tests/_staged/internal/negfail/probe_test.go" <<'GO'
package negfail

import "testing"

func TestProbeFails(t *testing.T) { t.Fatalf("探针故意失败") }
func TestProbePasses(t *testing.T) {
	if Probe() != 1 {
		t.Fatal("unreachable")
	}
}
GO
cat >"${SB}/tests/_staged/internal/negskip/probe_test.go" <<'GO'
package negskip

import "testing"

func TestProbeSkips(t *testing.T) { t.Skip("隐式跳过：D6.2 不允许") }
func TestProbeOK(t *testing.T) {
	if Probe() != 1 {
		t.Fatal("unreachable")
	}
}
GO
# negempty：测试文件在盘、`func Test…` 也在盘，但被 build tag 挡在编译面之外——
# go test 看到的是 `no test files`。这是"零执行"最真实的形态（清单静态扫描看得见用例，
# 工具链却一个都不跑），比"直接不放测试文件"更难被清单层拦住，正好用来验 runner
# 执行期的 `no test files` 判据。
cat >"${SB}/tests/_staged/internal/negempty/probe_test.go" <<'GO'
//go:build eg_never_built

package negempty

import "testing"

func TestProbeGhost(t *testing.T) {
	if Probe() != 1 {
		t.Fatal("unreachable")
	}
}
GO
# negempty2：连测试文件都没有 —— 用来验"清单层"就该拦住（min_cases 与盘面不一致）。
mkdir -p "${SB}/internal/negempty2" "${SB}/tests/_staged/internal/negempty2"
printf 'package negempty2

func Probe() int { return 1 }
' >"${SB}/internal/negempty2/probe.go"
# 探针是副本里的新增运行期资产：materializer 拒绝物化"未登记的 tests/ 资产"（D3.4-2 孤儿），
# 所以必须在副本内重算派生表，让探针被正式登记——这同时反过来证明了孤儿门禁真的在生效。
git -C "${SB}" add -A
( cd "${SB}" && python3 tests/tools/inventory.py --write >/dev/null ) ||
  die "副本内重算清单派生表失败"
ok "副本就绪；探针：probe_pass/fail/zero/skip + internal/{negfail,negskip,negempty}（已登记）"

# ---------------------------------------------------------------- 2. 生成探针清单
step "生成探针清单 tests/manifest/_neg_exec.yaml（只在副本内）"
NEG_EXEC='tests/manifest/_neg_exec.yaml'
python3 - "${SB}/${NEG_EXEC}" <<'PY'
import sys, pathlib

def suite(sid, kind, target, min_cases=1, extra=""):
    return f"""  - id: {sid}
    layer: contract
    capability: runner-semantics
    kind: {kind}
    target: {target}
    source: {target}
    origin: tool
    owner_task: T-evergreen.system_assurance-158614-006
    profiles: [contract]
    timeout_s: 600
    min_cases: {min_cases}
    required: true
    platform_required: linux/amd64
    not_run_when: null
    issue: null
{extra}"""

body = "".join([
    suite("neg.shell-pass", "shell", "tests/_neg/probe_pass.sh", 2),
    suite("neg.shell-fail", "shell", "tests/_neg/probe_fail.sh", 1),
    suite("neg.shell-zero", "shell", "tests/_neg/probe_zero.sh", 1),
    suite("neg.shell-skip", "shell", "tests/_neg/probe_skip.sh", 1),
    suite("neg.go-fail", "go", "./internal/negfail", 2),
    suite("neg.go-skip", "go", "./internal/negskip", 2),
    suite("neg.go-empty", "go", "./internal/negempty", 1),
])
pathlib.Path(sys.argv[1]).write_text("# 反例探针清单（沙箱副本专用，主干清单不含）。\nsuites:\n" + body)
PY
ok "探针清单写入副本（7 支：1 真绿 + 6 反例）"

# ---------------------------------------------------------------- 3. N1~N3 执行期反例
step "N1~N3：一次执行覆盖失败传播三连 / 零执行 / 隐式 skip"
C="$(run_in_sb "${NEG_EXEC}" contract)"
[ "${C}" != "0" ] || { cat "${WORK}/out.txt"; die "含 6 个反例的清单必须非零退出，实得 0（红没有传播）" ; }
want_out "PASS 1 / FAIL 6"
S="$(ls -d "${SB}"/tests/_report/*/ | tail -1)"
python3 - "${S}/summary.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
st = {s["id"]: s for s in d["suites"]}
exp = {"neg.shell-pass": "PASS", "neg.shell-fail": "FAIL", "neg.shell-zero": "FAIL",
       "neg.shell-skip": "FAIL", "neg.go-fail": "FAIL", "neg.go-skip": "FAIL",
       "neg.go-empty": "FAIL"}
for sid, want in exp.items():
    got = st[sid]["status"]
    assert got == want, f"{sid} 期望 {want} 实得 {got}：{st[sid]['diagnosis']}"
# 诊断必须点名根因，不能只说"失败了"
def has(sid, kw):
    assert kw in st[sid]["diagnosis"], f"{sid} 诊断应点名「{kw}」，实得：{st[sid]['diagnosis']}"
has("neg.go-fail", "用例失败")
has("neg.go-skip", "skip")
has("neg.go-empty", "no test files")
has("neg.shell-zero", "min_cases")
has("neg.shell-skip", "[SKIP]")
has("neg.shell-fail", "退出码")
# 红必须传播到汇总层，且真绿不被反例带红
assert d["failed"] == 6 and d["passed"] == 1, d
assert d["gate_a"]["pass"] is False, d["gate_a"]
# 每支 suite 的四个必填字段（status/duration/case_count/log_path）都在
for s in d["suites"]:
    for k in ("status", "duration", "duration_s", "case_count", "log_path"):
        assert k in s, (s["id"], k)
assert st["neg.shell-pass"]["case_count"] >= 2, st["neg.shell-pass"]
print("  [ok] summary.json：7 支状态逐条符合预期，诊断点名根因")
print("  [ok] 失败传播：failed=6 / passed=1 / gate_a.pass=false，退出码非 0")
PY
PASS=$((PASS + 2))
ok "N1 失败传播三连 + N2 零执行 + N3 隐式 skip：6 个反例全部判红"

# ---------------------------------------------------------------- 4. 真绿对照
step "真绿对照：只跑 probe_pass 必须退 0（排除\"runner 永远返回非零\"）"
python3 - "${SB}/${NEG_EXEC}" "${SB}/tests/manifest/_neg_green.yaml" <<'PY'
import sys, pathlib, re
src = pathlib.Path(sys.argv[1]).read_text()
blocks = re.split(r"(?=  - id: )", src)
head = blocks[0]
keep = [b for b in blocks[1:] if "id: neg.shell-pass" in b]
pathlib.Path(sys.argv[2]).write_text(head + "".join(keep))
PY
C="$(run_in_sb 'tests/manifest/_neg_green.yaml' contract)"
[ "${C}" = "0" ] || { cat "${WORK}/out.txt"; die "纯真绿清单必须退 0，实得 ${C}" ; }
want_out "PASS 1 / FAIL 0"
ok "真绿清单退 0：runner 的红是判据驱动的，不是恒红"

# ---------------------------------------------------------------- 5. N4~N6 清单期反例
step "N4~N6：坏清单必须在自检阶段非零（required+not_run_when / 未绑 issue / 重复 id / 幽灵 / 阶段聚合）"
bad_case() { # <名字> <python 生成脚本> <期望关键字>
  local name="$1" gen="$2" kw="$3" c=0
  python3 -c "${gen}" "${SB}/tests/manifest/_neg_bad.yaml" "${SB}"
  c="$(run_in_sb 'tests/manifest/_neg_bad.yaml' contract --plan-only)"
  [ "${c}" != "0" ] || { cat "${WORK}/out.txt"; die "${name}：坏清单必须非零退出"; }
  want_out "${kw}"
  ok "${name}：自检非零并点名（${kw}）"
}

GEN_BASE='
import sys, pathlib
tpl = """  - id: {sid}
    layer: contract
    capability: runner-semantics
    kind: shell
    target: {target}
    source: {target}
    origin: tool
    owner_task: T-evergreen.system_assurance-158614-006
    profiles: [contract]
    timeout_s: 600
    min_cases: 1
    required: {req}
    platform_required: linux/amd64
    not_run_when: {nrw}
    issue: {issue}
"""
def w(path, body): pathlib.Path(path).write_text("suites:\n" + body)
'

bad_case "N4a required:true 带 not_run_when" \
  "${GEN_BASE}"'
w(sys.argv[1], tpl.format(sid="neg.bad-required-notrun", target="tests/_neg/probe_pass.sh",
                          req="true", nrw="no_sibling_teamwork", issue="null"))
' "required: true"

bad_case "N4b not_run_when 未绑 issue" \
  "${GEN_BASE}"'
w(sys.argv[1], tpl.format(sid="neg.bad-notrun-noissue", target="tests/_neg/probe_pass.sh",
                          req="false", nrw="no_sibling_teamwork", issue="null"))
' "issue"

bad_case "N5a 重复 suite id" \
  "${GEN_BASE}"'
b = tpl.format(sid="neg.dup", target="tests/_neg/probe_pass.sh", req="true", nrw="null", issue="null")
w(sys.argv[1], b + b)
' "重复"

bad_case "N5b 幽灵 target（登记不在盘）" \
  "${GEN_BASE}"'
w(sys.argv[1], tpl.format(sid="neg.ghost", target="tests/_neg/does_not_exist.sh",
                          req="true", nrw="null", issue="null"))
' "不在盘"

bad_case "N5c go 包无用例却登记（min_cases 与盘面不一致）" \
  "${GEN_BASE}"'
import pathlib
b = tpl.format(sid="neg.go-nocase", target="./internal/negempty2", req="true", nrw="null", issue="null")
w(sys.argv[1], b.replace("kind: shell", "kind: go"))
' "min_cases"

bad_case "N6 阶段聚合脚本被登记（D5 防递归重跑）" \
  "${GEN_BASE}"'
import shutil, os
os.makedirs(sys.argv[2] + "/tests/_neg", exist_ok=True)
open(sys.argv[2] + "/tests/_neg/m5_acceptance.sh", "w").write("#!/usr/bin/env bash\necho stage\n")
w(sys.argv[1], tpl.format(sid="neg.stage-agg", target="tests/_neg/m5_acceptance.sh",
                          req="true", nrw="null", issue="null"))
' "阶段聚合"

# ---------------------------------------------------------------- 6. N7 可选 NOT_RUN 语义
step "N7：单仓（无 sibling teamwork）下 contract-live-drift 单列 NOT_RUN、退 0；--strict 退 2"
[ ! -d "${WORK}/teamwork" ] || die "沙箱父目录不该有 teamwork（本步前提是单仓）"
C="$(run_in_sb 'tests/manifest/suites.yaml' contract --only contract.contract-live-drift)"
[ "${C}" = "0" ] || { cat "${WORK}/out.txt"; die "可选 NOT_RUN 必须退 0，实得 ${C}"; }
want_out "NOT_RUN 单列"
S="$(ls -d "${SB}"/tests/_report/*/ | tail -1)"
python3 - "${S}/summary.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
s = d["suites"][0]
assert s["id"] == "contract.contract-live-drift" and s["status"] == "NOT_RUN", s
assert s["issue"], "NOT_RUN 必须绑 issue"
assert s["required"] is False, s
assert d["passed"] == 0 and d["not_run"] == 1, d          # 不计 PASS 分子
assert d["gate_a"]["pass"] is True, d["gate_a"]           # 可选 NOT_RUN 不破门禁
print(f"  [ok] NOT_RUN 单列且绑 {s['issue']}；passed=0、not_run=1、gate_a.pass=true")
PY
PASS=$((PASS + 1))
C="$(run_in_sb 'tests/manifest/suites.yaml' contract --only contract.contract-live-drift --strict)"
[ "${C}" = "2" ] || { cat "${WORK}/out.txt"; die "--strict 下可选 NOT_RUN 应退 2，实得 ${C}"; }
ok "N7：非 strict 退 0 且不计 PASS 分子；--strict 退 2（发布审计口径）"

# ---------------------------------------------------------------- 7. N8 单仓 full 语义
step "N8：full 计划里\"允许 NOT_RUN\"的 suite 恰好只有 contract-live-drift"
C="$(run_in_sb 'tests/manifest/suites.yaml' full --plan-only)"
[ "${C}" = "0" ] || { cat "${WORK}/out.txt"; die "full --plan-only 应退 0，实得 ${C}"; }
python3 - "${SB}/tests/manifest/suites.yaml" <<'PY'
import sys, yaml
suites = yaml.safe_load(open(sys.argv[1]))["suites"]
full = [s for s in suites if "full" in s["profiles"]]
optional = sorted(s["id"] for s in full if not s["required"])
assert optional == ["contract.contract-live-drift"], f"full 里可选 suite 应恰为 1 条，实得 {optional}"
assert all(s["not_run_when"] is None for s in full if s["required"]), "required suite 不得带 not_run_when"
nr = sorted(s["id"] for s in full if s["not_run_when"])
assert nr == ["contract.contract-live-drift"], nr
print(f"  [ok] full 计划 {len(full)} 支：可选恰 1 条（contract.contract-live-drift，绑 issue），其余全必需")
PY
PASS=$((PASS + 1))
ok "N8：单仓 full 的 NOT_RUN 面收敛为 1 条（执行期证据由 T-…-007 全量给出）"

# ---------------------------------------------------------------- 8. N9 收尾标记合同
step "N9：收尾标记 done_re —— 默认族外的历史文案需声明；声明不符仍判红；非法正则自检非零"
# 探针：证据标记齐全、退出码 0，收尾文案用 real_article.sh 那种历史写法
# （"…端到端验收脚本通过：N 步 / M 条断言"，既无 [PASS] 也无"全部…通过"）。
cat >"${SB}/tests/_neg/probe_legacy_done.sh" <<'SH'
#!/usr/bin/env bash
set -Eeuo pipefail
echo "  [ok] 断言 1"
echo "  [ok] 断言 2"
printf 'M9 端到端验收脚本通过：%d 步 / %d 条断言\n' 2 2
SH
git -C "${SB}" add -A
( cd "${SB}" && python3 tests/tools/inventory.py --write >/dev/null ) || die "副本内重算派生表失败"

# gen_done <done_re>：生成只含一支探针 suite 的清单；参数为空表示不声明 done_re。
gen_done() {
  DONE_RE_VAL="$1" python3 - "${SB}/tests/manifest/_neg_done.yaml" <<'PYGEN'
import os, pathlib, sys
done = os.environ.get("DONE_RE_VAL", "")
extra = "    done_re: '%s'\n" % done if done else ""
pathlib.Path(sys.argv[1]).write_text("""suites:
  - id: neg.legacy-done
    layer: contract
    capability: runner-semantics
    kind: shell
    target: tests/_neg/probe_legacy_done.sh
    source: tests/_neg/probe_legacy_done.sh
    origin: tool
    owner_task: T-evergreen.system_assurance-158614-007
    profiles: [contract]
    timeout_s: 600
    min_cases: 2
    required: true
    platform_required: linux/amd64
    not_run_when: null
    issue: null
""" + extra)
PYGEN
}

# N9a 不声明 done_re → 默认族识别不到历史文案 → 判红（这正是 I-…-006 的原始假红形态）
gen_done ""
C="$(run_in_sb 'tests/manifest/_neg_done.yaml' contract)"
[ "${C}" != "0" ] || { cat "${WORK}/out.txt"; die "N9a：默认族外的收尾文案未声明时必须判红"; }
want_out "缺少收尾标记"
ok "N9a 未声明 done_re → 判红（默认族不放宽，杜绝「输出含『通过』即算跑完」）"

# N9b 声明与实际输出一致 → 判绿（历史脚本与全部断言零改动，只在清单侧声明收尾文案）
gen_done 'M9 端到端验收脚本通过：\d+ 步 / \d+ 条断言'
C="$(run_in_sb 'tests/manifest/_neg_done.yaml' contract)"
[ "${C}" = "0" ] || { cat "${WORK}/out.txt"; die "N9b：声明匹配实际输出时应退 0，实得 ${C}"; }
want_out "PASS 1 / FAIL 0"
ok "N9b 声明匹配 → 退 0（脚本与断言原样保留）"

# N9c 声明与实际输出不符 → 仍判红（证明 done_re 不是"声明了就放过"的旁路）
gen_done '这条收尾文案根本不会出现 \d+'
C="$(run_in_sb 'tests/manifest/_neg_done.yaml' contract)"
[ "${C}" != "0" ] || { cat "${WORK}/out.txt"; die "N9c：声明与输出不符时必须判红"; }
want_out "缺少收尾标记"
ok "N9c 声明不符 → 判红（done_re 写错照样红）"

# N9d 非法正则 → 清单自检阶段非零，不进执行面
gen_done 'M9 (未闭合分组'
C="$(run_in_sb 'tests/manifest/_neg_done.yaml' contract --plan-only)"
[ "${C}" != "0" ] || { cat "${WORK}/out.txt"; die "N9d：非法 done_re 正则必须在自检阶段非零"; }
want_out "不是合法正则"
ok "N9d 非法正则 → 清单自检非零（坏声明进不了执行面）"

# ---------------------------------------------------------------- 9. 零污染自查
step "真实仓库零污染自查"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] || {
  diff <(printf '%s\n' "${BEFORE_REPO_STATUS}") <(printf '%s\n' "${AFTER_REPO_STATUS}") || true
  die "反证脚本污染了真实仓库工作区"; }
ok "真实仓库 git status 逐字不变（探针与坏清单只落副本）"

printf '\n[PASS] runner 反证门禁全部 %d 组断言通过（N1~N9；共 %d 步）\n' "${PASS}" "${STEP}"
