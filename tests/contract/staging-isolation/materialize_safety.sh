#!/usr/bin/env bash
# materializer 安全性与可分发性门禁（system_assurance · T-…-003 / 合同 D3.6 + D10）。
#
# 这条 suite 反证的是**交付风险**，不是功能：
#   D10 可分发性：无 `.git` 的源码分发包必须能原样物化并跑测试 —— 物化不得硬依赖 git 元数据；
#                 且分发包（文件系统列举）与开发仓（git 列举）的物化结果必须**逐项等量**。
#   D3.6 物化安全：
#     ① run-id 受约束：`../x`、`a/b`、空、超长一律拒绝，且拒绝时不得留下任何目录；
#     ② 清单是可编辑文本 → 必须当**不可信输入**：`..` / 绝对路径 / 非法 root 一律拒绝，
#        且拒绝时隔离根之外零文件生成；
#     ③ 旧 run 清理**不按 mtime 猜活跃**：mtime 最旧但有 flock 持有者的 run 必须活下来，
#        无持有者且 pid 已退出的陈旧 run 才允许被淘汰；
#     ④ 隔离根本身是 symlink 时拒绝物化（防止把 stage 写到仓外）。
#
# 破坏性用例一律在 **TMPDIR 内的分发副本** 上做，绝不改真实仓。
# 用法：cd evergreen && bash tests/contract/staging-isolation/materialize_safety.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
cd "${REPO_ROOT}"
eg_require_disk 1536

FAIL=0
pass() { printf '  [ok] %s\n' "$1"; }
bad()  { printf '  [FAIL] %s\n' "$1" >&2; FAIL=1; }
sec()  { printf '\n=== %s ===\n' "$1"; }

WORK="$(eg_scratch eg-mat-safety)"
# 步骤 1 用固定 run-id `safety-git` 在真实仓落一份 stage 做等量对比（--no-cleanup），
# 因此退出时自己收走，别把生成物留在开发者仓里。
# 注：D3.7 落地后，残骸已不会再卡死后续同名物化（无主残骸自动回收），
# 这里的清理只是卫生，不再是"能否继续跑"的前提。只清本门禁自己命名的 run-id。
OWN_STAGE="${REPO_ROOT}/.tests-staging/safety-git"
cleanup() {
  chmod -R u+w "${WORK}" 2>/dev/null || true; rm -rf "${WORK}"
  chmod -R u+w "${OWN_STAGE}" 2>/dev/null || true; rm -rf "${OWN_STAGE}"
}
trap cleanup EXIT
# 进入前先清一次同名残骸（上一次异常中断可能留下）
chmod -R u+w "${OWN_STAGE}" 2>/dev/null || true; rm -rf "${OWN_STAGE}"

jq_get() { python3 -c 'import json,sys;print(json.load(sys.stdin)[sys.argv[1]])' "$1"; }

# ---------------------------------------------------------------- 0. 造一个无 .git 分发包
sec "步骤0：构造无 .git 的源码分发副本（D10 前提）"
DIST="${WORK}/dist"
mkdir -p "${DIST}"
if [ -d "${REPO_ROOT}/.git" ]; then
  git -C "${REPO_ROOT}" ls-files -z | tar -C "${REPO_ROOT}" --null -T - -cf - | tar -C "${DIST}" -xf -
else
  # 本身已是分发包：直接整树复制（排除运行期产物）
  tar -C "${REPO_ROOT}" --exclude='./.tests-staging' --exclude='./tests/_report' -cf - . | tar -C "${DIST}" -xf -
fi
[ ! -e "${DIST}/.git" ] && pass "分发副本内无 .git（source_mode 必须走 walk）" || bad "分发副本仍含 .git，D10 前提不成立"
[ -f "${DIST}/tests/runner/materialize.py" ] && pass "分发副本含 materializer" || bad "分发副本缺 materializer"

# ---------------------------------------------------------------- 1. D10：分发包可物化 + 等量
sec "步骤1：D10 无 .git 分发包物化，且与 git 模式逐项等量"
git_json="${WORK}/git.json"; dist_json="${WORK}/dist.json"
( cd "${REPO_ROOT}" && python3 tests/runner/materialize.py --run-id safety-git --no-cleanup --json ) \
  >"${git_json}" 2>"${WORK}/git.err" || bad "真实仓物化失败：$(head -2 "${WORK}/git.err")"
