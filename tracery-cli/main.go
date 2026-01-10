package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	pb "github.com/Aneesh-Hegde/tracery/control-plane/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	conn, err := grpc.NewClient("localhost:50051",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := pb.NewControlPlaneClient(conn)
	ctx := context.Background()

	switch os.Args[1] {
	case "set-breakpoint":
		if len(os.Args) < 4 {
			fmt.Println("Usage: dcdot-cli set-breakpoint <service> <endpoint> [key=value...]")
			os.Exit(1)
		}
		setBreakpoint(ctx, client, os.Args[2:])

	case "list-breakpoints":
		listBreakpoints(ctx, client)

	case "delete-breakpoint":
		if len(os.Args) < 3 {
			fmt.Println("Usage: dcdot-cli delete-breakpoint <id>")
			os.Exit(1)
		}
		deleteBreakpoint(ctx, client, os.Args[2])

	case "watch-traces":
		watchTraces(ctx, client)

	case "get-snapshot":
		if len(os.Args) < 4 || os.Args[2] != "--trace" {
			fmt.Println("Usage: tracery-cli get-snapshot --trace <trace-id>")
			os.Exit(1)
		}
		getSnapshot(ctx, client, os.Args[3])

	case "debug-app":
		if len(os.Args) < 4 || os.Args[2] != "--trace" {
			fmt.Println("Usage: tracery-cli debug-app --trace <trace-id>")
			os.Exit(1)
		}
		getAppSnapshot(ctx, client, os.Args[3])
	case "mesh":
		if len(os.Args) < 3 || os.Args[2] != "topology" {
			fmt.Println("Usage: tracery-cli mesh topology")
			os.Exit(1)
		}
		getTopology(ctx, client)

	case "system":
		if len(os.Args) < 3 || os.Args[2] != "health" {
			fmt.Println("Usage: tracery-cli system health")
			os.Exit(1)
		}
		getSystemHealth(ctx, client)

	case "emergency":
		if len(os.Args) < 3 || os.Args[2] != "disable" {
			fmt.Println("Usage: tracery-cli emergency disable")
			os.Exit(1)
		}
		emergencyRelease(ctx, client)
	case "freeze":
		if len(os.Args) < 3 {
			fmt.Println("Usage: dcdot-cli freeze start|status|list|release ...")
			os.Exit(1)
		}
		switch os.Args[2] {
		case "start":
			if len(os.Args) < 5 {
				fmt.Println("Usage: dcdot-cli freeze start --trace <id> --services a,b,c")
				os.Exit(1)
			}
			var traceID string
			var services []string
			for i := 3; i < len(os.Args); i++ {
				if os.Args[i] == "--trace" && i+1 < len(os.Args) {
					traceID = os.Args[i+1]
					i++
				} else if os.Args[i] == "--services" && i+1 < len(os.Args) {
					services = strings.Split(os.Args[i+1], ",")
					i++
				}
			}
			if traceID == "" {
				fmt.Println("Missing --trace")
				os.Exit(1)
			}
			freezeTrace(ctx, client, traceID, services)

		case "status":
			if len(os.Args) != 5 || os.Args[3] != "--trace" {
				fmt.Println("Usage: dcdot-cli freeze status --trace <id>")
				os.Exit(1)
			}
			freezeStatus(ctx, client, os.Args[4])

		case "list":
			listFreezes(ctx, client)

		case "release":
			if len(os.Args) < 5 {
				fmt.Println("Usage: dcdot-cli freeze release --trace <id> [--override-body <json>]")
				os.Exit(1)
			}
			traceID := ""
			overrideBody := ""

			for i := 3; i < len(os.Args); i++ {
				if os.Args[i] == "--trace" && i+1 < len(os.Args) {
					traceID = os.Args[i+1]
				}
				if os.Args[i] == "--override-body" && i+1 < len(os.Args) {
					overrideBody = os.Args[i+1]
				}
			}
			releaseTrace(ctx, client, traceID, overrideBody)

		default:
			fmt.Printf("Unknown freeze subcommand: %s\n", os.Args[2])
			printUsage()
			os.Exit(1)
		}

	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("DCDOT CLI")
	fmt.Println("\nCommands:")
	fmt.Println("  set-breakpoint <service> <endpoint> [conditions...]")
	fmt.Println("  list-breakpoints")
	fmt.Println("  delete-breakpoint <id>")
	fmt.Println("  watch-traces")
	fmt.Println("  get-snapshot <trace-id>")
	fmt.Println("  freeze start --trace <id> --services a,b,c")
	fmt.Println("  freeze status --trace <id>")
	fmt.Println("  freeze list")
	fmt.Println("  freeze release --trace <id>")
	fmt.Println("  get-snapshot --trace <id>")
	fmt.Println("  debug-app --trace <id>        (Application Snapshot - Stack/Vars)")
}

