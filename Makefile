.PHONY: build master gateway agent clean dev

build: master gateway agent

master:
	go build -o bin/master ./cmd/master/

gateway:
	go build -o bin/gateway ./cmd/gateway/

agent:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/agent ./cmd/agent/

clean:
	rm -rf bin/

dev-master:
	go run ./cmd/master/

dev-gateway:
	go run ./cmd/gateway/

dev-agent:
	go run ./cmd/agent/

docker:
	docker-compose up --build

docker-down:
	docker-compose down
