package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/KLORQ/quarshell-cli/internal/storage/gwconfig"
	"github.com/coder/websocket"
)

func (a *App) edgeTunnelURL(gatewayURL string) (string, string, bool) {
	if a == nil {
		return "", "", false
	}
	key := strings.TrimRight(gatewayURL, "/")
	cfg, _, err := gwconfig.Load()
	if err != nil {
		return "", "", false
	}
	var edgeToken string
	for _, gateway := range cfg.Gateways {
		if strings.TrimRight(gateway.URL, "/") == strings.TrimRight(gatewayURL, "/") {
			edgeToken = strings.TrimSpace(gateway.EdgeToken)
			break
		}
	}
	if edgeToken == "" {
		return "", "", false
	}
	a.edgeTunnelMu.Lock()
	defer a.edgeTunnelMu.Unlock()
	if addr := a.edgeTunnels[key]; addr != "" {
		return "http://" + addr, edgeToken, true
	}
	addr, err := startEdgeTunnelProxy(gatewayURL, edgeToken)
	if err != nil {
		return "", "", false
	}
	if a.edgeTunnels == nil {
		a.edgeTunnels = make(map[string]string)
	}
	a.edgeTunnels[key] = addr
	return "http://" + addr, edgeToken, true
}

func startEdgeTunnelProxy(gatewayURL, edgeToken string) (string, error) {
	endpoint, err := url.Parse(gatewayURL)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return "", fmt.Errorf("invalid edge gateway URL")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("start edge tunnel listener: %w", err)
	}
	wsURL := *endpoint
	if wsURL.Scheme == "https" {
		wsURL.Scheme = "wss"
	} else {
		wsURL.Scheme = "ws"
	}
	wsURL.Path = "/_ws_tunnel"
	wsURL.RawQuery = ""
	wsURL.Fragment = ""
	go func() {
		for {
			tcp, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			if stream, ok := tcp.(*net.TCPConn); ok {
				_ = stream.SetNoDelay(true)
			}
			go tunnelEdgeConnection(tcp, wsURL.String(), edgeToken)
		}
	}()
	return listener.Addr().String(), nil
}

func tunnelEdgeConnection(tcp net.Conn, wsURL, edgeToken string) {
	defer tcp.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	header := make(http.Header)
	header.Set("Cf-Access-Token", edgeToken)
	header.Set("Cf-Access-Jwt-Assertion", edgeToken)
	header.Set("Cookie", "CF_Authorization="+edgeToken)
	ws, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: header})
	cancel()
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		buffer := make([]byte, 32*1024)
		for {
			n, readErr := tcp.Read(buffer)
			if n > 0 {
				if writeErr := ws.Write(ctx, websocket.MessageBinary, buffer[:n]); writeErr != nil {
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			_, data, readErr := ws.Read(ctx)
			if readErr != nil {
				return
			}
			for len(data) > 0 {
				n, writeErr := tcp.Write(data)
				if writeErr != nil || n == 0 {
					return
				}
				data = data[n:]
			}
		}
	}()
	<-done
	cancel()
	_ = tcp.Close()
	_ = ws.Close(websocket.StatusNormalClosure, "tunnel closed")
}
