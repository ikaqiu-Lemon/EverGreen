#!/usr/bin/env bash
# 资源上限与隔离门禁（defect_zeroing · T-…-002 · 修 I-evergreen.defect_zeroing-158614-003 暴露的
# T-001/A1 内容缺口：合同 D7「隔离与资源上限」原文只有描述性条款，没有「判据 + 反例」，
# 因此 D7 是全文 14 组决策里唯一无人执行的一组）。
#
# D7 的四条实体约束此前只写在合同里、没有任何门禁承接：
#   ① run 级 TMPDIR 在 staging 内，每个 suite 独立 scratch；
#   ② Go 默认 `-p 2`，e2e 串行；
#   ③ 每个 suite `timeout_s` 必填且逐 suite 生效；
#   ④ 任何等待 / sleep ≤ 120 秒；
#   ⑤ 报告落 tests/_report/<run-id>/{summary.json,summary.md,logs/}。
# 没有门禁 = 有人把 sleep 改成 600、把 -p 2 改成 -p 8、给 suite 去掉 timeout_s，全绿照过。
#
# 判据
#   A1 suites.yaml 每个 suite 的 timeout_s 为正整数（缺键 / 零 / 负 / 非数字 → 红）；
#   A2 timeout_s 逐 suite 生效：runner 把它传进 `go test -timeout` 且用于脚本 suite 的墙钟上限；
#   A3 Go 命令固定 `-p 2`（不得为了快而放开并行度）；
#   A4 执行计划**串行**：runner 不含并发执行原语（多线程 / 多进程 / 后台 &），
#      因此"e2e 串行"按构造成立，而不是靠约定；
#   A5 每 suite 独立 scratch 且 TMPDIR 指向它（env["TMPDIR"] = scratch）；
#   A6 报告三件套路径固定为 tests/_report/<run-id>/{summary.json,summary.md,logs/}；
#   A7 测试树内任何 `sleep <字面量>` ≤ 120 秒（含小数；变量形式必须有上限注释豁免登记）；
#   A8 反例八连（本门禁自证不是假的）：把 sleep 改成 300、把 -p 2 改成 -p 8、
#      给某 suite 去掉 timeout_s、给 runner 塞一个 ThreadPoolExecutor、删掉既有 sleep 豁免标记、
#      把超限字面量藏进未登记文件的字符串里、删掉本文件的夹具文件登记、把登记写进字符串冒充
#      —— 对应判据必须逐个变红。
#
# eg:sleep-text-fixture-file 本门禁必须生成并打印「sleep 300」这类反例字面量来自证 A7 判据非空，
#   这些字面量都在注释或字符串里、不会产生真实等待；本文件是唯一登记文件，A8-6/A8-7 证明
#   未登记文件即使把超限字面量藏进字符串也会被判红。
#
# 只读：反例全部在 mktemp 副本上做，真实 runner / 清单一个字节不动（结尾复验 sha256）。
#
# 用法：cd evergreen && bash tests/contract/resource-limits/limits.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"

SUITES="${EG_TESTS_ROOT}/manifest/suites.yaml"
RUNNER="${EG_TESTS_ROOT}/runner/run.py"
SLEEP_CAP=120

WORK="$(eg_scratch eg-d7-limits)"
trap 'rm -rf "${WORK}"' EXIT

STEP=0
PASS=0
step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

[ -f "${SUITES}" ] || die "缺 ${SUITES}"
[ -f "${RUNNER}" ] || die "缺 ${RUNNER}"

BEFORE_SUITES="$(sha256sum "${SUITES}" | awk '{print $1}')"
BEFORE_RUNNER="$(sha256sum "${RUNNER}" | awk '{print $1}')"

# ---------------------------------------------------------------- A1
step "A1 每个 suite 的 timeout_s 为正整数"
bad="$(EG_SUITES="${SUITES}" python3 - <<'PYEOF'
import os, sys, yaml
suites = yaml.safe_load(open(os.environ["EG_SUITES"]))["suites"]
bad = []
for s in suites:
    v = s.get("timeout_s")
    if not isinstance(v, int) or isinstance(v, bool) or v <= 0:
        bad.append(f'{s.get("id")} timeout_s={v!r}')