( cd "${DIST}" && python3 tests/runner/materialize.py --run-id safety-dist --json ) \
  >"${dist_json}" 2>"${WORK}/dist.err" || bad "分发包物化失败（D10 未兑现）：$(head -2 "${WORK}/dist.err")"

if [ -s "${git_json}" ] && [ -s "${dist_json}" ]; then
  [ "$(jq_get source_mode <"${git_json}")" = "git" ] && pass "真实仓 source_mode=git" \
    || bad "真实仓 source_mode 非 git（实际 $(jq_get source_mode <"${git_json}")）"
  [ "$(jq_get source_mode <"${dist_json}")" = "walk" ] && pass "分发包 source_mode=walk（不依赖 git 元数据）" \
    || bad "分发包 source_mode 非 walk（实际 $(jq_get source_mode <"${dist_json}")）"
  for k in product_files runtime_map_entries staged_test_files contract_snapshot_files; do
    a="$(jq_get "${k}" <"${git_json}")"; b="$(jq_get "${k}" <"${dist_json}")"
    [ "${a}" = "${b}" ] && pass "两种列举方式 ${k} 等量（${a}）" \
      || bad "${k} 不等：git=${a} walk=${b}（分发包会缺件 / 多件）"
  done
fi

# 分发包物化产物必须真的能跑 Go 测试（抽一个最小包，证明布局有效而不只是文件数对）
sec "步骤2：分发包 stage 内 Go 白盒测试可跑"
DSTAGE="${DIST}/.tests-staging/safety-dist/evergreen"
if [ -d "${DSTAGE}" ]; then
  if ( cd "${DSTAGE}" && CGO_ENABLED=0 go test -count=1 ./internal/version/ >"${WORK}/dgo.log" 2>&1 ); then
    pass "分发包 stage 内 go test ./internal/version/ 通过（白盒回填有效）"
  else
    bad "分发包 stage 内 go test 失败：$(tail -3 "${WORK}/dgo.log" | tr '\n' ' ')"
  fi
else
  bad "分发包 stage 目录缺失：${DSTAGE}"
fi

# ---------------------------------------------------------------- 3. D3.6① run-id 约束
sec "步骤3：D3.6① 非法 run-id 一律拒绝且不留残骸"
before="$(find "${DIST}/.tests-staging" -maxdepth 1 -mindepth 1 | wc -l)"
for bad_id in "../evil" "a/b" "" "$(printf 'x%.0s' $(seq 1 80))" ".hidden"; do
  rc=0
  ( cd "${DIST}" && python3 tests/runner/materialize.py --run-id "${bad_id}" --no-cleanup >/dev/null 2>&1 ) || rc=$?
  [ "${rc}" -ne 0 ] && pass "run-id=$(printf '%.20s' "${bad_id}")… 被拒（退出 ${rc}）" \
    || bad "run-id=${bad_id} 竟被接受（路径注入面未封）"
done
after="$(find "${DIST}/.tests-staging" -maxdepth 1 -mindepth 1 | wc -l)"
[ "${before}" = "${after}" ] && pass "非法 run-id 未在隔离根留下任何目录（${after} 个不变）" \
  || bad "非法 run-id 留下残骸（${before} → ${after}）"
[ ! -e "${DIST}/evil" ] && [ ! -e "${WORK}/evil" ] && pass "隔离根之外零残留" || bad "隔离根之外出现残留"

