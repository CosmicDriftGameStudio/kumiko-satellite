package pairing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func fakeServer(t *testing.T, tokenAnswers []string) *httptest.Server {
	t.Helper()
	i := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/connectors/v1/device_authorization", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("client_id") != "kumiko-satellite" {
			t.Errorf("client_id = %q", r.PostForm.Get("client_id"))
		}
		_, _ = w.Write([]byte(`{"device_code":"dev","user_code":"AB12-CD34","verification_uri":"https://app/connector-pair","expires_in":600,"interval":5}`))
	})
	mux.HandleFunc("/connectors/v1/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("device_code") != "dev" || r.PostForm.Get("grant_type") != grantType {
			t.Errorf("bad token request: %v", r.PostForm)
		}
		ans := tokenAnswers[i]
		i++
		if ans == "ok" {
			_, _ = w.Write([]byte(`{"access_token":"ksat_x","token_type":"Bearer"}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"` + ans + `"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newFlow(t *testing.T, url string) (*Flow, *[]time.Duration) {
	f, err := New(url)
	if err != nil {
		t.Fatal(err)
	}
	var slept []time.Duration
	f.Sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	return f, &slept
}

func TestPairingPendingSlowDownSuccess(t *testing.T) {
	srv := fakeServer(t, []string{"authorization_pending", "slow_down", "ok"})
	f, slept := newFlow(t, srv.URL)
	auth, err := f.Start(context.Background())
	if err != nil || auth.UserCode != "AB12-CD34" {
		t.Fatalf("start: %+v %v", auth, err)
	}
	token, err := f.Poll(context.Background(), auth)
	if err != nil || token != "ksat_x" {
		t.Fatalf("poll: %q %v", token, err)
	}
	want := []time.Duration{5 * time.Second, 5 * time.Second, 10 * time.Second}
	for i, d := range want {
		if (*slept)[i] != d {
			t.Fatalf("intervals = %v, want %v", *slept, want)
		}
	}
}

func TestPairingExpired(t *testing.T) {
	srv := fakeServer(t, []string{"expired_token"})
	f, _ := newFlow(t, srv.URL)
	auth, _ := f.Start(context.Background())
	if _, err := f.Poll(context.Background(), auth); !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v", err)
	}
}

func TestPairingRejectsPlainHTTPToRemoteHost(t *testing.T) {
	if _, err := New("http://app.example.com"); err == nil {
		t.Fatal("plain http to a remote host must be refused")
	}
}
