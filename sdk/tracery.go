package tracery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"time"
)

type Config struct {
	ServiceName     string
	ControlPlaneURL string
}

var globalConfig Config

func Init(serviceName string) {
	cpURL := os.Getenv("CONTROL_PLANE_HTTP")
	if cpURL == "" {
		cpURL = "http://localhost:8080"
	}

	globalConfig = Config{
		ServiceName:     serviceName,
		ControlPlaneURL: cpURL,
	}
	fmt.Printf("[Tracery SDK] Initialized for %s (CP: %s)\n", serviceName, cpURL)
}

//Internal App states
type SnapshotPayload struct {
	TraceID       string                 `json:"trace_id"`
	ServiceName   string                 `json:"service_name"`
	Checkpoint    string                 `json:"checkpoint"`
	StackTrace    string                 `json:"stack_trace"`
	LocalVars     map[string]interface{} `json:"local_variables"`
	Timestamp     string                 `json:"timestamp"`
}

//Used by user to capture state
func Checkpoint(traceID string, checkpointName string, vars map[string]interface{}) {
	buf := make([]byte, 4096)
	n := runtime.Stack(buf, false)
	stackTrace := string(buf[:n])

	payload := SnapshotPayload{
		TraceID:     traceID,
		ServiceName: globalConfig.ServiceName,
		Checkpoint:  checkpointName,
		StackTrace:  stackTrace,
		LocalVars:   vars,
		Timestamp:   time.Now().Format(time.RFC3339),
	}

	go uploadSnapshot(payload)
}

func uploadSnapshot(payload SnapshotPayload) {
	data, _ := json.Marshal(payload)
	resp, err := http.Post(
		globalConfig.ControlPlaneURL+"/app-snapshot", 
		"application/json", 
		bytes.NewBuffer(data),
	)
	if err != nil {
		fmt.Printf("[Tracery SDK] Failed to upload snapshot: %v\n", err)
		return
	}
	defer resp.Body.Close()
	fmt.Printf("[Tracery SDK] 📸 Captured Application State at '%s'\n", payload.Checkpoint)
}
