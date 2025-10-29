package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var tracer trace.Tracer

func initTracer() func() {
	ctx := context.Background()

	otelEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if otelEndpoint == "" {
		otelEndpoint = "otel-collector:4317"
	}

	conn, err := grpc.DialContext(ctx, otelEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		log.Fatalf("Failed to create gRPC connection: %v", err)
	}

	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		log.Fatalf("Failed to create trace exporter: %v", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("service-b-processing"),
			semconv.ServiceVersion("1.0.0"),
			attribute.String("environment", "development"),
		),
	)
	if err != nil {
		log.Fatalf("Failed to create resource: %v", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	tracer = tp.Tracer("service-b-processing")

	return func() {
		if err := tp.Shutdown(ctx); err != nil {
			log.Printf("Error shutting down tracer provider: %v", err)
		}
	}
}

type OrderRequest struct {
	OrderID    string  `json:"order_id"`
	CustomerID string  `json:"customer_id"`
	Amount     float64 `json:"amount"`
}

type ProcessingResponse struct {
	OrderID       string `json:"order_id"`
	ProcessedAt   string `json:"processed_at"`
	PaymentStatus string `json:"payment_status"`
}

func handleProcess(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	span := trace.SpanFromContext(ctx)
	
	traceID := span.SpanContext().TraceID().String()
	
	log.Printf("[Service B] Received order processing request - TraceID: %s", traceID)

	span.SetAttributes(
		attribute.String("service", "order-processing"),
		attribute.String("handler", "handleProcess"),
	)

	var orderReq OrderRequest
	if err := json.NewDecoder(r.Body).Decode(&orderReq); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		span.RecordError(err)
		return
	}

	log.Printf("[Service B] Processing order %s - TraceID: %s", orderReq.OrderID, traceID)

	// Create child span for order validation
	_, validationSpan := tracer.Start(ctx, "validate-order-details")
	validationSpan.SetAttributes(
		attribute.String("order.id", orderReq.OrderID),
		attribute.Float64("order.amount", orderReq.Amount),
	)
	time.Sleep(100 * time.Millisecond) // Simulate validation
	validationSpan.End()

	// Create child span for inventory check
	_, inventorySpan := tracer.Start(ctx, "check-inventory")
	inventorySpan.SetAttributes(
		attribute.String("order.id", orderReq.OrderID),
		attribute.Bool("inventory.available", true),
	)
	time.Sleep(75 * time.Millisecond) // Simulate inventory check
	inventorySpan.End()

	// Call Service C (Payment Service)
	serviceCURL := os.Getenv("SERVICE_C_URL")
	if serviceCURL == "" {
		serviceCURL = "http://service-c:8080"
	}

	client := http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
		Timeout:   10 * time.Second,
	}

	paymentReq := map[string]interface{}{
		"order_id":    orderReq.OrderID,
		"customer_id": orderReq.CustomerID,
		"amount":      orderReq.Amount,
	}
	reqBody, _ := json.Marshal(paymentReq)
	
	req, err := http.NewRequestWithContext(ctx, "POST", 
		serviceCURL+"/payment", 
		io.NopCloser(bytes.NewBuffer(reqBody)))
	if err != nil {
		http.Error(w, "Failed to create payment request", http.StatusInternalServerError)
		span.RecordError(err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	log.Printf("[Service B] Calling Service C for payment - TraceID: %s", traceID)
	
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "Failed to call service-c", http.StatusInternalServerError)
		span.RecordError(err)
		return
	}
	defer resp.Body.Close()

	var paymentResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&paymentResp); err != nil {
		http.Error(w, "Failed to parse payment response", http.StatusInternalServerError)
		span.RecordError(err)
		return
	}

	response := ProcessingResponse{
		OrderID:       orderReq.OrderID,
		ProcessedAt:   time.Now().Format(time.RFC3339),
		PaymentStatus: paymentResp["status"].(string),
	}

	log.Printf("[Service B] Order processed successfully - TraceID: %s, Payment: %s", 
		traceID, response.PaymentStatus)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func healthCheck(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Service B is healthy")
}

func main() {
	cleanup := initTracer()
	defer cleanup()

	http.Handle("/process", otelhttp.NewHandler(
		http.HandlerFunc(handleProcess),
		"handle-process",
	))
	http.HandleFunc("/health", healthCheck)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Service B (Order Processing) starting on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
