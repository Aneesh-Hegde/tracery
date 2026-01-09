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

type ServiceBResponse struct {
	OrderID       string `json:"order_id"`
	ProcessedAt   string `json:"processed_at"`
	PaymentStatus string `json:"payment_status"`
}

func initTracer() func() {
	ctx := context.Background()

	otelEndPoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if otelEndPoint == "" {
		otelEndPoint = "otel-collector:4317"
	}

	conn, err := grpc.NewClient(otelEndPoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("Error in establishing grpc connection: %v", err)
	}

	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		log.Fatalf("Error in creating trace exporter: %v", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName("service-a-gateway"),
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

	tracer = tp.Tracer("service-a-gateway")

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

type OrderResponse struct {
	TraceID       string  `json:"trace_id"`
	OrderID       string  `json:"order_id"`
	Status        string  `json:"status"`
	ProcessedBy   string  `json:"processed_by"`
	PaymentStatus string  `json:"payment_status"`
	Amount        float64 `json:"amount"`
}

func handleOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	span := trace.SpanFromContext(ctx)

	traceID := span.SpanContext().TraceID().String()

	log.Printf("[Service A] Received request - TraceID: %s", traceID)

	span.SetAttributes(
		attribute.String("service", "api-gateway"),
		attribute.String("handler", "handleOrder"),
	)

	var orderReq OrderRequest
	if err := json.NewDecoder(r.Body).Decode(&orderReq); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		span.RecordError(err)
	}
	log.Printf("[Service A] Processing order %s for customer %s - TraceID: %s",
		orderReq.OrderID, orderReq.CustomerID, traceID)

	_, businessSpan := tracer.Start(ctx, "validate-order")
	businessSpan.SetAttributes(
		attribute.String("order_id", orderReq.OrderID),
		attribute.String("customer_id", orderReq.CustomerID),
		attribute.Float64("amount", orderReq.Amount),
	)

	time.Sleep(50 * time.Millisecond)
	businessSpan.End()

	serviceBUrl := os.Getenv("SERVICE_B_URL")
	if serviceBUrl == "" {
		serviceBUrl = "http://service-b:8080"
	}

	client := http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
		Timeout:   10 * time.Second,
	}

	reqBody, _ := json.Marshal(orderReq)
	req, err := http.NewRequestWithContext(ctx, "POST", "http://localhost:10002"+"/process",
		io.NopCloser(bytes.NewBuffer(reqBody)))
	if err != nil {
		http.Error(w, "Failed to create request", http.StatusInternalServerError)
		span.RecordError(err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	log.Printf("[Service A] Calling Service B - TraceID: %s", traceID)

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "Failed to create request", http.StatusInternalServerError)
		span.RecordError(err)
		return
	}
	defer resp.Body.Close()

	var processingResp ServiceBResponse
	if err := json.NewDecoder(resp.Body).Decode(&processingResp); err != nil {
		http.Error(w, "Failed to parse response", http.StatusInternalServerError)
		span.RecordError(err)
		return
	}

	response := OrderResponse{
		TraceID:       traceID,
		OrderID:       orderReq.OrderID,
		Status:        "completed",
		ProcessedBy:   "service-a,service-b,service-c",
		PaymentStatus: processingResp.PaymentStatus,
		Amount:        orderReq.Amount,
	}

	log.Printf("[Service A] Request completed - TraceID: %s, Status: %s",
		traceID, response.Status)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)

}

func healthCheck(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Service A is healthy")
}

func main() {
	cleanup := initTracer()
	defer cleanup()

	http.Handle("/order", otelhttp.NewHandler(
		http.HandlerFunc(handleOrder),
		"handle-order",
	))
	http.HandleFunc("/health", healthCheck)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Service A (API Gateway) starting on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
