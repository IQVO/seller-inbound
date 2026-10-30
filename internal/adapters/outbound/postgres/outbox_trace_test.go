package postgres

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/claudioed/seller-inbound/internal/application/outbox"
)

func useTraceContextPropagator(t *testing.T) {
	t.Helper()
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })
}

func headerValue(hs []outbox.Header, key string) (string, bool) {
	for _, h := range hs {
		if h.Key == key {
			return h.Value, true
		}
	}
	return "", false
}

// Under a span the stored headers carry the W3C traceparent of that span next to
// the encoder's own header, which stays untouched and first.
func TestTraceHeadersStampTheActiveSpan(t *testing.T) {
	useTraceContextPropagator(t)
	ctx, span := sdktrace.NewTracerProvider().Tracer("test").Start(context.Background(), "POST /inbound-plans/{planAction}")
	defer span.End()
	encoder := []outbox.Header{{Key: "content-type", Value: "application/cloudevents+json; charset=UTF-8"}}

	got := withTraceHeaders(encoder, traceHeaders(ctx))

	if len(got) != 2 || got[0] != encoder[0] {
		t.Fatalf("headers = %v, want the encoder's header first, unchanged", got)
	}
	tp, ok := headerValue(got, "traceparent")
	if !ok {
		t.Fatalf("headers %v carry no traceparent", got)
	}
	mc := propagation.MapCarrier{"traceparent": tp}
	extracted := trace.SpanContextFromContext(propagation.TraceContext{}.Extract(context.Background(), mc))
	if extracted.TraceID() != span.SpanContext().TraceID() || extracted.SpanID() != span.SpanContext().SpanID() {
		t.Errorf("traceparent %q is not the active span's %s/%s", tp, span.SpanContext().TraceID(), span.SpanContext().SpanID())
	}
	if len(encoder) != 1 {
		t.Errorf("the encoder's slice was modified: %v", encoder)
	}
}

// Without a span (a background job, a test) the message is stored exactly as the
// encoder built it.
func TestTraceHeadersAddNothingWithoutASpan(t *testing.T) {
	useTraceContextPropagator(t)
	encoder := []outbox.Header{{Key: "content-type", Value: "x"}}

	got := withTraceHeaders(encoder, traceHeaders(context.Background()))

	if len(got) != 1 {
		t.Fatalf("headers = %v, want only the encoder's", got)
	}
}

// An encoder-set header always wins over a propagated one.
func TestWithTraceHeadersNeverOverwritesAnEncoderHeader(t *testing.T) {
	got := withTraceHeaders(
		[]outbox.Header{{Key: "traceparent", Value: "encoder"}},
		[]outbox.Header{{Key: "traceparent", Value: "propagated"}, {Key: "tracestate", Value: "a=b"}},
	)
	if v, _ := headerValue(got, "traceparent"); v != "encoder" || len(got) != 2 {
		t.Fatalf("headers = %v", got)
	}
}
