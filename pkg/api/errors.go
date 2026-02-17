package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// follows RFC 7807: Problem Details for HTTP APIs
type ProblemDetails struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance,omitempty"`
}

func (pd *ProblemDetails) Error() string {
	return fmt.Sprintf("%d %s: %s", pd.Status, pd.Title, pd.Detail)
}

func WriteError(w http.ResponseWriter, status int, title, detail, instance string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)

	pd := &ProblemDetails{
		Type:     "about:blank",
		Title:    title,
		Status:   status,
		Detail:   detail,
		Instance: instance,
	}

	json.NewEncoder(w).Encode(pd)
}

func WriteInternalServerError(w http.ResponseWriter, err error, instance string) {
	WriteError(w, http.StatusInternalServerError, "Internal Server Error", err.Error(), instance)
}

func WriteBadRequest(w http.ResponseWriter, detail, instance string) {
	WriteError(w, http.StatusBadRequest, "Bad Request", detail, instance)
}

func WriteNotFound(w http.ResponseWriter, detail, instance string) {
	WriteError(w, http.StatusNotFound, "Not Found", detail, instance)
}
