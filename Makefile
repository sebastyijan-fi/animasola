APP := animasola

.PHONY: run
run:
	go run ./cmd/$(APP) --config ./config.example.yaml

.PHONY: build
build:
	go build -o ./$(APP) ./cmd/$(APP)

.PHONY: test
test:
	go test ./...

