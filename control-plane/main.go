package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	// pb "github.com/Aneesh-Hegde/tracery/controlplane/proto/controlplane"
	pb "github.com/Aneesh-Hegde/tracery/controlplane/proto"
	"github.com/google/uuid"
	collectorpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

type FreezeResponse struct {
	Action       string `json:"action"` //tell the action to take i.e to freeze or allow
	OverrideBody string `json:"override_body,omitempty"`
}

func flattenJSON(data interface{}, prefix string, result map[string]string) {
	switch v := data.(type) {
	case map[string]interface{}:
		for k, val := range v {
			fullKey := k
			if prefix != "" {
				fullKey = prefix + "." + k
			}
			flattenJSON(val, fullKey, result)
		}
	case string:
		result[prefix] = v
	case float64:
		// Convert numbers to string without trailing zeros if possible
		result[prefix] = fmt.Sprintf("%v", v)
	case bool:
		result[prefix] = fmt.Sprintf("%v", v)
	case []interface{}:
		// Simple handling for arrays: index based keys (e.g. items.0)
		for i, val := range v {
			fullKey := fmt.Sprintf("%s.%d", prefix, i)
			flattenJSON(val, fullKey, result)
		}
	}
}

func startHttpServer(fc *FreezeCoordinator, cp *ControlPlaneServer) {
	mux := http.NewServeMux()

	mux.HandleFunc("/check", func(w http.ResponseWriter, r *http.Request) {
		// 1. Read Metadata from Headers (sent by WASM)
		traceID := r.Header.Get("x-trace-id")
		service := r.Header.Get("x-service-name")
		endpoint := r.Header.Get("x-endpoint")

		w.Header().Set("Content-Type", "application/json")
		if traceID != "" {
			go cp.BroadcastTrace(traceID, service, endpoint)
		}
		// 2. Logic: Check Freeze & Breakpoints
		isFrozen := fc.IsTraceFrozen(traceID)
		isReleased := fc.IsTraceReleased(traceID)

		if !isFrozen && !isReleased && service != "" {

			// --- 🔍 BUILD SEARCHABLE DATA MAP ---
			searchableData := make(map[string]string)

			// A. Process HEADERS (Strict & Short)
			for k, v := range r.Header {
				k = strings.ToLower(k)
				if strings.HasPrefix(k, "x-orig-") {
					cleanKey := strings.TrimPrefix(k, "x-orig-")
					val := v[0] // Take first value

					// 1. Strict Key: "header.user-type"
					searchableData["header."+cleanKey] = val

					// 2. Short Key: "user-type" (only if empty to prefer body later)
					if _, exists := searchableData[cleanKey]; !exists {
						searchableData[cleanKey] = val
					}
				}
			}

			// B. Process BODY (Strict & Short)
			// We clone the body bytes because we might need them,
			// though usually Decode reads stream once.
			// Since this is a lightweight control plane request, direct decode is fine.
			var bodyMap map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&bodyMap); err == nil {
				flatBody := make(map[string]string)
				flattenJSON(bodyMap, "", flatBody)

				for k, v := range flatBody {
					// 1. Strict Key: "body.amount"
					searchableData["body."+k] = v

					// 2. Short Key: "amount" (Body usually wins priority in short mode)
					searchableData[k] = v
				}
			}

			// --- 🎯 CHECK BREAKPOINTS ---
			cp.mu.RLock()
			for _, bp := range cp.breakPoints {
				// Match Service & Endpoint
				if bp.ServiceName == service && strings.Contains(endpoint, bp.EndPoint) {

					// Match Conditions using Searchable Data
					conditionsMet := true
					for k, expectedVal := range bp.Conditions {
						// Look up in our rich map (supports "header.x", "body.x", or just "x")
						actualVal, exists := searchableData[k]

						if !exists || actualVal != expectedVal {
							conditionsMet = false
							break
						}
					}

					if conditionsMet {
						log.Printf("🪤 TRAP TRIGGERED! %s matched condition in %s", bp.ID, service)
						fc.InitiateFreeze(traceID, []string{service}, bp.ID)
						isFrozen = true
						break
					}
				}
			}
			cp.mu.RUnlock()
		}

		if isFrozen {
			json.NewEncoder(w).Encode(FreezeResponse{Action: "freeze"})
		} else {
			override := fc.PopOverride(traceID)
			json.NewEncoder(w).Encode(FreezeResponse{Action: "allow", OverrideBody: override})
		}
	})

	mux.HandleFunc("/snapshot", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var snap pb.SnapshotData
		// Decode JSON from WASM
		if err := json.NewDecoder(r.Body).Decode(&snap); err != nil {
			log.Printf("❌ Failed to decode snapshot: %v", err)
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}

		// Store in memory
		cp.mu.Lock()
		cp.snapshots[snap.TraceId] = &snap
		cp.mu.Unlock()

		log.Printf("📸 Captured Snapshot for Trace: %s (Body: %d bytes)", snap.TraceId, len(snap.Body))
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/app-snapshot", func(w http.ResponseWriter, r *http.Request) {
		var snap AppSnapshotData
		if err := json.NewDecoder(r.Body).Decode(&snap); err != nil {
			log.Printf("❌ Failed to decode app snapshot: %v", err)
			return
		}

		cp.mu.Lock()
		if cp.appSnapshots == nil {
			cp.appSnapshots = make(map[string][]*AppSnapshotData)
		}
		cp.appSnapshots[snap.TraceID] = append(cp.appSnapshots[snap.TraceID], &snap)
		cp.mu.Unlock()

		log.Printf("💾 Received App State for Trace: %s (Vars: %d)", snap.TraceID, len(snap.LocalVars))
		w.WriteHeader(http.StatusOK)
	})
	log.Println("🌍 Universal HTTP Interface listening on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Printf("HTTP Server failed: %v", err)
	}

}

