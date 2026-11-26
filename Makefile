ifneq (,$(wildcard .env))
include .env
export
endif

.PHONY: build synth deploy test clean local test-api-local

build:
	mkdir -p build/lambda
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o build/lambda/bootstrap ./cmd/function

synth: build
	cdk synth

deploy: build
	cdk deploy

diff: build
	cdk diff

# test:
# 	go test ./...

test:
	./test.sh

local:
	go run ./cmd/function --local

test-local:
	./test.sh --local

clean:
	rm -rf build cdk.out

destroy:
	cdk destroy
