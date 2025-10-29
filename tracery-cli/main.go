package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	pb "github.com/Aneesh-Hegde/tracery/control-plane/proto/controlplane"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	conn, err := grpc.NewClient("localhost:30051",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithTimeout(5*time.Second),
	)
	if err != nil {
		log.Fatal("Failed to connect:%v", err)
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
		if len(os.Args) < 3 {
			fmt.Println("Usage: dcdot-cli get-snapshot <trace-id>")
			os.Exit(1)
		}
		getSnapshot(ctx, client, os.Args[2])
	default:
		fmt.Printf("Unknown command :%s\n", os.Args[1])
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
}

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
		log.Fatal("Error:%v", err)
	}

	fmt.Printf("✅ Breakpoint: %s\n", resp.BreakpointId)
	fmt.Printf("   Service: %s%s\n", args[0], args[1])
	if len(conditions) > 0 {
		fmt.Printf("   Conditions: %v\n", conditions)
	}
}

func listBreakpoints(ctx context.Context, client pb.ControlPlaneClient) {
	resp, err := client.ListBreakpoints(ctx, &pb.ListBreakpointsRequest{})
	if err != nil {
		log.Fatal("Error:%v", err)
	}

	if len(resp.Breakpoints) == 0 {
		fmt.Println("No breakpoints")
		return
	}

	fmt.Println("BreakPoints (%d):\n\n", len(resp.Breakpoints))
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
		log.Fatal("Error:%v", err)
	}

	if resp.Success {
		fmt.Printf("✅ Deleted: %s\n", id)
	} else {
		fmt.Printf("❌ %s\n", resp.RespMessage)
	}

}

func watchTraces(ctx context.Context, client pb.ControlPlaneClient) {
	fmt.Println("Watching traces (Ctrl+C to stop)...\n")
	stream, err := client.StreamTraces(ctx, &pb.StreamTracesRequest{})
	if err != nil {
		log.Fatal("Error: %v", err)
	}

	for {
		event, err := stream.Recv()
		if err != nil {
			log.Fatal("Stream Error: %v", err)
		}
		fmt.Printf("[%s] %s %s%s\n", 
			time.Unix(event.Timestamp, 0).Format("15:04:05"),
			event.TraceId, event.ServiceName, event.Endpoint)
	}
}

func getSnapshot(ctx context.Context, client pb.ControlPlaneClient, traceID string) {
	resp, err := client.GetSnapshot(ctx, &pb.GetSnapshotRequest{TraceId: traceID})
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	if resp.Success {
		fmt.Println(resp.SnapshotData)
	} else {
		fmt.Printf("❌ %s\n", resp.RespMessage)
	}
}
