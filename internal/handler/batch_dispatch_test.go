package handler

import (
	"strings"
	"testing"
)

func TestParseBatchCSVBasic(t *testing.T) {
	csv := &BatchDispatchCSV{Content: "a,b\n1,2\n3,4\n", SkipHeader: true}
	rows, err := parseBatchCSV(csv)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 2 || rows[0][0] != "1" || rows[0][1] != "2" {
		t.Fatalf("unexpected rows: %v", rows)
	}
}

func TestParseBatchCSVQuotedComma(t *testing.T) {
	csv := &BatchDispatchCSV{Content: "h1,h2\n\"a,b\",\"c\"\n", SkipHeader: true}
	rows, err := parseBatchCSV(csv)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 1 || rows[0][0] != "a,b" || rows[0][1] != "c" {
		t.Fatalf("quoted comma broken: %v", rows)
	}
}

func TestParseBatchCSVCRLFAndBOM(t *testing.T) {
	csv := &BatchDispatchCSV{Content: "\ufeffh1,h2\r\nx,y\r\n"}
	rows, err := parseBatchCSV(csv)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 2 || rows[0][0] != "h1" {
		t.Fatalf("BOM/CRLF broken: %v", rows)
	}
}

func TestParseBatchCSVGBK(t *testing.T) {
	gbk := []byte{0xC4, 0xE3, 0xBA, 0xC3, ',', 'x', '\n'} // "你好,x"
	csv := &BatchDispatchCSV{Content: string(gbk), Encoding: "gbk"}
	rows, err := parseBatchCSV(csv)
	if err != nil {
		t.Fatalf("gbk parse: %v", err)
	}
	if len(rows) != 1 || rows[0][0] != "你好" || rows[0][1] != "x" {
		t.Fatalf("gbk decode wrong: %v", rows)
	}
}

func TestParseBatchCSVSemicolon(t *testing.T) {
	csv := &BatchDispatchCSV{Content: "a;b\n1;2\n", Delimiter: ";"}
	rows, err := parseBatchCSV(csv)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 2 || rows[0][1] != "b" {
		t.Fatalf("semicolon broken: %v", rows)
	}
}

func TestParseBatchCSVTooLarge(t *testing.T) {
	csv := &BatchDispatchCSV{Content: strings.Repeat("x", MaxBatchDispatchCSVBytes+1)}
	if _, err := parseBatchCSV(csv); err == nil {
		t.Fatal("expected size limit error")
	}
}

func TestRenderBatchMessages(t *testing.T) {
	rows := [][]string{{"1.2.3.4", "80"}, {"5.6.7.8", "443"}}
	ph := []BatchDispatchPlaceholder{{Name: "target", Column: 1}}
	msgs, skipped, err := renderBatchMessages("打点 {{target}}", ph, rows, "skip_row")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if skipped != 0 || len(msgs) != 2 || msgs[0] != "打点 1.2.3.4" || msgs[1] != "打点 5.6.7.8" {
		t.Fatalf("unexpected: %v %v", msgs, skipped)
	}
}

func TestRenderBatchMessagesMultiplePlaceholders(t *testing.T) {
	rows := [][]string{{"1.2.3.4", "80", "门户"}}
	ph := []BatchDispatchPlaceholder{{Name: "target", Column: 1}, {Name: "note", Column: 3}}
	msgs, _, err := renderBatchMessages("目标{{target}} 备注{{note}} 端口{{target}}", ph, rows, "keep")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if msgs[0] != "目标1.2.3.4 备注门户 端口1.2.3.4" {
		t.Fatalf("multi placeholder wrong: %q", msgs[0])
	}
}

func TestRenderBatchMessagesUnmappedPlaceholder(t *testing.T) {
	_, _, err := renderBatchMessages("打点 {{target}} 看 {{other}}", nil, [][]string{{"x"}}, "keep")
	if err == nil || !strings.Contains(err.Error(), "other") {
		t.Fatalf("expected unmapped error, got %v", err)
	}
}

func TestRenderBatchMessagesUnknownMapping(t *testing.T) {
	_, _, err := renderBatchMessages("打点 {{target}}", []BatchDispatchPlaceholder{{Name: "targte", Column: 1}}, [][]string{{"x"}}, "keep")
	if err == nil || !strings.Contains(err.Error(), "targte") {
		t.Fatalf("expected unknown-mapping error, got %v", err)
	}
}

