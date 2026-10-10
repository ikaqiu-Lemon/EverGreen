package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

func TestClientHandshakeAndPlanDoNotSendPrincipalOrCapability(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Evergreen-Caller") != CallerCLI {
			t.Fatalf("caller header = %q", request.Header.Get("X-Evergreen-Caller"))
		}
		if request.Header.Get("Authorization") != "Token secret" {
			t.Fatalf("authorization header = %q", request.Header.Get("Authorization"))
		}
		switch request.URL.Path {
		case "/api/evergreen/v1/capabilities":
			_ = json.NewEncoder(writer).Encode(evergreencore.APIResponse[evergreencore.Capabilities]{
				OK: true, Data: evergreencore.CurrentCapabilities(),
			})
		case "/api/evergreen/v1/plan":
			var raw map[string]json.RawMessage
			if err := json.NewDecoder(request.Body).Decode(&raw); err != nil {
				t.Fatal(err)
			}
			if _, exists := raw["principal"]; exists {
				t.Fatal("client sent a self-asserted principal")
			}
			if _, exists := raw["capability"]; exists {
				t.Fatal("client sent a self-asserted capability")
			}
			_ = json.NewEncoder(writer).Encode(evergreencore.APIResponse[evergreencore.PlannedOperation]{
				OK: true, Data: evergreencore.PlannedOperation{PlanHash: "sha256:plan"},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := New(Options{
		BaseURL: server.URL, Token: "secret", Caller: CallerCLI, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Capabilities(context.Background()); err != nil {
		t.Fatal(err)
	}
	planned, err := client.Plan(context.Background(), evergreencore.PlanRequest{
		Protocol: evergreencore.ChangePlanProtocol, OperationID: "op-1", Command: "claim.update",
	})
	if err != nil {
		t.Fatal(err)
	}
	if planned.PlanHash != "sha256:plan" {
		t.Fatalf("plan hash = %q", planned.PlanHash)
	}
}

func TestClientReturnsStableDiagnostics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(evergreencore.APIResponse[evergreencore.OperationRecord]{
			OK: false,
			Diagnostics: []evergreencore.Diagnostic{{
				Code: evergreencore.CodeBaseMismatch, Level: "error", Path: "base[0]",
				Message: "stale",
			}},
		})
	}))
	defer server.Close()
	client, err := New(Options{BaseURL: server.URL, Caller: CallerAgent, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Apply(context.Background(), evergreencore.ApplyRequest{}); !evergreencore.HasDiagnostic(err, evergreencore.CodeBaseMismatch) {
		t.Fatalf("diagnostic error = %v", err)
	}
}

func TestFromEnvironmentDoesNotInventOfflineFallback(t *testing.T) {
	oldURL, oldToken := os.Getenv(KernelURLEnv), os.Getenv(KernelTokenEnv)
	t.Cleanup(func() {
		_ = os.Setenv(KernelURLEnv, oldURL)
		_ = os.Setenv(KernelTokenEnv, oldToken)
	})
	_ = os.Unsetenv(KernelURLEnv)
	if _, err := FromEnvironment(CallerCLI); err != ErrKernelUnavailable {
		t.Fatalf("missing endpoint error = %v", err)
	}
	_ = os.Setenv(KernelURLEnv, "http://127.0.0.1:1")
	client, err := FromEnvironment(CallerCLI)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Capabilities(context.Background()); err == nil {
		t.Fatal("unreachable configured kernel silently fell back")
	}
}
