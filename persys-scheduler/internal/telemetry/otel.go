package telemetry

import (
	"context"
	"net/url"
	"strings"

	cfgpkg "github.com/persys-dev/persys-cloud/persys-scheduler/internal/config"
	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/logging"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
)

func SetupOpenTelemetry(ctx context.Context, serviceName string, cfg *cfgpkg.Config) (func(context.Context) error, error) {
	telemetryLogger := logging.C("telemetry.otel")
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		if err != nil {
			telemetryLogger.WithError(err).Warn("OpenTelemetry runtime error")
		}
	}))

	endpoint := strings.TrimSpace(cfg.OTLPEndpoint)
	if endpoint == "" {
		endpoint = strings.TrimSpace(cfg.JaegerEndpoint)
	}
	if endpoint == "" {
		telemetryLogger.Info("OpenTelemetry exporter disabled: OTEL endpoint not configured")
		return func(context.Context) error { return nil }, nil
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

	if cfg.OTLPInsecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, err
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(
			attribute.String("service.name", serviceName),
		),
	)
	if err != nil {
		return nil, err
	}

	tracerProvider := tracesdk.NewTracerProvider(
		tracesdk.WithBatcher(exporter),
		tracesdk.WithResource(res),
	)

	otel.SetTracerProvider(tracerProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tracerProvider.Shutdown, nil
}
