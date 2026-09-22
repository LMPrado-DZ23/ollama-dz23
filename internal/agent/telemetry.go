package agent

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type Telemetry struct {
	provider *trace.TracerProvider
	tracer   oteltrace.Tracer
}

func NewTelemetry(ctx context.Context, endpoint string) (*Telemetry, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return &Telemetry{tracer: oteltrace.NewNoopTracerProvider().Tracer("ollama-dz23-agent")}, nil
	}
	if !strings.HasPrefix(endpoint, "https://") && osBool("OLLAMA_AGENT_OTLP_ALLOW_INSECURE") != "1" {
		return nil, errors.New("OTLP endpoint must use HTTPS unless insecure mode is explicitly enabled")
	}
	options := []otlptracehttp.Option{otlptracehttp.WithEndpointURL(endpoint)}
	if osBool("OLLAMA_AGENT_OTLP_ALLOW_INSECURE") == "1" {
		options = append(options, otlptracehttp.WithInsecure())
	}
	exporter, err := otlptracehttp.New(ctx, options...)
	if err != nil {
		return nil, err
	}
	provider := trace.NewTracerProvider(trace.WithBatcher(exporter))
	return &Telemetry{provider: provider, tracer: provider.Tracer("ollama-dz23-agent")}, nil
}

func (t *Telemetry) Start(ctx context.Context, name string, attrs map[string]string) (context.Context, oteltrace.Span) {
	if t == nil || t.tracer == nil {
		tracer := oteltrace.NewNoopTracerProvider().Tracer("ollama-dz23-agent")
		return tracer.Start(ctx, name)
	}
	pairs := make([]attribute.KeyValue, 0, len(attrs))
	for key, value := range attrs {
		pairs = append(pairs, attribute.String(key, value))
	}
	return t.tracer.Start(ctx, name, oteltrace.WithAttributes(pairs...))
}
func (t *Telemetry) Shutdown(ctx context.Context) error {
	if t == nil || t.provider == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return t.provider.Shutdown(shutdownCtx)
}
func osBool(key string) string { return strings.TrimSpace(os.Getenv(key)) }