// ---------------------------------------------------------------------
// Rest of your functions (unchanged)
// ---------------------------------------------------------------------

func setBreakpoint(ctx context.Context, client pb.ControlPlaneClient, args []string) {
	conditions := make(map[string]string)
	for i := 2; i < len(args); i++ {
		parts := strings.SplitN(args[i], "=", 2)
		if len(parts) == 2 {
			conditions[parts[0]] = parts[1]
		}
	}

	resp, err := client.RegisterBreakpoint(ctx, &pb.RegisterBreakPointRequest{
		ServiceName: args[0],
		Endpoint:    args[1],
		Conditions:  conditions,
	})
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	fmt.Printf("Breakpoint: %s\n", resp.BreakpointId)
	fmt.Printf("   Service: %s%s\n", args[0], args[1])
	if len(conditions) > 0 {
		fmt.Printf("   Conditions: %v\n", conditions)
	}
}

func listBreakpoints(ctx context.Context, client pb.ControlPlaneClient) {
	resp, err := client.ListBreakpoints(ctx, &pb.ListBreakpointsRequest{})
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	if len(resp.Breakpoints) == 0 {
		fmt.Println("No breakpoints")
		return
	}
	fmt.Printf("BreakPoints (%d):\n\n", len(resp.Breakpoints))
	for i, bp := range resp.Breakpoints {
		fmt.Printf("%d. %s\n", i+1, bp.Id)
		fmt.Printf("   %s%s\n", bp.ServiceName, bp.Endpoint)
		if len(bp.Conditions) > 0 {
			fmt.Printf("   Conditions: %v\n", bp.Conditions)
		}
		fmt.Println()
	}
}

func deleteBreakpoint(ctx context.Context, client pb.ControlPlaneClient, id string) {
	resp, err := client.DeleteBreakPoint(ctx, &pb.DeleteBreakPointRequest{
		BreakpointId: id,
	})
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	if resp.Success {
		fmt.Printf("Deleted: %s\n", id)
	} else {
		fmt.Printf("%s\n", resp.RespMessage)
	}
}

func watchTraces(ctx context.Context, client pb.ControlPlaneClient) {
	fmt.Println("Watching traces (Ctrl+C to stop)...")
	stream, err := client.StreamTraces(ctx, &pb.StreamTracesRequest{})
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	for {
		event, err := stream.Recv()
		if err != nil {
			log.Fatalf("Stream Error: %v", err)
		}
		fmt.Printf("[%s] %s %s%s\n",
			time.Unix(event.Timestamp, 0).Format("15:04:05"),
			event.TraceId, event.ServiceName, event.Endpoint)
	}
}

func freezeTrace(ctx context.Context, client pb.ControlPlaneClient, traceID string, services []string) {
	req := &pb.FreezeTraceRequest{
		TraceId:  traceID,
		Services: services,
	}
	resp, err := client.FreezeTrace(ctx, req)
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	if resp.Success {
		fmt.Printf("Freeze initiated for trace %s\n", traceID)
		if len(services) > 0 {
			fmt.Printf("   Services: %s\n", strings.Join(services, ", "))
		} else {
			fmt.Println("   Services: all (default)")
		}
		fmt.Printf("   State: %s\n", resp.State)
	} else {
		fmt.Printf("Failed: %s\n", resp.RespMessage)
	}
}

