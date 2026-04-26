// Package telemetry wires OpenTelemetry tracing into each service.
//
// Init installs a global TracerProvider that exports spans over OTLP HTTP
// (default target: the Jaeger all-in-one container on the `obs` compose profile).
// It also installs a W3C TraceContext + Baggage propagator so trace context
// flows across HTTP calls and Kafka messages.
//
// When Enabled is false, Init is a no-op — services run without tracing
// overhead, and any tracer.Start() calls hit the default no-op provider.
package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

type Config struct {
	Enabled      bool
	OTLPEndpoint string // host:port of an OTLP HTTP receiver, e.g. "localhost:4318"
	ServiceName  string
}

// ShutdownFunc flushes and stops the tracer provider. Call at process exit.
type ShutdownFunc func(context.Context) error

// Init sets up the global tracer provider + propagator.
// Returns a no-op ShutdownFunc when Enabled is false.
func Init(ctx context.Context, cfg Config) (ShutdownFunc, error) {
	if !cfg.Enabled {
		// Still install the propagator so services forwarding trace headers
		// (e.g. via otelhttp or Kafka headers) work uniformly in both modes.
		otel.SetTextMapPropagator(defaultPropagator())
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(cfg.OTLPEndpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(defaultPropagator())

	return tp.Shutdown, nil
}

func defaultPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}
