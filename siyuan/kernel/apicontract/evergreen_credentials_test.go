package apicontract

import (
	"strings"
	"testing"
)

func TestEvergreenCredentialsContract(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"action":"rotate"}`, `{"action":"create","agent_id":123,"confirm":true}`,
		`{"action":"create","agent_id":"agent","confirm":"true"}`, `{"action":"list","confirm":null}`,
	} {
		if _, err := EvergreenCredentialsAPI.Decode(strings.NewReader(body)); err == nil {
			t.Fatalf("invalid credential command accepted: %s", body)
		}
	}
	for _, body := range []string{
		`{"action":"list"}`, `{"action":"create","agent_id":"agent","confirm":true}`,
		`{"action":"revoke","agent_id":"agent","confirm":false}`,
	} {
		if _, err := EvergreenCredentialsAPI.Decode(strings.NewReader(body)); err != nil {
			t.Fatalf("typed credential command rejected: %s: %v", body, err)
		}
	}
}
