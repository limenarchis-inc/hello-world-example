.PHONY: run loadgen build test docker-test docker-build docker-up docker-up-loadgen docker-down docker-logs curl-examples

APP_NAME := hello-world-example
HOST_PORT ?= 8090
BASE_URL := http://localhost:$(HOST_PORT)
.DEFAULT_GOAL:=build
TAG:=$(shell git log -1 --pretty=format:"%H")

run:
	HOST_PORT=$(HOST_PORT) docker compose up

build:
	docker build -t $(APP_NAME):latest -f Dockerfile .

test:
	docker run --rm -v "$(PWD):/src" -w /src golang:1.25 go test ./...


curl-examples:
	curl -s -v $(BASE_URL)/hello | json_pp
	curl -s -v $(BASE_URL)/hello/123/world | json_pp
	curl -s -v $(BASE_URL)/hello/with/uuid/123e4567-e89b-12d3-a456-426614174000/world | json_pp
	curl -s -v $(BASE_URL)/hello/1/multi/2/path | json_pp
	curl -s -v $(BASE_URL)/error | json_pp
	curl -s -v $(BASE_URL)/external/call | json_pp
	curl -s -v $(BASE_URL)/chain | json_pp

.PHONY: build-amd64
build-amd64:
	docker buildx build --platform linux/amd64 -t $(APP_NAME):latest -f Dockerfile .

.PHONY: push-public
push-public: build-amd64
	aws ecr get-login-password --region us-east-1 | docker login --username AWS --password-stdin 096568562421.dkr.ecr.us-east-1.amazonaws.com
	docker tag $(APP_NAME):latest 096568562421.dkr.ecr.us-east-1.amazonaws.com/$(APP_NAME):latest
	docker push 096568562421.dkr.ecr.us-east-1.amazonaws.com/$(APP_NAME):latest

	docker tag $(APP_NAME):latest 096568562421.dkr.ecr.us-east-1.amazonaws.com/$(APP_NAME):$(TAG)
	docker push 096568562421.dkr.ecr.us-east-1.amazonaws.com/$(APP_NAME):$(TAG)
