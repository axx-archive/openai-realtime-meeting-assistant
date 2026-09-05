package business

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestDocumentTransportNegotiatesHTTP1WithFreshConnections(t *testing.T) {
	var mu sync.Mutex
	addresses := map[string]bool{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 1 {
			t.Errorf("unexpected protocol %s", r.Proto)
		}
		mu.Lock()
		addresses[r.RemoteAddr] = true
		mu.Unlock()
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(200)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	tr := newDocumentHTTPTransport()
	tr.TLSClientConfig.RootCAs = server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}
	for range 2 {
		res, err := client.Post(server.URL, "application/json", strings.NewReader(`{"bounded":true}`))
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}
	mu.Lock()
	defer mu.Unlock()
	if len(addresses) != 2 {
		t.Fatalf("reused connection: %d", len(addresses))
	}
}
