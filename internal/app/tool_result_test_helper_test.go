package app

import (
	"encoding/json"

	"cyberstrike-ai/internal/mcp"
)

// toolResultText 提取工具结果文本（供 MCP 工具单测断言）。
func toolResultText(result *mcp.ToolResult) string {
	if result == nil {
		return ""
	}
	for _, c := range result.Content {
		if c.Type == "text" {
			return c.Text
		}
	}
	// 无 text 内容时退化为 JSON 概览
	b, err := json.Marshal(result)
	if err != nil {
		return ""
	}
	return string(b)
}
