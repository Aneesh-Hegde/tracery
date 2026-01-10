package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	_ "github.com/lib/pq"

	sdk "github.com/Aneesh-Hegde/tracery/sdk"
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
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"
)

var (
	tracer trace.Tracer
	db     *sql.DB
)

func initTracer() (func(context.Context) error, error) {
	log.Println("[Service C] Initializing tracer...")
	ctx := context.Background()

	otelEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if otelEndpoint == "" {
		otelEndpoint = "otel-collector:4317"
	}
	log.Printf("[Service C] OTel endpoint: %s", otelEndpoint)

	log.Printf("[Service C] Creating gRPC connection to %s...", otelEndpoint)
	conn, err := grpc.NewClient(otelEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("[Service C] FATAL: Failed to create gRPC connection to %s: %v", otelEndpoint, err)
	}
	log.Println("[Service C] gRPC connection created successfully")

	log.Println("[Service C] Creating trace exporter...")
	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		conn.Close() // Clean up connection on error
		log.Fatalf("[Service C] FATAL: Failed to create trace exporter: %v", err)
	}
	log.Println("[Service C] Trace exporter created successfully")

	log.Println("[Service C] Creating resource with service name...")
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("service-c-payment"),
			semconv.ServiceVersion("1.0.0"),
			attribute.String("environment", "development"),
		),
	)
	if err != nil {
		traceExporter.Shutdown(ctx) // Clean up exporter
		conn.Close()
		log.Fatalf("[Service C] FATAL: Failed to create resource: %v", err)
	}
	log.Println("[Service C] Resource created successfully")

	log.Println("[Service C] Creating tracer provider...")

	bsp := sdktrace.NewBatchSpanProcessor(traceExporter)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	tracer = tp.Tracer("service-c-payment")
	log.Println("[Service C] Tracer initialized successfully ✓")

	// Return a shutdown function that properly cleans up
	shutdownFunc := func(ctx context.Context) error {
		log.Println("[Service C] Shutting down tracer provider...")

		// Force flush any pending spans
		log.Println("[Service C] Forcing flush of pending spans...")
		if err := bsp.ForceFlush(ctx); err != nil {
			log.Printf("[Service C] WARNING: Failed to force flush spans: %v", err)
		}

		// Shutdown tracer provider
		log.Println("[Service C] Shutting down tracer provider...")
		if err := tp.Shutdown(ctx); err != nil {
			log.Printf("[Service C] WARNING: Failed to shutdown tracer provider: %v", err)
			return err
		}

		// Close gRPC connection
		log.Println("[Service C] Closing gRPC connection...")
		if err := conn.Close(); err != nil {
			log.Printf("[Service C] WARNING: Failed to close gRPC connection: %v", err)
		}

		log.Println("[Service C] Tracer shutdown complete")
		return nil
	}

	return shutdownFunc, nil
}