type BreakPoint struct {
	ID          string
	ServiceName string
	EndPoint    string
	Conditions  map[string]string
	Enabled     bool
	CreatedAt   time.Time
}

type AppSnapshotData struct {
	TraceID     string                 `json:"trace_id"`
	ServiceName string                 `json:"service_name"`
	Checkpoint  string                 `json:"checkpoint"`
	StackTrace  string                 `json:"stack_trace"`
	LocalVars   map[string]interface{} `json:"local_variables"`
	Timestamp   string                 `json:"timestamp"`
}

type ControlPlaneServer struct {
	pb.UnimplementedControlPlaneServer
	mu                sync.RWMutex
	breakPoints       map[string]*BreakPoint
	traceListeners    []chan *pb.TraceEvent
	freezeCoordinator *FreezeCoordinator
	traceMonitor      *TraceMonitor
	snapshots         map[string]*pb.SnapshotData
	appSnapshots      map[string][]*AppSnapshotData
}

func NewControlPlaneServer() *ControlPlaneServer {
	return &ControlPlaneServer{
		breakPoints:    make(map[string]*BreakPoint),
		traceListeners: make([]chan *pb.TraceEvent, 0),
		snapshots:      make(map[string]*pb.SnapshotData),
		appSnapshots:   make(map[string][]*AppSnapshotData),
	}
}

func (s *ControlPlaneServer) RegisterBreakpoint(ctx context.Context, req *pb.RegisterBreakPointRequest) (*pb.RegisterBreakPointResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	bpID := uuid.New().String()

	breakpoint := &BreakPoint{
		ID:          bpID,
		ServiceName: req.GetServiceName(),
		EndPoint:    req.GetEndpoint(),
		Enabled:     true,
		CreatedAt:   time.Now(),
		Conditions:  req.GetConditions(),
	}

	s.breakPoints[bpID] = breakpoint

	log.Printf("[ControlPlane] Registered breakpoint %s for %s%s with conditions: %v",
		bpID, req.GetServiceName(), req.GetEndpoint(), req.GetConditions())

	return &pb.RegisterBreakPointResponse{
		BreakpointId: bpID,
		Success:      true,
		RespMessage:  fmt.Sprintf("Breakpoint registered at %s%s", req.GetServiceName(), req.GetEndpoint()),
	}, nil
}

