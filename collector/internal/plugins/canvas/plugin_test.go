package canvas

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSkeletonPluginHasNoSideEffects(t *testing.T) {
	t.Parallel()

	plugin := New()
	mux := http.NewServeMux()
	plugin.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/api/canvas", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unready plugin status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if err := plugin.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := plugin.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
