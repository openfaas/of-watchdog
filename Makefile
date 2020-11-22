
.GIT_COMMIT=$(shell git rev-parse HEAD)
.GIT_VERSION=$(shell git describe --tags --always --dirty 2>/dev/null)
.GIT_UNTRACKEDCHANGES := $(shell git status --porcelain --untracked-files=no)
ifneq ($(.GIT_UNTRACKEDCHANGES),)
	.GIT_VERSION := $(.GIT_VERSION)-$(shell date +"%s")
endif


.IMAGE=ghcr.io/openfaas/of-watchdog
TAG?=latest

export GOFLAGS=-mod=vendor

.PHONY: test
test: fmt
	@echo "+ $@"
	@go test -v ./...

.PHONY: fmt
fmt:
	@echo "+ $@"
	@gofmt -l -d $(shell find . -type f -name '*.go' -not -path "./vendor/*")


.PHONY: build
build:
	@echo "+ $@"
	@docker build \
		--build-arg GIT_COMMIT=${.GIT_COMMIT} \
		--build-arg VERSION=${.GIT_VERSION} \
		-t ${.IMAGE}:${TAG} .

.PHONY: redist
redist:
	@echo "+ $@"
	@docker build \
		--build-arg GIT_COMMIT=${.GIT_COMMIT} \
		--build-arg VERSION=${.GIT_VERSION} \
		-f Dockerfile.redist \
		-t ${.IMAGE}:${TAG} .
	@./ci/copy_redist.sh ${.IMAGE} ${TAG}
	@./ci/hashgen.sh

# use this with
# `./ci/copy_redist.sh $(make print-image) && ./ci/hashgen.sh`
print-image:
	@echo ${.IMAGE}
