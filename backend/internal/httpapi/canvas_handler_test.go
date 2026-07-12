package httpapi

import (
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/service/canvas"
)

type fakeCanvasStore struct {
	create func(context.Context, string) (domain.CanvasBoardRecord, error)
	save   func(context.Context, string, canvas.SaveInput) (domain.CanvasBoardRecord, error)
}

func (s fakeCanvasStore) Create(ctx context.Context, title string) (domain.CanvasBoardRecord, error) {
	return s.create(ctx, title)
}

func (s fakeCanvasStore) List(context.Context) ([]domain.CanvasBoardRecord, error) {
	return []domain.CanvasBoardRecord{}, nil
}

func (s fakeCanvasStore) Get(context.Context, string) (domain.CanvasBoardRecord, error) {
	return domain.CanvasBoardRecord{}, canvas.ErrBoardNotFound
}

func (s fakeCanvasStore) Save(ctx context.Context, id string, input canvas.SaveInput) (domain.CanvasBoardRecord, error) {
	return s.save(ctx, id, input)
}

func (s fakeCanvasStore) Delete(context.Context, string) error { return nil }

func (s fakeCanvasStore) UploadAsset(context.Context, string, string, multipart.File, *multipart.FileHeader) (domain.CanvasAssetRecord, error) {
	return domain.CanvasAssetRecord{}, nil
}

func (s fakeCanvasStore) GetAsset(context.Context, string) (domain.CanvasAssetRecord, error) {
	return domain.CanvasAssetRecord{}, canvas.ErrAssetNotFound
}

func TestCreateCanvasBoardReturnsCreatedRecord(t *testing.T) {
	store := fakeCanvasStore{create: func(_ context.Context, title string) (domain.CanvasBoardRecord, error) {
		return domain.CanvasBoardRecord{ID: "board-1", Title: title, Revision: 1}, nil
	}, save: func(context.Context, string, canvas.SaveInput) (domain.CanvasBoardRecord, error) {
		return domain.CanvasBoardRecord{}, nil
	}}
	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{CanvasService: store})
	req := httptest.NewRequest(http.MethodPost, "/api/canvas/boards", strings.NewReader(`{"title":"Ideas"}`))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"title":"Ideas"`) {
		t.Fatalf("unexpected response %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSaveCanvasBoardMapsRevisionConflict(t *testing.T) {
	store := fakeCanvasStore{create: func(context.Context, string) (domain.CanvasBoardRecord, error) {
		return domain.CanvasBoardRecord{}, nil
	}, save: func(context.Context, string, canvas.SaveInput) (domain.CanvasBoardRecord, error) {
		return domain.CanvasBoardRecord{}, canvas.ErrRevisionConflict
	}}
	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{CanvasService: store})
	req := httptest.NewRequest(http.MethodPut, "/api/canvas/boards/board-1", strings.NewReader(`{"title":"Ideas","revision":1,"scene":{"elements":[],"appState":{},"files":{}}}`))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d: %s", rec.Code, rec.Body.String())
	}
}
