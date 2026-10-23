package service

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const edgeAuthCodeAlphabet = "ABCD" + "EFGH" + "JKLM" + "NPQR" + "STUV" + "WXYZ" + "2345" + "6789"

func browserEdgeAuth(ctx context.Context, gatewayURL string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("OPENSHELL_NO_BROWSER"))); v == "1" || v == "true" {
		return "", fmt.Errorf("browser authentication disabled by OPENSHELL_NO_BROWSER")
	}
	gateway, err := url.Parse(strings.TrimRight(gatewayURL, "/"))
	if err != nil || gateway.Host == "" || (gateway.Scheme != "http" && gateway.Scheme != "https") {
		return "", fmt.Errorf("invalid gateway URL")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("start auth callback: %w", err)
	}
	defer listener.Close()
	code, err := newEdgeAuthCode()
	if err != nil {
		return "", err
	}
	callbackOrigin := gateway.Scheme + "://" + gateway.Host
	callback := make(chan string, 1)
	mux := http.NewServeMux()
	mux.Handle("/callback", edgeAuthCallbackHandler(code, callbackOrigin, callback))
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	authURL := *gateway
	authURL.Path = strings.TrimRight(authURL.Path, "/") + "/auth/connect"
	query := authURL.Query()
	query.Set("callback_port", fmt.Sprint(listener.Addr().(*net.TCPAddr).Port))
	query.Set("code", code)
	authURL.RawQuery = query.Encode()
	fmt.Printf("Confirmation code: %s\n", code)
	fmt.Println("Verify this code in your browser to finish gateway login.")
	if err := openBrowser(authURL.String()); err != nil {
		fmt.Printf("Open this URL to continue: %s\n", authURL.String())
	}
	select {
	case token := <-callback:
		return token, nil
	case <-time.After(2 * time.Minute):
		return "", fmt.Errorf("gateway browser authentication timed out")
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func edgeAuthCallbackHandler(code, allowedOrigin string, callback chan<- string) http.Handler {
	var mu sync.Mutex
	used := false
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		w.Header().Set("Vary", "Origin")
		if r.Header.Get("Origin") != allowedOrigin {
			http.Error(w, "invalid origin", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()
		var body struct {
			Token string `json:"token"`
			Code  string `json:"code"`
		}
		decoder := json.NewDecoder(io.LimitReader(r.Body, 4097))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || len(body.Token) > 4096 || strings.TrimSpace(body.Token) == "" || body.Code != code {
			http.Error(w, "invalid callback", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			http.Error(w, "invalid callback", http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if used {
			http.Error(w, "callback already used", http.StatusConflict)
			return
		}
		select {
		case callback <- body.Token:
			used = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.Error(w, "callback already used", http.StatusConflict)
		}
	})
}

func newEdgeAuthCode() (string, error) {
	var raw [7]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate confirmation code: %w", err)
	}
	var b strings.Builder
	for i := 0; i < 7; i++ {
		if i == 3 {
			b.WriteByte('-')
		}
		b.WriteByte(edgeAuthCodeAlphabet[int(raw[i%len(raw)]&31)])
	}
	return b.String(), nil
}
