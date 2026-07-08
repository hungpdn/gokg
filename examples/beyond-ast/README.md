# Beyond AST: GoKG's Go-First Graph Model

Many polyglot knowledge graph tools start from source syntax using parsers such as [Tree-sitter](https://tree-sitter.github.io/tree-sitter/). That is fast and broadly useful, but Go teams often need relationships that come from Go's package, type, and concurrency model.

GoKG is built on `golang.org/x/tools/go/packages` and `go/types` (the same compiler tooling behind `gopls`). It analyzes your code exactly how the Go compiler sees it.

This directory contains concrete examples where GoKG exposes Go-specific relationships as first-class graph nodes and edges.

## 1. Implicit Interfaces (`01-implicit-interface`)

Go interfaces are satisfied implicitly. If a struct has the right methods, it implements the interface.

Syntax-only indexes can list interface and method declarations, but the implementation relationship itself is a type-system fact. GoKG emits it as an `IMPLEMENTS` edge.

**Test it with GoKG:**
```bash
# From the repository root
go run ./cmd/gokg analyze --rebuild
go run ./cmd/gokg query 'MATCH (s:STRUCT)-[r:IMPLEMENTS]->(i:INTERFACE) WHERE s.Name = "StripeProcessor" RETURN s.Name, i.Name LIMIT 10'
```

**GoKG Output:**
```json
[
  {
    "i.Name": "PaymentProcessor",
    "s.Name": "StripeProcessor"
  }
]
```

## 2. Concurrency Topology (`02-concurrency`)

Goroutines and channels are first-class citizens in Go programs. For architecture review, it is useful to query them directly instead of treating them as ordinary source text.

GoKG explicitly models concurrency boundaries. It tracks which functions spawn which goroutines, and which functions send to or receive from specific channels.

**Test it with GoKG:**

Find what spawns a goroutine:
```bash
go run ./cmd/gokg query 'MATCH (f:FUNC)-[r:SPAWNS]->(g:GOROUTINE) WHERE f.Name = "Dispatcher" RETURN f.Name, g.Name LIMIT 10'
```

Find who sends to a channel:
```bash
go run ./cmd/gokg query 'MATCH (f:FUNC)-[r:SENDS_TO]->(c:CHANNEL) WHERE f.Name = "Dispatcher" RETURN f.Name, c.Name LIMIT 10'
```

Find who receives from a channel:
```bash
go run ./cmd/gokg query 'MATCH (f:FUNC)-[r:RECEIVES_FROM]->(c:CHANNEL) WHERE f.Name = "worker" RETURN f.Name, c.Name LIMIT 10'
```

## Why this matters for AI Agents

When an AI coding agent tries to understand a large microservice architecture, it asks questions like *"What structs implement this interface?"* or *"Where does this channel receive data from?"*

When these relationships are first-class graph edges, an agent can ask targeted questions before editing code instead of reconstructing architecture from raw source snippets.

## 3. Multi-Repo Go Microservices (`03-multi-repo-workspace`)

If you are building microservices using Go, your code is likely split across multiple repositories (e.g., `order-service`, `payment-service`, and `shared-libs`). General-purpose indexes can inspect projects or containing folders; GoKG adds an explicit Go workspace model with `RepoID` preserved on graph nodes and edges.

GoKG analyzes multiple Go modules and merges their graphs while preserving repository ownership, so you can query service-level relationships across Go modules.

**Test it with GoKG:**

First, initialize a workspace and add the mock repos:
```bash
# From the repository root
gokg workspace init go-microservices-demo
gokg workspace add --workspace go-microservices-demo ./examples/beyond-ast/03-multi-repo-workspace/shared-libs
gokg workspace add --workspace go-microservices-demo ./examples/beyond-ast/03-multi-repo-workspace/order-service
gokg workspace add --workspace go-microservices-demo ./examples/beyond-ast/03-multi-repo-workspace/payment-service

# Analyze the workspace
gokg analyze --workspace go-microservices-demo --rebuild
```

Now, query for services that call methods owned by `shared-libs`:
```bash
gokg query --workspace go-microservices-demo 'MATCH (caller:FUNC)-[r:CALLS]->(callee:METHOD) WHERE callee.RepoID = "example.com/acme/shared-libs" RETURN caller.Name, caller.RepoID, callee.Name LIMIT 20'
```

**GoKG Output Shape** (result order may vary):
```json
[
  {
    "callee.Name": "Validate",
    "caller.Name": "ProcessOrder",
    "caller.RepoID": "example.com/acme/order-service"
  },
  {
    "callee.Name": "Validate",
    "caller.Name": "ConsumeOrderCreated",
    "caller.RepoID": "example.com/acme/payment-service"
  }
]
```

With GoKG, your AI agent can answer: *"If I change `OrderCreated.Validate()` in the shared library, which downstream Go services and routes may be affected?"*

See [`03-multi-repo-workspace/README.md`](03-multi-repo-workspace/README.md) for the full CodeGraph positioning example.
