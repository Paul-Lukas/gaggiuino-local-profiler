# Phase 2a (#901): templ generate is a required codegen step before
# build/vet/test can see internal/web/templates' generated _templ.go files
# (not committed — see internal/web/templates/generate.go and go/README.md's
# Frontend section). Every target below runs it first so `make build`/
# `make test`/`make vet` work the same whether or not generate has already
# run.

.PHONY: generate build vet test fmt-check clean-generated

generate:
	go generate ./...

build: generate
	go build ./...

vet: generate
	go vet ./...

test: generate
	go test ./...

fmt-check: generate
	gofmt -l .

clean-generated:
	find . -name '*_templ.go' -delete
