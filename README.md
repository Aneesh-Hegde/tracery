# Tracery

**Distributed debugging woven into your mesh**

---

## What is Tracery?

Tracery is a distributed debugging tool that enables developers to set breakpoints across microservice boundaries and capture consistent snapshots of application state and database records for a single request trace.

Think of it as a traditional debugger, but instead of pausing one process, it can freeze an entire distributed request across multiple services and databases simultaneously.

---

## The Problem

Debugging distributed systems is incredibly difficult:

- When a request fails after touching 5 different microservices, how do you know which service had the wrong state?
- How do you inspect variables across multiple services at the exact same moment in time?
- Traditional debuggers only work on single processes - they can't coordinate across network boundaries
- By the time you check the database, the application state has already changed

**Example Scenario**: 
A user's payment succeeds in the Payment Service, but their order shows as "pending" in the Order Service. The bug happens somewhere between three microservices and two databases. Traditional logging shows timestamps but not the exact state at the moment of failure.

---

## The Solution

Tracery solves this by providing:

### 1. **Distributed Breakpoints**
Set a breakpoint in one service, and Tracery automatically pauses the entire request across all services it touches.

### 2. **Coordinated Traffic Freezing**
Using service mesh technology (Istio/Envoy), Tracery pauses network traffic for a specific request trace while letting all other requests continue normally.

### 3. **Consistent State Snapshots**
Captures a synchronized snapshot of:
- Application variables (local variables, stack traces, function arguments)
- Database state (relevant records from PostgreSQL, MongoDB, Redis)
- Request context and timing information

All captured at the same logical moment, giving you a complete picture of what went wrong.

---

## How It Works

```
1. Developer sets breakpoint → "Pause when Order Service processes order #123"

2. Request flows through system → User creates order #123

3. Breakpoint is hit → Order Service reaches the specified line

4. Tracery freezes the trace → All services processing this request are paused

5. Snapshots are captured → Application state + database state collected

6. Developer inspects → Complete view of distributed state at moment of failure

7. Execution resumes → Request continues, other traffic unaffected
```

---

## Key Features

- **Cross-Service Breakpoints**: Set breakpoints that work across microservice boundaries
- **Zero Impact on Other Requests**: Only the debugged trace is frozen, everything else runs normally
- **Polyglot Support**: Works with Go, Python, Node.js applications
- **Multi-Database**: Captures consistent state from PostgreSQL, MongoDB, and Redis
- **Production-Safe**: Built-in rate limiting and timeout mechanisms
- **Full Observability**: Integrates with OpenTelemetry and Jaeger for trace visualization

---

## Architecture Overview

Tracery consists of four main layers:

1. **Tracing Layer** (OpenTelemetry): Tags and tracks requests across services
2. **Traffic Control Layer** (Istio/Envoy): Intercepts and pauses network traffic
3. **Orchestration Layer** (Control Plane): Coordinates the freeze and snapshot process
4. **Capture Layer** (Snapshot Agents): Collects application and database state

---

## Use Cases

**Debugging Race Conditions**: Freeze the exact moment when two concurrent requests conflict

**Investigating Failed Transactions**: See why a distributed transaction left the system in an inconsistent state

**Understanding Data Flow**: Visualize how data transforms as it moves through your microservices

**Production Debugging**: Debug specific user requests in production without affecting other users

---

## Technology Stack

- **Kubernetes**: Orchestration platform
- **Istio/Envoy**: Service mesh for traffic control
- **OpenTelemetry + Jaeger**: Distributed tracing
- **Go**: Control plane and agents
- **gRPC**: Inter-component communication

---

## Project Status

**Current Status**: Beta / Hackathon Project  
**Goal**: Prove the concept of distributed breakpoints with consistent state capture

This is an experimental tool demonstrating advanced distributed systems debugging techniques. It combines service mesh technology, distributed tracing, and runtime debugging to solve problems that traditional tools cannot handle.

---

## Quick Example

```bash
# Set a breakpoint in the Order Service
tracery breakpoint set --service order-service --line 42

# Send a request that will hit the breakpoint
curl http://api-gateway/orders

# View the captured state
tracery snapshot view <trace-id>

# Resume execution
tracery trace resume <trace-id>
```

---

## Why "Tracery"?

The name combines "trace" (distributed tracing) with the suffix "-ery" (like Meshery), and references the architectural concept of tracery - ornamental stonework forming intricate patterns, much like how traces form patterns across distributed systems.


