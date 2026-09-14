#!/usr/bin/env bash
# 规模棘轮反证门禁（defect_zeroing · T-…-002）。
#
# 为什么必须有这一支：`manifest.scale-ratchet` 自己只会在"当前盘面"上输出绿灯，绿灯本身
# 不证明它拦得住缩水。初版 `scale_ratchet.py` 就栽在这上面——已冻结维度整类消失时它只打
# 一行 `[warn]` 然后照样 PASS（监督复核实证：注入式删掉 `cur['suites_layer_fuzz']` 后
# `--check` 退出 0）。缺的不是判据，是**判据的反证**。本门禁把六种绕过手法固化成永久用例。
#
# 判据（每条都在 mktemp 沙箱里合成盘面，真实仓一个字节不动）
#   N1 删除整类维度（清空全部 fuzz suite，使 suites_layer_fuzz 键不再产生）：
#      --check 非零，且 --write 也非零并**不改 baseline**（不许把"消失"固化成新基线）；
#   N2 换类补总数（删 1 支 e2e、加 1 支 unit，suites_total 不变）：
#      --check 必须非零 —— 总量守恒不等于覆盖守恒；
#   N3 正常新增（加 1 支 e2e）：--check 绿；--write 抬高 high_water 且 initial 不变；
#   N4 下调数值（删 1 支 e2e）：--check 非零；--write 非零且 baseline 字节不变；
#   N5 手改水位线到冻结初值以下：--check 非零；
#   N6 盘面维度未登记（baseline 缺行）：--check 非零。
#
# 边界：本门禁验证的是**棘轮工具的判据强度**，不是产品行为。它不读产品代码。
#   `initial` 列被手工下调这一情形**不在**机器判据内（任何仓内记录都能被同一只手改掉），
#   按 scale_ratchet.py 头注释「治理约束」一节由 review 兜底，本门禁不假装能拦。
#
# 用法：cd evergreen && bash tests/contract/scale-ratchet/negatives.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"

TOOL_REL="tests/tools/scale_ratchet.py"
MANIFEST_REL="tests/manifest"
REAL_BASELINE="${REPO_ROOT}/${MANIFEST_REL}/scale_baseline.tsv"

WORK="$(eg_scratch eg-ratchet-neg)"
trap 'rm -rf "${WORK}"' EXIT

STEP=0
PASS=0
step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

[ -f "${REAL_BASELINE}" ] || die "缺 ${MANIFEST_REL}/scale_baseline.tsv"
REAL_SHA="$(sha256sum "${REAL_BASELINE}" | awk '{print $1}')"

