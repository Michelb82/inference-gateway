package management

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func testMux() *http.ServeMux { mux := http.NewServeMux(); Register(mux); return mux }

func TestSupportAvailableWhileDormant(t *testing.T) {
	rec := httptest.NewRecorder()
	testMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/management/v1/manifest-support", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var body supportResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ContractVersion != ContractVersion || len(body.Capabilities) == 0 || len(body.InferenceVendors) != 2 {
		t.Fatalf("unexpected support response: %+v", body)
	}
}

func TestValidateCurrentDistributionManifest(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "manifest.json.dist"))
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	testMux().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/management/v1/manifests/validate", bytes.NewReader(data)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
}

func TestValidateRejectsInvalidManifest(t *testing.T) {
	rec := httptest.NewRecorder()
	testMux().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/management/v1/manifests/validate", bytes.NewBufferString(`{"inference_endpoint":[]}`)))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
}

func TestManagementMethodsAndLimit(t *testing.T) {
	rec := httptest.NewRecorder()
	testMux().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/management/v1/manifest-support", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("support POST status = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	testMux().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/management/v1/manifests/validate", bytes.NewReader(make([]byte, MaxManifestBytes+1))))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status = %d", rec.Code)
	}
}
