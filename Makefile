# Evergreen `eg` build entry point.
# Run `make help` for the supported developer commands.

BIN        := bin/eg
PKG        := ./cmd/eg
# Keep this default in sync with internal/version.DefaultVersion.
# Release builds may override it with `make release VERSION=x.y.z`.
VERSION    ?= 0.7.0-m7
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE       ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -s -w -X github.com/ikaqiu-Lemon/EverGreen/internal/version.Version=$(VERSION) -X github.com/ikaqiu-Lemon/EverGreen/internal/version.Commit=$(COMMIT) -X github.com/ikaqiu-Lemon/EverGreen/internal/version.Date=$(DATE)
PLATFORMS  := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: help build dist test test-full test-race test-manifest test-contract test-unit \
        test-e2e test-perf test-fuzz test-mutation test-go lint fmt vet fmt-check guard \
        dep-gate public-check verify-dist-provenance release clean print-version

help:
	@echo "make build    - 编译单二进制到 $(BIN) 并从当期 HEAD 重建四平台 dist/ + SHA256SUMS + PROVENANCE.txt"
	@echo "make dist     - 仅交叉编译四平台到 dist/ + SHA256SUMS + PROVENANCE.txt（不产 $(BIN)）"
	@echo "make test     - bash tests/run.sh --profile core（唯一入口；清单驱动）"
	@echo "make test-full - bash tests/run.sh --profile full（全量一次遍历，去重）"
	@echo "make test-race - bash tests/run.sh --profile race（-race 子集）"
	@echo "make test-go   - go test ./...（裸工具链逃生口，不是验收口径）"
	@echo "make lint     - gofmt + go vet + 写路径守卫 + 依赖方向 + 公开仓卫生"
	@echo "make fmt      - gofmt -w ."
	@echo "make release  - clean 后 CGO_ENABLED=0 交叉编译四平台到 dist/ + SHA256SUMS"
	@echo "make clean    - 删除 bin/ dist/"
	@echo "make print-version - 打印 VERSION 缺省值（版本号一致性判据消费）"

# print-version 只打印缺省版本号（无副作用），供文档与 e2e 做同源比对。
print-version:
	@echo $(VERSION)

# build：先产本机单二进制 $(BIN)，再**从当期 HEAD** 一次性重建四平台 dist/ 产物与 SHA256SUMS。
# 关闭 phaseA A-F-01（dist 构建自旧 commit）：dist 产物的 -ldflags COMMIT 恒取 `git rev-parse --short HEAD`，
# 溯源可复算——重建产物的 `--version` commit 段 == 当期 HEAD（构建时间戳注入使字节逐次不同，故 SHA 随每次重建刷新，
# 但 commit 溯源恒定；见 INSTALL.md「dist 产物与源码 commit 的对应关系」）。
build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) $(PKG)
	@$(MAKE) --no-print-directory dist

# dist：四平台交叉编译 + 校验和 + 当期溯源（不删既有 bin/），供 build / release 复用。
dist:
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "build dist/eg_$${os}_$${arch}"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' \
			-o dist/eg_$${os}_$${arch} $(PKG) || exit 1; \
	done
	@cd dist && (sha256sum eg_* > SHA256SUMS 2>/dev/null || shasum -a 256 eg_* > SHA256SUMS)
	@{ \
		echo "EverGreen release provenance"; \
		echo "version: $(VERSION)"; \
		echo "commit: $(COMMIT)"; \
		echo "built_at: $(DATE)"; \
		echo "sha256sums: dist/SHA256SUMS"; \
		echo; \
		echo "artifact checksums:"; \
		cat dist/SHA256SUMS; \
	} > dist/PROVENANCE.txt
	@$(MAKE) --no-print-directory verify-dist-provenance
	@echo "dist 产物："; ls -l dist

