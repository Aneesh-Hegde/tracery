package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"github.com/Aneesh-Hegde/tracery/controlplane/proto/controlplane"
)

type BreakPoint struct{
	ID string
	ServicName string
	EndPoint string
	Conditions map[string]string
	Enabled bool
	CreatedAt time.Time
}

type ControlPlaneServer struct{
	mu sync.RWMutex
	breakPoints map[string]*BreakPoint
	traceListener []chan *TraceEvent
}
