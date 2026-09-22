package genos_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AnthonyPoschen/genos-discord/internal/genos"
)

func TestTokenCachedUntilExpiryAndScopedToUser(t *testing.T) {
	current := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	var mints int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/internal/discord/impersonation-tokens":
			if r.Header.Get("Authorization") != "Bearer bot-secret" {
				t.Errorf("mint auth = %s", r.Header.Get("Authorization"))
			}
			mints++
			fmt.Fprintf(w, `{"token":"genos_discord_%d","expiresAt":%q,"tokenKind":"discord"}`, mints, current.Add(time.Minute).Format(time.RFC3339))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/servers":
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer genos_discord_") || auth == "Bearer bot-secret" || strings.Contains(strings.ToLower(auth), "kube") {
				t.Errorf("follow-up auth = %s", auth)
			}
			_, _ = w.Write([]byte(`{"servers":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := genos.NewClient(server.URL, "bot-secret", server.Client())
	client.Now = func() time.Time { return current }
	ctx := context.Background()
	if _, err := client.ListServers(ctx, "user-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListServers(ctx, "user-a"); err != nil {
		t.Fatal(err)
	}
	if mints != 1 {
		t.Fatalf("mints before expiry = %d", mints)
	}
	current = current.Add(2 * time.Minute)
	if _, err := client.ListServers(ctx, "user-a"); err != nil {
		t.Fatal(err)
	}
	if mints != 2 {
		t.Fatalf("mints after expiry = %d", mints)
	}
	if _, err := client.ListServers(ctx, "user-b"); err != nil {
		t.Fatal(err)
	}
	if mints != 3 {
		t.Fatalf("mints for second user = %d", mints)
	}
}

func TestMintErrorDoesNotLeakToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"token":"genos_discord_supersecret"}`, http.StatusInternalServerError)
	}))
	defer server.Close()
	client := genos.NewClient(server.URL, "bot-secret", server.Client())
	_, err := client.ListServers(context.Background(), "user-1")
	if err == nil {
		t.Fatal("expected mint error")
	}
	if strings.Contains(err.Error(), "supersecret") || strings.Contains(err.Error(), "genos_discord_supersecret") {
		t.Fatalf("token leaked in error: %v", err)
	}
}

func TestGetServerAcceptsWrappedAndBare(t *testing.T) {
	const body = `{"id":"srv-1","name":"Alpha","game":{"name":"Factorio"},"status":"Running","notableUpdates":["patch"],"metrics":{"playerCount":2}}`
	mode := "wrapped"
	var sawUser bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/internal/discord/impersonation-tokens":
			_, _ = w.Write([]byte(`{"token":"genos_discord_user","expiresAt":"2030-01-01T00:00:00Z","tokenKind":"discord"}`))
		case r.URL.Path == "/api/v1/servers/srv-1":
			if r.Header.Get("Authorization") != "Bearer genos_discord_user" {
				t.Errorf("detail auth = %s", r.Header.Get("Authorization"))
			}
			sawUser = true
			if mode == "wrapped" {
				_, _ = w.Write([]byte(`{"server":` + body + `}`))
				return
			}
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := genos.NewClient(server.URL, "bot-secret", server.Client())
	for _, want := range []string{"wrapped", "bare"} {
		mode = want
		got, err := client.GetServer(context.Background(), "user-1", "srv-1")
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != "srv-1" || got.Name != "Alpha" || got.Game.Name != "Factorio" || got.Status != "Running" || got.PlayerCount() != 2 || len(got.Notable()) != 1 || got.Notable()[0] != "patch" {
			t.Fatalf("%s server = %#v", want, got)
		}
	}
	if !sawUser {
		t.Fatal("detail was not called with the user token")
	}
}
