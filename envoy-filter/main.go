package main

import (
	"strings"

	"github.com/tetratelabs/proxy-wasm-go-sdk/proxywasm"
	"github.com/tetratelabs/proxy-wasm-go-sdk/proxywasm/types"
)

func main() {}

func init() {
	proxywasm.SetVMContext(&vmContext{})
}

type vmContext struct{ types.DefaultVMContext }

func (*vmContext) NewPluginContext(contextID uint32) types.PluginContext {
	return &pluginContext{}
}

type pluginContext struct{ types.DefaultPluginContext }

func (*pluginContext) NewHttpContext(contextID uint32) types.HttpContext {
	return &httpContext{}
}

type httpContext struct {
	types.DefaultHttpContext
	traceID      string
	snapshotSent bool
	method       string
	path         string
	authority    string
}

func (ctx *httpContext) OnHttpRequestHeaders(numHeaders int, endOfStream bool) types.Action {
	ctx.traceID = ctx.extractTraceID()
	if ctx.traceID == "" {
		return types.ActionContinue
	}

	// Cache metadata early
	ctx.method, _ = proxywasm.GetHttpRequestHeader(":method")
	ctx.path, _ = proxywasm.GetHttpRequestHeader(":path")
	ctx.authority, _ = proxywasm.GetHttpRequestHeader(":authority")

	// If no body (GET), freeze now
	if endOfStream {
		if ctx.callControlPlane() {
			return types.ActionPause
		}
	}

	return types.ActionContinue
}

func (ctx *httpContext) OnHttpRequestBody(bodySize int, endOfStream bool) types.Action {
	if ctx.traceID == "" {
		return types.ActionContinue
	}

	// Force buffering until full body is received
	if !endOfStream {
		return types.ActionPause
	}

	// Trigger freeze check with full body
	if ctx.callControlPlane() {
		return types.ActionPause
	}

	return types.ActionContinue
}

func (ctx *httpContext) callControlPlane() bool {
	_, err := proxywasm.DispatchHttpCall(
		"control_plane",
		[][2]string{
			{":method", "GET"},
			{":path", "/check?trace_id=" + ctx.traceID},
			{":authority", "control-plane"},
		},
		nil, nil, 5000, ctx.OnCheckResponse,
	)

	if err != nil {
		proxywasm.LogCriticalf("Dispatch failed: %v", err)
		proxywasm.ResumeHttpRequest()
		return false
	}
	return true
}

func (ctx *httpContext) OnCheckResponse(numHeaders int, bodySize int, numTrailers int) {
	body, err := proxywasm.GetHttpCallResponseBody(0, bodySize)
	if err != nil {
		proxywasm.ResumeHttpRequest()
		return
	}

	responseStr := string(body)

	if strings.Contains(responseStr, "freeze") {
		proxywasm.LogInfof("FREEZING Trace: %s", ctx.traceID)

		if !ctx.snapshotSent {
			ctx.captureAndSendSnapshot()
			ctx.snapshotSent = true
		}

		ctx.callControlPlane()

	} else {
		proxywasm.LogInfof("UNFREEZING Trace: %s", ctx.traceID)
		proxywasm.ResumeHttpRequest()
	}
}

func (ctx *httpContext) captureAndSendSnapshot() {
	bodyBytes, err := proxywasm.GetHttpRequestBody(0, 1024*1024)
	if err != nil {
		bodyBytes = []byte("{}")
	}

	bodyStr := string(bodyBytes)
	safeBody := strings.ReplaceAll(bodyStr, "\"", "\\\"")
	safeBody = strings.ReplaceAll(safeBody, "\n", " ")

	if ctx.method == "" {
		ctx.method = "UNKNOWN"
	}
	if ctx.path == "" {
		ctx.path = "/"
	}
	if ctx.authority == "" {
		ctx.authority = "unknown"
	}

	var sb strings.Builder
	sb.WriteString(`{`)
	sb.WriteString(`"trace_id":"` + ctx.traceID + `", `)
	sb.WriteString(`"service_name":"` + ctx.authority + `", `)
	sb.WriteString(`"method":"` + ctx.method + ` ` + ctx.path + `", `)
	sb.WriteString(`"body":"` + safeBody + `"`)
	sb.WriteString(`}`)

	payload := sb.String()

	proxywasm.DispatchHttpCall(
		"control_plane",
		[][2]string{
			{":method", "POST"},
			{":path", "/snapshot"},
			{":authority", "control-plane"},
			{"content-type", "application/json"},
		},
		[]byte(payload),
		nil, 5000, func(n, b, t int) {},
	)
	proxywasm.LogInfo("Snapshot sent!")
}

func (ctx *httpContext) extractTraceID() string {
	if val, err := proxywasm.GetHttpRequestHeader("traceparent"); err == nil && len(val) >= 35 {
		return val[3:35]
	}
	return ""
}
