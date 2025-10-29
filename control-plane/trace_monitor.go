package main

import (
	"log"
	"time"

	pb "github.com/Aneesh-Hegde/tracery/controlplane/proto/controlplane"
	// pb "github.com/Aneesh-Hegde/tracery/controlplane/proto"
)

type TraceMonitor struct{
	controlplane *ControlPlaneServer
}

func NewTraceMonitor(cp *ControlPlaneServer) *TraceMonitor{
	return &TraceMonitor{
		controlplane: cp,
	}
}

func (tm *TraceMonitor) Start(){
	log.Println("[TraceMonitor] Starting trace monitoring with breakpoint detection...")

	//Connect to OpenTelemetry Collector to receive traces
	
	ticker:=time.NewTicker(5*time.Second)
	defer ticker.Stop()

	for range ticker.C{
		log.Println("[TraceMonitor] Monitoring active traces...")
	}
}

func (tm *TraceMonitor) ProcessTrace(traceID,serviceName,endpoint string,attributes map[string]string){
	log.Printf("[TraceMonitor] Processing trace %s from %s%s",traceID,serviceName,serviceName,endpoint)

	breakpoint:=tm.controlplane.CheckBreakpoint(serviceName,endpoint,attributes)

	if breakpoint !=nil{
		log.Printf("[TraceMonitor] 🎯 Trace %s hit breakpoint %s!",traceID,breakpoint.ID)
		
		event:=&pb.TraceEvent{
			TraceId: traceID,
			ServiceName: serviceName,
			Endpoint: endpoint,
			Timestamp: time.Now().Unix(),
			Attributes: attributes,
		}
		tm.controlplane.BroadcastTraceEvent(event)

		//Trigger freeze
		tm.controlplane.OnBreakpointHit(traceID,breakpoint)

	}
}

//SimulateTraceEvent simulates a trace event for string
func(tm *TraceMonitor) SimulateTraceEvent(traceID,serviceName,endpoint string,attributes map[string]string){
	tm.ProcessTrace(traceID,serviceName,endpoint,attributes)
}

