package handler

import (
	"encoding/csv"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"

	"cyberstrike-ai/internal/config"
)

const (
	// MaxBatchDispatchRows CSV 派发最大数据行数
	MaxBatchDispatchRows = 10000
	// MaxBatchQueuesPerDispatch 单次派发最大队列数
	MaxBatchQueuesPerDispatch = 16
	// MaxBatchDispatchCSVBytes CSV 内容最大字节数（2MB）
	MaxBatchDispatchCSVBytes = 2 << 20
	// MaxBatchDispatchTemplate 提示词模板最大字符数
	MaxBatchDispatchTemplate = 20000
)

// batchPlaceholderRegex 匹配 {{name}} 形式的占位符（name 不含空白与花括号，支持中文）。
var batchPlaceholderRegex = regexp.MustCompile(`\{\{\s*([^{}\s]+)\s*\}\}`)

// BatchDispatchPlaceholder 占位符 → 列映射。
type BatchDispatchPlaceholder struct {
	Name   string `json:"name"`   // 模板中的占位符名（不含花括号）
	Column int    `json:"column"` // 1 起始的 CSV 列号（基于原始列，不受 skipHeader 影响）
}

// BatchDispatchCSV CSV 输入配置。
type BatchDispatchCSV struct {
	Content         string `json:"content"`
	FileName        string `json:"fileName,omitempty"`
	Delimiter       string `json:"delimiter,omitempty"`       // "," | ";" | "\t"
	SkipHeader      bool   `json:"skipHeader"`
	Encoding        string `json:"encoding,omitempty"`        // utf-8 | gbk
	EmptyCellPolicy string `json:"emptyCellPolicy,omitempty"` // skip_row | keep
}

// BatchDispatchQueuePlan 单个队列的派发计划。
type BatchDispatchQueuePlan struct {
	Title       string `json:"title,omitempty"`
	AIChannelID string `json:"aiChannelId,omitempty"`
	AgentMode   string `json:"agentMode,omitempty"`
	Role        string `json:"role,omitempty"`
	Concurrency int    `json:"concurrency,omitempty"`
	TaskCount   int    `json:"taskCount,omitempty"` // block 模式封顶，0=承接剩余
}

// BatchDispatchRequest CSV 批量派发请求。
type BatchDispatchRequest struct {
	Title          string                     `json:"title"`
	Template       string                     `json:"template" binding:"required"`
	Placeholders   []BatchDispatchPlaceholder `json:"placeholders" binding:"required"`
	CSV            BatchDispatchCSV           `json:"csv" binding:"required"`
	Queues         []BatchDispatchQueuePlan   `json:"queues" binding:"required"`
	DistributeMode string                     `json:"distributeMode,omitempty"` // block(默认) | round_robin
	ExecuteNow     bool                       `json:"executeNow,omitempty"`
	ProjectID      string                     `json:"projectId,omitempty"`
}

// BatchDispatchQueueResult 派发响应中的单个队列结果。
type BatchDispatchQueueResult struct {
	QueueID   string `json:"queueId"`
	TaskCount int    `json:"taskCount"`
	Started   bool   `json:"started"`
}

// BatchDispatchResponse 派发响应。
type BatchDispatchResponse struct {
	GroupID     string                     `json:"groupId"`
	TotalTasks  int                        `json:"totalTasks"`
	SkippedRows int                        `json:"skippedRows"`
	Queues      []BatchDispatchQueueResult `json:"queues"`
}

// normalizeBatchDispatch 填充默认值：delimiter=,、encoding=utf-8、policy=skip_row、mode=block；
// 队列并发数规范化、AgentMode 归一化。
func normalizeBatchDispatch(req *BatchDispatchRequest) {
	if req.CSV.Delimiter == "" {
		req.CSV.Delimiter = ","
	}
	if req.CSV.Encoding == "" {
		req.CSV.Encoding = "utf-8"
	}
	if req.CSV.EmptyCellPolicy == "" {
		req.CSV.EmptyCellPolicy = "skip_row"
	}
	if req.DistributeMode == "" {
		req.DistributeMode = "block"
	}
	for i := range req.Queues {
		req.Queues[i].Concurrency = normalizeBatchQueueConcurrency(req.Queues[i].Concurrency)
		req.Queues[i].AgentMode = config.NormalizeAgentMode(req.Queues[i].AgentMode)
	}
}

// validateBatchDispatch 校验请求结构（不含 CSV/模板内容级校验，那些由各自函数负责）。
func validateBatchDispatch(req *BatchDispatchRequest) error {
	if len(req.Queues) == 0 {
		return fmt.Errorf("至少需要一个队列")
	}
	if len(req.Queues) > MaxBatchQueuesPerDispatch {
		return fmt.Errorf("队列数超过上限 %d", MaxBatchQueuesPerDispatch)
	}
	if len(req.Placeholders) == 0 {
		return fmt.Errorf("至少需要一个占位符映射")
	}
	if strings.TrimSpace(req.Template) == "" {
		return fmt.Errorf("提示词模板不能为空")
	}
	return nil
}

