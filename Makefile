.PHONY: check fmt test test-race

check: fmt test
	go vet ./...

fmt:
	gofmt -w $$(find . -type f -name '*.go' -not -path './vendor/*')

test:
	go test ./...

test-race:
	go test -race ./...