print("\n".join(bad))
PYEOF
)"
[ -z "${bad}" ] || die "以下 suite 的 timeout_s 不是正整数：${bad}"
n_suites="$(EG_SUITES="${SUITES}" python3 -c 'import os,yaml;print(len(yaml.safe_load(open(os.environ["EG_SUITES"]))["suites"]))')"
ok "${n_suites} 个 suite 的 timeout_s 全部为正整数"

# ---------------------------------------------------------------- A2/A3
step "A2/A3 timeout_s 逐 suite 生效且 Go 并行度锁死 -p 2"
grep -Fq -- '"-timeout", f"{s['"'"'timeout_s'"'"']}s"' "${RUNNER}" \
  || die "runner 未把 suite 的 timeout_s 传给 go test -timeout（A2）"
ok "go test 的 -timeout 取自 suite 的 timeout_s"

grep -Fq -- '"-p", "2"' "${RUNNER}" || die "runner 的 go test 未固定 -p 2（A3）"
# 不得出现别的 -p 取值
otherp="$(grep -oE '"-p", *"[0-9]+"' "${RUNNER}" | grep -v '"-p", *"2"' || true)"
[ -z "${otherp}" ] || die "runner 出现非 2 的并行度：${otherp}（A3）"
ok "Go 并行度固定 -p 2，无第二个取值"

grep -Fq 'run_cmd(cmd, cwd, env, s["timeout_s"], log)' "${RUNNER}" \
  || die "runner 未把 timeout_s 作为墙钟上限传给 run_cmd（A2：脚本 suite 也必须受限）"
ok "脚本 suite 同样按 timeout_s 施加墙钟上限"

# ---------------------------------------------------------------- A4
step "A4 执行计划串行：runner 无并发执行原语"
conc="$(grep -nE 'ThreadPool|ProcessPool|multiprocessing|concurrent\.futures|threading\.Thread|Popen\(.*&' \
        "${RUNNER}" || true)"
[ -z "${conc}" ] || die "runner 出现并发执行原语，'e2e 串行'不再按构造成立：${conc}"
ok "runner 单进程顺序执行（无 ThreadPool / ProcessPool / threading / multiprocessing）"

# ---------------------------------------------------------------- A5
step "A5 每 suite 独立 scratch 且 TMPDIR 指向它"
grep -Fq 'scratch = tmp_root / sid' "${RUNNER}" || die "runner 未按 suite id 建独立 scratch（A5）"
grep -Fq 'env["TMPDIR"] = str(scratch)' "${RUNNER}" || die "runner 未把 TMPDIR 指向 suite scratch（A5）"
ok "scratch = tmp_root/<suite-id> 且 TMPDIR 指向该目录"

# ---------------------------------------------------------------- A6
step "A6 报告三件套路径固定"
grep -Fq 'REPO / "tests/_report" / run_id' "${RUNNER}" || die "报告根不是 tests/_report/<run-id>（A6）"
for f in 'summary.json' 'summary.md'; do
  grep -Fq "${f}" "${RUNNER}" || die "runner 未产出 ${f}（A6）"
done
grep -Fq 'report_dir / "logs"' "${RUNNER}" || die "runner 未产出 logs/ 目录（A6）"
ok "报告落 tests/_report/<run-id>/{summary.json,summary.md,logs/}"

# ---------------------------------------------------------------- A7
step "A7 测试树内 sleep 字面量 ≤ ${SLEEP_CAP} 秒（豁免必须带显式标记且有上限）"
# 扫描器是外置的唯一实现（tests/lib/sleep_scan.py，与 common.sh 同属共享库层：它自己不判红，
# 判红在本门禁），A7 与 A8 反例调用同一个入口，因此反例证明的是真实判据面本身，
# 不存在"内联两份实现各自漂移"的空转。
#
# 三类语义（详见扫描器头注释）：
#   EXEC/OVER    剔除注释与字符串后仍出现在代码面的超限字面量 —— 违规，本步判红；
#   EXEC/EXEMPT  带 `eg:sleep-exempt <理由>`，只用于"停在某点挂住进程、由父测试 kill 终结"的
#                崩溃留态 harness：墙钟由父测试决定，数字本身不构成等待。总数封顶 EXEMPT_CAP，
#                新增一个必须改本门禁（一次留在 diff 里的显式动作），不存在静默放行；
#   TEXTFIXTURE  只在注释/字符串里的超限字面量（本行不产生等待）。**不是无条件放过**：只有
#                在文件头**注释行**上用 `eg:sleep-text-fixture-file <理由>` 显式登记过的文件
#                才允许（写在字符串里不算，见 A8-8）—— 本门禁自己要打印/生成超限反例字面量，
#                故本文件是唯一登记文件（A8-6/A8-7 证明未登记文件把超限字面量藏进字符串一样会红）。
EXEMPT_CAP=1
DECL_CAP=1
TEXTFIXTURE_CAP=8
SCANNER="${EG_TESTS_ROOT}/lib/sleep_scan.py"
[ -f "${SCANNER}" ] || die "缺扫描器 ${SCANNER}（A7 无判据面）"
scan="$(python3 "${SCANNER}" --repo "${REPO_ROOT}" --git-subtree tests \
        --cap "${SLEEP_CAP}" --print-decl)"
