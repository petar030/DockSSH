.PHONY: check fmt test test-unit test-race test-integration test-integration-race test-all vet

check: fmt vet test

fmt:
	gofmt -w $$(find . -type f -name '*.go' -not -path './vendor/*')

vet:
	go vet ./...

test: test-unit

test-unit:
	go test ./...

test-race:
	go test -race ./...

test-integration:
	go test -v -tags=integration ./...

test-integration-race:
	go test -v -race -tags=integration ./...

test-all: test-race test-integration-race
