SHELL := /bin/bash
VERSION ?= v1.0.0
BUILD_FLAGS=-s -w -X main.version=${VERSION}
TRIM_FLAGS=

build:
	@mkdir -p bin && go build ${TRIM_FLAGS} -ldflags "${BUILD_FLAGS}" -o bin/modernfbv main.go

.PHONY: build