over="$(printf '%s\n' "${scan}" | awk -F'\t' '$1=="OVER"{print $2" "$3" ("$4")"}')"
[ -z "${over}" ] || die "存在超限等待（D7 硬上限）：${over}"

decl="$(printf '%s\n' "${scan}" | awk -F'\t' '$1=="DECL"')"
n_decl="$(printf '%s\n' "${decl}" | grep -c . || true)"
[ "${n_decl}" -le "${DECL_CAP}" ] \
  || die "登记为注释/字符串夹具文件的文件 ${n_decl} 个，超过上限 ${DECL_CAP}：${decl}"
if [ "${n_decl}" -gt 0 ]; then
  while IFS=$'\t' read -r _k dfile dwhy; do
    [ -n "${dfile}" ] || continue
    [ -n "${dwhy}" ] || die "夹具文件登记缺理由：${dfile}"
    printf '  [登记] %s  ← %s\n' "${dfile}" "${dwhy}"
  done <<< "${decl}"
fi
fixture="$(printf '%s\n' "${scan}" | awk -F'\t' '$1=="TEXTFIXTURE"')"
n_fixture="$(printf '%s\n' "${fixture}" | grep -c . || true)"
[ "${n_fixture}" -le "${TEXTFIXTURE_CAP}" ] \
  || die "注释/字符串夹具字面量 ${n_fixture} 处，超过登记上限 ${TEXTFIXTURE_CAP}：${fixture}"
if [ "${n_fixture}" -gt 0 ]; then
  printf '%s\n' "${fixture}" | awk -F'\t' 'NF>0{printf "  [夹具] %s  %s\n", $2, $3}'
fi
exempt="$(printf '%s\n' "${scan}" | awk -F'\t' '$1=="EXEMPT"')"
n_exempt="$(printf '%s\n' "${exempt}" | grep -c . || true)"
[ "${n_exempt}" -le "${EXEMPT_CAP}" ] \
  || die "豁免点 ${n_exempt} 个，超过登记上限 ${EXEMPT_CAP}：${exempt}"
# 豁免必须给出与"被 kill 终结"相符的理由，不接受空理由或随口理由。
badreason="$(printf '%s\n' "${exempt}" | awk -F'\t' 'NF>0 && $4 !~ /kill/ {print $2" 理由="$4}')"
[ -z "${badreason}" ] || die "豁免理由未说明由父测试 kill 终结：${badreason}"
if [ "${n_exempt}" -gt 0 ]; then
  printf '%s\n' "${exempt}" | awk -F'\t' 'NF>0{printf "  [豁免] %s  %s  ← %s\n", $2, $3, $4}'
fi
ok "无标记的超限等待为 0；已登记豁免 ${n_exempt}/${EXEMPT_CAP} 个（逐条给出 kill 语义理由）"

# ---------------------------------------------------------------- A8 反例七连
# 反例全部走 A7 用的同一个扫描器（${SCANNER}），因此下面证明的是真实判据面本身。
step "A8-1 反例：真实会执行的 sleep 300 必须被 A7 判 OVER"
cp -a "${REPO_ROOT}/tests" "${WORK}/tests"
mkdir -p "${WORK}/tests/e2e/_d7probe"
# 注意：下面写出的探针文件里 \n 会展开成真实换行，probe.sh 的 sleep 位于**可执行位置**，
# 而本行的字面量在单引号字符串内（不会等待）。
printf '#!/usr/bin/env bash\nsleep 300\n' > "${WORK}/tests/e2e/_d7probe/probe.sh"
neg="$(python3 "${SCANNER}" --root "${WORK}/tests" --cap "${SLEEP_CAP}")"
printf '%s\n' "${neg}" | grep -qE '^OVER[[:space:]]+e2e/_d7probe/probe\.sh:2[[:space:]]+sleep 300' \
  || die "合成的可执行 sleep 300 未被判 OVER —— A7 的扫描面是假的：${neg}"
