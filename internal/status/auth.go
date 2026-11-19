package status

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// The secret never travels: client and server prove they know it with HMAC
// answers to each other's random challenges, so a program that merely listens
// on the recorded port learns nothing and cannot pass for the satellite.

const challengeTTL = 30 * time.Second

// ErrForeignServer means the recorded port is answered by something that does not know the secret.
var ErrForeignServer = errors.New("Another program is using the satellite's port")

func mac(secret, role, challenge string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(role + "|" + challenge))
	return hex.EncodeToString(m.Sum(nil))
}

func macEqual(got, want string) bool { return hmac.Equal([]byte(got), []byte(want)) }

func readJSON(w http.ResponseWriter, r *http.Request, into any) bool {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(into) == nil
}

// hello answers the client's challenge and hands out a one-time challenge of its own.
func (s *server) hello(w http.ResponseWriter, r *http.Request) {
	var in struct {
		C1 string `json:"c1"`
	}
	if !readJSON(w, r, &in) || len(in.C1) != 64 {
		unauthorized(w)
		return
	}
	c2, err := s.challenges.New()
	if err != nil {
		http.Error(w, "no challenge", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"s": mac(s.opt.Secret, "server", in.C1), "c2": c2})
}

// proven checks the client's answer to a challenge from hello; each challenge counts once.
func (s *server) proven(w http.ResponseWriter, r *http.Request) bool {
	var in struct {
		C2 string `json:"c2"`
		M  string `json:"m"`
	}
	if !readJSON(w, r, &in) || !s.challenges.Consume(in.C2) {
		return false
	}
	return macEqual(in.M, mac(s.opt.Secret, "client", in.C2))
}

func postJSON(ctx context.Context, addr, path string, body any) (*http.Response, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(req)
}

// callAuthed proves the server knows the secret before sending anything derived
// from it, then calls path with the client's proof and decodes the answer into out.
func callAuthed(addr, secret, path string, out any) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return 0, err
	}
	c1 := hex.EncodeToString(raw)
	resp, err := postJSON(ctx, addr, "/hello", map[string]string{"c1": c1})
	if err != nil {
		return 0, err
	}
	var hello struct {
		S  string `json:"s"`
		C2 string `json:"c2"`
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 1024)).Decode(&hello)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || err != nil || !macEqual(hello.S, mac(secret, "server", c1)) {
		return 0, ErrForeignServer
	}
	resp, err = postJSON(ctx, addr, path, map[string]string{"c2": hello.C2, "m": mac(secret, "client", hello.C2)})
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1024)).Decode(out); err != nil {
			return resp.StatusCode, errors.New("unreadable answer")
		}
	}
	return resp.StatusCode, nil
}

// SignalShow asks the running instance to open its window again.
func SignalShow(addr, secret string) error {
	code, err := callAuthed(addr, secret, "/show-window", nil)
	if err != nil {
		return err
	}
	if code != http.StatusNoContent {
		return fmt.Errorf("instance refused the request (%d)", code)
	}
	return nil
}

// FetchNonce asks the running instance for a single-use login nonce.
func FetchNonce(addr, secret string) (string, error) {
	var out struct {
		Nonce string `json:"nonce"`
	}
	code, err := callAuthed(addr, secret, "/login-nonce", &out)
	if err != nil {
		return "", err
	}
	if code != http.StatusOK || out.Nonce == "" {
		return "", fmt.Errorf("instance refused the request (%d)", code)
	}
	return out.Nonce, nil
}
