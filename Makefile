.PHONY: build test vet check

build:
	CGO_ENABLED=0 go build ./...

test:
	go test ./...

vet:
	go vet ./...

check: build vet test
