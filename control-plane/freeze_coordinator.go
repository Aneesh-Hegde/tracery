package main

import (
	"context"
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
	mu           sync.RWMutex
	freezes      map[string]*TraceFreeze
	envoyManager *EnvoyCommunicator
	controlplane *ControlPlaneServer
}

func NewFreezeCoordinator(cp *ControlPlaneServer) *FreezeCoordinator {
	fc := &FreezeCoordinator{
		freezes:      make(map[string]*TraceFreeze),
		controlplane: cp,
	}

	var err error
	fc.envoyManager, err = NewEnvoyCommunicator("default")
	if err != nil {
		log.Fatalf("Failed to initialize EnvoyCommunicator: %v", err)
	}

	return fc
}

func (fc *FreezeCoordinator) InitiateFreeze(traceID string, services []string, breakpointID string) error {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	if _, exists := fc.freezes[traceID]; exists {
		return fmt.Errorf("trace %s is already frozen", traceID)
	}

	freeze := &TraceFreeze{
		TraceID:      traceID,
		Services:     services,
		State:        FreezeStatePreparing,
		PreparedAt:   time.Now(),
		AckServices:  make(map[string]bool),
		BreakPointID: breakpointID,
		Timeout:      30 * time.Second,
	}

	fc.freezes[traceID] = freeze

	log.Println("[FreezeCoordinator] Initiating freeze for trace %s across services: %v", traceID, services)

	go fc.executeTwoPhaseFreeze(freeze)

	return nil
}

func (fc *FreezeCoordinator) executeTwoPhaseFreeze(freeze *TraceFreeze) {
	ctx, cancel := context.WithTimeout(context.Background(), freeze.Timeout)
	defer cancel()

	log.Printf("[FreezeCoordinator] Phase 1:Preparing freeze for trace:%s", freeze.TraceID)

	err := fc.preparePhase(ctx, freeze)
	if err != nil {
		log.Printf("[FreezeCoordinator] Prepare phase failed for trace %s: %v", freeze.TraceID, err)
		fc.abortFreeze(freeze, err)
		return
	}

	log.Printf("[FreezeCoordinator] Phase 2: Committing freeze for trace %s", freeze.TraceID)

	err = fc.commitPhase(ctx, freeze)
	if err != nil {
		log.Printf("[FreezeCoordinator] Commit phase failed for trace %s: %v", freeze.TraceID, err)
		fc.abortFreeze(freeze, err)
		return
	}

	fc.mu.Lock()
	freeze.State = FreezeStateFrozen
	freeze.FrozenAt = time.Now()
	fc.mu.Unlock()

	log.Printf("[FreezeCoordinator] ✅ Trace %s is now FROZEN", freeze.TraceID)

	//Broadcast freeze event
	fc.controlplane.BroadcastFreezeEvent(freeze.TraceID, "frozen")

	go func() {
		time.Sleep(freeze.Timeout)
		fc.ReleaseFreeze(freeze.TraceID)
	}()
}

func (fc *FreezeCoordinator) preparePhase(ctx context.Context, freeze *TraceFreeze) error {
	//CreateEnvoy filter for each service to prepare freeze
	for _, service := range freeze.Services {
		filterName := fmt.Sprintf("freeze-prepare-%s-%s", freeze.TraceID, service)

		err := fc.envoyManager.CreateFreezeFilter(filterName, freeze.TraceID, service, "prepare")
		if err != nil {
			return fmt.Errorf("failed to create prepare filter for %s: %v", service, err)
		}

		freeze.AckServices[service] = true
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("Prepare phase timeout")
	case <-time.After(2 * time.Second):
		//simulated time
		return nil
	}
}

// actual implementation of freeze
func (fc *FreezeCoordinator) commitPhase(ctx context.Context, freeze *TraceFreeze) error {
	//Update EnvoyFilter to execute freeze
	for _, service := range freeze.Services {
		filterName := fmt.Sprintf("freeze-commit-%s-%s", freeze.TraceID, service)

		err := fc.envoyManager.CreateFreezeFilter(filterName, freeze.TraceID, service, "prepare")
		if err != nil {
			return fmt.Errorf("failed to create freeze filter for %s: %v", service, err)
		}
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("Commit phase timeout")
	case <-time.After(1 * time.Second):
		//simulated time
		return nil
	}
}

func (fc *FreezeCoordinator) ReleaseFreeze(traceID string) error {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	freeze, exists := fc.freezes[traceID]
	if !exists {
		return fmt.Errorf("trace %s is not frozen", traceID)
	}

	if freeze.State != FreezeStateFrozen {
		return fmt.Errorf("trace %s is not in frozen state (current: %s)", traceID, freeze.State)
	}

	log.Printf("[FreezeCoordinator] Releasing freeze for trace %s", traceID)

	freeze.State = FreezeStateReleasing
	freeze.ReleasedAt = time.Now()

	for _, service := range freeze.Services {
		prepareFilter := fmt.Sprintf("freeze-prepare-%s-%s", traceID, service)
		commitFilter := fmt.Sprintf("freeze-commit-%s-%s", traceID, service)
		fc.envoyManager.DeleteFreezeFilter(prepareFilter)
		fc.envoyManager.DeleteFreezeFilter(commitFilter)
	}

	freeze.State = FreezeStateCompleted

	fc.controlplane.BroadcastFreezeEvent(traceID, "released")

	log.Printf("[FreezeCoordinator] ✅ Trace %s released (frozen for %v)", traceID, freeze.ReleasedAt.Sub(freeze.FrozenAt))

	go func() {
		time.Sleep(5 * time.Second)
		fc.mu.Lock()
		delete(fc.freezes, traceID)
		fc.mu.Unlock()
	}()

	return nil

}

func (fc *FreezeCoordinator) abortFreeze(freeze *TraceFreeze, reason error) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	log.Printf("[FreezeCoordinator] Aborting freeze for trace %s: %v", freeze.TraceID, reason)

	freeze.State = FreezeStateFailed

	for _, service := range freeze.Services {
		prepareFilter := fmt.Sprintf("freeze-prepare-%s-%s", freeze.TraceID, service)
		commitFilter := fmt.Sprintf("freeze-commit-%s-%s", freeze.TraceID, service)

		fc.envoyManager.DeleteFreezeFilter(prepareFilter)
		fc.envoyManager.DeleteFreezeFilter(commitFilter)
	}

	//Broadcast event failure
	fc.controlplane.BroadcastFreezeEvent(freeze.TraceID, "failed")

	//cleanup
	delete(fc.freezes, freeze.TraceID)
}

func (fc *FreezeCoordinator) GetFreezeStatus(traceID string) (*TraceFreeze, error) {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	freeze, exists := fc.freezes[traceID]
	if !exists {
		return nil, fmt.Errorf("trace %s is not frozen", traceID)
	}

	return freeze, nil
}

func (fc *FreezeCoordinator) ListActiveFreezes() []*TraceFreeze {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	freezes := make([]*TraceFreeze, 0, len(fc.freezes))
	for _, freeze := range fc.freezes {
		freezes = append(freezes, freeze)
	}
	return freezes
}
