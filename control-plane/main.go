package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
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
	Action string `json:"action"` //tell the action to take i.e to freeze or allow
}

func startHttpServer(fc *FreezeCoordinator) {
	mux := http.NewServeMux()

	mux.HandleFunc("/check", func(w http.ResponseWriter, r *http.Request) {
		traceID := r.URL.Query().Get("trace_id")

		w.Header().Set("Content-Type", "application/json")

		if fc.IsTraceFrozen(traceID) {
			json.NewEncoder(w).Encode(FreezeResponse{Action: "freeze"})
		} else {
			json.NewEncoder(w).Encode(FreezeResponse{Action: "allow"})
		}
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

type ControlPlaneServer struct {
	pb.UnimplementedControlPlaneServer
	mu                sync.RWMutex
	breakPoints       map[string]*BreakPoint
	traceListeners    []chan *pb.TraceEvent
	freezeCoordinator *FreezeCoordinator
	traceMonitor      *TraceMonitor
}

func NewControlPlaneServer() *ControlPlaneServer {
	return &ControlPlaneServer{
		breakPoints:    make(map[string]*BreakPoint),
		traceListeners: make([]chan *pb.TraceEvent, 0),
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
	}

	s.breakPoints[bpID] = breakpoint

	log.Printf("[ControlPlane] Registered breakpoint %s for %s%s with the conditions: %v", bpID, req.GetServiceName(), req.GetEndpoint(), req.GetConditions())

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

	if _, exists := s.breakPoints[req.BreakpointId]; !exists {
		return &pb.DeleteBreakPointResponse{
			Success:     false,
			RespMessage: "Breakpoint not found",
		}, nil
	}

	delete(s.breakPoints, req.GetBreakpointId())
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
		for i, listener := range s.traceListeners {
			if listener == ch {
				s.traceListeners = append(s.traceListeners[:i], s.traceListeners[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
		close(ch)
	}()

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

	err := s.freezeCoordinator.ReleaseFreeze(req.TraceId)
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

func main() {
	log.Println("🚀 Starting Tracery Control Plane...")

	// Initialize Control Plane Server
	controlplane := NewControlPlaneServer()

	// Initialize Freeze Coordinator
	freezeCoordinator := NewFreezeCoordinator(controlplane)
	controlplane.freezeCoordinator = freezeCoordinator

	// Initialize Trace Monitor
	traceMonitor := NewTraceMonitor(controlplane)
	controlplane.traceMonitor = traceMonitor

	// Initialize OTel Collector integration
	otelCollector, err := NewOTelCollector(traceMonitor)
	if err != nil {
		log.Fatalf("❌ Failed to initialize OTelCollector: %v", err)
	}
	log.Println("✅ OTel trace receiver initialized")

	//start http server for wasm agent
	go startHttpServer(freezeCoordinator)

	// Start Trace Monitor
	go traceMonitor.Start()

	// Setup gRPC server
	listener, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("❌ Failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterControlPlaneServer(grpcServer, controlplane)

	// ⬇️ ADD THIS: Register the OTel trace receiver service
	collectorpb.RegisterTraceServiceServer(grpcServer, otelCollector)

	reflection.Register(grpcServer)

	log.Println("✅ Control Plane gRPC server listening on :50051")
	log.Println("📡 Breakpoint API: RegisterBreakpoint, ListBreakpoints, DeleteBreakpoint")
	log.Println("❄️  Freeze API: FreezeTrace, ReleaseTrace, GetFreezeStatus, ListActiveFreezes")
	log.Println("📊 Stream API: StreamTraces")
	// ⬇️ ADD THIS: Log that the OTLP receiver is active
	log.Println("🛰️  OTLP Trace Receiver listening on :50051")

	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("❌ Failed to serve: %v", err)
	}

}
