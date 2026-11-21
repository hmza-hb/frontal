package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOK(t *testing.T) {
	got := OK()
	if got.Status != "ok" {
		t.Fatalf("status = %q", got.Status)
	}
}

func TestRoutes(t *testing.T) {
	mux := NewMux()
	tests := []struct {
		name   string
		method string
		path   string
		status int
		body   string
	}{
		{name: "health", method: http.MethodGet, path: "/v1/health", status: http.StatusOK, body: `{"status":"ok"}`},
		{name: "missing", method: http.MethodGet, path: "/v1/missing", status: http.StatusNotFound, body: ""},
		{name: "method", method: http.MethodPost, path: "/v1/health", status: http.StatusMethodNotAllowed, body: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("status = %d", recorder.Code)
			}
			if test.body == "" {
				return
			}
			if recorder.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("content type = %q", recorder.Header().Get("Content-Type"))
			}
			var payload Response
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if payload != OK() {
				t.Fatalf("payload = %+v", payload)
			}
		})
	}
}