func releaseTrace(ctx context.Context, client pb.ControlPlaneClient, traceID string, body string) {
	resp, err := client.ReleaseTrace(ctx, &pb.ReleaseTraceRequest{TraceId: traceID, OverrideBody: body})
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	if resp.Success {
		fmt.Printf("Trace %s released\n", traceID)
	} else {
		fmt.Printf("Failed: %s\n", resp.RespMessage)
	}
}

func freezeStatus(ctx context.Context, client pb.ControlPlaneClient, traceID string) {
	resp, err := client.GetFreezeStatus(ctx, &pb.GetFreezeStatusRequest{TraceId: traceID})
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	if resp.State == "not_found" {
		fmt.Printf("No freeze found for trace %s\n", traceID)
		return
	}
	fmt.Printf("Trace %s – Freeze status\n", resp.TraceId)
	fmt.Printf("   State       : %s\n", resp.State)
	fmt.Printf("   Frozen at   : %s\n", time.Unix(resp.FrozenAt, 0).Format(time.RFC3339))
	fmt.Printf("   Breakpoint  : %s\n", resp.BreakpointId)
	fmt.Printf("   Services    : %s\n", strings.Join(resp.Services, ", "))
}

func listFreezes(ctx context.Context, client pb.ControlPlaneClient) {
	resp, err := client.ListActiveFreezes(ctx, &pb.ListActiveFreezesRequest{})
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	if len(resp.Freezes) == 0 {
		fmt.Println("No active freezes")
		return
	}
	fmt.Printf("Active freezes (%d):\n\n", len(resp.Freezes))
	for i, f := range resp.Freezes {
		fmt.Printf("%d. Trace %s\n", i+1, f.TraceId)
		fmt.Printf("   State    : %s\n", f.State)
		fmt.Printf("   Frozen at: %s\n", time.Unix(f.FrozenAt, 0).Format(time.RFC3339))
		fmt.Printf("   Services : %s\n", strings.Join(f.Services, ", "))
		fmt.Println()
	}
}

func getSnapshot(ctx context.Context, client pb.ControlPlaneClient, traceID string) {
	resp, err := client.GetSnapshot(ctx, &pb.GetSnapshotRequest{TraceId: traceID})
	if err != nil {
		log.Fatalf("Error communicating with Control Plane: %v", err)
	}

	if !resp.Success {
		fmt.Printf("❌ %s\n", resp.RespMessage)
		return
	}

	snap := resp.SnapshotData
	fmt.Println("📸 SNAPSHOT DATA CAPTURED")
	fmt.Println("==================================================")
	fmt.Printf("Trace ID:    %s\n", snap.TraceId)
	fmt.Printf("Service:     %s\n", snap.ServiceName)
	fmt.Printf("Method:      %s\n", snap.Method)
	fmt.Println("--------------------------------------------------")
	fmt.Println("📦 BODY PAYLOAD:")
	fmt.Println(snap.Body)
	fmt.Println("==================================================")
}

func getAppSnapshot(ctx context.Context, client pb.ControlPlaneClient, traceID string) {
	resp, err := client.GetAppSnapshot(ctx, &pb.GetAppSnapshotRequest{TraceId: traceID})
	if err != nil {
		log.Fatalf("Error communicating with Control Plane: %v", err)
	}

	if !resp.Success || len(resp.Snapshots) == 0 {
		fmt.Println("❌ No application snapshots found for this trace.")
		return
	}

	fmt.Printf("\n🚀 DISTRIBUTED TRACE JOURNEY (%d Hops)\n", len(resp.Snapshots))

	// ✅ Loop through every snapshot in the list
	for i, snap := range resp.Snapshots {
		printSnapshot(i+1, snap)
	}
}

