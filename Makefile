APP_NAME := rate-limiter
BIN_DIR := bin
CMD_DIR := cmd

TAG:=$(shell date +%Y%m%d)
DOCKER_REPO:=swr.cn-east-3.myhuaweicloud.com
DOCKER_APP_GROUP:=erlang-ops

IMAGE_NAME:=$(DOCKER_REPO)/$(DOCKER_APP_GROUP)/$(APP_NAME):$(TAG)

.PHONY: build
build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(APP_NAME) ./$(CMD_DIR)/$(APP_NAME)

# .PHONY: build-web
# build-web:
# 	cd web && pnpm run build
# 	rm -rf internal/static/dist && mkdir -p internal/static/dist
# 	cp -rf web/dist/* internal/static/dist

.PHONY: build-image
build-image:
	@echo "Building Docker image..."
	docker build -t $(IMAGE_NAME) .
	@echo "Image built: $(IMAGE_NAME)"


.PHONY: clean
clean:
	rm -rf $(BIN_DIR)