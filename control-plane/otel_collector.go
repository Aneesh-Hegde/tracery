package main

import (
	"context"
	"log"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// OTELReceiver receives traces from OpenTelemetry Collector
type OTELReceiver struct {
	controlPlane *ControlPlaneServer
	processor    *BreakpointSpanProcessor
}

// BreakpointSpanProcessor processes spans and checks for breakpoint matches
type BreakpointSpanProcessor struct {
	controlPlane *ControlPlaneServer
}

func NewOTELReceiver(cp *ControlPlaneServer) *OTELReceiver {
	return &OTELReceiver{
		controlPlane: cp,
		processor:    &BreakpointSpanProcessor{controlPlane: cp},
	}
}

// Start begins receiving traces from OTEL Collector
func (or *OTELReceiver) Start() error {
	ctx := context.Background()

	// Connect to OTEL Collector
	conn, err := grpc.DialContext(ctx, "otel-collector:4317",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithTimeout(5*time.Second))
	if err != nil {
		log.Printf("[OTELReceiver] Warning: Could not connect to OTEL Collector: %v", err)
		log.Printf("[OTELReceiver] Running without real trace monitoring")
		return err
	}

	// Create OTLP exporter (we use it as a receiver by registering our processor)
	exporter, err := otlptrace.New(ctx, otlptracegrpc.NewClient(
		otlptracegrpc.WithGRPCConn(conn),
	))
	if err != nil {
		return err
	}

	// Create resource
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("dcdot-control-plane"),
			attribute.String("component", "trace-receiver"),
		),
	)
	if err != nil {
		return err
	}

	// Create trace provider with our custom processor
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(or.processor),
	)

	log.Println("[OTELReceiver] Connected to OTEL Collector, monitoring traces...",tp)
	
	// Keep running
	select {}
}

// OnStart is called when a span starts
func (bsp *BreakpointSpanProcessor) OnStart(parent context.Context, s sdktrace.ReadWriteSpan) {
	spanContext := s.SpanContext()
	traceID := spanContext.TraceID().String()
	
	// Extract service name from resource
	serviceName := "unknown"
	if resource := s.Resource(); resource != nil {
		for _, attr := range resource.Attributes() {
			if attr.Key == semconv.ServiceNameKey {
				serviceName = attr.Value.AsString()
				break
			}
		}
	}
	
	// Extract endpoint/operation name
	endpoint := s.Name()
	
	// Extract span attributes
	attributes := make(map[string]string)
	for _, attr := range s.Attributes() {
		attributes[string(attr.Key)] = attr.Value.Emit()
	}
	
	// Add HTTP-specific attributes if available
	if httpMethod, ok := attributes["http.method"]; ok {
		if httpTarget, ok := attributes["http.target"]; ok {
			endpoint = httpMethod + " " + httpTarget
		}
	}
	
	log.Printf("[OTELReceiver] Trace: %s | Service: %s | Endpoint: %s", 
		traceID[:16]+"...", serviceName, endpoint)
	
	// Check if this trace matches any breakpoint
	breakpoint := bsp.controlPlane.CheckBreakpoint(serviceName, endpoint, attributes)
	if breakpoint != nil {
		log.Printf("[OTELReceiver] 🎯 BREAKPOINT HIT! Trace: %s | Breakpoint: %s", 
			traceID, breakpoint.ID)
		
		// Trigger freeze
		bsp.controlPlane.OnBreakpointHit(traceID, breakpoint)
	}
}

// OnEnd is called when a span ends
func (bsp *BreakpointSpanProcessor) OnEnd(s sdktrace.ReadOnlySpan) {
	// Nothing to do on span end for now
}

// Shutdown shuts down the processor
func (bsp *BreakpointSpanProcessor) Shutdown(ctx context.Context) error {
	return nil
}

// ForceFlush forces the processor to flush
func (bsp *BreakpointSpanProcessor) ForceFlush(ctx context.Context) error {
	return nil
}
