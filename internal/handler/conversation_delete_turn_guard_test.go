package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cyberstrike-ai/internal/database"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// staticTaskRegistry 测试用固定任务快照。
type staticTaskRegistry struct {
	status string
	exists bool
}

func (s staticTaskRegistry) ConversationTaskSnapshot(string) (string, bool) {
	return s.status, s.exists
}

// TestDeleteConversationTurnRejectsWhileTaskRunningOrCancelling
// 删除轮次运行态守卫：任务 running / cancelling 时必须 409 拒绝，无在飞任务时放行。
// 这是「停止后立刻删消息 → 旧 prompt 复活」竞态的第一道防线。
func TestDeleteConversationTurnRejectsWhileTaskRunningOrCancelling(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name     string
		status   string
		exists   bool
		wantCode int
	}{
		{"running", "running", true, http.StatusConflict},
		{"cancelling", "cancelling", true, http.StatusConflict},
		{"no task", "", false, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := database.NewDB(t.TempDir()+"/delete-turn-guard.db", zap.NewNop())
			if err != nil {
				t.Fatalf("NewDB: %v", err)
			}
			defer func() { _ = db.Close() }()
			conv, err := db.CreateConversation("guard", database.ConversationCreateMeta{})
			if err != nil {
				t.Fatalf("CreateConversation: %v", err)
			}
			if _, err := db.AddMessage(conv.ID, "user", "turn1", nil); err != nil {
				t.Fatalf("AddMessage: %v", err)
			}
			msgs, err := db.GetMessages(conv.ID)
			if err != nil || len(msgs) == 0 {
				t.Fatalf("GetMessages: %v (n=%d)", err, len(msgs))
			}

			h := NewConversationHandler(db, zap.NewNop())
			h.SetTaskRegistry(staticTaskRegistry{status: tc.status, exists: tc.exists})

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			body := `{"messageId":"` + msgs[0].ID + `"}`
			c.Request = httptest.NewRequest(http.MethodPost, "/api/conversations/"+conv.ID+"/delete-turn", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Params = gin.Params{{Key: "id", Value: conv.ID}}
			h.DeleteConversationTurn(c)

			if w.Code != tc.wantCode {
				t.Fatalf("%s: got %d want %d (body=%s)", tc.name, w.Code, tc.wantCode, w.Body.String())
			}
			if tc.wantCode == http.StatusOK {
				left, err := db.GetMessages(conv.ID)
				if err != nil || len(left) != 0 {
					t.Fatalf("%s: messages should be deleted, got %d left (err=%v)", tc.name, len(left), err)
				}
			}
		})
	}
}
