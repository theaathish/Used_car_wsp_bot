.PHONY: run build tidy seed-demo
run:
	go run ./cmd/server
build:
	go build -o /tmp/sellingbot ./cmd/server
tidy:
	go mod tidy
