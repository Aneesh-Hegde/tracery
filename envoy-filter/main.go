package main

import (
	"strings"

	"github.com/buger/jsonparser"
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
	
	// Cache headers to reuse them during the freeze loop
	cachedHeaders [][2]string
}

func (ctx *httpContext) OnHttpRequestHeaders(numHeaders int, endOfStream bool) types.Action {
	// 1. Cache headers immediately. 
	// We cannot access them later during OnCheckResponse (the freeze loop).
	headers, err := proxywasm.GetHttpRequestHeaders()
	if err == nil {
		ctx.cachedHeaders = headers
	} else {
		proxywasm.LogErrorf("failed to get request headers: %v", err)
	}

	ctx.traceID = ctx.extractTraceID()
	if ctx.traceID == "" {
		return types.ActionContinue
	}

	// Remove Content-Length to allow body mutation later
	proxywasm.RemoveHttpRequestHeader("content-length")
	
	ctx.method, _ = proxywasm.GetHttpRequestHeader(":method")
	ctx.path, _ = proxywasm.GetHttpRequestHeader(":path")
	ctx.authority, _ = proxywasm.GetHttpRequestHeader(":authority")

	// If no body (GET request), check freeze logic immediately
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

	// Buffer until full body is received
	if !endOfStream {
		return types.ActionPause
	}

	// Trigger freeze check
	if ctx.callControlPlane() {
		return types.ActionPause
	}

	return types.ActionContinue
}

func (ctx *httpContext) callControlPlane() bool {
	// 1. Get the Request Body
	// We read up to 1MB (industry standard safety limit for inspection)
	bodyBytes, err := proxywasm.GetHttpRequestBody(0, 1024*1024)
	if err != nil {
		bodyBytes = []byte("{}") // Empty JSON if no body
	}

	// 2. Prepare Headers for the /check call
	// We move metadata (TraceID, Service) to Headers so the Body can be just the payload
	cpHeaders := [][2]string{
		{":method", "POST"}, // ✅ Changed to POST
		{":path", "/check"},
		{":authority", "control-plane"},
		{"x-trace-id", ctx.traceID},
		{"x-service-name", ctx.authority},
		{"x-endpoint", ctx.path},
		{"content-type", "application/json"},
	}

	// 3. Forward Original Headers (for header conditions)
	if len(ctx.cachedHeaders) > 0 {
		for _, h := range ctx.cachedHeaders {
			key := strings.ToLower(h[0])
			if !strings.HasPrefix(key, ":") {
				// Prefix them so Control Plane knows they are from the user
				cpHeaders = append(cpHeaders, [2]string{"x-orig-" + key, h[1]})
			}
		}
	}

	// 4. Dispatch with BODY
	_, err = proxywasm.DispatchHttpCall(
		"control_plane",
		cpHeaders,
		bodyBytes, // ✅ Sending the actual user body!
		nil, 5000, ctx.OnCheckResponse,
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

	action, err := jsonparser.GetString(body, "action")
	if err != nil {
		proxywasm.ResumeHttpRequest()
		return
	}

	if action == "freeze" {
		proxywasm.LogInfof("FREEZING Trace: %s", ctx.traceID)

		if !ctx.snapshotSent {
			ctx.captureAndSendSnapshot()
			ctx.snapshotSent = true
		}

		// Recursively check (Infinite Loop / Polling) until released
		ctx.callControlPlane()

	} else {
		proxywasm.LogInfof("UNFREEZING Trace: %s", ctx.traceID)

		// Check for Body Mutation
		overrideBody, err := jsonparser.GetString(body, "override_body")
		if err == nil && overrideBody != "" {
			proxywasm.LogInfof("✏️ MUTATING BODY: %s", overrideBody)
			proxywasm.ReplaceHttpRequestBody([]byte(overrideBody))
		}
		
		// Recalculate content-length if needed
		currentBody, _ := proxywasm.GetHttpRequestBody(0, 1024*1024)
		if currentBody != nil {
			proxywasm.ReplaceHttpRequestHeader("content-length", itoa(len(currentBody)))
		}

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

	if ctx.method == "" { ctx.method = "UNKNOWN" }
	if ctx.path == "" { ctx.path = "/" }
	if ctx.authority == "" { ctx.authority = "unknown" }

	var sb strings.Builder
	sb.WriteString(`{`)
	sb.WriteString(`"trace_id":"` + ctx.traceID + `", `)
	sb.WriteString(`"service_name":"` + ctx.authority + `", `)
	sb.WriteString(`"method":"` + ctx.method + ` ` + ctx.path + `", `)
	sb.WriteString(`"body":"` + safeBody + `"`)
	sb.WriteString(`}`)

	proxywasm.DispatchHttpCall(
		"control_plane",
		[][2]string{
			{":method", "POST"},
			{":path", "/snapshot"},
			{":authority", "control-plane"},
			{"content-type", "application/json"},
		},
		[]byte(sb.String()),
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

func itoa(i int) string {
	if i == 0 { return "0" }
	var b [20]byte
	bp := len(b) - 1
	for i >= 10 || i < 0 {
		q := i / 10
		b[bp] = byte('0' + i - q*10)
		bp--
		i = q
	}
	b[bp] = byte('0' + i)
	return string(b[bp:])
}
