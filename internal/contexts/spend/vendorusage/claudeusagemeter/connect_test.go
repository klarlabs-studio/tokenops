package claudeusagemeter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// twoOrgServer serves an account holding a personal org with nothing to
// meter and a work org that reports usage — the shape of a real account.
func twoOrgServer(t *testing.T, workBody string, workStatus int) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/organizations":
			_, _ = w.Write([]byte(`[{"uuid":"personal","name":"Personal Org"},{"uuid":"work","name":"Work Org"}]`))
		case "/api/organizations/personal/usage":
			_, _ = w.Write([]byte(chatOnlyUsage))
		case "/api/organizations/work/usage":
			if workStatus != http.StatusOK {
				w.WriteHeader(workStatus)
				return
			}
			_, _ = w.Write([]byte(workBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	c := NewClient("sk")
	c.BaseURL = srv.URL
	return c
}

// Setup used to ask which organization to meter. Only one of them reports
// usage at all, so the data answers the question: asking put a choice to
// the operator that TokenOps could make.
func TestConnectPicksTheOrganizationThatReportsUsage(t *testing.T) {
	conn, err := Connect(context.Background(), twoOrgServer(t, enterpriseUsage, http.StatusOK), "")
	if err != nil {
		t.Fatal(err)
	}
	if conn.Org.Name != "Work Org" || len(conn.Orgs) != 2 {
		t.Errorf("chose %+v out of %d orgs", conn.Org, len(conn.Orgs))
	}
	if conn.Usage == nil || conn.Usage.ExtraUsage == nil {
		t.Errorf("usage not carried back: %+v", conn.Usage)
	}
}

// An operator who wants a specific organization names it; a name that is
// not on the account is refused rather than silently metering another.
func TestConnectHonoursAnExplicitChoice(t *testing.T) {
	c := twoOrgServer(t, enterpriseUsage, http.StatusOK)
	conn, err := Connect(context.Background(), c, "Work Org")
	if err != nil || conn.Org.UUID != "work" {
		t.Fatalf("by name: %+v %v", conn.Org, err)
	}
	if _, err := Connect(context.Background(), c, "Nope Ltd"); err == nil {
		t.Error("accepted an organization that is not on the account")
	}
	if _, err := Connect(context.Background(), c, "Personal Org"); !errors.Is(err, ErrNothingToMeter) {
		t.Errorf("an explicitly chosen org with no usage: %v, want ErrNothingToMeter", err)
	}
}

// An organization whose usage call fails is passed over while another may
// still answer — but if none does, the failure is what the operator needs
// to see, not "nothing to meter".
func TestConnectSkipsARefusingOrganizationButReportsItWhenNoneAnswer(t *testing.T) {
	_, err := Connect(context.Background(), twoOrgServer(t, "", http.StatusForbidden), "")
	if err == nil || errors.Is(err, ErrNothingToMeter) {
		t.Fatalf("err = %v, want the refusal reported", err)
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "Work Org") {
		t.Errorf("err = %v, want it to name the organization and the status", err)
	}
	_ = ErrBotCheck
	// One that answers wins over one that refuses, whatever the order.
	conn, err := Connect(context.Background(), twoOrgServer(t, enterpriseUsage, http.StatusOK), "")
	if err != nil || conn.Org.UUID != "work" {
		t.Errorf("conn = %+v err = %v", conn.Org, err)
	}
}

func TestConnectReportsARejectedKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := NewClient("expired")
	c.BaseURL = srv.URL
	if _, err := Connect(context.Background(), c, ""); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestConnectNoOrganizations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c := NewClient("sk")
	c.BaseURL = srv.URL
	_, err := Connect(context.Background(), c, "")
	if err == nil || !strings.Contains(err.Error(), "no organizations") {
		t.Errorf("err = %v", err)
	}
}

// claude.ai sits behind a bot check that answers 403 to a client it does
// not recognise. Reporting that as "no usage limits" sent the operator to
// look at their plan instead of at the request.
func TestConnectReportsAFailedUsageCallRatherThanNothingToMeter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/organizations" {
			_, _ = w.Write([]byte(`[{"uuid":"a","name":"Personal"}]`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<!DOCTYPE html><title>Just a moment...</title>"))
	}))
	defer srv.Close()
	c := NewClient("sk")
	c.BaseURL = srv.URL

	_, err := Connect(context.Background(), c, "")
	if err == nil || errors.Is(err, ErrNothingToMeter) {
		t.Fatalf("err = %v, want the 403 reported", err)
	}
	if !errors.Is(err, ErrBotCheck) {
		t.Errorf("err = %v, want it recognised as the bot check", err)
	}
	if !strings.Contains(err.Error(), "Personal") {
		t.Errorf("err %v does not name the organization", err)
	}
}
