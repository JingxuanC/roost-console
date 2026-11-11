.PHONY: api web test build

api:
	cd apps/api && go run ./cmd/roost-console

web:
	cd apps/web && npm run dev

test:
	cd apps/api && go test ./...
	cd apps/web && npx tsc -b

build:
	cd apps/api && go build -o ../../bin/roost-console ./cmd/roost-console
	cd apps/web && npm run build
