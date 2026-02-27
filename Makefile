# Easy Proxies - 多架构 Docker 镜像构建
REGISTRY    := ghcr.io
IMAGE_NAME  := adiecho/easy_proxies
PLATFORMS   := linux/amd64,linux/arm64
BUILDER     := multiarch

# 版本号：优先使用 git tag，否则用 git short hash
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

FULL_IMAGE  := $(REGISTRY)/$(IMAGE_NAME)

# ============================================================

.PHONY: help builder login build push push-version clean inspect

help: ## 显示帮助信息
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

builder: ## 创建多平台构建器（首次使用需要执行）
	@docker buildx inspect $(BUILDER) >/dev/null 2>&1 || \
		docker buildx create --name $(BUILDER) --driver docker-container --bootstrap
	@docker buildx use $(BUILDER)
	@echo "✓ 构建器 $(BUILDER) 已就绪"

login: ## 登录 GHCR（需要设置 GITHUB_TOKEN 环境变量）
	@echo $(GITHUB_TOKEN) | docker login $(REGISTRY) -u $(GITHUB_USER) --password-stdin
	@echo "✓ 已登录 $(REGISTRY)"

build: builder ## 本地构建（仅当前平台，用于测试）
	docker buildx build --load --tag $(FULL_IMAGE):local .
	@echo "✓ 本地镜像构建完成: $(FULL_IMAGE):local"

push: builder ## 构建多架构镜像并推送 :latest
	docker buildx build \
		--platform $(PLATFORMS) \
		--tag $(FULL_IMAGE):latest \
		--push .
	@echo "✓ 已推送 $(FULL_IMAGE):latest"

push-version: builder ## 构建多架构镜像并推送 :latest + :VERSION（可用 VERSION=v1.2.0 指定）
	docker buildx build \
		--platform $(PLATFORMS) \
		--tag $(FULL_IMAGE):latest \
		--tag $(FULL_IMAGE):$(VERSION) \
		--push .
	@echo "✓ 已推送 $(FULL_IMAGE):latest 和 $(FULL_IMAGE):$(VERSION)"

inspect: ## 查看远程镜像的多架构信息
	docker buildx imagetools inspect $(FULL_IMAGE):latest

clean: ## 删除多平台构建器
	docker buildx rm $(BUILDER) 2>/dev/null || true
	@echo "✓ 构建器 $(BUILDER) 已删除"
