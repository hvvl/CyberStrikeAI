package multiagent

import "testing"

// TestCloneAnyMapSkipsLibraryMetaKeys 验证内存修复 G★：cloneAnyMap 跳过库内部
// Extra 元数据 key（_eino* 前缀 + openai-*/openai_* 前缀），仅保留业务自定义 key。
// 背景：首版只挡 `_eino` 前缀，14 小时真实使用后 cloneAnyMap 又涨回 0.75GB——
// eino-ext/libs/acl/openai 写入的 openai-request-id 等逃过了白名单。
func TestCloneAnyMapSkipsLibraryMetaKeys(t *testing.T) {
	// 纯库内部元数据：返回 nil（不产生任何拷贝对象）
	pureMeta := map[string]any{
		"_eino_ext_agenticopenai_chat_response_meta_ext": map[string]any{"id": "resp_1"},
		"openai-request-id":       "req_abc123",
		"openai-audio-id":         "audio_1",
		"openai_audio-transcript": "你好",
	}
	if got := cloneAnyMap(pureMeta); got != nil {
		t.Fatalf("pure library meta should yield nil, got %v", got)
	}

	// 混合：库元数据丢弃，业务 key 原样保留
	mixed := map[string]any{
		"_eino_ext_agenticopenai_chat_response_meta_ext": map[string]any{"id": "resp_2"},
		"openai-request-id":         "req_def456",
		"cyberstrike_trace_version": "v2",
		"custom_business_flag":      true,
	}
	got := cloneAnyMap(mixed)
	if len(got) != 2 {
		t.Fatalf("mixed map should keep 2 business keys, got %v", got)
	}
	if v, ok := got["cyberstrike_trace_version"].(string); !ok || v != "v2" {
		t.Fatalf("business key cyberstrike_trace_version lost or wrong: %v", got)
	}
	if v, ok := got["custom_business_flag"].(bool); !ok || !v {
		t.Fatalf("business key custom_business_flag lost or wrong: %v", got)
	}
	for _, k := range []string{"_eino_ext_agenticopenai_chat_response_meta_ext", "openai-request-id"} {
		if _, leaked := got[k]; leaked {
			t.Fatalf("library meta key %q leaked into clone", k)
		}
	}

	// 纯业务 key：全量拷贝、行为不变
	biz := map[string]any{
		"cyberstrike_trace_version": "v3",
		"my_tool_context":           42,
	}
	gotBiz := cloneAnyMap(biz)
	if len(gotBiz) != 2 {
		t.Fatalf("pure business map should be copied fully, got %v", gotBiz)
	}
	if v, ok := gotBiz["my_tool_context"].(int); !ok || v != 42 {
		t.Fatalf("business key my_tool_context lost or wrong: %v", gotBiz)
	}

	// 空输入
	if got := cloneAnyMap(nil); got != nil {
		t.Fatalf("nil input should yield nil, got %v", got)
	}
	if got := cloneAnyMap(map[string]any{}); got != nil {
		t.Fatalf("empty input should yield nil, got %v", got)
	}
}
