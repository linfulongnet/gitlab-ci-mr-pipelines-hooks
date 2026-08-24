BINARY := gitlab-ci-mr-pipelines-hooks
PKG := ./...
IMAGE ?= gitlab-ci-mr-pipelines-hooks:latest
OUTPUT_DIR := output
CONFIG_SRC := config.example.yaml
CONFIG_DST := $(OUTPUT_DIR)/config.yaml

.PHONY: build run test vet fmt docker-build clean

# 一键编译：生成二进制与配置文件到 output/ 目录
build:
	@mkdir -p $(OUTPUT_DIR)
	go build -trimpath -ldflags="-s -w" -o $(OUTPUT_DIR)/$(BINARY) .
	@cp $(CONFIG_SRC) $(CONFIG_DST)
	@echo "构建完成："
	@ls -lh $(OUTPUT_DIR)

run:
	go run .

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

docker-build:
	docker build -t $(IMAGE) .

clean:
	rm -rf $(OUTPUT_DIR)