func printSnapshot(order int, snap *pb.AppSnapshot) {
	fmt.Printf("\n[%d] 📦 SERVICE: %s\n", order, strings.ToUpper(snap.ServiceName))
	fmt.Println("==================================================")
	fmt.Printf("Checkpoint:  %s\n", snap.Checkpoint)
	fmt.Printf("Time:        %s\n", snap.Timestamp)
	fmt.Println("--------------------------------------------------")
	fmt.Println("🔍 LOCAL VARIABLES:")
	for k, v := range snap.LocalVars {
		if v == "" {
			v = "(empty)"
		}
		fmt.Printf("   %-12s = %s\n", k, v)
	}
	fmt.Println("--------------------------------------------------")
	fmt.Println("🥞 STACK TRACE (User Code):")

	lines := strings.Split(snap.StackTrace, "\n")
	foundUserCode := false

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "goroutine") {
			continue
		}

		// Heuristic: Is this line a function call? (vs a file path line)
		isFuncLine := !strings.Contains(line, "/") && !strings.HasPrefix(line, "\t")

		if isFuncLine {
			// Look ahead to the NEXT line (which contains the file path) to decide context
			if i+1 < len(lines) {
				pathLine := strings.TrimSpace(lines[i+1])

				// 🛡️ UNIVERSAL FILTER:
				// If path contains "/pkg/mod/" -> It's a 3rd party dependency
				// If path contains "/src/runtime/" or "/src/net/" -> It's Go StdLib
				// If path contains ".pb.go" -> It's generated Proto code (usually noise)
				isDependency := strings.Contains(pathLine, "/pkg/mod/") ||
					strings.Contains(pathLine, "/src/") || // Catch-all for stdlib
					strings.Contains(pathLine, "vendor/") ||
					strings.Contains(pathLine, ".pb.go")

				if !isDependency {
					// It's USER CODE!
					funcName := strings.Split(line, "(")[0] // Clean arguments
					fmt.Printf("   👉 %s\n", funcName)

					// Print the path cleanly
					cleanPath := strings.Split(pathLine, " +")[0] // Remove offset
					// Try to shorten absolute paths for readability
					// e.g. /Users/aneesh/go/src/github.com/repo/main.go -> main.go
					parts := strings.Split(cleanPath, "/")
					if len(parts) > 2 {
						cleanPath = parts[len(parts)-2] + "/" + parts[len(parts)-1]
					}
					fmt.Printf("      └─ 📂 %s\n", cleanPath)

					foundUserCode = true
				}
			}
		}
	}

	if !foundUserCode {
		fmt.Println("   (No user code found in stack - check filters)")
	}
	fmt.Println("==================================================")
}

func emergencyRelease(ctx context.Context, client pb.ControlPlaneClient) {
	resp, err := client.EmergencyRelease(ctx, &pb.Empty{})
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	fmt.Println("🚨 EMERGENCY PROTOCOL EXECUTED 🚨")
	fmt.Println("==================================================")
	fmt.Printf("Status:      %s\n", resp.Message)
	fmt.Printf("Traces Freed: %d\n", resp.FreedCount)
	fmt.Println("==================================================")
}

func getSystemHealth(ctx context.Context, client pb.ControlPlaneClient) {
	resp, err := client.GetSystemHealth(ctx, &pb.Empty{})
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	fmt.Println("🏥 SYSTEM HEALTH REPORT")
	fmt.Println("==================================================")
	status := "HEALTHY"
	if !resp.Healthy { status = "UNHEALTHY" }
	fmt.Printf("Overall Status: %s\n", status)
	fmt.Println("--------------------------------------------------")
	for k, v := range resp.ComponentStatus {
		fmt.Printf("   %-20s : %s\n", k, strings.ToUpper(v))
	}
	fmt.Println("==================================================")
}

func getTopology(ctx context.Context, client pb.ControlPlaneClient) {
	resp, err := client.GetTopology(ctx, &pb.Empty{})
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	fmt.Println("🕸️  SERVICE MESH TOPOLOGY")
	fmt.Println("==================================================")
	if len(resp.Links) == 0 {
		fmt.Println("   (No traffic detected yet)")
	}
	for _, link := range resp.Links {
		fmt.Printf("   %s  ──▶  %s\n", strings.ToUpper(link.Source), strings.ToUpper(link.Target))
	}
	fmt.Println("==================================================")
}
