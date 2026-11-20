package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHeartbeatParsesReply(t *testing.T) {
	long := strings.Repeat("ä", 200)
	cases := []struct {
		body string
		want HeartbeatReply
	}{
		{`{"ok":true}`, HeartbeatReply{}},
		{`{"ok":true,"retry":true}`, HeartbeatReply{Retry: true}},
		{`{"ok":true,"name":"Büro-PC"}`, HeartbeatReply{Name: "Büro-PC"}},
		{`{"ok":true,"retry":true,"name":"  Büro\u0000-PC\n"}`, HeartbeatReply{Retry: true, Name: "Büro-PC"}},
		{`{"ok":true,"name":"` + long + `"}`, HeartbeatReply{Name: strings.Repeat("ä", 120)}},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(c.body))
		}))
		client, err := New(srv.URL, "ksat_t")
		if err != nil {
			t.Fatal(err)
		}
		got, err := client.Heartbeat(context.Background(), "1.0", nil)
		srv.Close()
		if err != nil || got != c.want {
			t.Errorf("%s: got %+v, %v; want %+v", c.body, got, err, c.want)
		}
	}
}