func (s *ControlPlaneServer) ListBreakpoints(ctx context.Context, req *pb.ListBreakpointsRequest) (*pb.ListBreakpointsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	breakpoints := make([]*pb.Breakpoint, 0, len(s.breakPoints))
	for _, bp := range s.breakPoints {
		breakpoints = append(breakpoints, &pb.Breakpoint{
			Id:          bp.ID,
			ServiceName: bp.ServiceName,
			Endpoint:    bp.EndPoint,
			Conditions:  bp.Conditions,
			Enabled:     bp.Enabled,
			CreatedAt:   bp.CreatedAt.Unix(),
		})
	}
	return &pb.ListBreakpointsResponse{
		Breakpoints: breakpoints,
	}, nil
}

func (s *ControlPlaneServer) DeleteBreakPoint(ctx context.Context, req *pb.DeleteBreakPointRequest) (*pb.DeleteBreakPointResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	fmt.Println(req.BreakpointId)
	if _, exists := s.breakPoints[req.BreakpointId]; !exists {
		return &pb.DeleteBreakPointResponse{
			Success:     false,
			RespMessage: "Breakpoint not found",
		}, nil
	}

	delete(s.breakPoints, req.BreakpointId)
	fmt.Println("hope it is deleted")
	return &pb.DeleteBreakPointResponse{
		Success:     true,
		RespMessage: "Breakpoint deleted",
	}, nil

}

// func (s* ControlPlaneServer) GetSnapshot(ctx context.Context,req *pb.GetSnapshotRequest) (*pb.GetSnapshotResponse,error){
// 	s.mu.Lock()
// 	defer s.mu.Unlock()
// Implementation in phase4
// }

