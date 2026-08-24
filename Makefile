BINARY := gitlab-ci-mr-pipelines-hooks
PKG := ./...
IMAGE ?= gitlab-ci-mr-pipelines-hooks:latest

.PHONY: build run test vet fmt docker-build clean

build:
	go build -o $(BINARY) .

run:
	go run .

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

docker-build:
	docker build -t $(IMAGE) .

clean:
	rm -f $(BINARY)