func TestRenderBatchMessagesSkipRowPolicy(t *testing.T) {
	rows := [][]string{{"1.2.3.4", ""}, {"5.6.7.8", "x"}}
	ph := []BatchDispatchPlaceholder{{Name: "t", Column: 1}, {Name: "n", Column: 2}}
	msgs, skipped, err := renderBatchMessages("{{t}}/{{n}}", ph, rows, "skip_row")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if skipped != 1 || len(msgs) != 1 || msgs[0] != "5.6.7.8/x" {
		t.Fatalf("skip_row wrong: %v %v", msgs, skipped)
	}
	msgs2, _, _ := renderBatchMessages("{{t}}/{{n}}", ph, rows, "keep")
	if len(msgs2) != 2 || msgs2[0] != "1.2.3.4/" {
		t.Fatalf("keep wrong: %v", msgs2)
	}
}

func TestRenderBatchMessagesEmptyTemplate(t *testing.T) {
	if _, _, err := renderBatchMessages("   ", []BatchDispatchPlaceholder{{Name: "t", Column: 1}}, [][]string{{"x"}}, "keep"); err == nil {
		t.Fatal("expected empty template error")
	}
}

func TestDistributeRoundRobin(t *testing.T) {
	msgs := []string{"m1", "m2", "m3", "m4", "m5"}
	got, err := distributeBatchMessages(msgs, make([]BatchDispatchQueuePlan, 2), "round_robin")
	if err != nil {
		t.Fatal(err)
	}
	if len(got[0]) != 3 || len(got[1]) != 2 || got[0][0] != "m1" || got[1][0] != "m2" || got[0][1] != "m3" {
		t.Fatalf("rr wrong: %v", got)
	}
}

func TestDistributeBlockEven(t *testing.T) {
	msgs := []string{"m1", "m2", "m3", "m4", "m5", "m6"}
	plans := []BatchDispatchQueuePlan{{}, {}, {}}
	got, err := distributeBatchMessages(msgs, plans, "block")
	if err != nil {
		t.Fatal(err)
	}
	// 无封顶 → 均分剩余：ceil(6/3)=2 每队
	if len(got[0]) != 2 || len(got[1]) != 2 || len(got[2]) != 2 {
		t.Fatalf("block even wrong: %v", got)
	}
	if got[0][0] != "m1" || got[1][0] != "m3" || got[2][0] != "m5" {
		t.Fatalf("block contiguous broken: %v", got)
	}
}

func TestDistributeBlockWithCaps(t *testing.T) {
	msgs := []string{"m1", "m2", "m3", "m4", "m5", "m6"}
	plans := []BatchDispatchQueuePlan{{TaskCount: 2}, {TaskCount: 0}}
	got, err := distributeBatchMessages(msgs, plans, "block")
	if err != nil {
		t.Fatal(err)
	}
	if len(got[0]) != 2 || len(got[1]) != 4 || got[0][1] != "m2" || got[1][0] != "m3" {
		t.Fatalf("block caps wrong: %v", got)
	}
}

func TestDistributeBlockOverflow(t *testing.T) {
	msgs := []string{"m1", "m2", "m3", "m4", "m5", "m6"}
	plans := []BatchDispatchQueuePlan{{TaskCount: 1}, {TaskCount: 1}}
	if _, err := distributeBatchMessages(msgs, plans, "block"); err == nil {
		t.Fatal("expected overflow error")
	}
}

func TestDistributeBlockCapsExceedTotal(t *testing.T) {
	msgs := []string{"m1", "m2"}
	plans := []BatchDispatchQueuePlan{{TaskCount: 5}}
	if _, err := distributeBatchMessages(msgs, plans, "block"); err == nil {
		t.Fatal("expected caps-exceed-total error")
	}
}

func TestDistributeNoQueues(t *testing.T) {
	if _, err := distributeBatchMessages([]string{"m1"}, nil, "block"); err == nil {
		t.Fatal("expected no-queue error")
	}
}

func TestDistributeTooManyQueues(t *testing.T) {
	plans := make([]BatchDispatchQueuePlan, MaxBatchQueuesPerDispatch+1)
	if _, err := distributeBatchMessages([]string{"m1"}, plans, "round_robin"); err == nil {
		t.Fatal("expected queue limit error")
	}
}

func TestDistributeDefaultModeIsBlock(t *testing.T) {
	req := &BatchDispatchRequest{
		CSV:   BatchDispatchCSV{Content: "x"},
		Queues: []BatchDispatchQueuePlan{{}},
	}
	normalizeBatchDispatch(req)
	if req.DistributeMode != "block" {
		t.Fatalf("default mode should be block, got %q", req.DistributeMode)
	}
	if req.CSV.Delimiter != "," || req.CSV.Encoding != "utf-8" || req.CSV.EmptyCellPolicy != "skip_row" {
		t.Fatalf("defaults wrong: %+v", req.CSV)
	}
	if req.Queues[0].Concurrency != 1 {
		t.Fatalf("concurrency default wrong: %d", req.Queues[0].Concurrency)
	}
}