func (s *ControlPlaneServer) StreamTraces(req *pb.StreamTracesRequest, stream pb.ControlPlane_StreamTracesServer) error {
	ch := make(chan *pb.TraceEvent, 100)

	s.mu.Lock()
	s.traceListeners = append(s.traceListeners, ch)
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		// Remove the listener when client disconnects
		for i, listener := range s.traceListeners {
			if listener == ch {
				s.traceListeners = append(s.traceListeners[:i], s.traceListeners[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
		close(ch)
	}()

	// Stream events to the CLI
	for event := range ch {
		if err := stream.Send(event); err != nil {
			return err
		}
	}

	return nil
}

func (s *ControlPlaneServer) BroadcastTraceEvent(event *pb.TraceEvent) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, ch := range s.traceListeners {
		select {
		case ch <- event:
		default:
			// Non-blocking drop if client is too slow
		}
	}
}

func (s *ControlPlaneServer) BroadcastFreezeEvent(traceID, status string) {
	event := &pb.TraceEvent{
		TraceId:     traceID,
		ServiceName: "control-plane",
		Endpoint:    "/freeze",
		Timestamp:   time.Now().Unix(),
		Attributes: map[string]string{
			"freeze_status": status,
		},
	}
	s.BroadcastTraceEvent(event)
}

// This bridges the strings from the /check handler to the event struct
func (s *ControlPlaneServer) BroadcastTrace(traceID, service, endpoint string) {
	event := &pb.TraceEvent{
		TraceId:     traceID,
		ServiceName: service,
		Endpoint:    endpoint,
		Timestamp:   time.Now().Unix(),
	}
	s.BroadcastTraceEvent(event)
}

func (s *ControlPlaneServer) CheckBreakpoint(serviceName, endpoint string, attributes map[string]string) *BreakPoint {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, bp := range s.breakPoints {
		if !bp.Enabled {
			continue
		}

		if bp.ServiceName != serviceName {
			continue
		}

		if bp.EndPoint != "" && bp.EndPoint != endpoint {
			continue
		}

		match := true
		for key, value := range bp.Conditions {
			if attributes[key] != value {
				match = false
				break
			}
		}

		if match {
			log.Printf("[ControlPlane] 🎯 Breakpoint %s matched for %s%s", bp.ID, serviceName, endpoint)
			return bp
		}
	}

	return nil
}

// OnBreakpointHit is called when a trace hits a breakpoint
func (s *ControlPlaneServer) OnBreakpointHit(traceID string, breakpoint *BreakPoint) {
	log.Printf("[ControlPlane] 🔥 Breakpoint hit! Initiating freeze for trace %s", traceID)

	// Determine which services are involved in this trace
	// For now, freeze all services (in production, analyze trace topology)
	services := []string{"service-a", "service-b", "service-c"}

	err := s.freezeCoordinator.InitiateFreeze(traceID, services, breakpoint.ID)
	if err != nil {
		log.Printf("[ControlPlane] Failed to initiate freeze: %v", err)
	}
}

// FreezeTrace manually freezes a trace
func (s *ControlPlaneServer) FreezeTrace(ctx context.Context, req *pb.FreezeTraceRequest) (*pb.FreezeTraceResponse, error) {
	log.Printf("[ControlPlane] Manual freeze requested for trace %s", req.TraceId)

	services := req.Services
	if len(services) == 0 {
		services = []string{"service-a", "service-b", "service-c"}
	}

	err := s.freezeCoordinator.InitiateFreeze(req.TraceId, services, "manual")
	if err != nil {
		return &pb.FreezeTraceResponse{
			Success:     false,
			RespMessage: err.Error(),
			State:       "failed",
		}, nil
	}

	return &pb.FreezeTraceResponse{
		Success:     true,
		RespMessage: "Freeze initiated",
		State:       "preparing",
	}, nil
}

// ReleaseTrace manually releases a frozen trace
func (s *ControlPlaneServer) ReleaseTrace(ctx context.Context, req *pb.ReleaseTraceRequest) (*pb.ReleaseTraceResponse, error) {
	log.Printf("[ControlPlane] Manual release requested for trace %s", req.TraceId)

	err := s.freezeCoordinator.ReleaseFreeze(req.TraceId, req.OverrideBody)
	if err != nil {
		return &pb.ReleaseTraceResponse{
			Success:     false,
			RespMessage: err.Error(),
		}, nil
	}

	return &pb.ReleaseTraceResponse{
		Success:     true,
		RespMessage: "Trace released",
	}, nil
}

// GetFreezeStatus returns the status of a frozen trace
func (s *ControlPlaneServer) GetFreezeStatus(ctx context.Context, req *pb.GetFreezeStatusRequest) (*pb.GetFreezeStatusResponse, error) {
	freeze, err := s.freezeCoordinator.GetFreezeStatus(req.TraceId)
	if err != nil {
		return &pb.GetFreezeStatusResponse{
			TraceId: req.TraceId,
			State:   "not_found",
		}, nil
	}

	return &pb.GetFreezeStatusResponse{
		TraceId:      freeze.TraceID,
		State:        string(freeze.State),
		Services:     freeze.Services,
		FrozenAt:     freeze.FrozenAt.Unix(),
		BreakpointId: freeze.BreakPointID,
	}, nil
}

// ListActiveFreezes returns all active freezes
func (s *ControlPlaneServer) ListActiveFreezes(ctx context.Context, req *pb.ListActiveFreezesRequest) (*pb.ListActiveFreezesResponse, error) {
	freezes := s.freezeCoordinator.ListActiveFreezes()

	freezeInfos := make([]*pb.FreezeInfo, 0, len(freezes))
	for _, freeze := range freezes {
		freezeInfos = append(freezeInfos, &pb.FreezeInfo{
			TraceId:  freeze.TraceID,
			State:    string(freeze.State),
			Services: freeze.Services,
			FrozenAt: freeze.FrozenAt.Unix(),
		})
	}

	return &pb.ListActiveFreezesResponse{
		Freezes: freezeInfos,
	}, nil
}

func (s *ControlPlaneServer) GetSnapshot(ctx context.Context, req *pb.GetSnapshotRequest) (*pb.GetSnapshotResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if snap, exists := s.snapshots[req.TraceId]; exists {
		return &pb.GetSnapshotResponse{
			Success:      true,
			SnapshotData: snap,
		}, nil
	}

	return &pb.GetSnapshotResponse{
		Success:     false,
		RespMessage: "Snapshot not found (trace might not be frozen or captured yet)",
	}, nil
}

func (s *ControlPlaneServer) GetAppSnapshot(ctx context.Context, req *pb.GetAppSnapshotRequest) (*pb.GetAppSnapshotResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshots, exists := s.appSnapshots[req.TraceId]
	if !exists || len(snapshots) == 0 {
		return &pb.GetAppSnapshotResponse{Success: false}, nil
	}

	// Build the Proto response list
	var protoSnapshots []*pb.AppSnapshot
	for _, snap := range snapshots {
		vars := make(map[string]string)
		for k, v := range snap.LocalVars {
			vars[k] = fmt.Sprintf("%v", v)
		}

		protoSnapshots = append(protoSnapshots, &pb.AppSnapshot{
			ServiceName: snap.ServiceName,
			Checkpoint:  snap.Checkpoint,
			StackTrace:  snap.StackTrace,
			LocalVars:   vars,
			Timestamp:   snap.Timestamp,
		})
	}

	return &pb.GetAppSnapshotResponse{
		Success:   true,
		Snapshots: protoSnapshots,
	}, nil
}

func (s *ControlPlaneServer) EmergencyRelease(ctx context.Context, _ *pb.Empty) (*pb.EmergencyReleaseResponse, error) {
	log.Println("🚨 EMERGENCY RELEASE TRIGGERED 🚨")

	count := s.freezeCoordinator.ReleaseAll() // We need to add this helper below

	return &pb.EmergencyReleaseResponse{
		Success:    true,
		FreedCount: int32(count),
		Message:    "All active freezes have been cleared. System returning to normal flow.",
	}, nil
}

// 2. SYSTEM HEALTH
func (s *ControlPlaneServer) GetSystemHealth(ctx context.Context, _ *pb.Empty) (*pb.SystemHealthResponse, error) {
	status := make(map[string]string)
	status["control-plane"] = "healthy"

	// Check known services from snapshots
	s.mu.RLock()
	knownServices := make(map[string]bool)
	for _, snaps := range s.appSnapshots {
		for _, snap := range snaps {
			knownServices[snap.ServiceName] = true
		}
	}
	s.mu.RUnlock()

	if len(knownServices) == 0 {
		status["services"] = "no_heartbeats_yet"
	} else {
		for svc := range knownServices {
			status["service:"+svc] = "active"
		}
	}

	return &pb.SystemHealthResponse{
		Healthy:         true,
		ComponentStatus: status,
	}, nil
}

func (s *ControlPlaneServer) GetTopology(ctx context.Context, _ *pb.Empty) (*pb.TopologyResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	linksMap := make(map[string]bool)
	var links []*pb.TopologyLink

	for _, snapshots := range s.appSnapshots {

		if len(snapshots) < 2 {
			continue
		}

		for i := 0; i < len(snapshots)-1; i++ {
			source := snapshots[i].ServiceName
			target := snapshots[i+1].ServiceName

			if source == target {
				continue
			}

			linkKey := source + "->" + target
			if !linksMap[linkKey] {
				linksMap[linkKey] = true
				links = append(links, &pb.TopologyLink{
					Source: source,
					Target: target,
				})
			}
		}
	}

	return &pb.TopologyResponse{Links: links}, nil
}

func main() {
	log.Println("🚀 Starting Tracery Control Plane...")

	controlplane := NewControlPlaneServer()

	freezeCoordinator := NewFreezeCoordinator(controlplane)
	controlplane.freezeCoordinator = freezeCoordinator

	traceMonitor := NewTraceMonitor(controlplane)
	controlplane.traceMonitor = traceMonitor

	otelCollector, err := NewOTelCollector(traceMonitor)
	if err != nil {
		log.Fatalf("❌ Failed to initialize OTelCollector: %v", err)
	}
	log.Println("✅ OTel trace receiver initialized")

	go startHttpServer(freezeCoordinator, controlplane)

	go traceMonitor.Start()

	// Setup gRPC server
	listener, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("❌ Failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterControlPlaneServer(grpcServer, controlplane)

	collectorpb.RegisterTraceServiceServer(grpcServer, otelCollector)

	reflection.Register(grpcServer)

	log.Println("✅ Control Plane gRPC server listening on :50051")
	log.Println("📡 Breakpoint API: RegisterBreakpoint, ListBreakpoints, DeleteBreakpoint")
	log.Println("❄️  Freeze API: FreezeTrace, ReleaseTrace, GetFreezeStatus, ListActiveFreezes")
	log.Println("📊 Stream API: StreamTraces")
	log.Println("🛰️  OTLP Trace Receiver listening on :50051")

	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("❌ Failed to serve: %v", err)
	}

}
