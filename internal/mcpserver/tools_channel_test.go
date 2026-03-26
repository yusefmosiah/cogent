package mcpserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yusefmosiah/cogent/internal/service"
)

func TestReportToolIsNotRegistered(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	server := New(&service.Service{})

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		_ = server.MCP.Run(ctx, serverTransport)
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer func() { _ = session.Close() }()

	_, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "report",
		Arguments: map[string]any{"message": "hello from mcp"},
	})
	if err == nil {
		t.Fatal("expected report tool call to fail when zero tools are registered")
	}
	if !strings.Contains(err.Error(), `unknown tool "report"`) {
		t.Fatalf("unexpected error: %v", err)
	}

	cancel()
	<-serverDone
}
