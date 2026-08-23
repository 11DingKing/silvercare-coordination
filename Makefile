.PHONY: test race vet build run

test:
	GOTOOLCHAIN=local go test ./... -count=1

race:
	GOTOOLCHAIN=local go test -race ./... -count=1

vet:
	GOTOOLCHAIN=local go vet ./...

build:
	GOTOOLCHAIN=local go build -o silvercare-server ./cmd/server

run:
	GOTOOLCHAIN=local go run ./cmd/server
