SHELL := /bin/bash
BUILD_FLAGS=-s -w
TRIM_FLAGS=

build:
	@mkdir -p bin && go build ${TRIM_FLAGS} -ldflags "${BUILD_FLAGS}" -o bin/modernfbv main.go

.PHONY: build
