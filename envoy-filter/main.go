package main

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/tetratelabs/proxy-wasm-go-sdk/proxywasm"
	"github.com/tetratelabs/proxy-wasm-go-sdk/proxywasm/types"
)

func main() {
	proxywasm.SetVMContext(&vmContext{})
}

type vmContext struct {
	types.DefaultVMContext
}

func (*vmContext) NewPluginContext(contextID uint32) types.PluginContext {
	return &pluginContext{
		contextID:    contextID,
		frozenTraces: make(map[string]*FreezeState),
	}
}

type FreezeState struct {
	TraceID      string
	State        string
	TimeoutMs    int64
	FrozenAtNano int64
}

type FreezeConfig struct {
	TraceID   string `json:"trace_id"`
	State     string `json:"state"`
	TimeoutMs int64  `json:"timeout_ms"`
}

type pluginContext struct {
	types.PluginContext
	contextID    uint32
	frozenTraces map[string]*FreezeState
}

func (ctx *pluginContext) onPluginStart(pluginConfigurationSize int) types.OnPluginStartStatus {
	proxywasm.LogInfo("🔧 Freeze Filter Plugin Started (Resilient Mode)")
	return types.OnPluginStartStatusOK
}

func (ctx *pluginContext) NewHttpContext(contextID uint32) types.HttpContext {
	return &httpContext{
		contextID:     contextID,
		pluginContext: ctx,
	}
}

func (ctx *pluginContext) onPLuginConfiguration(configurationSize int) types.OnPluginStartStatus {
	data, err := proxywasm.GetPluginConfiguration()
	if err != nil {
		proxywasm.LogCriticalf("❌ Error reading plugin configuration: %v", err)
		return types.OnPluginStartStatusFailed
	}
	if len(data) == 0 {
		return types.OnPluginStartStatusOK
	}

	var config FreezeConfig
	if err := json.Unmarshal(data, &config); err != nil {
		proxywasm.LogCriticalf("❌ Error parsing configuration: %v", err)
	}

	ctx.handleFreezeCommand(config)
	return types.OnPluginStartStatusOK
}

func (ctx *pluginContext) handleFreezeCommand(config FreezeConfig) {
	traceID := config.TraceID

	proxywasm.LogInfof("📨 Received freeze command: trace_id=%s, state=%s, timeout=%dms",
		traceID, config.State, config.TimeoutMs)

	switch config.State {
	case "PREPARE":
		if _, exists := ctx.frozenTraces[traceID]; !exists {
			ctx.frozenTraces[traceID] = &FreezeState{
				TraceID:      traceID,
				State:        "PREPARE",
				TimeoutMs:    config.TimeoutMs,
				FrozenAtNano: time.Now().UnixNano(),
			}
			proxywasm.LogInfof("✅ Prepared freeze for trace: %s", traceID)
		}
	case "FREEZE":
		if state, exists := ctx.frozenTraces[traceID]; exists {
			state.State = "FROZEN"
			state.FrozenAtNano = time.Now().UnixNano()
			proxywasm.LogInfof("❄️ FROZEN trace: %s", traceID)
		} else {
			ctx.frozenTraces[traceID] = &FreezeState{
				TraceID:      traceID,
				State:        "FROZEN",
				TimeoutMs:    config.TimeoutMs,
				FrozenAtNano: time.Now().UnixNano(),
			}
			proxywasm.LogInfof("❄️ FROZEN trace (direct): %s", traceID)
		}
	case "UNFREEZE":
		if _, exists := ctx.frozenTraces[traceID]; exists {
			delete(ctx.frozenTraces, traceID)
			proxywasm.LogInfof("✅ UNFROZEN and cleaned up trace: %s", traceID)
		}
	}

}

type httpContext struct {
	types.DefaultHttpContext
	contextID     uint32
	pluginContext *pluginContext
	traceID       string
	frozen        bool
}

func (ctx *httpContext) onHttpRequestHeaders(numHeaders int, endOfStream bool) types.Action {
	traceID := ctx.extractTraceID()

	if traceID == "" {
		return types.ActionContinue
	}

	ctx.traceID = traceID

	freezeState, exists := ctx.pluginContext.frozenTraces[traceID]
	if !exists {
		return types.ActionContinue
	}

	now := time.Now().UnixNano()
	elapsed := (now - freezeState.FrozenAtNano) / 1e6 //convert to millisecond

	if elapsed > freezeState.TimeoutMs {
		proxywasm.LogWarnf("⏰ Freeze timeout exceeded for trace: %s (elapsed: %dms), auto-releasing",
			traceID, elapsed)
		delete(ctx.pluginContext.frozenTraces, traceID)
		return types.ActionContinue
	}

	if freezeState.State == "FROZEN" {
		ctx.frozen = true
		proxywasm.LogWarnf("🛑 BLOCKING request for frozen trace: %s", traceID)

		// CRITICAL: Return immediate response to prevent service degradation
		// This ensures:
		// 1. Client gets immediate feedback (not timeout)
		// 2. Service worker thread is not blocked
		// 3. Load balancer health checks still work
		// 4. Circuit breakers don't trip
		// 5. Connection pools don't exhaust

		responseBody := []byte(`{
			"error": "trace_frozen",
			"message": "This trace is frozen for debugging. The request will be replayed after release.",
			"trace_id": "` + traceID + `",
			"debug_info": {
				"frozen_for_ms": ` + string(rune(elapsed)) + `,
				"auto_release_in_ms": ` + string(rune(freezeState.TimeoutMs-elapsed)) + `,
				"reason": "distributed_breakpoint"
			}
		}`)

		// Return 202 Accepted instead of 503
		// 202 = Request accepted but not processed yet
		// This is semantically correct for "frozen" requests
		// And won't trigger circuit breakers or retries
		proxywasm.SendHttpResponse(202, [][2]string{
			{"content-type", "application/json"},
			{"x-freeze-status", "frozen"},
			{"x-freeze-trace-id", traceID},
			{"x-freeze-elapsed-ms", string(rune(elapsed))},
			{"cache-control", "no-store"}, // Don't cache frozen responses
			{"retry-after", "5"},          // Client can retry after 5 seconds
		}, responseBody, -1)

		return types.ActionPause
	}

	return types.ActionContinue

}

func (ctx *httpContext) extractTraceID() string {
	// Try x-b3-traceid (Zipkin B3 format)
	if traceID, err := proxywasm.GetHttpRequestHeader("x-b3-traceid"); err == nil && traceID != "" {
		return traceID
	}

	// Try x-trace-id
	if traceID, err := proxywasm.GetHttpRequestHeader("x-trace-id"); err == nil && traceID != "" {
		return traceID
	}

	// Try traceparent (W3C Trace Context format)
	if traceparent, err := proxywasm.GetHttpRequestHeader("traceparent"); err == nil && traceparent != "" {
		parts := strings.Split(traceparent, "-")
		if len(parts) >= 2 {
			return parts[1]
		}
	}

	return ""
}

func (ctx *httpContext) onHttpResponseHeaders(numHeaders int,endofStream bool) types.Action{
	if ctx.frozen{
		proxywasm.AddHttpRequestHeader("x-freeze-filter","request-frozen")
	}else{
		proxywasm.AddHttpRequestHeader("x-freeze-filter","active")
	} 
	return types.ActionContinue
}
