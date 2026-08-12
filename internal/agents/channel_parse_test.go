package agents

import (
	"testing"
)

// TestChannelFrontmatterParsing 回归：agents/*.md 的 channel frontmatter 字段
// 必须被解析进 MultiAgentSubConfig.Channel / OrchestratorMarkdown.Channel，
// 供多代理通道路由使用。
func TestChannelFrontmatterParsing(t *testing.T) {
	src := `---
id: demo
name: 演示
description: 测试通道解析
channel: cheap
tools: []
max_iterations: 0
---

正文
`
	sub, err := ParseMarkdownSubAgent("demo.md", src)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if sub.Channel != "cheap" {
		t.Fatalf("channel 应为 cheap, got %q", sub.Channel)
	}

	// 回写后再解析（BuildMarkdownFile 保留 channel）
	out, err := BuildMarkdownFile(sub)
	if err != nil {
		t.Fatalf("BuildMarkdownFile: %v", err)
	}
	sub2, err := ParseMarkdownSubAgent("demo.md", string(out))
	if err != nil {
		t.Fatalf("回写后解析失败: %v", err)
	}
	if sub2.Channel != "cheap" {
		t.Fatalf("BuildMarkdownFile 应保留 channel, got %q", sub2.Channel)
	}
}

// TestChannelEmptyMeansFollowSession 未写 channel 时解析为空串（跟随会话通道）。
func TestChannelEmptyMeansFollowSession(t *testing.T) {
	src := `---
id: demo2
name: 演示2
description: 无通道
---

正文
`
	sub, err := ParseMarkdownSubAgent("demo2.md", src)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if sub.Channel != "" {
		t.Fatalf("未配置 channel 应为空, got %q", sub.Channel)
	}
}
