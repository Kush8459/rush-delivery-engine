//go:build integration

// Integration test that exercises the full order → assignment chain against
// real Postgres + Redis + Kafka containers. Opt-in (build tag) because it
// takes ~30s to spin up containers and requires a running Docker daemon.
//
// Run with:   go test -tags integration -v ./test/integration/...

package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Kush8459/rush-delivery-engine/internal/assignment"
	"github.com/Kush8459/rush-delivery-engine/internal/order"
	dekafka "github.com/Kush8459/rush-delivery-engine/pkg/kafka"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestOrderAssignmentFlow stands up the real data plane, seeds a rider, places
// an order through the order service, starts the assignment consumer, and
// asserts the order lands on a rider within a reasonable timeout.
func TestOrderAssignmentFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	log := zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.InfoLevel)

	// ---- containers ----
	pgDSN := startPostgres(t, ctx)
	redisAddr := startRedis(t, ctx)
	kafkaBrokers := startKafka(t, ctx)

	// ---- clients ----
	db, err := pgxpool.New(ctx, pgDSN)
	if err != nil {
		t.Fatalf("pg pool: %v", err)
	}
	defer db.Close()

	rdb := goredis.NewClient(&goredis.Options{Addr: redisAddr})
	defer rdb.Close()

	producer := dekafka.NewProducer(kafkaBrokers)
	defer producer.Close()

	applyMigrations(t, ctx, db)

	// ---- seed rider ----
	riderID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	seedRider(t, ctx, db, rdb, riderID, 28.6139, 77.2090)

	// ---- start assignment consumer in background ----
	assignSvc := assignment.NewService(db, rdb, producer, log)
	consumer := dekafka.NewConsumer(kafkaBrokers, dekafka.TopicOrderPlaced, "it.assignment", log)
	defer consumer.Close()

	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		_ = consumer.Run(ctx, assignment.Handler(assignSvc, log))
	}()

	// ---- place an order ----
	orderSvc := order.NewService(order.NewRepository(db), producer)
	o, err := orderSvc.Place(ctx, order.PlaceOrderRequest{
		CustomerID:      uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		RestaurantID:    uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		TotalAmount:     249.5,
		DeliveryAddress: "integration test",
		PickupLat:       28.6139,
		PickupLng:       77.2090,
		DropoffLat:      28.6200,
		DropoffLng:      77.2150,
	})
	if err != nil {
		t.Fatalf("place order: %v", err)
	}
	t.Logf("placed order %s", o.ID)

	// ---- poll for rider_id ----
	deadline := time.Now().Add(30 * time.Second)
	var assignedRider *uuid.UUID
	for time.Now().Before(deadline) {
		if err := db.QueryRow(ctx,
			`SELECT rider_id FROM orders WHERE id = $1`, o.ID).Scan(&assignedRider); err != nil {
			t.Fatalf("query order: %v", err)
		}
		if assignedRider != nil {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if assignedRider == nil {
		t.Fatal("order was never assigned to a rider")
	}
	if *assignedRider != riderID {
		t.Errorf("assigned to unexpected rider: got %v want %v", *assignedRider, riderID)
	}

	// Rider should now be BUSY.
	var riderStatus string
	if err := db.QueryRow(ctx, `SELECT status FROM riders WHERE id = $1`, riderID).Scan(&riderStatus); err != nil {
		t.Fatalf("query rider: %v", err)
	}
	if riderStatus != "BUSY" {
		t.Errorf("rider status: got %s want BUSY", riderStatus)
	}

	// Audit log should have both order.placed and order.assigned rows.
	var events []string
	rows, err := db.Query(ctx, `SELECT event_type FROM order_events WHERE order_id = $1 ORDER BY id`, o.ID)
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e string
		_ = rows.Scan(&e)
		events = append(events, e)
	}
	if !contains(events, "order.placed") || !contains(events, "order.assigned") {
		t.Errorf("expected order.placed and order.assigned in audit, got %v", events)
	}
}

// ---- container helpers ----

func startPostgres(t *testing.T, ctx context.Context) string {
	t.Helper()
	c, err := tcpostgres.Run(ctx,
		"postgres:15-alpine",
		tcpostgres.WithDatabase("delivery_engine"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("postgres: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("pg dsn: %v", err)
	}
	return dsn
}

func startRedis(t *testing.T, ctx context.Context) string {
	t.Helper()
	c, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		t.Fatalf("redis: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("redis host: %v", err)
	}
	port, err := c.MappedPort(ctx, "6379/tcp")
	if err != nil {
		t.Fatalf("redis port: %v", err)
	}
	return host + ":" + port.Port()
}

func startKafka(t *testing.T, ctx context.Context) []string {
	t.Helper()
	c, err := tckafka.Run(ctx,
		"confluentinc/confluent-local:7.6.0",
		tckafka.WithClusterID("test-cluster"),
	)
	if err != nil {
		t.Fatalf("kafka: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })

	brokers, err := c.Brokers(ctx)
	if err != nil {
		t.Fatalf("kafka brokers: %v", err)
	}
	// Some kafka module versions return brokers before controller is fully ready.
	time.Sleep(2 * time.Second)
	_ = wait.ForLog
	return brokers
}

// applyMigrations reads migrations/*.up.sql (relative to repo root) and
// executes them in order.
func applyMigrations(t *testing.T, ctx context.Context, db *pgxpool.Pool) {
	t.Helper()

	// Walk up from test/integration to find project root (has go.mod).
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root (no go.mod)")
		}
		dir = parent
	}

	entries, err := os.ReadDir(filepath.Join(dir, "migrations"))
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	var ups []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			ups = append(ups, e.Name())
		}
	}
	sort.Strings(ups)

	for _, f := range ups {
		b, err := os.ReadFile(filepath.Join(dir, "migrations", f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if _, err := db.Exec(ctx, string(b)); err != nil {
			t.Fatalf("apply %s: %v", f, err)
		}
	}
}

// seedRider inserts an AVAILABLE rider and seeds Redis geo, matching what
// rider-service does on a successful PATCH status=AVAILABLE.
func seedRider(t *testing.T, ctx context.Context, db *pgxpool.Pool, rdb *goredis.Client, id uuid.UUID, lat, lng float64) {
	t.Helper()
	if _, err := db.Exec(ctx, `
		INSERT INTO riders (id, name, phone, status)
		VALUES ($1, 'IT Rider', '+919999999999', 'AVAILABLE')
		ON CONFLICT (phone) DO UPDATE SET status='AVAILABLE'
	`, id); err != nil {
		t.Fatalf("seed rider pg: %v", err)
	}
	if err := rdb.GeoAdd(ctx, assignment.RidersGeoKey, &goredis.GeoLocation{
		Name: id.String(), Latitude: lat, Longitude: lng,
	}).Err(); err != nil {
		t.Fatalf("seed rider redis: %v", err)
	}
}

func contains(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}