ok "可执行位置的 sleep 300 被判 OVER（A7 判据有效）"

step "A8-6 反例：把超限字面量藏进未登记文件的字符串里，同样必须判 OVER"
# 证明 TEXT 类不是"凡在字符串里就放过"：没有文件级登记，注释/字符串里的超限字面量一律 OVER。
mkdir -p "${WORK}/tests/e2e/_d7hidden"
printf '#!/usr/bin/env bash\nbash -c "sleep 300"\n' > "${WORK}/tests/e2e/_d7hidden/hidden.sh"
neg6="$(python3 "${SCANNER}" --root "${WORK}/tests" --cap "${SLEEP_CAP}")"
printf '%s\n' "${neg6}" | grep -qE '^OVER[[:space:]]+e2e/_d7hidden/hidden\.sh:2' \
  || die "未登记文件把 sleep 300 藏进字符串后仍不红 —— TEXT 类是无条件放过，A7 被弱化了：${neg6}"
ok "未登记文件的字符串内超限字面量被判 OVER（TEXT 类为登记驱动）"

step "A8-7 反例：删掉本门禁的夹具文件登记后，其自身的注释/字符串字面量必须转红"
# 证明"本门禁自己的反例字面量被放过"是**文件级显式登记**的结果，而不是对本文件特殊照顾。
undecl="${WORK}/tests/e2e/_d7hidden/undeclared_gate.sh"
sed 's|eg:sleep-text-fixture-file|eg-decl-removed|' "${BASH_SOURCE[0]}" > "${undecl}"
neg7="$(python3 "${SCANNER}" --root "${WORK}/tests" --cap "${SLEEP_CAP}")"
printf '%s\n' "${neg7}" | grep -qE '^OVER[[:space:]]+e2e/_d7hidden/undeclared_gate\.sh:' \
  || die "去掉文件级登记后本门禁自身字面量仍不红 —— 登记不是必要条件，判据被弱化了"
# 同一次扫描里，带登记的正本必须仍是 TEXTFIXTURE（证明差异只来自那一行登记）
printf '%s\n' "${neg7}" | grep -qE '^TEXTFIXTURE[[:space:]]+contract/resource-limits/limits\.sh:' \
  || die "带登记的正本未被判 TEXTFIXTURE —— 登记与判定不对应"
ok "去登记即转红、留登记即 TEXTFIXTURE（同一次扫描内对照成立）"

step "A8-8 反例：把登记标记写进字符串（非注释行）不算登记，仍必须判 OVER"
# 证明登记是"注释行上的显式书写"，不是"文件里出现过这个字符串"：否则任何在字符串里提到
# 标记名的文件（含扫描器自己的常量定义）都会自动登记，登记就形同虚设。
faked="${WORK}/tests/e2e/_d7hidden/faked_decl.sh"
printf '#!/usr/bin/env bash\nMARK="eg:sleep-text-fixture-file 假登记：写在字符串里"\nbash -c "sleep 300"\n' \
  > "${faked}"
neg8="$(python3 "${SCANNER}" --root "${WORK}/tests" --cap "${SLEEP_CAP}" --print-decl)"
printf '%s\n' "${neg8}" | grep -qE '^OVER[[:space:]]+e2e/_d7hidden/faked_decl\.sh:3' \
  || die "字符串里的假登记被当成真登记 —— 登记形同虚设，A7 被弱化了：${neg8}"
if printf '%s\n' "${neg8}" | grep -qE '^DECL[[:space:]]+e2e/_d7hidden/faked_decl\.sh'; then
  die "字符串里的假登记出现在 DECL 列表里 —— 登记判定未限定注释行"
fi
ok "字符串里的假登记不生效（登记只认注释行）"


