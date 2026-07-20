package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/database"
	"mongojson/backend/internal/service/steward"
)

func TestStewardProfileHistoryAndReportRegenerationAPI(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the personal-intelligence API integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "personal_intelligence_api"), "personal-intelligence-api")

	const factKey = "preferences.focus_mode"
	first, err := node.service.CorrectProfileFact(ctx, steward.UpsertProfileFactInput{
		Key: factKey, Value: map[string]any{"mode": "quiet"}, Summary: "first explicit preference",
	})
	if err != nil {
		t.Fatalf("create first profile fact: %v", err)
	}
	second, err := node.service.CorrectProfileFact(ctx, steward.UpsertProfileFactInput{
		Key: factKey, Value: map[string]any{"mode": "music"}, Summary: "corrected explicit preference",
	})
	if err != nil {
		t.Fatalf("create corrected profile fact: %v", err)
	}

	historyURL := node.apiBase + "/steward/personal-intelligence/profile/history?view=explicit&key=" + url.QueryEscape(factKey) + "&limit=10"
	response, err := http.Get(historyURL)
	if err != nil {
		t.Fatalf("get profile history: %v", err)
	}
	var history struct {
		Facts []domain.StewardProfileFact `json:"facts"`
	}
	decodeHTTPJSON(t, response, http.StatusOK, &history)
	if len(history.Facts) != 2 {
		t.Fatalf("history count = %d, want 2: %+v", len(history.Facts), history.Facts)
	}
	if history.Facts[0].ID != second.ID || history.Facts[0].Version != 2 || history.Facts[0].Status != domain.StewardProfileFactActive {
		t.Fatalf("newest history fact = %+v", history.Facts[0])
	}
	if history.Facts[1].ID != first.ID || history.Facts[1].Version != 1 || history.Facts[1].Status != domain.StewardProfileFactSuperseded {
		t.Fatalf("superseded history fact = %+v", history.Facts[1])
	}

	response, err = http.Get(node.apiBase + "/steward/profile/history?horizon=explicit&status=active&key=" + url.QueryEscape(factKey) + "&limit=1")
	if err != nil {
		t.Fatalf("get filtered profile history: %v", err)
	}
	var filtered struct {
		Facts []domain.StewardProfileFact `json:"facts"`
	}
	decodeHTTPJSON(t, response, http.StatusOK, &filtered)
	if len(filtered.Facts) != 1 || filtered.Facts[0].ID != second.ID {
		t.Fatalf("filtered history = %+v", filtered.Facts)
	}

	response, err = http.Get(node.apiBase + "/steward/profile")
	if err != nil {
		t.Fatalf("get profile without view: %v", err)
	}
	decodeHTTPJSON(t, response, http.StatusBadRequest, &map[string]any{})
	response, err = http.Get(node.apiBase + "/steward/profile?view=all")
	if err != nil {
		t.Fatalf("get invalid profile view: %v", err)
	}
	decodeHTTPJSON(t, response, http.StatusBadRequest, &map[string]any{})
	response, err = http.Get(node.apiBase + "/steward/profile?view=merged")
	if err != nil {
		t.Fatalf("get merged profile projection: %v", err)
	}
	var projectedProfile map[string]json.RawMessage
	decodeHTTPJSON(t, response, http.StatusOK, &projectedProfile)
	if len(projectedProfile) != 2 || string(projectedProfile["view"]) != `"merged"` || len(projectedProfile["profile"]) == 0 {
		t.Fatalf("profile projection response = %#v", projectedProfile)
	}

	periodStart := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	periodEnd := periodStart.Add(2 * time.Hour)
	report, err := node.service.WriteReport(ctx, steward.WriteReportInput{
		Cadence: domain.StewardReportDaily, PeriodKey: "regeneration-api-e2e", PeriodStart: periodStart,
		PeriodEnd: periodEnd, Title: "Original daily report", Summary: "original", Body: "Original report body.",
		Evidence: []steward.ProfileEvidenceInput{{SourceType: "profile_fact", SourceID: second.ID, EvidenceDay: time.Now()}},
	})
	if err != nil {
		t.Fatalf("write source report: %v", err)
	}

	regenerationURL := node.apiBase + "/steward/personal-intelligence/reports/" + report.ID + "/regenerate"
	firstRegeneration := postReportRegeneration(t, regenerationURL, `{"reason":"refresh with latest activity"}`, http.StatusAccepted)
	if !firstRegeneration.Regeneration.Created || firstRegeneration.Regeneration.Job.ID == "" {
		t.Fatalf("first regeneration = %+v", firstRegeneration.Regeneration)
	}
	if firstRegeneration.Regeneration.SourceReportID != report.ID || firstRegeneration.Regeneration.SourceRevision != report.Revision {
		t.Fatalf("regeneration source = %+v, report = %+v", firstRegeneration.Regeneration, report)
	}
	job := firstRegeneration.Regeneration.Job
	if job.Status != "pending" || job.Kind != "report_daily" || job.PeriodKey != report.PeriodKey {
		t.Fatalf("queued regeneration job = %+v", job)
	}
	if job.Input["regenerate_report_id"] != report.ID || intFromJSON(job.Input["source_revision"]) != report.Revision || job.Input["reason"] != "refresh with latest activity" {
		t.Fatalf("regeneration checkpoint input = %#v", job.Input)
	}

	secondRegeneration := postReportRegeneration(t, regenerationURL, "", http.StatusAccepted)
	if secondRegeneration.Regeneration.Created || secondRegeneration.Regeneration.Job.ID != job.ID {
		t.Fatalf("active regeneration was not reused: first=%+v second=%+v", firstRegeneration.Regeneration, secondRegeneration.Regeneration)
	}

	// A fresh service instance can read and lease the persisted request. This
	// proves regeneration is a restart-safe background job, not a success shell
	// whose work only existed in the HTTP handler's memory.
	restarted := steward.NewService(&database.DB{Pool: node.pool},
		steward.WithAgentID("personal-intelligence-restarted"),
		steward.WithStorageDir(t.TempDir()),
		steward.WithAutonomyAdvisor(steward.DisabledAutonomyAdvisor("test")),
	)
	persisted, err := restarted.GetIntelligenceJob(ctx, job.ID)
	if err != nil || persisted.Status != "pending" || persisted.Input["regenerate_report_id"] != report.ID {
		t.Fatalf("persisted regeneration after service recreation = %+v, err=%v", persisted, err)
	}
	claimed, err := restarted.ClaimIntelligenceJobs(ctx, "regeneration-recovery-worker", time.Now().UTC().Add(time.Second), time.Minute, 32)
	if err != nil {
		t.Fatalf("claim persisted regeneration: %v", err)
	}
	var claimedJob *domain.StewardIntelligenceJob
	for index := range claimed {
		if claimed[index].ID == job.ID {
			claimedJob = &claimed[index]
			break
		}
	}
	if claimedJob == nil || claimedJob.Status != "processing" || claimedJob.LeaseOwner != "regeneration-recovery-worker" {
		t.Fatalf("claimed regeneration = %+v", claimed)
	}

	missingURL := node.apiBase + "/steward/personal-intelligence/reports/00000000-0000-0000-0000-000000000000/regenerate"
	response, err = http.Post(missingURL, "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatalf("post missing report regeneration: %v", err)
	}
	decodeHTTPJSON(t, response, http.StatusNotFound, &map[string]any{})
}

type reportRegenerationResponse struct {
	Regeneration steward.ReportRegenerationResult `json:"regeneration"`
}

func postReportRegeneration(t *testing.T, endpoint, body string, expectedStatus int) reportRegenerationResponse {
	t.Helper()
	var requestBody io.Reader = http.NoBody
	contentType := ""
	if body != "" {
		requestBody = strings.NewReader(body)
		contentType = "application/json"
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, requestBody)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("post report regeneration: %v", err)
	}
	var payload reportRegenerationResponse
	decodeHTTPJSON(t, response, expectedStatus, &payload)
	return payload
}

func decodeHTTPJSON(t *testing.T, response *http.Response, expectedStatus int, target any) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		var payload map[string]any
		_ = json.NewDecoder(response.Body).Decode(&payload)
		t.Fatalf("HTTP status = %s, want %d; payload=%#v", response.Status, expectedStatus, payload)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode HTTP response: %v", err)
	}
}

func intFromJSON(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return 0
	}
}
