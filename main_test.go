package main

import (
	"encoding/json"
	"hunter-base/pkg/api"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProductHandler(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		expectedStatus int
		expectedType   string
		expectedDetail string
	}{
		{
			name:           "Invalid Path - Missing parts",
			path:           "/stores",
			expectedStatus: http.StatusBadRequest,
			expectedType:   "about:blank",
			expectedDetail: "Invalid path. Expected /stores/{store}/products/{id}",
		},
		{
			name:           "Invalid Path - Wrong keyword",
			path:           "/stores/spar/items/123",
			expectedStatus: http.StatusBadRequest,
			expectedType:   "about:blank",
			expectedDetail: "Invalid path. Expected /stores/{store}/products/{id}",
		},
		{
			name:           "Unsupported Store",
			path:           "/stores/unknown/products/123",
			expectedStatus: http.StatusBadRequest,
			expectedType:   "about:blank",
			expectedDetail: "Store not supported. Available: spar, billa",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", tt.path, nil)
			if err != nil {
				t.Fatal(err)
			}

			rr := httptest.NewRecorder()
			handler := http.HandlerFunc(productHandler)

			handler.ServeHTTP(rr, req)

			if status := rr.Code; status != tt.expectedStatus {
				t.Errorf("handler returned wrong status code: got %v want %v",
					status, tt.expectedStatus)
			}

			// Check Content-Type
			expectedContentType := "application/problem+json"
			if contentType := rr.Header().Get("Content-Type"); contentType != expectedContentType {
				t.Errorf("handler returned wrong content type: got %v want %v",
					contentType, expectedContentType)
			}

			// Check JSON Body
			var pd api.ProblemDetails
			if err := json.Unmarshal(rr.Body.Bytes(), &pd); err != nil {
				t.Errorf("handler returned invalid JSON: %v. Body: %s", err, rr.Body.String())
			}

			if pd.Status != tt.expectedStatus {
				t.Errorf("JSON status mismatch: got %v want %v", pd.Status, tt.expectedStatus)
			}
			if pd.Type != tt.expectedType {
				t.Errorf("JSON type mismatch: got %v want %v", pd.Type, tt.expectedType)
			}
			if !strings.Contains(pd.Detail, tt.expectedDetail) {
				t.Errorf("JSON detail mismatch: got %q, want substring %q", pd.Detail, tt.expectedDetail)
			}
			if pd.Instance != tt.path {
				t.Errorf("JSON instance mismatch: got %v want %v", pd.Instance, tt.path)
			}
		})
	}
}
