package main

import (
	"fmt"
	"log"
	"sync"
	"time"
)

type FreezeState string

const (
	FreezeStatePreparing FreezeState = "preparing"
	FreezeStateFrozen    FreezeState = "frozen"
	FreezeStateReleasing FreezeState = "releasing"
	FreezeStateCompleted FreezeState = "completed"
	FreezeStateFailed    FreezeState = "failed"
)

type TraceFreeze struct {
	TraceID      string
	Services     []string
	State        FreezeState
	PreparedAt   time.Time
	FrozenAt     time.Time
	ReleasedAt   time.Time
	AckServices  map[string]bool
	BreakPointID string
	Timeout      time.Duration
}

type FreezeCoordinator struct {
	mu               sync.RWMutex
	frozenTraces     map[string]bool
	releaseOverrides map[string]string
	controlplane     *ControlPlaneServer
}

func NewFreezeCoordinator(cp *ControlPlaneServer) *FreezeCoordinator {
	fc := &FreezeCoordinator{
		frozenTraces: make(map[string]bool),
		releaseOverrides: make(map[string]string),
		controlplane: cp,
	}
	return fc
}

func (fc *FreezeCoordinator) IsTraceFrozen(traceID string) bool {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	return fc.frozenTraces[traceID]
}

func (fc *FreezeCoordinator) InitiateFreeze(traceID string, services []string, breakpointID string) error {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	fc.frozenTraces[traceID] = true
	log.Printf("[FreezeCoordinator] ❄️ TRAP SET for Trace ID: %s", traceID)
	fc.controlplane.BroadcastFreezeEvent(traceID, "frozen")
	return nil
}

func (fc *FreezeCoordinator) ReleaseFreeze(traceID string,override string) error {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	if _, exists := fc.frozenTraces[traceID]; exists {
		delete(fc.frozenTraces, traceID)
		if override!=""{
			fc.releaseOverrides[traceID]=override
		}
		log.Printf("[FreezeCoordinator] 🟢 RELEASED Trace ID: %s", traceID)
		fc.controlplane.BroadcastFreezeEvent(traceID, "released")
	}
	return nil
}

func (fc *FreezeCoordinator) ListActiveFreezes() []*TraceFreeze {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	var list []*TraceFreeze
	for traceID := range fc.frozenTraces {
		// Since we only store bools, we construct a "Live" status object
		list = append(list, &TraceFreeze{
			TraceID:  traceID,
			State:    FreezeStateFrozen,
			Services: []string{"all"}, // Universal architecture freezes everything
			FrozenAt: time.Now(),      // Approximate time
		})
	}
	return list
}

func (fc *FreezeCoordinator) GetFreezeStatus(id string) (*TraceFreeze, error) {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	if _, exists := fc.frozenTraces[id]; exists {
		return &TraceFreeze{
			TraceID:  id,
			State:    FreezeStateFrozen,
			Services: []string{"all"},
			FrozenAt: time.Now(),
		}, nil
	}
	return nil, fmt.Errorf("trace not found")
}

func(fc *FreezeCoordinator) PopOverride(traceID string)string{
	fc.mu.Lock()
	defer fc.mu.Unlock()
	val:=fc.releaseOverrides[traceID]
	if val!=""{
		delete(fc.releaseOverrides,traceID)
	}
	return val
}