# ---------------------------------------------------------------- 4. D3.6② 清单路径逃逸
sec "步骤4：D3.6② runtime_map 路径逃逸一律拒绝"
esc_case() { # esc_case <注入的 runtime_path> <说明>
  local inject="$1" desc="$2" rc=0
  local sandbox="${WORK}/esc"; rm -rf "${sandbox}"; mkdir -p "${sandbox}"
  tar -C "${DIST}" --exclude='./.tests-staging' -cf - . | tar -C "${sandbox}" -xf -
  python3 - "${sandbox}" "${inject}" <<'PY'
import sys, pathlib
root, inject = pathlib.Path(sys.argv[1]), sys.argv[2]
p = root / "tests/manifest/runtime_map.tsv"
lines = p.read_text().splitlines()
head, first = lines[0], lines[1].split("\t")
first[1] = inject                      # 只改 runtime_path，保持其余列合法
lines[1] = "\t".join(first)
p.write_text("\n".join(lines) + "\n")
PY
  ( cd "${sandbox}" && python3 tests/runner/materialize.py --run-id esc --json >/dev/null 2>"${WORK}/esc.err" ) || rc=$?
  if [ "${rc}" -ne 0 ] && grep -q 'D3.6' "${WORK}/esc.err"; then
    pass "${desc} 被 D3.6 拒绝（退出 ${rc}）"
  else
    bad "${desc} 未被拒绝（退出 ${rc}）：$(head -1 "${WORK}/esc.err")"
  fi
  [ ! -e "${sandbox}/../pwned" ] && [ ! -e "/tmp/pwned" ] || bad "${desc} 竟在隔离根外写出文件"
  rm -rf "${sandbox}"
}
esc_case "../../pwned"        "runtime_path 含 ../.."
esc_case "/tmp/pwned"         "runtime_path 为绝对路径"
esc_case "a/../../../pwned"   "runtime_path 中段回溯"

# ---------------------------------------------------------------- 5. D3.6③ 活跃 run 保护
sec "步骤5：D3.6③ 活跃 run 不被清理（且不按 mtime 判活跃）"
SR="${DIST}/.tests-staging"
mkdir -p "${SR}/stale-old" "${SR}/active-oldest"
printf '{"run_id":"stale-old","pid":999999}\n'    >"${SR}/stale-old/materialize.json"
printf '{"run_id":"active-oldest","pid":%d}\n' $$ >"${SR}/active-oldest/materialize.json"
: >"${SR}/active-oldest/.materialize.lock"
# active-oldest 的 mtime 造成**最旧**：若实现按 mtime 淘汰，它会第一个被删 —— 这正是要反证的
touch -t 200001010000 "${SR}/active-oldest" "${SR}/active-oldest/.materialize.lock"
touch -t 202001010000 "${SR}/stale-old"
# 后台进程持有 flock，模拟「正在跑的 suite」
python3 - "${SR}/active-oldest/.materialize.lock" <<'PY' &
import fcntl, sys, time
f = open(sys.argv[1], "r+"); fcntl.flock(f, fcntl.LOCK_EX); time.sleep(25)
PY
HOLDER=$!
sleep 1
( cd "${DIST}" && python3 tests/runner/materialize.py --run-id cleanup-probe --keep 1 --json ) \
  >"${WORK}/clean.json" 2>"${WORK}/clean.err" || bad "清理探针物化失败：$(head -2 "${WORK}/clean.err")"
kill "${HOLDER}" 2>/dev/null || true; wait "${HOLDER}" 2>/dev/null || true
if [ -s "${WORK}/clean.json" ]; then
  [ -d "${SR}/active-oldest" ] \
    && pass "mtime 最旧但被 flock 持有的 run 存活（反证：未按 mtime 误删活跃 run）" \
    || bad "活跃 run 被删除 —— 清理仍在按 mtime 猜活跃"
  [ ! -d "${SR}/stale-old" ] && pass "无持有者且 pid 已退出的陈旧 run 被淘汰（生命周期清理有效）" \
    || bad "陈旧 run 未被清理（磁盘会持续膨胀）"
  python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); sys.exit(0 if any("active-oldest" in s for s in d["kept_runs"]) else 1)' \
    "${WORK}/clean.json" && pass "报告 kept_runs 明示活跃 run 被保留的原因" \
    || bad "报告未记录活跃 run 的保留原因（不可审计）"
fi

# ---------------------------------------------------------------- 6. D3.6④ 隔离根 symlink
sec "步骤6：D3.6④ 隔离根是 symlink 时拒绝物化"
SYM="${WORK}/symcase"; rm -rf "${SYM}"; mkdir -p "${SYM}"
tar -C "${DIST}" --exclude='./.tests-staging' -cf - . | tar -C "${SYM}" -xf -
mkdir -p "${WORK}/outside"
ln -s "${WORK}/outside" "${SYM}/.tests-staging"
rc=0
( cd "${SYM}" && python3 tests/runner/materialize.py --run-id symprobe >/dev/null 2>"${WORK}/sym.err" ) || rc=$?
[ "${rc}" -ne 0 ] && pass "隔离根为 symlink 时拒绝物化（退出 ${rc}）" || bad "隔离根为 symlink 仍继续物化"
[ -z "$(ls -A "${WORK}/outside")" ] && pass "symlink 指向的仓外目录零写入" || bad "已向仓外目录写入内容"

