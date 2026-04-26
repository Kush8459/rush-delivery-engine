.PHONY: tidy up down migrate-up migrate-down run-order run-rider run-location run-assignment run-eta run-notification build seed-rider docker-build docker-up docker-down docker-logs help

# Postgres connection string used by golang-migrate.
PG_URL ?= postgres://postgres:postgres@localhost:5433/delivery_engine?sslmode=disable

help:
	@echo "Common targets:"
	@echo "  tidy               go mod tidy"
	@echo "  up                 start infrastructure (kafka, redis, postgres)"
	@echo "  down               stop infrastructure"
	@echo "  migrate-up         apply DB migrations (requires golang-migrate CLI)"
	@echo "  migrate-down       revert latest migration"
	@echo "  build              build all six service binaries into ./bin"
	@echo "  run-order          run order-service        (:8080)"
	@echo "  run-rider          run rider-service        (:8081)"
	@echo "  run-location       run location-service     (:8083)"
	@echo "  run-notification   run notification-service (:8085)"
	@echo "  run-assignment     run assignment-service   (consumer)"
	@echo "  run-eta            run eta-service          (consumer)"
	@echo "  seed-rider         insert one AVAILABLE rider + seed Redis geo position"

tidy:
	go mod tidy

up:
	docker compose up -d

down:
	docker compose down

migrate-up:
	migrate -path migrations -database "$(PG_URL)" up

migrate-down:
	migrate -path migrations -database "$(PG_URL)" down 1

build:
	go build -o bin/order-service.exe        ./cmd/order-service
	go build -o bin/rider-service.exe        ./cmd/rider-service
	go build -o bin/location-service.exe     ./cmd/location-service
	go build -o bin/assignment-service.exe   ./cmd/assignment-service
	go build -o bin/eta-service.exe          ./cmd/eta-service
	go build -o bin/notification-service.exe ./cmd/notification-service

run-order:
	go run ./cmd/order-service

run-rider:
	go run ./cmd/rider-service

run-location:
	go run ./cmd/location-service

run-assignment:
	go run ./cmd/assignment-service

run-eta:
	go run ./cmd/eta-service

run-notification:
	go run ./cmd/notification-service

# Seed one test rider. Replace the UUID with your own if you want.
docker-build:
	docker compose --profile app build

docker-up:
	docker compose --profile app up -d

docker-down:
	docker compose --profile app down

docker-logs:
	docker compose --profile app logs -f --tail=100

RIDER_ID ?= 11111111-1111-1111-1111-111111111111
seed-rider:
	@docker compose exec -T postgres psql -U postgres -d delivery_engine -c \
		"INSERT INTO riders (id,name,phone,status) VALUES ('$(RIDER_ID)','Test Rider','+911111111111','AVAILABLE') ON CONFLICT (phone) DO UPDATE SET status='AVAILABLE';"
	@docker compose exec -T redis redis-cli GEOADD riders:geo 77.2090 28.6139 $(RIDER_ID)
	@echo "Seeded rider $(RIDER_ID) at (28.6139, 77.2090)"