// parseBatchCSV 解析 CSV 内容为二维数组（若 SkipHeader 则去掉首行）。
func parseBatchCSV(c *BatchDispatchCSV) ([][]string, error) {
	if len(c.Content) > MaxBatchDispatchCSVBytes {
		return nil, fmt.Errorf("CSV 内容超过 %dMB 上限", MaxBatchDispatchCSVBytes>>20)
	}
	content := strings.TrimPrefix(c.Content, "\ufeff")
	if strings.TrimSpace(c.Encoding) == "gbk" {
		decoded, err := simplifiedchinese.GBK.NewDecoder().String(content)
		if err != nil {
			return nil, fmt.Errorf("GBK 解码失败: %w", err)
		}
		content = decoded
	}
	r := csv.NewReader(strings.NewReader(content))
	switch c.Delimiter {
	case ";", "\t", "|":
		runes := []rune(c.Delimiter)
		r.Comma = runes[0]
	default:
		r.Comma = ','
	}
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	all, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("CSV 解析失败: %w", err)
	}
	if c.SkipHeader && len(all) > 0 {
		all = all[1:]
	}
	if len(all) > MaxBatchDispatchRows {
		return nil, fmt.Errorf("数据行数 %d 超过上限 %d", len(all), MaxBatchDispatchRows)
	}
	return all, nil
}

// renderBatchMessages 逐行用占位符渲染模板。返回渲染后的消息列表与跳过的行数。
func renderBatchMessages(template string, placeholders []BatchDispatchPlaceholder, rows [][]string, emptyPolicy string) ([]string, int, error) {
	if strings.TrimSpace(template) == "" {
		return nil, 0, fmt.Errorf("提示词模板不能为空")
	}
	if utf8.RuneCountInString(template) > MaxBatchDispatchTemplate {
		return nil, 0, fmt.Errorf("提示词模板超过 %d 字符上限", MaxBatchDispatchTemplate)
	}

	// 模板中实际出现的占位符名集合
	used := map[string]bool{}
	for _, m := range batchPlaceholderRegex.FindAllStringSubmatch(template, -1) {
		used[m[1]] = true
	}

	// 映射表：占位符名 → 0 起始列索引
	mapped := map[string]int{}
	for _, p := range placeholders {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			continue
		}
		if !used[name] {
			return nil, 0, fmt.Errorf("占位符 {{%s}} 在模板中不存在", name)
		}
		if p.Column < 1 {
			return nil, 0, fmt.Errorf("占位符 {{%s}} 的列号无效: %d", name, p.Column)
		}
		mapped[name] = p.Column - 1
	}

	var unmapped []string
	for name := range used {
		if _, ok := mapped[name]; !ok {
			unmapped = append(unmapped, name)
		}
	}
	if len(unmapped) > 0 {
		sort.Strings(unmapped)
		return nil, 0, fmt.Errorf("模板中的占位符未映射列: %s", strings.Join(unmapped, ", "))
	}

	cell := func(row []string, idx int) string {
		if idx < len(row) {
			return strings.TrimSpace(row[idx])
		}
		return ""
	}

	msgs := make([]string, 0, len(rows))
	skipped := 0
	for _, row := range rows {
		if emptyPolicy == "skip_row" {
			skip := false
			for name := range used {
				if cell(row, mapped[name]) == "" {
					skip = true
					break
				}
			}
			if skip {
				skipped++
				continue
			}
		}
		msg := batchPlaceholderRegex.ReplaceAllStringFunc(template, func(m string) string {
			name := batchPlaceholderRegex.FindStringSubmatch(m)[1]
			return cell(row, mapped[name])
		})
		msgs = append(msgs, msg)
	}
	return msgs, skipped, nil
}

// distributeBatchMessages 将消息分发到各队列计划。
// block（默认）：按顺序连续分块；taskCount>0 的队列封顶，taskCount==0 的队列均分剩余。
// round_robin：第 i 条消息 → 第 i%N 个队列。
func distributeBatchMessages(msgs []string, plans []BatchDispatchQueuePlan, mode string) ([][]string, error) {
	n := len(plans)
	if n == 0 {
		return nil, fmt.Errorf("至少需要一个队列")
	}
	if n > MaxBatchQueuesPerDispatch {
		return nil, fmt.Errorf("队列数超过上限 %d", MaxBatchQueuesPerDispatch)
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("没有可派发的任务")
	}
	out := make([][]string, n)
	switch strings.TrimSpace(mode) {
	case "round_robin":
		for i, m := range msgs {
			out[i%n] = append(out[i%n], m)
		}
	default: // block
		caps := make([]int, n)
		cappedTotal := 0
		uncapped := 0
		for i, p := range plans {
			if p.TaskCount < 0 {
				return nil, fmt.Errorf("队列 %d 的任务数不能为负", i+1)
			}
			caps[i] = p.TaskCount
			if p.TaskCount > 0 {
				cappedTotal += p.TaskCount
			} else {
				uncapped++
			}
		}
		if cappedTotal > len(msgs) {
			return nil, fmt.Errorf("队列容量(%d)超过任务总数(%d)", cappedTotal, len(msgs))
		}
		if cappedTotal < len(msgs) && uncapped == 0 {
			return nil, fmt.Errorf("队列容量不足：%d 行未分配", len(msgs)-cappedTotal)
		}
		pos := 0
		for i := 0; i < n; i++ {
			var take int
			if caps[i] > 0 {
				take = caps[i]
			} else {
				// taskCount==0 的队列均分剩余（向上取整）
				remaining := len(msgs) - pos
				take = (remaining + uncapped - 1) / uncapped
				uncapped--
			}
			if take > len(msgs)-pos {
				take = len(msgs) - pos
			}
			out[i] = append(out[i], msgs[pos:pos+take]...)
			pos += take
		}
	}
	return out, nil
}