func initDB() {
	log.Println("[Service C] ==========================================")
	log.Println("[Service C] Initializing database connection...")
	log.Println("[Service C] ==========================================")

	dbHost := os.Getenv("DB_HOST")
	if dbHost == "" {
		dbHost = "localhost"
	}
	log.Printf("[Service C] Database host: %s", dbHost)

	connStr := fmt.Sprintf("host=%s port=5432 user=aneesh dbname=payments sslmode=disable", dbHost)
	log.Printf("[Service C] Connection string: host=%s port=5432 user=aneesh dbname=payments", dbHost)

	var err error
	for i := 0; i < 10; i++ {
		log.Printf("[Service C] Database connection attempt %d/10...", i+1)
		db, err = sql.Open("postgres", connStr)
		if err == nil {
			log.Println("[Service C] sql.Open succeeded, testing connection with Ping...")
			err = db.Ping()
			if err == nil {
				log.Println("[Service C] Database ping successful ✓")
				break
			}
			log.Printf("[Service C] Database ping failed: %v", err)
		} else {
			log.Printf("[Service C] sql.Open failed: %v", err)
		}
		log.Printf("[Service C] Waiting 2 seconds before retry...")
		time.Sleep(2 * time.Second)
	}

	if err != nil {
		log.Fatalf("[Service C] FATAL: Failed to connect to database after 10 attempts: %v", err)
	}
	log.Println("[Service C] Database connection established successfully")

	// Create payments table
	log.Println("[Service C] Creating payments table if not exists...")
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
		log.Fatalf("[Service C] FATAL: Failed to create payments table: %v", err)
	}

	log.Println("[Service C] Database initialized successfully ✓")
	log.Println("[Service C] ==========================================")
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
	spanID := span.SpanContext().SpanID().String()

	log.Printf("[Service C] ========== NEW PAYMENT REQUEST ==========")
	log.Printf("[Service C] Received payment request")
	log.Printf("[Service C] TraceID: %s", traceID)
	log.Printf("[Service C] SpanID: %s", spanID)
	log.Printf("[Service C] Method: %s, Path: %s", r.Method, r.URL.Path)
	log.Printf("[Service C] Remote Address: %s", r.RemoteAddr)

	span.SetAttributes(
		attribute.String("service", "payment-service"),
		attribute.String("handler", "handlePayment"),
	)

	// Decode request
	log.Println("[Service C] Decoding payment request body...")
	var paymentReq PaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&paymentReq); err != nil {
		log.Printf("[Service C] ERROR: Failed to decode request body: %v", err)
		log.Printf("[Service C] TraceID: %s - Responding with 400 Bad Request", traceID)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		span.RecordError(err)
		return
	}
	sdk.Checkpoint(traceID, "payment_processing", map[string]interface{}{
		"orderID":    paymentReq.OrderID,
		"amount":     paymentReq.Amount,
		"customerID": paymentReq.CustomerID,
		"db_status":  "connected", // Useful debugging info
	})
	log.Printf("[Service C] Request decoded: OrderID=%s, CustomerID=%s, Amount=%.2f",
		paymentReq.OrderID, paymentReq.CustomerID, paymentReq.Amount)

	log.Printf("[Service C] Processing payment for order %s, amount: $%.2f - TraceID: %s",
		paymentReq.OrderID, paymentReq.Amount, traceID)

	// Create child span for fraud check
	log.Println("[Service C] Starting fraud detection span...")
	_, fraudSpan := tracer.Start(ctx, "fraud-detection")
	fraudSpan.SetAttributes(
		attribute.String("order.id", paymentReq.OrderID),
		attribute.Float64("payment.amount", paymentReq.Amount),
		attribute.Bool("fraud.detected", false),
	)
	time.Sleep(80 * time.Millisecond) // Simulate fraud check
	fraudSpan.End()
	log.Println("[Service C] Fraud detection completed - No fraud detected")

	// Create child span for payment processing
	log.Println("[Service C] Starting payment processing span...")
	_, processSpan := tracer.Start(ctx, "process-payment")
	processSpan.SetAttributes(
		attribute.String("order.id", paymentReq.OrderID),
		attribute.String("payment.method", "credit_card"),
	)
	time.Sleep(120 * time.Millisecond) // Simulate payment processing
	processSpan.End()
	log.Println("[Service C] Payment processing completed")

	// Create child span for database insertion
	log.Println("[Service C] Starting database insertion span...")
	_, dbSpan := tracer.Start(ctx, "insert-payment-record")

	log.Printf("[Service C] Inserting payment record into database...")
	log.Printf("[Service C] SQL: INSERT INTO payments (order_id, customer_id, amount, status, processed_at, trace_id)")
	log.Printf("[Service C] Values: ('%s', '%s', %.2f, 'success', now(), '%s')",
		paymentReq.OrderID, paymentReq.CustomerID, paymentReq.Amount, traceID)

	var paymentID int
	err := db.QueryRowContext(ctx, `
		INSERT INTO payments (order_id, customer_id, amount, status, processed_at, trace_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, paymentReq.OrderID, paymentReq.CustomerID, paymentReq.Amount, "success", time.Now(), traceID).Scan(&paymentID)

	if err != nil {
		log.Printf("[Service C] ERROR: Database insertion failed")
		log.Printf("[Service C] Error details: %v", err)
		log.Printf("[Service C] TraceID: %s - Responding with 500", traceID)
		dbSpan.RecordError(err)
		dbSpan.End()
		http.Error(w, "Failed to record payment", http.StatusInternalServerError)
		span.RecordError(err)
		return
	}

	log.Printf("[Service C] Payment record inserted successfully with ID: %d", paymentID)
	dbSpan.SetAttributes(attribute.Int("payment.id", paymentID))
	dbSpan.End()
	log.Println("[Service C] Database insertion span completed")

	// Build response
	log.Println("[Service C] Building payment response...")
	response := PaymentResponse{
		PaymentID:   paymentID,
		OrderID:     paymentReq.OrderID,
		Status:      "success",
		ProcessedAt: time.Now().Format(time.RFC3339),
	}
	log.Printf("[Service C] Response: %+v", response)

	log.Printf("[Service C] Payment processed successfully - TraceID: %s, PaymentID: %d",
		traceID, paymentID)

	// Send response
	log.Println("[Service C] Encoding and sending response...")
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("[Service C] ERROR: Failed to encode response: %v", err)
		return
	}
	log.Printf("[Service C] Response sent successfully - TraceID: %s", traceID)
	log.Printf("[Service C] ========== PAYMENT COMPLETE ==========\n")
}

func healthCheck(w http.ResponseWriter, r *http.Request) {
	log.Printf("[Service C] Health check from %s", r.RemoteAddr)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Service C is healthy")
}

func main() {
	sdk.Init("service-c")
	log.Println("[Service C] ==========================================")
	log.Println("[Service C] Starting Service C (Payment Service)")
	log.Println("[Service C] ==========================================")

	shutdownTracerProvider, err := initTracer()
	if err != nil {
		log.Fatal(err)
	}

	_, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Use a separate context with timeout for shutdown
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()

		if err := shutdownTracerProvider(shutdownCtx); err != nil {
			log.Printf("[Service C] Failed to shutdown TracerProvider: %s", err)
		}
	}()

	initDB()
	defer func() {
		log.Println("[Service C] Closing database connection...")
		if err := db.Close(); err != nil {
			log.Printf("[Service C] ERROR: Failed to close database: %v", err)
		} else {
			log.Println("[Service C] Database connection closed")
		}
	}()

	log.Println("[Service C] Setting up HTTP handlers...")
	http.Handle("/payment", otelhttp.NewHandler(
		http.HandlerFunc(handlePayment),
		"handle-payment",
	))
	http.HandleFunc("/health", healthCheck)
	log.Println("[Service C] HTTP handlers registered")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("[Service C] ==========================================")
	log.Printf("[Service C] Service C listening on port %s", port)
	log.Printf("[Service C] Endpoints:")
	log.Printf("[Service C]   - POST /payment (Payment Processing)")
	log.Printf("[Service C]   - GET  /health  (Health Check)")
	log.Printf("[Service C] ==========================================")

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("[Service C] FATAL: Failed to start HTTP server: %v", err)
	}
}