step "A8-2 反例：并行度改成 -p 8 必须被 A3 判红"
sed 's/"-p", "2"/"-p", "8"/' "${RUNNER}" > "${WORK}/run_p8.py"
if grep -oE '"-p", *"[0-9]+"' "${WORK}/run_p8.py" | grep -qv '"-p", *"2"'; then
  ok "-p 8 被检出（A3 判据有效）"
else
  die "把 -p 2 改成 -p 8 后 A3 仍不红 —— A3 是假的"
fi

step "A8-3 反例：suite 去掉 timeout_s 必须被 A1 判红"
EG_SUITES="${SUITES}" EG_OUT="${WORK}/suites_notimeout.yaml" python3 - <<'PYEOF'
import os, yaml
d = yaml.safe_load(open(os.environ["EG_SUITES"]))
d["suites"][0].pop("timeout_s", None)
yaml.safe_dump(d, open(os.environ["EG_OUT"], "w"), allow_unicode=True, sort_keys=False)
PYEOF
neg2="$(EG_SUITES="${WORK}/suites_notimeout.yaml" python3 - <<'PYEOF'
import os, yaml
suites = yaml.safe_load(open(os.environ["EG_SUITES"]))["suites"]
bad = [s.get("id") for s in suites
       if not isinstance(s.get("timeout_s"), int) or s.get("timeout_s", 0) <= 0]
print("\n".join(str(x) for x in bad))
PYEOF
)"
[ -n "${neg2}" ] || die "去掉 timeout_s 后 A1 仍不红 —— A1 是假的"
ok "缺 timeout_s 被检出：${neg2}（A1 判据有效）"

step "A8-4 反例：runner 塞入 ThreadPoolExecutor 必须被 A4 判红"
{ cat "${RUNNER}"; printf '\nfrom concurrent.futures import ThreadPoolExecutor  # 合成反例\n'; } \
  > "${WORK}/run_conc.py"
grep -qE 'ThreadPool|concurrent\.futures' "${WORK}/run_conc.py" \
  || die "合成并发原语未被检出 —— A4 是假的"
ok "ThreadPoolExecutor 被检出（A4 判据有效）"

step "A8-5 反例：去掉 eg:sleep-exempt 标记后既有豁免点必须转红"
# 证明豁免是**标记驱动**而不是"对 txnctl 一律放过"：把标记删掉后同一行必须被判 OVER。
# 同样走 A7 的那个扫描器（不再内联第二份实现），并在同一次扫描里与带标记的正本对照。
probe_dir="${WORK}/tests/lib/txnctl_exempt_probe"
grep -n 'eg:sleep-exempt' "${REPO_ROOT}/tests/lib/txnctl/commit.go" > /dev/null \
  || die "找不到既有豁免点，A8-5 无法自证"
mkdir -p "${probe_dir}"
sed 's|// eg:sleep-exempt.*||' "${REPO_ROOT}/tests/lib/txnctl/commit.go" \
  > "${probe_dir}/commit_nomark.go"
neg5="$(python3 "${SCANNER}" --root "${WORK}/tests" --cap "${SLEEP_CAP}" \
        | grep -E 'txnctl(_exempt_probe)?/commit' || true)"
printf '%s\n' "${neg5}" | grep -qE '^OVER[[:space:]]+lib/txnctl_exempt_probe/commit_nomark\.go:' \
  || die "去掉豁免标记后仍不红 —— 豁免是无条件放过，A7 被弱化了：${neg5}"
printf '%s\n' "${neg5}" | grep -qE '^EXEMPT[[:space:]]+lib/txnctl/commit\.go:' \
  || die "带标记的正本未被判 EXEMPT —— 标记与判定不对应"
ok "去标记即 OVER、留标记即 EXEMPT（豁免为标记驱动，非白名单放过）"


# ---------------------------------------------------------------- 零副作用
step "只读复验：真实 runner / 清单逐字节未变"
[ "$(sha256sum "${SUITES}" | awk '{print $1}')" = "${BEFORE_SUITES}" ] || die "suites.yaml 被改动"
[ "$(sha256sum "${RUNNER}" | awk '{print $1}')" = "${BEFORE_RUNNER}" ] || die "run.py 被改动"
ok "suites.yaml / run.py sha256 与门禁开始时一致"

printf '\n[PASS] D7 隔离与资源上限门禁通过（%d 条判据，含 8 条反例自证）\n' "${PASS}"
