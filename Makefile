export RELEASE_VERSION ?= dev

.PHONY: build
build:
	goreleaser build --clean --single-target --snapshot
	@echo "go to '.tdl/dist' directory to see the package!"

.PHONY: packaging
packaging:
	goreleaser release --skip=publish --snapshot --clean
	@echo "go to '.tdl/dist' directory to see the packages!"
