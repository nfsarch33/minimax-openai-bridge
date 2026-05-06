.PHONY: test vet build docker-build

test:
	go test -race ./...

vet:
	go vet ./...

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -ldflags="-w -s" -o bin/minimax-openai-bridge ./cmd/minimax-openai-bridge

docker-build:
	docker build -t minimax-openai-bridge:local .
