package accounts

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

const ollamaCookie = "__Secure-session=sess"

func ollamaServer(t *testing.T, page string) *webServer {
	t.Helper()
	return newWebServer(t, ollamaCookie, http.StatusFound, map[string]string{"/settings": page})
}

func TestOllamaReadsTheMonthlyUsage(t *testing.T) {
	srv := ollamaServer(t, routesOf(t, "ollama")["/settings"])
	got, err := Ollama{BaseURL: srv.URL}.Read(context.Background(), ollamaCookie)
	if err != nil || !got.Subscription || len(got.Windows) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	// "$7.50 of $60 used" is 12.5%.
	if w := got.Windows[0]; w.Name != "month" || !approx(w.UsedPct, 12.5) || !w.ResetsAt.Equal(time.Date(2026, 9, 30, 15, 14, 29, 0, time.UTC)) {
		t.Errorf("month %+v", w)
	}
}

func TestOllamaReadsTheOlderPage(t *testing.T) {
	srv := ollamaServer(t, routesOf(t, "ollama")["/settings#legacy"])
	got, err := Ollama{BaseURL: srv.URL}.Read(context.Background(), ollamaCookie)
	if err != nil || len(got.Windows) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "5h" || !approx(w.UsedPct, 0.1) || w.Duration != 5*time.Hour || !w.ResetsAt.Equal(time.Date(2026, 1, 30, 18, 0, 0, 0, time.UTC)) {
		t.Errorf("session %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || !approx(w.UsedPct, 0.7) || !w.ResetsAt.Equal(time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("week %+v", w)
	}
	free := ollamaServer(t, `<span>Free usage</span><span>69.5% used</span>`)
	if got, err := (Ollama{BaseURL: free.URL}).Read(context.Background(), ollamaCookie); err != nil || got.Windows[0].Name != "month" || got.Windows[0].UsedPct != 69.5 {
		t.Errorf("free = %+v, %v", got, err)
	}
}

func TestOllamaExpiredSession(t *testing.T) {
	srv := ollamaServer(t, routesOf(t, "ollama")["/settings"])
	// ollama.com sends an expired session to its sign-in page.
	if _, err := (Ollama{BaseURL: srv.URL}).Read(context.Background(), "__Secure-session=old"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("redirect = %v", err)
	}
	signIn := ollamaServer(t, `<html><h1>Sign in to Ollama</h1><form action="/api/auth/signin"><input type="email"><input type="password"></form></html>`)
	if _, err := (Ollama{BaseURL: signIn.URL}).Read(context.Background(), ollamaCookie); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("sign-in page = %v", err)
	}
	if _, err := (Ollama{BaseURL: srv.URL}).Read(context.Background(), "plainkey"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("a key = %v", err)
	}
}

func TestOllamaUnknownPage(t *testing.T) {
	srv := ollamaServer(t, `<html><body>Settings, redesigned</body></html>`)
	if _, err := (Ollama{BaseURL: srv.URL}).Read(context.Background(), ollamaCookie); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("unknown page = %v, want a parse error", err)
	}
}
