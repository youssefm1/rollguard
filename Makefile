.PHONY: build test demo graphs

build:
	go build -o rollguard ./cmd/rollguard

test:
	go vet ./...
	go test ./...

demo:
	go run ./cmd/rollguard demo -scenario timeout

graphs:
	go run ./cmd/rollguard demo -scenario all -quiet -out docs
	rm -f docs/*.jsonl
