package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	_ "github.com/lib/pq"
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

var (
	tracer trace.Tracer
	db     *sql.DB
)

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
			semconv.ServiceName("service-c-payment"),
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

	tracer = tp.Tracer("service-c-payment")

	return func() {
		if err := tp.Shutdown(ctx); err != nil {
			log.Printf("Error shutting down tracer provider: %v", err)
		}
	}
}

func initDB() {
	dbHost := os.Getenv("DB_HOST")
	if dbHost == "" {
		dbHost = "postgres"
	}
	
	connStr := fmt.Sprintf("host=%s port=5432 user=dcdot password=dcdot123 dbname=payments sslmode=disable",
		dbHost)
	
	var err error
	for i := 0; i < 10; i++ {
		db, err = sql.Open("postgres", connStr)
		if err == nil {
			err = db.Ping()
			if err == nil {
				break
			}
		}
		log.Printf("Waiting for database connection... (attempt %d/10)", i+1)
		time.Sleep(2 * time.Second)
	}
	
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	// Create payments table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS payments (
			id SERIAL PRIMARY KEY,
			order_id VARCHAR(100) NOT NULL,
			customer_id VARCHAR(100) NOT NULL,
			amount DECIMAL(10, 2) NOT NULL,
			status VARCHAR(50) NOT NULL,
			processed_at TIMESTAMP NOT NULL,
			trace_id VARCHAR(100)
		)
	`)
	if err != nil {
		log.Fatalf("Failed to create table: %v", err)
	}

	log.Println("Database initialized successfully")
}

type PaymentRequest struct {
	OrderID    string  `json:"order_id"`
	CustomerID string  `json:"customer_id"`
	Amount     float64 `json:"amount"`
}

type PaymentResponse struct {
	PaymentID   int    `json:"payment_id"`
	OrderID     string `json:"order_id"`
	Status      string `json:"status"`
	ProcessedAt string `json:"processed_at"`
}

func handlePayment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	span := trace.SpanFromContext(ctx)
	
	traceID := span.SpanContext().TraceID().String()
	
	log.Printf("[Service C] Received payment request - TraceID: %s", traceID)

	span.SetAttributes(
		attribute.String("service", "payment-service"),
		attribute.String("handler", "handlePayment"),
	)

	var paymentReq PaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&paymentReq); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		span.RecordError(err)
		return
	}

	log.Printf("[Service C] Processing payment for order %s, amount: $%.2f - TraceID: %s", 
		paymentReq.OrderID, paymentReq.Amount, traceID)

	// Create child span for fraud check
	_, fraudSpan := tracer.Start(ctx, "fraud-detection")
	fraudSpan.SetAttributes(
		attribute.String("order.id", paymentReq.OrderID),
		attribute.Float64("payment.amount", paymentReq.Amount),
		attribute.Bool("fraud.detected", false),
	)
	time.Sleep(80 * time.Millisecond) // Simulate fraud check
	fraudSpan.End()

	// Create child span for payment processing
	_, processSpan := tracer.Start(ctx, "process-payment")
	processSpan.SetAttributes(
		attribute.String("order.id", paymentReq.OrderID),
		attribute.String("payment.method", "credit_card"),
	)
	time.Sleep(120 * time.Millisecond) // Simulate payment processing
	processSpan.End()

	// Create child span for database insertion
	_, dbSpan := tracer.Start(ctx, "insert-payment-record")
	
	var paymentID int
	err := db.QueryRowContext(ctx, `
		INSERT INTO payments (order_id, customer_id, amount, status, processed_at, trace_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, paymentReq.OrderID, paymentReq.CustomerID, paymentReq.Amount, "success", time.Now(), traceID).Scan(&paymentID)
	
	if err != nil {
		log.Printf("[Service C] Database error - TraceID: %s, Error: %v", traceID, err)
		dbSpan.RecordError(err)
		dbSpan.End()
		http.Error(w, "Failed to record payment", http.StatusInternalServerError)
		span.RecordError(err)
		return
	}
	
	dbSpan.SetAttributes(attribute.Int("payment.id", paymentID))
	dbSpan.End()

	response := PaymentResponse{
		PaymentID:   paymentID,
		OrderID:     paymentReq.OrderID,
		Status:      "success",
		ProcessedAt: time.Now().Format(time.RFC3339),
	}

	log.Printf("[Service C] Payment processed successfully - TraceID: %s, PaymentID: %d", 
		traceID, paymentID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func healthCheck(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Service C is healthy")
}

func main() {
	cleanup := initTracer()
	defer cleanup()

	initDB()
	defer db.Close()

	http.Handle("/payment", otelhttp.NewHandler(
		http.HandlerFunc(handlePayment),
		"handle-payment",
	))
	http.HandleFunc("/health", healthCheck)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Service C (Payment Service) starting on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
