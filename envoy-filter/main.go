package main

import (
	"strings"

	"github.com/tetratelabs/proxy-wasm-go-sdk/proxywasm"
	"github.com/tetratelabs/proxy-wasm-go-sdk/proxywasm/types"
)

const tickMilliseconds uint32 = 1000

func main() {}

func init() {
	proxywasm.SetVMContext(&vmContext{})
}

type vmContext struct{ types.DefaultVMContext }

func (*vmContext) NewPluginContext(contextID uint32) types.PluginContext {
	return &pluginContext{
		contextID:      contextID,
		frozenRequests: make(map[uint32]*frozenRequest),
	}
}

// --- PLUGIN CONTEXT (Global Manager) ---

type frozenRequest struct {
	traceID        string
	checkScheduled bool
}

type pluginContext struct {
	types.DefaultPluginContext
	contextID      uint32
	frozenRequests map[uint32]*frozenRequest
	tickCount      uint32
}

func (ctx *pluginContext) OnPluginStart(pluginConfigurationSize int) types.OnPluginStartStatus {
	if err := proxywasm.SetTickPeriodMilliSeconds(tickMilliseconds); err != nil {
		proxywasm.LogCriticalf("Failed to set tick period: %v", err)
		return types.OnPluginStartStatusFailed
	}
	proxywasm.LogInfof("Ticker started (%d ms)", tickMilliseconds)
	return types.OnPluginStartStatusOK
}

func (ctx *pluginContext) OnTick() {
	ctx.tickCount++
	
	if len(ctx.frozenRequests) == 0 {
		return
	}

	proxywasm.LogInfof("[TICK #%d] Checking %d frozen requests", ctx.tickCount, len(ctx.frozenRequests))

	for httpID, fr := range ctx.frozenRequests {
		if fr.checkScheduled {
			continue
		}

		currentHttpID := httpID
		currentTraceID := fr.traceID
		fr.checkScheduled = true

		if _, err := proxywasm.DispatchHttpCall(
			"control_plane",
			[][2]string{
				{":method", "GET"},
				{":path", "/check?trace_id=" + currentTraceID},
				{":authority", "control-plane"},
			},
			nil, nil, 5000,
			ctx.createTickCheckCallback(currentHttpID, currentTraceID),
		); err != nil {
			proxywasm.LogErrorf("Failed to dispatch check for context %d: %v", currentHttpID, err)
			fr.checkScheduled = false
		}
	}
}

func (ctx *pluginContext) createTickCheckCallback(httpID uint32, traceID string) func(int, int, int) {
	return func(numHeaders, bodySize, numTrailers int) {
		if fr, exists := ctx.frozenRequests[httpID]; exists {
			fr.checkScheduled = false
		}

		body, err := proxywasm.GetHttpCallResponseBody(0, bodySize)
		if err != nil {
			proxywasm.LogErrorf("Failed to get response body: %v", err)
			return
		}

		responseStr := string(body)

		if !strings.Contains(responseStr, "freeze") {
			proxywasm.LogInfof("🟢 ALLOW - Resuming Context %d (Trace: %s)", httpID, traceID)

			delete(ctx.frozenRequests, httpID)

			if err := proxywasm.SetEffectiveContext(httpID); err != nil {
				proxywasm.LogCriticalf("❌ Failed to set effective context %d: %v", httpID, err)
				return
			}

			if err := proxywasm.ResumeHttpRequest(); err != nil {
				proxywasm.LogCriticalf("❌ Failed to resume request %d: %v", httpID, err)
			} else {
				proxywasm.LogInfof("✅ Resumed request %d", httpID)
			}
		}
	}
}

func (ctx *pluginContext) NewHttpContext(contextID uint32) types.HttpContext {
	return &httpContext{
		contextID: contextID,
		pluginCtx: ctx,
	}
}

// --- HTTP CONTEXT (Per Request) ---

type httpContext struct {
	types.DefaultHttpContext
	contextID uint32
	pluginCtx *pluginContext
	traceID   string
}

func (ctx *httpContext) OnHttpRequestHeaders(numHeaders int, endOfStream bool) types.Action {
	ctx.traceID = ctx.extractTraceID()

	if ctx.traceID == "" {
		return types.ActionContinue
	}

	proxywasm.LogInfof("📥 Request with Trace: %s", ctx.traceID)
	ctx.initialCheck()
	return types.ActionPause
}

func (ctx *httpContext) initialCheck() {
	if _, err := proxywasm.DispatchHttpCall(
		"control_plane",
		[][2]string{
			{":method", "GET"},
			{":path", "/check?trace_id=" + ctx.traceID},
			{":authority", "control-plane"},
		},
		nil, nil, 5000,
		ctx.createInitialCheckCallback(),
	); err != nil {
		proxywasm.LogErrorf("Failed initial check dispatch: %v", err)
		proxywasm.ResumeHttpRequest()
	}
}

func (ctx *httpContext) createInitialCheckCallback() func(int, int, int) {
	return func(numHeaders, bodySize, numTrailers int) {
		body, err := proxywasm.GetHttpCallResponseBody(0, bodySize)
		if err != nil {
			proxywasm.LogErrorf("Failed to get response body: %v", err)
			proxywasm.ResumeHttpRequest()
			return
		}

		responseStr := string(body)

		if strings.Contains(responseStr, "freeze") {
			proxywasm.LogInfof("❄️ FREEZE - Context %d (Trace: %s)", ctx.contextID, ctx.traceID)
			
			ctx.pluginCtx.frozenRequests[ctx.contextID] = &frozenRequest{
				traceID:        ctx.traceID,
				checkScheduled: false,
			}

			proxywasm.LogInfof("Registered - Total frozen: %d", len(ctx.pluginCtx.frozenRequests))
		} else {
			proxywasm.LogInfof("🟢 ALLOW - Context %d (Trace: %s)", ctx.contextID, ctx.traceID)
			if err := proxywasm.ResumeHttpRequest(); err != nil {
				proxywasm.LogErrorf("Failed to resume: %v", err)
			}
		}
	}
}

func (ctx *httpContext) extractTraceID() string {
	if val, err := proxywasm.GetHttpRequestHeader("traceparent"); err == nil && len(val) >= 35 {
		return val[3:35]
	}
	return ""
}

func (ctx *httpContext) OnHttpStreamDone() {
	if _, exists := ctx.pluginCtx.frozenRequests[ctx.contextID]; exists {
		delete(ctx.pluginCtx.frozenRequests, ctx.contextID)
		proxywasm.LogInfof("Cleaned up frozen context %d", ctx.contextID)
	}
}
