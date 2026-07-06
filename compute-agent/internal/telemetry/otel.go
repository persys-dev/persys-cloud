package telemetry

import (
	"context"
	"net/url"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

// Setup initializes OpenTelemetry tracing for the compute agent.
func Setup(ctx context.Context, logger *logrus.Logger, defaultServiceName string, endpoint string) (func(context.Context) error, error) {

	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_JAEGER_ENDPOINT"))
	}
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	}
	if endpoint == "" {
		logger.Info("OpenTelemetry exporter disabled: endpoint not configured")
		return func(context.Context) error { return nil }, nil
	}

	serviceName := strings.TrimSpace(os.Getenv("OTEL_SERVICE_NAME"))
	if serviceName == "" {
		serviceName = defaultServiceName
	}

	opts := make([]otlptracehttp.Option, 0, 3)
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, err
		}
		opts = append(opts, otlptracehttp.WithEndpoint(u.Host))
		if p := strings.TrimSpace(u.Path); p != "" && p != "/" {
			opts = append(opts, otlptracehttp.WithURLPath(p))
		}
		if u.Scheme != "https" {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
	} else {
		opts = append(opts, otlptracehttp.WithEndpoint(endpoint), otlptracehttp.WithInsecure())
	}

	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, err
	}

	tp := tracesdk.NewTracerProvider(
		tracesdk.WithBatcher(exporter),
		tracesdk.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
		)),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	logger.WithFields(logrus.Fields{
		"service":  serviceName,
		"endpoint": endpoint,
	}).Info("OpenTelemetry tracing enabled")

	return tp.Shutdown, nil
}
