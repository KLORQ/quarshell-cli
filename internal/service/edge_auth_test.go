package service

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEdgeAuthCallbackChecksOriginCodeAndConsumesOnce(t *testing.T) {
	callback := make(chan string, 1)
	handler := edgeAuthCallbackHandler("ABC-2345", "https://gateway.example", callback)
	request := func(origin, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/callback", bytes.NewBufferString(body))
		req.Header.Set("Origin", origin)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}
	if got := request("https://attacker.example", `{"token":"secret","code":"ABC-2345"}`).Code; got != http.StatusForbidden {
		t.Fatalf("wrong origin status = %d", got)
	}
	if got := request("https://gateway.example", `{"token":"secret","code":"bad"}`).Code; got != http.StatusBadRequest {
		t.Fatalf("wrong code status = %d", got)
	}
	first := request("https://gateway.example", `{"token":"secret","code":"ABC-2345"}`)
	if first.Code != http.StatusOK {
		t.Fatalf("callback status = %d: %s", first.Code, first.Body)
	}
	var body map[string]bool
	if err := json.Unmarshal(first.Body.Bytes(), &body); err != nil || !body["ok"] {
		t.Fatalf("callback response = %q, err=%v", first.Body, err)
	}
	if token := <-callback; token != "secret" {
		t.Fatalf("token = %q", token)
	}
	if got := request("https://gateway.example", `{"token":"second","code":"ABC-2345"}`).Code; got != http.StatusConflict {
		t.Fatalf("replayed callback status = %d", got)
	}
}

func TestEdgeAuthCallbackPreflight(t *testing.T) {
	handler := edgeAuthCallbackHandler("ABC-2345", "https://gateway.example", make(chan string, 1))
	req := httptest.NewRequest(http.MethodOptions, "/callback", nil)
	req.Header.Set("Origin", "https://gateway.example")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNoContent || res.Header().Get("Access-Control-Allow-Methods") != "POST, OPTIONS" {
		t.Fatalf("preflight = %d headers=%v", res.Code, res.Header())
	}
}
