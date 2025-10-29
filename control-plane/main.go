package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	pb "github.com/Aneesh-Hegde/tracery/control-plane/proto/controlplane"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

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
	mu            sync.RWMutex
	breakPoints   map[string]*BreakPoint
	traceListeners []chan *pb.TraceEvent
}

func NewControlPlaneServer() *ControlPlaneServer {
	return &ControlPlaneServer{
		breakPoints:   make(map[string]*BreakPoint),
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

func (s *ControlPlaneServer) DeleteBreakpoint(ctx context.Context, req *pb.DeleteBreakPointRequest) (*pb.DeleteBreakPointResponse, error) {
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

func (s *ControlPlaneServer) StreamSnapshot (req *pb.StreamTracesRequest, stream pb.ControlPlane_StreamTracesServer) (error){
	ch:=make(chan *pb.TraceEvent,100)

	s.mu.Lock()
	s.traceListeners=append(s.traceListeners,ch)
	s.mu.Unlock()

	defer func(){
		s.mu.Lock()
		for i, listener:=range s.traceListeners{
			if listener==ch{
				s.traceListeners= append(s.traceListeners[:i],s.traceListeners[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
		close(ch)
	}()

	for event:=range ch {
		if err:=stream.Send(event); err!=nil{
			return err
		}
	}

	return nil

}

func main(){
	listener,err:=net.Listen("tcp",":50051")
	if err!=nil{
		log.Fatal("Failed to listen: %v",err)
	}

	grpcServer:=grpc.NewServer()
	controlplane:=NewControlPlaneServer()

	pb.RegisterControlPlaneServer(grpcServer,controlplane)
	reflection.Register(grpcServer)

	if err:=grpcServer.Serve(listener);err!=nil{
		log.Fatal("Failed to serve: %v",err)
	}

}
