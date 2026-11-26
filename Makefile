# TNLCMD 本机编译与验证
# 仅编译当前平台（GOOS/GOARCH 取自本机）可运行的二进制

BIN_DIR := bin

.PHONY: all example zebra_example test clean

## all: 编译全部示例二进制（默认目标）
all: example zebra_example

## example: 编译简单示例（端口 2324）→ bin/example
example:
	go build -o $(BIN_DIR)/example ./examples/example

## zebra_example: 编译 Zebra 风格示例（端口 2323）→ bin/zebra_example
zebra_example:
	go build -o $(BIN_DIR)/zebra_example ./cmd/zebra_example

## test: 运行全部单元测试与集成测试
test:
	go test ./...

## clean: 清理编译产物
clean:
	rm -rf $(BIN_DIR)
