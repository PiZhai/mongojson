package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"mongojson/backend/internal/domain"
	stewardsvc "mongojson/backend/internal/service/steward"
)

func TestStewardBackgroundStatusHTTPSerialization(t *testing.T) {
	recorder := httptest.NewRecorder()
	status := stewardsvc.StewardBackgroundStatus{
		State:  "degraded",
		Issues: []string{"模型未配置"},
		IssueDetails: []domain.StewardHealthIssue{{
			Code: "model_unconfigured", Message: "模型未配置", Action: "配置并测试 AI 模型连接",
		}},
		Metrics: domain.StewardBackgroundMetrics{
			BatchStatusCounts: map[string]int{"completed": 2},
			ReminderFeedback1H: domain.StewardReminderFeedbackMetrics{
				Total: 1, ByAction: map[string]int{"opened": 1},
			},
			ModelUsage: domain.StewardModelUsageMetrics{
				Available: false, Reason: "provider token and cost usage is not persisted",
			},
		},
	}
	respondJSON(recorder, http.StatusOK, map[string]any{"status": status})
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected HTTP response: code=%d content-type=%q", recorder.Code, recorder.Header().Get("Content-Type"))
	}
	var payload struct {
		Status struct {
			Issues       []string                        `json:"issues"`
			IssueDetails []domain.StewardHealthIssue     `json:"issue_details"`
			Metrics      domain.StewardBackgroundMetrics `json:"metrics"`
		} `json:"status"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Status.Issues) != 1 || len(payload.Status.IssueDetails) != 1 || payload.Status.IssueDetails[0].Code != "model_unconfigured" {
		t.Fatalf("compatibility or structured issues missing: %s", recorder.Body.String())
	}
	if payload.Status.Metrics.BatchStatusCounts["completed"] != 2 || payload.Status.Metrics.ReminderFeedback1H.ByAction["opened"] != 1 {
		t.Fatalf("observability metrics missing: %s", recorder.Body.String())
	}
	if payload.Status.Metrics.ModelUsage.Available || payload.Status.Metrics.ModelUsage.Reason == "" {
		t.Fatalf("model usage availability is ambiguous: %s", recorder.Body.String())
	}
}