# ---------------------------------------------------------------- 沙箱
# 棘轮工具用 Path(__file__).parents[2] 定位仓根，所以沙箱只要复刻 tests/tools + tests/manifest
# 就是一个完整可跑的度量面，不需要产品代码、不需要 git。
new_sandbox() {
  local sb="$1"
  rm -rf "${sb}"
  mkdir -p "${sb}/tests/tools" "${sb}/${MANIFEST_REL}"
  cp "${REPO_ROOT}/${TOOL_REL}" "${sb}/tests/tools/"
  cp "${REPO_ROOT}/${MANIFEST_REL}"/*.tsv "${REPO_ROOT}/${MANIFEST_REL}/suites.yaml" \
     "${sb}/${MANIFEST_REL}/"
}

# run_tool <sandbox> <--check|--write> → 回显 rc，输出落 $WORK/last.out / last.err
run_tool() {
  local sb="$1" mode="$2" rc=0
  set +e
  python3 "${sb}/${TOOL_REL}" "${mode}" > "${WORK}/last.out" 2> "${WORK}/last.err"
  rc=$?
  set -e
  printf '%s' "${rc}"
}

sb_baseline_sha() { sha256sum "$1/${MANIFEST_REL}/scale_baseline.tsv" | awk '{print $1}'; }

# mutate_suites <sandbox> <python 片段>：在沙箱 suites.yaml 上做结构化改动
mutate_suites() {
  local sb="$1" code="$2"
  EG_SB="${sb}" EG_MAN="${MANIFEST_REL}" python3 -c "
import os, yaml
p = os.path.join(os.environ['EG_SB'], os.environ['EG_MAN'], 'suites.yaml')
d = yaml.safe_load(open(p))
suites = d['suites']
${code}
d['suites'] = suites
yaml.safe_dump(d, open(p, 'w'), allow_unicode=True, sort_keys=False)
"
}

# 沙箱自证：未改动时必须绿（否则后面每条反证都无从区分"本来就红"）
step "S0 沙箱基线自证：未改动盘面时 --check 必须绿"
new_sandbox "${WORK}/s0"
rc="$(run_tool "${WORK}/s0" --check)"
[ "${rc}" = "0" ] || { cat "${WORK}/last.err" >&2; die "沙箱未改动却红了，反证无参照系"; }
ok "沙箱复刻的度量面与真实盘面同绿（exit 0）"

# ---------------------------------------------------------------- N1
step "N1 删除整类维度：suites_layer_fuzz 消失 → --check / --write 都必须非零且不改 baseline"
new_sandbox "${WORK}/n1"
mutate_suites "${WORK}/n1" "suites = [s for s in suites if s.get('layer') != 'fuzz']"
before="$(sb_baseline_sha "${WORK}/n1")"
rc="$(run_tool "${WORK}/n1" --check)"
[ "${rc}" != "0" ] || die "整类维度消失后 --check 仍为 0（这正是初版的假绿）"
grep -q 'suites_layer_fuzz' "${WORK}/last.err" \
  || die "--check 非零但未指名消失的维度：$(cat "${WORK}/last.err")"
# 强度判据：消失必须以 [FAIL] 形态出现在 stderr，且**不得**再以 [warn] 形态出现在 stdout
# （初版就是把它打在 stdout 的 [warn] 里然后 PASS）。
grep -qE '^\[FAIL\].*suites_layer_fuzz' "${WORK}/last.err" \
  || die "消失未以 [FAIL] 形态报出：$(cat "${WORK}/last.err")"
grep -qE '^\s*\[warn\].*suites_layer_fuzz' "${WORK}/last.out" \
  && die "维度消失仍被降级成 stdout 的 [warn]（初版假绿形态复发）"
ok "--check 非零，并以 [FAIL] 指名 suites_layer_fuzz 消失（非 warn 降级）"

rc="$(run_tool "${WORK}/n1" --write)"
[ "${rc}" != "0" ] || die "--write 把'维度消失'固化成了新基线（必须拒绝落盘）"
[ "$(sb_baseline_sha "${WORK}/n1")" = "${before}" ] \
  || die "--write 失败却改动了 baseline（必须先判后写、失败零副作用）"
ok "--write 非零且 baseline 逐字节未变"

# ---------------------------------------------------------------- N2
step "N2 换类补总数：删 1 支 e2e + 加 1 支 unit，suites_total 不变 → 必须非零"
new_sandbox "${WORK}/n2"
mutate_suites "${WORK}/n2" "
victim = next(s for s in suites if s.get('layer') == 'e2e')
donor = next(s for s in suites if s.get('layer') == 'unit')
suites = [s for s in suites if s is not victim]
clone = dict(donor); clone['id'] = donor['id'] + '.__n2_filler'
suites.append(clone)
"
rc="$(run_tool "${WORK}/n2" --check)"
[ "${rc}" != "0" ] || die "换类补总数后 --check 为 0 —— 棘轮只看总数，覆盖结构缩水判不出"
grep -q 'suites_layer_e2e' "${WORK}/last.err" \
  || die "--check 非零但未指出 e2e 层缩水：$(cat "${WORK}/last.err")"
ok "总数守恒仍被判红，且指名 suites_layer_e2e 低于水位"

# ---------------------------------------------------------------- N3
step "N3 正常新增：加 1 支 e2e → --check 绿，--write 抬高 high_water 且 initial 不变"
new_sandbox "${WORK}/n3"
mutate_suites "${WORK}/n3" "
src = next(s for s in suites if s.get('layer') == 'e2e')
clone = dict(src); clone['id'] = src['id'] + '.__n3_added'
suites.append(clone)
"
rc="$(run_tool "${WORK}/n3" --check)"
[ "${rc}" = "0" ] || { cat "${WORK}/last.err" >&2; die "新增测试被判红 —— 棘轮不能阻碍正常增长"; }
ok "新增 1 支 e2e 后 --check 仍绿（棘轮只拦缩水）"

hw_before="$(awk -F'\t' '$1=="suites_layer_e2e"{print $2}' "${WORK}/n3/${MANIFEST_REL}/scale_baseline.tsv")"
init_before="$(awk -F'\t' '$1=="suites_layer_e2e"{print $3}' "${WORK}/n3/${MANIFEST_REL}/scale_baseline.tsv")"
rc="$(run_tool "${WORK}/n3" --write)"
[ "${rc}" = "0" ] || { cat "${WORK}/last.err" >&2; die "正常增长时 --write 失败"; }
hw_after="$(awk -F'\t' '$1=="suites_layer_e2e"{print $2}' "${WORK}/n3/${MANIFEST_REL}/scale_baseline.tsv")"
init_after="$(awk -F'\t' '$1=="suites_layer_e2e"{print $3}' "${WORK}/n3/${MANIFEST_REL}/scale_baseline.tsv")"
[ "${hw_after}" -eq "$((hw_before + 1))" ] \
  || die "high_water 未抬高：${hw_before} → ${hw_after}"
[ "${init_after}" = "${init_before}" ] \
  || die "initial 被 --write 改写（冻结初值必须只在首次冻结时写入）：${init_before} → ${init_after}"
ok "high_water ${hw_before} → ${hw_after}，initial 保持 ${init_before}"

# 抬高后再删回去：新水位必须立刻生效（证明棘轮是"历史最高"而不是"当前值"）
mutate_suites "${WORK}/n3" "suites = [s for s in suites if not s['id'].endswith('.__n3_added')]"
rc="$(run_tool "${WORK}/n3" --check)"
[ "${rc}" != "0" ] || die "抬高水位后又删回原值却不红 —— 水位线没生效"
ok "抬高后回退立即判红（水位线记的是历史最高）"

# ---------------------------------------------------------------- N4
step "N4 下调数值：删 1 支 e2e → --check 非零，--write 非零且 baseline 不变"
new_sandbox "${WORK}/n4"
mutate_suites "${WORK}/n4" "
victim = next(s for s in suites if s.get('layer') == 'e2e')
suites = [s for s in suites if s is not victim]
"
before="$(sb_baseline_sha "${WORK}/n4")"
rc="$(run_tool "${WORK}/n4" --check)"
[ "${rc}" != "0" ] || die "删 suite 后 --check 为 0"
ok "--check 非零"
rc="$(run_tool "${WORK}/n4" --write)"
[ "${rc}" != "0" ] || die "--write 接受了缩水值"
[ "$(sb_baseline_sha "${WORK}/n4")" = "${before}" ] || die "--write 失败却改了 baseline"
grep -q '未改动' "${WORK}/last.err" || die "--write 失败信息未声明 baseline 未改动"
ok "--write 非零、baseline 逐字节未变、且明示未改动"

# ---------------------------------------------------------------- N5
step "N5 手改水位线到冻结初值以下 → --check 非零"
new_sandbox "${WORK}/n5"
python3 - "${WORK}/n5/${MANIFEST_REL}/scale_baseline.tsv" <<'PYEOF'
import sys, pathlib
p = pathlib.Path(sys.argv[1])
out = []
for line in p.read_text().splitlines():
    f = line.split("\t")
    if f[0] == "inventory_cases":
        f[1] = str(max(0, int(f[2]) - 1))   # high_water 压到 initial 之下
    out.append("\t".join(f))
p.write_text("\n".join(out) + "\n")
PYEOF
rc="$(run_tool "${WORK}/n5" --check)"
[ "${rc}" != "0" ] || die "high_water < initial 未被判红"
grep -q 'initial' "${WORK}/last.err" || die "红了但没指出 initial 约束"
ok "水位线低于冻结初值被判红"

# ---------------------------------------------------------------- N6
step "N6 盘面维度未登记（baseline 缺行）→ --check 非零"
new_sandbox "${WORK}/n6"
grep -v '^legacy_gate_assets' "${WORK}/n6/${MANIFEST_REL}/scale_baseline.tsv" > "${WORK}/n6.tsv"
mv "${WORK}/n6.tsv" "${WORK}/n6/${MANIFEST_REL}/scale_baseline.tsv"
rc="$(run_tool "${WORK}/n6" --check)"
[ "${rc}" != "0" ] || die "盘面有维度而清单未登记却不红（新增维度可游离在棘轮外）"
grep -q 'legacy_gate_assets' "${WORK}/last.err" || die "红了但未指名未登记的维度"
ok "未登记维度被判红"

# ---------------------------------------------------------------- 零副作用
step "只读复验：真实 scale_baseline.tsv 逐字节未变"
[ "$(sha256sum "${REAL_BASELINE}" | awk '{print $1}')" = "${REAL_SHA}" ] \
  || die "真实 baseline 被本门禁改动了"
ok "真实 baseline sha256 与门禁开始时一致"

printf '\n[PASS] 规模棘轮反证门禁通过（%d 条判据 / 6 类绕过手法全部被拦）\n' "${PASS}"
