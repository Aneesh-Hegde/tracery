#!/bin/bash
set -e

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m'

# Cleanup function to kill background processes on exit
cleanup() {
    echo ""
    echo -e "${RED}🛑 Shutting down all services...${NC}"
    pkill -P $$ 
    # Match against the directory names now
    pkill -f "go run ./service-" || true
    pkill -f "go run ./control-plane" || true
    pkill -f "envoy -c envoy-filter" || true
    echo "✅ Done."
}
trap cleanup EXIT INT

echo -e "${BLUE}==============================================${NC}"
echo -e "${BLUE}   🚀 Starting DCDOT Local System (Bare Envoy) ${NC}"
echo -e "${BLUE}==============================================${NC}"

# 0. Prep
mkdir -p logs
rm -f logs/*.log

# 1. Build WASM
echo -e "${GREEN}🔨 [1/4] Building WASM Filter...${NC}"
cd envoy-filter
tinygo build -buildmode=c-shared -o freeze-filter.wasm -scheduler=none -target=wasip1 main.go
# Using standard build command which is safer for most Envoy versions:
# tinygo build -o freeze-filter.wasm -scheduler=none -target=wasi main.go
cd ..

# 2. Build CLI
echo -e "${GREEN}🔨 [2/4] Building CLI...${NC}"
cd tracery-cli
go build -o tracery-cli .
cd ..

# 3. Start Control Plane
echo -e "${GREEN}🧠 [3/4] Starting Control Plane...${NC}"
# UPDATED: go run ./control-plane compiles ALL files in that dir
PORT=8080 go run ./control-plane > logs/cp.log 2>&1 &
sleep 2

# 4. Start Microservices & Sidecars
echo -e "${GREEN}📦 [4/4] Starting Services & Envoys...${NC}"

# --- Helper function to start Envoy safely ---
start_envoy() {
    SERVICE_NAME=$1
    CONFIG_FILE=$2
    LOG_FILE=$3
    
    # Start Envoy with DEBUG logs and pipe stderr/stdout to file
    envoy -c $CONFIG_FILE \
        --concurrency 1 \
        -l debug \
        --component-log-level wasm:debug \
        > $LOG_FILE 2>&1 &
        
    PID=$!
    sleep 1
    # Check if process is still running
    if ! kill -0 $PID 2>/dev/null; then
        echo -e "${RED}❌ Envoy for $SERVICE_NAME died immediately!${NC}"
        echo "--- Last 10 lines of $LOG_FILE ---"
        tail -n 10 $LOG_FILE
        echo "----------------------------------"
        exit 1
    fi
}

# --- Service A ---
# UPDATED: go run ./service-a
export SERVICE_B_URL="http://localhost:10002"
PORT=8081 go run ./service-a > logs/service-a.log 2>&1 &
start_envoy "Service A" "envoy-filter/envoy-service-a.yaml" "logs/envoy-a.log"
echo "   - Service A: http://localhost:10001 (App Port: 8081)"

# --- Service B ---
# UPDATED: go run ./service-b
export SERVICE_C_URL="http://localhost:10003"
PORT=8082 go run ./service-b > logs/service-b.log 2>&1 &
start_envoy "Service B" "envoy-filter/envoy-service-b.yaml" "logs/envoy-b.log"
echo "   - Service B: http://localhost:10002 (App Port: 8082)"

# --- Service C ---
# UPDATED: go run ./service-c
export DB_HOST="localhost"
PORT=8083 go run ./service-c > logs/service-c.log 2>&1 &
start_envoy "Service C" "envoy-filter/envoy-service-c.yaml" "logs/envoy-c.log"
echo "   - Service C: http://localhost:10003 (App Port: 8083)"

sleep 2

echo ""
echo -e "${BLUE}==============================================${NC}"
echo -e "${GREEN}✅ SYSTEM RUNNING${NC}"
echo -e "${BLUE}==============================================${NC}"
echo "📝 Logs are in ./logs/"
echo "   - tail -f logs/cp.log"
echo "   - tail -f logs/envoy-a.log"
echo ""
echo "👉 Control Plane is listening on :8080 (HTTP) and :50051 (gRPC)"
echo "👉 Send requests to: http://localhost:10001/order"
echo ""
echo "Press Ctrl+C to stop."

# Keep script running to maintain background processes
wait