# ---------------------------------------------------------------- 7. D3.7 同名残骸自恢复
# 为什么必须锁死：旧实现遇到同名目录一律 die（"残留污染"），于是**上一次被中断的物化**
# 留下的空壳会把后续每一次同名物化永久卡死，只能人工 rm -rf。那是门禁在保护一个没有
# 持有者的残骸。修复方向是"按活体证据判定"，而不是"见到就删" —— 所以四条反例都要立住：
# 无持有者→回收；pid 存活→拒绝抢占；flock 占用→拒绝抢占；--on-stale fail→退回严格失败。
sec "步骤7：D3.7① 同名残骸安全自恢复（无活体持有者才回收）"
stale_json="${WORK}/stale.json"
mkdir -p "${DIST}/.tests-staging/stale-recover/evergreen"
: >"${DIST}/.tests-staging/stale-recover/evergreen/leftover.txt"   # 中断残骸：连 meta 都没来得及写
rc=0
( cd "${DIST}" && python3 tests/runner/materialize.py --run-id stale-recover --json ) \
  >"${stale_json}" 2>"${WORK}/stale.err" || rc=$?
if [ "${rc}" -eq 0 ] && [ -s "${stale_json}" ]; then
  pass "同名残骸不再直接中断，物化自恢复成功（退出 0）"
  reclaimed="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1])).get("stale_reclaimed") or "")' "${stale_json}")"
  [ -n "${reclaimed}" ] && pass "报告留痕 stale_reclaimed=「${reclaimed}」（可审计，非静默删除）" \
    || bad "回收了残骸却未在报告留痕（不可审计）"
  [ ! -e "${DIST}/.tests-staging/stale-recover/evergreen/leftover.txt" ] \
    && pass "残骸内容已被清除（不是叠加在旧壳上继续用）" \
    || bad "残骸文件仍在 —— 物化叠加到了旧壳上，隔离性可疑"
else
  bad "同名残骸仍导致物化中断（D3.7 未兑现）：$(head -2 "${WORK}/stale.err")"
fi

sec "步骤7：D3.7② 有活体持有者时拒绝抢占（pid 存活 / flock 占用）"
mkdir -p "${DIST}/.tests-staging/live-pid"
printf '{"run_id":"live-pid","pid":%d}\n' $$ >"${DIST}/.tests-staging/live-pid/materialize.json"
rc=0
( cd "${DIST}" && python3 tests/runner/materialize.py --run-id live-pid >/dev/null 2>"${WORK}/live.err" ) || rc=$?
if [ "${rc}" -eq 3 ] && grep -q '拒绝抢占' "${WORK}/live.err"; then
  pass "pid 存活的同名 run 被拒绝抢占（退出 3 = 环境约束，不是产品缺陷）"
else
  bad "pid 存活的同名 run 未被保护（退出 ${rc}）：$(head -1 "${WORK}/live.err")"
fi
[ -f "${DIST}/.tests-staging/live-pid/materialize.json" ] \
  && pass "被占用的 run 目录未被删除" || bad "被占用的 run 竟被删除 —— 会腰斩正在跑的 suite"

mkdir -p "${DIST}/.tests-staging/live-lock"
: >"${DIST}/.tests-staging/live-lock/.materialize.lock"
python3 - "${DIST}/.tests-staging/live-lock/.materialize.lock" <<'PY' &
import fcntl, sys, time
f = open(sys.argv[1], "r+"); fcntl.flock(f, fcntl.LOCK_EX); time.sleep(20)
PY
LKH=$!
sleep 1
rc=0
( cd "${DIST}" && python3 tests/runner/materialize.py --run-id live-lock >/dev/null 2>"${WORK}/lock.err" ) || rc=$?
kill "${LKH}" 2>/dev/null || true; wait "${LKH}" 2>/dev/null || true
[ "${rc}" -eq 3 ] && pass "flock 被占用的同名 run 被拒绝抢占（退出 3）" \
  || bad "flock 占用未被识别（退出 ${rc}）：$(head -1 "${WORK}/lock.err")"

