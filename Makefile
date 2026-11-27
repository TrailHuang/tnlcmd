# TNLCMD 本机编译与验证
# 仅编译当前平台（GOOS/GOARCH 取自本机）可运行的二进制

BIN_DIR := bin

.PHONY: all example zebra_example build vet test test-fast clean

## all: 编译全部示例二进制（默认目标）
all: example zebra_example

## example: 编译简单示例（端口 2324）→ bin/example
example:
	go build -o $(BIN_DIR)/example ./examples/example

## zebra_example: 编译 Zebra 风格示例（端口 2323）→ bin/zebra_example
zebra_example:
	go build -o $(BIN_DIR)/zebra_example ./cmd/zebra_example

## build: 编译检查（不产出二进制）
build:
	go build ./...

## vet: 静态检查（含集成测试代码）
vet:
	go vet -tags integration ./...

## test: 全量并发安全测试，含集成测试（提交前必跑）
test:
	go test -race -count=1 -tags integration ./...

## test-fast: 仅单元测试（跳过集成测试，日常快速验证）
test-fast:
	go test -race -count=1 ./...

## clean: 清理编译产物
clean:
	rm -rf $(BIN_DIR)
