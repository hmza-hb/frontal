package health

import (
	"encoding/json"
	"net/http"
)

type Response struct {
	Status string `json:"status"`
}

func OK() Response {
	return Response{Status: "ok"}
}

func NewMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", serve)
	return mux
}

func serve(w http.ResponseWriter, r *http.Request) {
	body, err := json.Marshal(OK())
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, writeErr := w.Write(body)
	if writeErr != nil {
		return
	}
}