sec "步骤7：D3.7③ --on-stale fail 保留旧的严格失败语义（回收是默认，不是唯一选项）"
mkdir -p "${DIST}/.tests-staging/strict-probe"
rc=0
( cd "${DIST}" && python3 tests/runner/materialize.py --run-id strict-probe --on-stale fail \
    >/dev/null 2>"${WORK}/strict.err" ) || rc=$?
[ "${rc}" -eq 1 ] && grep -q 'D3.4-5' "${WORK}/strict.err" \
  && pass "--on-stale fail 时同名残骸仍判 D3.4-5 失败（严格模式可用于 CI 洁净性巡检）" \
  || bad "--on-stale fail 未退回严格语义（退出 ${rc}）"

sec "步骤7：D3.7④ 默认 run-id 唯一（同秒连续物化不撞名）"
a="$( cd "${DIST}" && python3 tests/runner/materialize.py --json | python3 -c 'import json,sys;print(json.load(sys.stdin)["run_id"])' )"
b="$( cd "${DIST}" && python3 tests/runner/materialize.py --json | python3 -c 'import json,sys;print(json.load(sys.stdin)["run_id"])' )"
[ -n "${a}" ] && [ -n "${b}" ] && [ "${a}" != "${b}" ] \
  && pass "两次默认物化 run-id 不同（${a} ≠ ${b}）" \
  || bad "默认 run-id 会撞名（${a} vs ${b}）—— 并发物化会互相误判成残留污染"

# ---------------------------------------------------------------- 8. D3.7 输出字段合同
sec "步骤8：D3.7 输出字段合同 —— staging 路径只有 stage 一个键，消费方禁引用 stage_root"
fld="${WORK}/fields.json"
( cd "${DIST}" && python3 tests/runner/materialize.py --run-id field-probe --json ) >"${fld}" 2>/dev/null
# 被禁字面量一律拼接生成（见下方 KEY），因此本门禁自身不含该字面量，无需给自己开豁免。
KEY="stage_$(printf 'root')"
python3 - "${fld}" "${KEY}" <<'PY' && pass "报告含 stage 且不含 ${KEY}（字段唯一，消费方无歧义）" \
  || bad "输出字段合同被破坏（stage 缺失或旧字段回归）"
import json, sys
d = json.load(open(sys.argv[1]))
banned = sys.argv[2]
sys.exit(0 if ("stage" in d and banned not in d) else 1)
PY
# 消费方侧静态判据：任何以「JSON 键 / 属性」形态引用旧字段的消费代码都判红。
# materialize.py 内部的局部变量 `stage_root = repo / STAGE_DIRNAME` 不是键引用形态，天然不命中。
PAT="[\"']${KEY}[\"']|\.${KEY}|\[[\"']${KEY}"
consumer_hits="$({ grep -rnE "${PAT}" tests/ Makefile 2>/dev/null || true; } | head -5 | tr '\n' ' ')"
[ -z "${consumer_hits}" ] && pass "消费方零处引用旧字段 ${KEY}（只认 stage）" \
  || bad "仍有消费方引用不存在的 ${KEY}：${consumer_hits}"
# 双侧反证：判据必须真能抓住"引用了旧字段"的消费方，否则它只是句空话
probe_dir="${WORK}/consumer-probe"; mkdir -p "${probe_dir}"
printf 'S=$(python3 m.py --json | python3 -c "import json,sys;print(json.load(sys.stdin)[%s%s%s])")\n' \
  "'" "${KEY}" "'" >"${probe_dir}/bad_consumer.sh"
[ -n "$({ grep -rnE "${PAT}" "${probe_dir}" 2>/dev/null || true; })" ] \
  && pass "反例：引用 ${KEY} 的消费方被判据抓住" \
  || bad "判据失效：引用 ${KEY} 的消费方未被抓住"
rm -rf "${probe_dir}"

printf '\n'
[ "${FAIL}" -eq 0 ] || { printf '[FAIL] materializer 安全性 / 可分发性门禁未通过\n' >&2; exit 1; }
printf '[PASS] materializer 安全性 / 可分发性门禁通过（D10 无 .git 可运行 + D3.6 四条安全约束 + D3.7 残骸自恢复与字段合同）\n'
