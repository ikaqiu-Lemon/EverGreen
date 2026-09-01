# Evergreen `eg` 构建入口
#
# 统一入口：make build | test | lint | fmt | release | clean | help
# 详见 ../teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-08-31-evergreen-s1-tech-design.md#eng-baseline

BIN        := bin/eg
PKG        := ./cmd/eg
# VERSION 口径（唯一决策文档：
# ../teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-10-03-m2-release-and-version.md）：
# 本行的缺省值与 internal/version/version.go 的 Version 默认字面量、决策文档声明的版本号
# **三处同源同值**；正式产物由下面的 -ldflags 注入同一个值（`make release VERSION=x.y.z` 可覆盖）。
VERSION    ?= 0.2.0-m2
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE       ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -s -w -X evergreen/internal/version.Version=$(VERSION) -X evergreen/internal/version.Commit=$(COMMIT) -X evergreen/internal/version.Date=$(DATE)
PLATFORMS  := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: help build test lint fmt vet fmt-check guard release clean

help:
	@echo "make build    - 编译单二进制到 $(BIN)"
	@echo "make test     - go test ./..."
	@echo "make lint     - gofmt -l . + go vet ./... + 写路径守卫"
	@echo "make fmt      - gofmt -w ."
	@echo "make release  - CGO_ENABLED=0 交叉编译四平台到 dist/ + SHA256SUMS"
	@echo "make clean    - 删除 bin/ dist/"

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) $(PKG)

test:
	go test ./...

lint: fmt-check vet guard

fmt:
	gofmt -w .

fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt 未通过，以下文件需格式化："; echo "$$out"; exit 1; fi
	@echo "gofmt -l . : 无输出，通过"

vet:
	go vet ./...

# 写路径硬约束（选型决策 I-2 / I-5）：写路径禁止 YAML 序列化回写。
guard:
	@if grep -rn --include='*.go' -E 'yaml\.Marshal|yaml\.NewEncoder' internal/ cmd/; then \
		echo "ERROR: 写路径禁止 YAML 序列化（yaml.Marshal / yaml.NewEncoder），只允许只读解析 + 字节区间写入"; \
		exit 1; \
	fi
	@echo "guard: 写路径无 yaml.Marshal / yaml.NewEncoder，通过"

release: clean
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "build dist/eg_$${os}_$${arch}"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' \
			-o dist/eg_$${os}_$${arch} $(PKG) || exit 1; \
	done
	@cd dist && (sha256sum eg_* > SHA256SUMS 2>/dev/null || shasum -a 256 eg_* > SHA256SUMS)
	@echo "release 产物："; ls -l dist

clean:
	rm -rf bin dist
