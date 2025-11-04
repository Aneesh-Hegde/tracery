package main

import (
	"context"
	"log"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	collectorpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/common/v1"
)

type OTelCollector struct {
	collectorpb.UnimplementedTraceServiceServer
	traceMonitor *TraceMonitor
}

func NewOTelCollector(tm *TraceMonitor) (*OTelCollector, error) {
return &OTelCollector{
		traceMonitor: tm,
	}, nil
}


func (oc *OTelCollector) ProcessTraceData(td ptrace.Traces) {
	log.Printf("[OTelCollector] Processing %d resource spans", td.ResourceSpans().Len())
	for i := 0; i < td.ResourceSpans().Len(); i++ {
		rs := td.ResourceSpans().At(i)

		// Extract service name from resource attributes
		serviceName := extractServiceName(rs.Resource().Attributes())

		for j := 0; j < rs.ScopeSpans().Len(); j++ {
			ss := rs.ScopeSpans().At(j)

			for k := 0; k < ss.Spans().Len(); k++ {
				span := ss.Spans().At(k)

				// Extract trace ID
				traceID := span.TraceID().String()

				// Extract endpoint (span name typically contains this)
				endpoint := span.Name()

				// Extract attributes
				attributes := extractAttributes(span.Attributes())

				// Process the trace through TraceMonitor
				log.Printf("[OTelCollector] Received span: TraceID=%s, Service=%s, Endpoint=%s",
					traceID, serviceName, endpoint)

				oc.traceMonitor.ProcessTrace(traceID, serviceName, endpoint, attributes)
			}
		}
	}
}



func extractServiceName(attrs pcommon.Map) string {
	if val, ok := attrs.Get("service.name"); ok {
		return val.Str()
	}
	return "unknown"
}

func extractAttributes(attrs pcommon.Map) map[string]string {
	result := make(map[string]string)
	attrs.Range(func(k string, v pcommon.Value) bool {
		result[k] = v.AsString()
		return true
	})
	return result
}

// Webhook receiver for OTel Collector to push traces
func (oc *OTelCollector) Export(ctx context.Context, req *collectorpb.ExportTraceServiceRequest) (*collectorpb.ExportTraceServiceResponse, error) {
	// Convert protobuf to pdata
	td := ptrace.NewTraces()

	for _, rs := range req.ResourceSpans {
		resourceSpan := td.ResourceSpans().AppendEmpty()

		// Copy resource attributes
		if rs.Resource != nil {
			for _, attr := range rs.Resource.Attributes {
				copyAttribute(resourceSpan.Resource().Attributes(), attr)
			}
		}

		// Process scope spans
		for _, ss := range rs.ScopeSpans {
			scopeSpan := resourceSpan.ScopeSpans().AppendEmpty()

			// Process each span
			for _, s := range ss.Spans {
				span := scopeSpan.Spans().AppendEmpty()

				// Copy trace and span IDs
				var traceID [16]byte
				var spanID [8]byte
				copy(traceID[:], s.TraceId)
				copy(spanID[:], s.SpanId)

				span.SetTraceID(pcommon.TraceID(traceID))
				span.SetSpanID(pcommon.SpanID(spanID))
				span.SetName(s.Name)

				// Copy attributes
				for _, attr := range s.Attributes {
					copyAttribute(span.Attributes(), attr)
				}
			}
		}
	}

	// Process the traces
	oc.ProcessTraceData(td)

	return &collectorpb.ExportTraceServiceResponse{}, nil
}

func copyAttribute(dest pcommon.Map, src *tracepb.KeyValue) {
	switch v := src.Value.Value.(type) {
	case *tracepb.AnyValue_StringValue:
		dest.PutStr(src.Key, v.StringValue)
	case *tracepb.AnyValue_IntValue:
		dest.PutInt(src.Key, v.IntValue)
	case *tracepb.AnyValue_DoubleValue:
		dest.PutDouble(src.Key, v.DoubleValue)
	case *tracepb.AnyValue_BoolValue:
		dest.PutBool(src.Key, v.BoolValue)
	}
}
