.PHONY: run build tidy test lint fmt vet docker-build seed-demo

run:
	go run ./cmd/server

build:
	go build -o /tmp/sellingbot ./cmd/server

tidy:
	go mod tidy

test:
	go test ./... -coverprofile=coverage.out

lint:
	golangci-lint run

fmt:
	gofmt -s -w .
	goimports -w .

vet:
	go vet ./...

docker-build:
	docker build -t sellingbot:latest .

seed-demo:
	@echo "Seeding demo data (if any)..."
