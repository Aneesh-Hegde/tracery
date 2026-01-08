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

type PaymentResponse struct {
	PaymentID   int    `json:"payment_id"`
	OrderID     string `json:"order_id"`
	Status      string `json:"status"`
	ProcessedAt string `json:"processed_at"`
}

type PaymentRequest struct {
	OrderID    string  `json:"order_id"`
	CustomerID string  `json:"customer_id"`
	Amount     float64 `json:"amount"`
}

func initTracer() func() {
	log.Println("[Service B] Initializing tracer...")
	ctx := context.Background()

	otelEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if otelEndpoint == "" {
		otelEndpoint = "otel-collector:4317"
	}
	log.Printf("[Service B] OTel endpoint: %s", otelEndpoint)

	log.Printf("[Service B] Creating gRPC connection to %s...", otelEndpoint)
	conn, err := grpc.NewClient(otelEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("[Service B] FATAL: Failed to create gRPC connection to %s: %v", otelEndpoint, err)
	}
	log.Println("[Service B] gRPC connection created successfully")

	log.Println("[Service B] Creating trace exporter...")
	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		log.Fatalf("[Service B] FATAL: Failed to create trace exporter: %v", err)
	}
	log.Println("[Service B] Trace exporter created successfully")

	log.Println("[Service B] Creating resource with service name...")
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("service-b-processing"),
			semconv.ServiceVersion("1.0.0"),
			attribute.String("environment", "development"),
		),
	)
	if err != nil {
		log.Fatalf("[Service B] FATAL: Failed to create resource: %v", err)
	}
	log.Println("[Service B] Resource created successfully")

	log.Println("[Service B] Creating tracer provider...")
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
	log.Println("[Service B] Tracer initialized successfully ✓")

	return func() {
		log.Println("[Service B] Shutting down tracer provider...")
		if err := tp.Shutdown(ctx); err != nil {
			log.Printf("[Service B] ERROR: Failed to shutdown tracer provider: %v", err)
		} else {
			log.Println("[Service B] Tracer provider shutdown successfully")
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
	spanID := span.SpanContext().SpanID().String()

	log.Printf("[Service B] ========== NEW REQUEST ==========")
	log.Printf("[Service B] Received order processing request")
	log.Printf("[Service B] TraceID: %s", traceID)
	log.Printf("[Service B] SpanID: %s", spanID)
	log.Printf("[Service B] Method: %s, Path: %s", r.Method, r.URL.Path)
	log.Printf("[Service B] Remote Address: %s", r.RemoteAddr)

	span.SetAttributes(
		attribute.String("service", "order-processing"),
		attribute.String("handler", "handleProcess"),
	)

	// Decode request body
	log.Println("[Service B] Decoding request body...")
	var orderReq OrderRequest
	if err := json.NewDecoder(r.Body).Decode(&orderReq); err != nil {
		log.Printf("[Service B] ERROR: Failed to decode request body: %v", err)
		log.Printf("[Service B] TraceID: %s - Responding with 400 Bad Request", traceID)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		span.RecordError(err)
		return
	}
	log.Printf("[Service B] Request decoded successfully: OrderID=%s, CustomerID=%s, Amount=%.2f",
		orderReq.OrderID, orderReq.CustomerID, orderReq.Amount)

	log.Printf("[Service B] Processing order %s - TraceID: %s", orderReq.OrderID, traceID)

	// Create child span for order validation
	log.Println("[Service B] Starting order validation span...")
	_, validationSpan := tracer.Start(ctx, "validate-order-details")
	validationSpan.SetAttributes(
		attribute.String("order.id", orderReq.OrderID),
		attribute.Float64("order.amount", orderReq.Amount),
	)
	time.Sleep(100 * time.Millisecond) // Simulate validation
	validationSpan.End()
	log.Println("[Service B] Order validation completed")

	// Create child span for inventory check
	log.Println("[Service B] Starting inventory check span...")
	_, inventorySpan := tracer.Start(ctx, "check-inventory")
	inventorySpan.SetAttributes(
		attribute.String("order.id", orderReq.OrderID),
		attribute.Bool("inventory.available", true),
	)
	time.Sleep(75 * time.Millisecond) // Simulate inventory check
	inventorySpan.End()
	log.Println("[Service B] Inventory check completed")

	// Call Service C (Payment Service)
	serviceCURL := os.Getenv("SERVICE_C_URL")
	if serviceCURL == "" {
		serviceCURL = "http://service-c:8080"
	}
	log.Printf("[Service B] Service C URL configured: %s", serviceCURL)

	log.Println("[Service B] Creating HTTP client with OTel transport...")
	client := http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
		Timeout:   10 * time.Second,
	}
	log.Println("[Service B] HTTP client created successfully")

	// Prepare payment request
	log.Println("[Service B] Preparing payment request...")
	paymentReq := PaymentRequest{
		OrderID:    orderReq.OrderID,
		CustomerID: orderReq.CustomerID,
		Amount:     orderReq.Amount,
	}
	
	reqBody, err := json.Marshal(paymentReq)
	if err != nil {
		log.Printf("[Service B] ERROR: Failed to marshal payment request: %v", err)
		log.Printf("[Service B] TraceID: %s - Responding with 500", traceID)
		http.Error(w, "Failed to create payment request", http.StatusInternalServerError)
		span.RecordError(err)
		return
	}
	
	fullURL := serviceCURL + "/payment"
	log.Printf("[Service B] Calling Service C → URL: %s", fullURL)
	log.Printf("[Service B] Request body: %s", string(reqBody))
	log.Printf("[Service B] TraceID being propagated: %s", traceID)

	// Create HTTP request
	log.Println("[Service B] Creating HTTP request with context...")
	req, err := http.NewRequestWithContext(ctx, "POST",
		"http://localhost:10003"+"/payment",
		io.NopCloser(bytes.NewBuffer(reqBody)))
	if err != nil {
		log.Printf("[Service B] ERROR: Failed to create HTTP request: %v", err)
		log.Printf("[Service B] TraceID: %s - Responding with 500", traceID)
		http.Error(w, "Failed to create payment request", http.StatusInternalServerError)
		span.RecordError(err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	log.Println("[Service B] HTTP request created with headers")

	// Make the HTTP call
	log.Printf("[Service B] Sending HTTP POST to Service C... (TraceID: %s)", traceID)
	startTime := time.Now()
	resp, err := client.Do(req)
	duration := time.Since(startTime)
	
	if err != nil {
		log.Printf("[Service B] ERROR: HTTP request to Service C failed after %v", duration)
		log.Printf("[Service B] Error details: %v", err)
		log.Printf("[Service B] Target URL: %s", fullURL)
		log.Printf("[Service B] TraceID: %s - Responding with 500", traceID)
		http.Error(w, fmt.Sprintf("Failed to call service-c: %v", err), http.StatusInternalServerError)
		span.RecordError(err)
		return
	}
	defer resp.Body.Close()
	
	log.Printf("[Service B] Received response from Service C in %v", duration)
	log.Printf("[Service B] Response status: %d %s", resp.StatusCode, resp.Status)
	log.Printf("[Service B] Response headers: %v", resp.Header)

	// Read response body
	log.Println("[Service B] Reading response body...")
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[Service B] ERROR: Failed to read response body: %v", err)
		log.Printf("[Service B] TraceID: %s - Responding with 500", traceID)
		http.Error(w, "Failed to read payment response", http.StatusInternalServerError)
		span.RecordError(err)
		return
	}
	log.Printf("[Service B] Response body (raw): %s", string(bodyBytes))

	// Check HTTP status
	if resp.StatusCode != http.StatusOK {
		log.Printf("[Service B] ERROR: Service C returned non-OK status: %d", resp.StatusCode)
		log.Printf("[Service B] Response body: %s", string(bodyBytes))
		log.Printf("[Service B] TraceID: %s - Responding with 500", traceID)
		http.Error(w, fmt.Sprintf("Service C error: %s", string(bodyBytes)), http.StatusInternalServerError)
		return
	}

	// Parse response
	log.Println("[Service B] Parsing payment response...")
	var paymentResp PaymentResponse
	if err := json.Unmarshal(bodyBytes, &paymentResp); err != nil {
		log.Printf("[Service B] ERROR: Failed to unmarshal payment response: %v", err)
		log.Printf("[Service B] Response body was: %s", string(bodyBytes))
		log.Printf("[Service B] TraceID: %s - Responding with 500", traceID)
		http.Error(w, "Failed to parse payment response", http.StatusInternalServerError)
		span.RecordError(err)
		return
	}
	log.Printf("[Service B] Payment response parsed: PaymentID=%d, Status=%s",
		paymentResp.PaymentID, paymentResp.Status)

	// Build final response
	log.Println("[Service B] Building final response...")
	response := PaymentResponse{
		PaymentID:   paymentResp.PaymentID,
		OrderID:     paymentResp.OrderID,
		Status:      paymentResp.Status,
		ProcessedAt: time.Now().Format(time.RFC3339),
	}
	log.Printf("[Service B] Final response: %+v", response)

	log.Printf("[Service B] Order processed successfully - TraceID: %s, PaymentStatus: %s",
		traceID, paymentResp.Status)

	// Send response
	log.Println("[Service B] Encoding and sending response...")
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(paymentResp); err != nil {
		log.Printf("[Service B] ERROR: Failed to encode response: %v", err)
		// Can't send error here as headers already sent
		return
	}
	log.Printf("[Service B] Response sent successfully - TraceID: %s", traceID)
	log.Printf("[Service B] ========== REQUEST COMPLETE ==========\n")
}

func healthCheck(w http.ResponseWriter, r *http.Request) {
	log.Printf("[Service B] Health check from %s", r.RemoteAddr)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Service B is healthy")
}

func main() {
	log.Println("[Service B] ==========================================")
	log.Println("[Service B] Starting Service B (Order Processing)")
	log.Println("[Service B] ==========================================")
	
	cleanup := initTracer()
	defer cleanup()

	log.Println("[Service B] Setting up HTTP handlers...")
	http.Handle("/process", otelhttp.NewHandler(
		http.HandlerFunc(handleProcess),
		"handle-process",
	))
	http.HandleFunc("/health", healthCheck)
	log.Println("[Service B] HTTP handlers registered")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("[Service B] ==========================================")
	log.Printf("[Service B] Service B listening on port %s", port)
	log.Printf("[Service B] Endpoints:")
	log.Printf("[Service B]   - POST /process (Order Processing)")
	log.Printf("[Service B]   - GET  /health  (Health Check)")
	log.Printf("[Service B] ==========================================")
	
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("[Service B] FATAL: Failed to start HTTP server: %v", err)
	}
}