verify-dist-provenance:
	@[ -f dist/SHA256SUMS ] || { echo "dist/SHA256SUMS missing; run make dist" >&2; exit 1; }
	@[ -f dist/PROVENANCE.txt ] || { echo "dist/PROVENANCE.txt missing; run make dist" >&2; exit 1; }
	@tmp=$$(mktemp); sed -n '/^artifact checksums:$$/,$$p' dist/PROVENANCE.txt | tail -n +2 > $$tmp; \
		cmp -s dist/SHA256SUMS $$tmp || { echo "dist/PROVENANCE.txt artifact checksums differ from dist/SHA256SUMS" >&2; rm -f $$tmp; exit 1; }; \
		rm -f $$tmp
	@cd dist && (sha256sum -c SHA256SUMS >/dev/null 2>&1 || shasum -a 256 -c SHA256SUMS >/dev/null 2>&1)
	@echo "dist provenance: SHA256SUMS and PROVENANCE.txt are consistent"

# 唯一测试入口（合同 D4）：所有 profile 都从 tests/manifest/suites.yaml 派生执行计划。
# `make test` 不再是 `go test ./...`——后者只覆盖 go 单测，看不见 contract / e2e / 清单自检，
# 且无法判"零执行 / 隐式 skip"。裸工具链仍保留在 `make test-go`，但它不是验收口径。
test:
	@bash tests/run.sh --profile core

test-full:
	@bash tests/run.sh --profile full

test-race:
	@bash tests/run.sh --profile race

test-manifest:
	@bash tests/run.sh --profile manifest

test-contract:
	@bash tests/run.sh --profile contract

test-unit:
	@bash tests/run.sh --profile unit

test-e2e:
	@bash tests/run.sh --profile e2e

test-perf:
	@bash tests/run.sh --profile perf

test-fuzz:
	@bash tests/run.sh --profile fuzz

test-mutation:
	@bash tests/run.sh --profile mutation

# 逃生口：只跑 go 工具链，不经清单。保留它是为了排查 runner 自身问题时有对照，
# **不得**用它替代 `make test` 做验收（覆盖面不同，且不判零执行 / 隐式 skip）。
test-go:
	go test ./...

lint: fmt-check vet guard dep-gate public-check

# 测试体系外置（合同 ADR-T1 / D3）配套：`.tests-staging/` 是 materializer 的**生成物**
# （产品源码 + 测试文件的副本），不是仓库源码。gofmt 是纯文件遍历，会走进该目录，
# 于是「一个旧 run 里的历史副本」会让 fmt-check 无故变红（实测发生）。
# go vet ./... 不受影响（go 工具链本就忽略以 . / _ 开头的目录）。
# 判据不放宽：仓库内**全部** .go（含测试权威位 tests/_staged/）仍逐个检查，只排除生成物目录。
GOFILES = $(shell find . -name '*.go' -not -path './.tests-staging/*' | sort)

fmt:
	gofmt -w $(GOFILES)

fmt-check:
	@out=$$(gofmt -l $(GOFILES)); if [ -n "$$out" ]; then echo "gofmt 未通过，以下文件需格式化："; echo "$$out"; exit 1; fi
	@echo "gofmt -l（排除 .tests-staging 生成物）: 无输出，通过"

vet:
	go vet ./...

# 写路径硬约束（选型决策 I-2 / I-5）：写路径禁止 YAML 序列化回写。
guard:
	@if grep -rn --include='*.go' -E 'yaml\.Marshal|yaml\.NewEncoder' internal/ cmd/; then \
		echo "ERROR: 写路径禁止 YAML 序列化（yaml.Marshal / yaml.NewEncoder），只允许只读解析 + 字节区间写入"; \
		exit 1; \
	fi
	@echo "guard: 写路径无 yaml.Marshal / yaml.NewEncoder，通过"

# §13 依赖方向硬门禁（M6 · T-…-074）：index⊥store、ADR-20 filter 文件级隔离、状态写口唯一。
# 判据与实现见 tests/contract/dep-direction/dep_direction_gate.sh
# （测试资产统一迁入 tests/ 后的新位置；离线、零交互、与 go test 的 arch/store 护栏互为冗余）。
dep-gate:
	@bash tests/contract/dep-direction/dep_direction_gate.sh

public-check:
	@bash scripts/check-public.sh

release: clean dist
	@echo "release 产物："; ls -l dist

clean:
	rm -rf bin dist
