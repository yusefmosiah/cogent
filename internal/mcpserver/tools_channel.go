package mcpserver

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type reportInput struct {
	Message string `json:"message" jsonschema:"status report to send to supervisor or host"`
	Type    string `json:"type,omitempty" jsonschema:"message type: info, status_update, escalation (default: info)"`
}

func registerChannelTools(server *mcp.Server, mcpSrv *Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "report",
		Description: "Report status to whoever dispatched you (supervisor or host). Use to report progress, completion, questions, or issues.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input reportInput) (*mcp.CallToolResult, any, error) {
		msg := strings.TrimSpace(input.Message)
		if msg == "" {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "error: message must not be empty"}}}, nil, nil
		}
		msgType := input.Type
		if msgType == "" {
			msgType = "info"
		}

		if err := mcpSrv.SendChannelEvent(msg, map[string]string{"source": "agent", "type": msgType}); err != nil {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "error: " + err.Error()}}}, nil, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Report sent."}}}, nil, nil
	})
}
